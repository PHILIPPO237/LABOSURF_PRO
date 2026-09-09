package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// resolveRunAsCredential résout dynamiquement (jamais codé en dur) l'UID et
// le GID de l'utilisateur système runAsUser via os/user, et renvoie le
// Credential syscall correspondant ainsi que son répertoire personnel.
// ok=false si l'utilisateur n'existe pas sur cette machine (ex: machine où
// labosurf-pro.sh:install_ssh_user n'a pas encore tourné).
func resolveRunAsCredential(runAsUser string) (cred *syscall.Credential, homeDir string, ok bool) {
	u, err := user.Lookup(runAsUser)
	if err != nil {
		return nil, "", false
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, "", false
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, "", false
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, u.HomeDir, true
}

// applySysProcAttr configure le drop de privilèges réel de cmd vers
// l'utilisateur système runAsUser. Une version antérieure de cette
// fonction posait un SysProcAttr entièrement vide (aucun Credential) :
// elle ne droppait donc RIEN, et toute session shell/exec héritait des
// privilèges du process labosurf-ssh (root en déploiement typique via
// systemd). Ici, si runAsUser est résolvable, le process fils démarre
// réellement avec son UID/GID (dropped=true, avec son home directory
// réel). S'il ne l'est pas (compte système pas encore créé sur cette
// machine), on continue SANS Credential plutôt que de refuser la session
// — casser l'accès au tunnel sur une machine mal provisionnée serait pire
// qu'un avertissement explicite — mais on le journalise bruyamment pour
// que ce ne soit jamais silencieux.
func applySysProcAttr(cmd *exec.Cmd, runAsUser string) (homeDir string, dropped bool) {
	if runAsUser == "" {
		return "", false
	}
	cred, home, ok := resolveRunAsCredential(runAsUser)
	if !ok {
		log.Printf(
			"SSH : utilisateur système %q introuvable — session démarrée SANS drop de privilèges "+
				"(voir labosurf-pro.sh:install_ssh_user pour provisionner ce compte)",
			runAsUser,
		)
		return "", false
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	return home, true
}

type Session struct {
	ID         string
	Username   string
	RemoteAddr string
	StartedAt  time.Time
	BytesIn    int64
	BytesOut   int64
}

type Server struct {
	config    SSHConfig
	listener  net.Listener
	sshConf   *gossh.ServerConfig
	sessions  map[string]*Session
	mu        sync.RWMutex
	cancel    context.CancelFunc
	closed    bool      // protégé par mu ; true après Close(), pour qu'Addr() ne rende jamais un endpoint pour un moteur arrêté
	closeOnce sync.Once // garantit un seul appel réel à listener.Close() (voir closeListener)
}

// closeListener ferme réellement s.listener une seule fois, quel que soit
// lequel des deux chemins d'arrêt l'appelle en premier : la goroutine
// interne de Run() qui réagit à ctx.Done() (nécessaire quand l'appelant
// annule directement le contexte sans passer par Close()), ou Close()
// lui-même (appelé explicitement par Stop()/Restart()). Sans cette garde,
// les deux chemins appellent listener.Close() de façon concurrente sur le
// même fd — l'un des deux reçoit "use of closed network connection", ce
// qui remontait auparavant comme une erreur de Close() (donc de Stop()/
// Restart()) même quand l'arrêt s'est en réalité bien passé.
func (s *Server) closeListener() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		ln := s.listener
		s.mu.Unlock()
		if ln != nil {
			err = ln.Close()
		}
	})
	return err
}

func NewServer(cfg SSHConfig) (*Server, error) {
	if cfg.RunAsUser == "" {
		cfg.RunAsUser = defaultRunAsUser
	}

	sshConf := &gossh.ServerConfig{
		PublicKeyCallback: nil,
		NoClientAuth:      false,
	}

	s := &Server{
		config:   cfg,
		sshConf:  sshConf,
		sessions: make(map[string]*Session),
	}
	return s, nil
}

func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	// s.cancel protégé par s.mu : même correctif de course que dans
	// engines/hysteria, engines/slowdns et engines/dnstt (Close() peut
	// être appelé depuis une autre goroutine juste après le démarrage).
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	hostKey, err := s.loadOrGenerateHostKey()
	if err != nil {
		return fmt.Errorf("clé hôte SSH : %w", err)
	}
	s.sshConf.AddHostKey(hostKey)

	// Écrire le fichier authorized_keys pour les utilisateurs activés.
	if err := s.writeAuthorizedKeys(); err != nil {
		log.Printf("Avertissement : impossible d'écrire authorized_keys : %v", err)
	}

	s.sshConf.PublicKeyCallback = func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
		for _, u := range s.config.Users {
			if !u.Enabled {
				continue
			}
			if u.Username == "" {
				continue
			}
			// Vérifier l'expiration du compte.
			if u.ExpiresAt != "" {
				if exp, err := time.Parse(time.RFC3339, u.ExpiresAt); err == nil {
					if time.Now().After(exp) {
						continue // compte expiré
					}
				}
			}
			if u.Username == "" {
				continue
			}
			storedKey, err := parsePublicKey(u.PublicKey)
			if err != nil {
				continue
			}
			if string(key.Marshal()) == string(storedKey.Marshal()) {
				return &gossh.Permissions{
					Extensions: map[string]string{
						"pubkey-user": u.Username,
					},
				}, nil
			}
		}
		return nil, fmt.Errorf("clé publique non autorisée")
	}

	addr := fmt.Sprintf(":%d", s.config.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("écoute TCP %s : %w", addr, err)
	}
	// s.listener protégé par s.mu, comme s.cancel : Close() (ou Addr())
	// peut être appelé depuis une autre goroutine.
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	log.Printf("✔ SSH Engine natif démarré sur %s", addr)

	go func() {
		<-ctx.Done()
		s.closeListener()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("erreur accept SSH : %v", err)
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, netConn net.Conn) {
	defer netConn.Close()

	deadline := time.Now().Add(30 * time.Second)
	netConn.SetDeadline(deadline)

	sshConn, chans, reqs, err := gossh.NewServerConn(netConn, s.sshConf)
	if err != nil {
		log.Printf("erreur handshake SSH : %v", err)
		return
	}
	netConn.SetDeadline(time.Time{})

	username := sshConn.Permissions.Extensions["pubkey-user"]
	log.Printf("✔ Connexion SSH : %s depuis %s", username, sshConn.RemoteAddr())

	sess := &Session{
		ID:         fmt.Sprintf("%s-%d", username, time.Now().UnixNano()),
		Username:   username,
		RemoteAddr: sshConn.RemoteAddr().String(),
		StartedAt:  time.Now(),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.sessions, sess.ID)
		s.mu.Unlock()
	}()

	go gossh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() == "session" {
			go s.handleChannel(ctx, sshConn, newChan, sess)
		} else {
			newChan.Reject(gossh.UnknownChannelType, "type de canal non supporté")
		}
	}
}

func (s *Server) handleChannel(ctx context.Context, sshConn *gossh.ServerConn, newChan gossh.NewChannel, sess *Session) {
	ch, chReqs, err := newChan.Accept()
	if err != nil {
		return
	}
	defer ch.Close()

	for req := range chReqs {
		switch req.Type {
		case "pty-req":
			req.Reply(true, nil)
		case "shell":
			// Répondre à LA REQUÊTE DE CANAL elle-même (SSH_MSG_CHANNEL_
			// SUCCESS) — une version antérieure envoyait à la place une
			// requête "x-accept" inventée sur le canal, qui n'est pas la
			// réponse SSH attendue. Un vrai client SSH (Session.Start,
			// utilisé par Run/Output/CombinedOutput) attend cette réponse
			// avant de continuer : sans elle, l'appel client restait
			// bloqué indéfiniment — exec/shell via un client SSH réel
			// n'avait donc jamais fonctionné jusqu'ici.
			if req.WantReply {
				req.Reply(true, nil)
			}
			s.handleShell(ctx, ch, sess)
			return
		case "exec":
			if req.WantReply {
				req.Reply(true, nil)
			}
			s.handleExec(ctx, ch, sess, req.Payload)
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func (s *Server) handleShell(ctx context.Context, ch gossh.Channel, sess *Session) {
	shellPath := "/bin/bash"
	if _, statErr := os.Stat(shellPath); statErr != nil {
		shellPath = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, shellPath, "--login")

	// home : celui du compte système réel si le drop de privilèges a
	// réussi (le process tourne effectivement sous cet utilisateur, donc
	// c'est SON HOME qui doit être valide) ; sinon, l'ancienne estimation
	// par convention (utile pour le confort de session, mais ne reflète
	// pas forcément un répertoire existant).
	home, dropped := applySysProcAttr(cmd, s.config.RunAsUser)
	if !dropped {
		home = "/home/" + sess.Username
	}
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"USER="+sess.Username,
		"LOGNAME="+sess.Username,
		"SHELL="+shellPath,
		"TERM=xterm-256color",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)

	cmd.Stdin = ch
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()

	runErr := cmd.Run()
	status := 0
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		status = exitErr.ExitCode()
	}
	msg := struct{ Status uint32 }{uint32(status)}
	ch.SendRequest("exit-status", false, gossh.Marshal(&msg))
	_ = ch.CloseWrite()
	log.Printf("session SSH %s terminée", sess.Username)
}

func (s *Server) handleExec(ctx context.Context, ch gossh.Channel, sess *Session, payload []byte) {
	var execReq struct {
		Value string
	}
	if err := gossh.Unmarshal(payload, &execReq); err != nil {
		ch.Close()
		return
	}

	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		shellPath = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, shellPath, "-c", execReq.Value)

	home, dropped := applySysProcAttr(cmd, s.config.RunAsUser)
	if !dropped {
		home = "/home/" + sess.Username
	}
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"USER="+sess.Username,
		"LOGNAME="+sess.Username,
		"SHELL="+shellPath,
		"TERM=xterm-256color",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)
	cmd.Stdin = ch
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()

	runErr := cmd.Run()
	if runErr != nil {
		log.Printf("exec SSH %s (%q) : %v", sess.Username, execReq.Value, runErr)
	}

	status := 0
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		status = exitErr.ExitCode()
	}
	msg := struct{ Status uint32 }{uint32(status)}
	ch.SendRequest("exit-status", false, gossh.Marshal(&msg))
	_ = ch.CloseWrite()
}

func parsePublicKey(hexKey string) (gossh.PublicKey, error) {
	b, err := decodeHex(hexKey)
	if err != nil {
		return nil, err
	}
	return gossh.ParsePublicKey(b)
}

func decodeHex(s string) ([]byte, error) {
	s = fmt.Sprintf("%s", s)
	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		var b byte
		for j := 0; j < 2; j++ {
			c := s[i+j]
			switch {
			case c >= '0' && c <= '9':
				b = b<<4 | (c - '0')
			case c >= 'a' && c <= 'f':
				b = b<<4 | (c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				b = b<<4 | (c - 'A' + 10)
			default:
				return nil, fmt.Errorf("caractère hex invalide : %c", c)
			}
		}
		out[i/2] = b
	}
	return out, nil
}

func (s *Server) loadOrGenerateHostKey() (gossh.Signer, error) {
	keyPath := hostKeyPath(s.config.Dir)

	raw, err := os.ReadFile(keyPath)
	if err == nil {
		return gossh.ParsePrivateKey(raw)
	}

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	marshaled, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return nil, err
	}

	pemBlock := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: marshaled,
	}

	keyPEM := pem.EncodeToMemory(pemBlock)

	if err := os.MkdirAll(s.config.Dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}

	return gossh.ParsePrivateKey(keyPEM)
}

func (s *Server) writeAuthorizedKeys() error {
	if s.config.Dir == "" {
		return fmt.Errorf("répertoire SSH non configuré")
	}
	keys := s.config.AuthorizedKeysBytes()
	if len(keys) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.config.Dir, 0o700); err != nil {
		return fmt.Errorf("création répertoire SSH : %w", err)
	}
	path := authorizedKeysPath(s.config.Dir)
	return os.WriteFile(path, keys, 0o600)
}

// Addr renvoie l'adresse d'écoute une fois le serveur démarré, nil avant
// (le listener n'est créé qu'à l'intérieur de Run(), pas de NewServer()).
// Addr renvoie l'adresse d'écoute une fois le serveur réellement démarré
// (nil, false avant que le listener ne soit ouvert dans Run(), ou après
// Close()) — jamais un placeholder.
func (s *Server) Addr() net.Addr {
	addr, _ := s.AddrOk()
	return addr
}

// AddrOk est la version explicite d'Addr : le bool distingue "pas encore
// prêt" de "adresse nil" pour un appelant qui a besoin de le savoir
// (Endpoint()).
func (s *Server) AddrOk() (net.Addr, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.listener == nil {
		return nil, false
	}
	return s.listener.Addr(), true
}

func (s *Server) Sessions() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, *sess)
	}
	return out
}

func (s *Server) Close() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.closeListener()
}

func copyConn(dst io.Writer, src io.Reader, counter *int64) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			w, _ := dst.Write(buf[:n])
			if counter != nil {
				*counter += int64(w)
			}
		}
		if err != nil {
			return
		}
	}
}
