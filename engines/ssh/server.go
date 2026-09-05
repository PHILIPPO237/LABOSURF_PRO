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
	"sync"
	"syscall"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// applySysProcAttr configure le drop de privilèges vers l'utilisateur labosurf.
func applySysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			// L'utilisateur 'labosurf' est créé par labosurf-pro.sh.
			// Si absent, on reste en root (fallback).
		},
	}
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
	config   SSHConfig
	listener net.Listener
	sshConf  *gossh.ServerConfig
	sessions map[string]*Session
	mu       sync.RWMutex
	cancel   context.CancelFunc
}

func NewServer(cfg SSHConfig) (*Server, error) {
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
	s.cancel = cancel

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
	s.listener = ln
	log.Printf("✔ SSH Engine natif démarré sur %s", addr)

	go func() {
		<-ctx.Done()
		ln.Close()
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
			s.handleShell(ctx, ch, sess, req.WantReply)
			return
		case "exec":
			s.handleExec(ctx, ch, sess, req.Payload, req.WantReply)
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func (s *Server) handleShell(ctx context.Context, ch gossh.Channel, sess *Session, wantReply bool) {
	if wantReply {
		_, _ = ch.SendRequest("x-accept", true, nil)
	}

	shellPath := "/bin/bash"
	if _, statErr := os.Stat(shellPath); statErr != nil {
		shellPath = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, shellPath, "--login")
	cmd.Env = append(os.Environ(),
		"HOME=/home/"+sess.Username,
		"USER="+sess.Username,
		"LOGNAME="+sess.Username,
		"SHELL="+shellPath,
		"TERM=xterm-256color",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)

	applySysProcAttr(cmd)

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

func (s *Server) handleExec(ctx context.Context, ch gossh.Channel, sess *Session, payload []byte, wantReply bool) {
	if wantReply {
		_, _ = ch.SendRequest("x-accept", true, nil)
	}

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
	cmd.Env = append(os.Environ(),
		"HOME=/home/"+sess.Username,
		"USER="+sess.Username,
		"LOGNAME="+sess.Username,
		"SHELL="+shellPath,
		"TERM=xterm-256color",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)
	applySysProcAttr(cmd)
	cmd.Stdin = ch
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()

	applySysProcAttr(cmd)
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
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
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
