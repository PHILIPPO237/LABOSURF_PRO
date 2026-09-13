package hysteria

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

const (
	defaultHysteriaPort = 8443

	helloMagic   = 0x4848414C // "HHAL"
	authMagic    = 0x48484154 // "HHAT"
	dataMagic    = 0x48484144 // "HHAD"
	pingMagic    = 0x48484150 // "HHAP"
	fragHeaderSz = 16
)

type HysteriaConfig struct {
	Port    int            `json:"port"`
	Obfs    string         `json:"obfs"`
	Users   []HysteriaUser `json:"users"`
	Backend string         `json:"backend"`
}

type HysteriaUser struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
}

type HysteriaSession struct {
	ID        string
	rawID     []byte // les mêmes octets que ID, mais bruts (pas hex)
	User      string
	ClientIP  string
	StartedAt time.Time
	BytesIn   int64
	BytesOut  int64
	Backend   net.Conn
}

type HysteriaServer struct {
	config    HysteriaConfig
	conn      *net.UDPConn
	mu        sync.RWMutex
	sessions  map[string]*HysteriaSession
	frags     map[string]*fragBuffers
	cancel    context.CancelFunc
	closed    bool      // protégé par mu ; true après Close(), pour qu'Addr() ne rende jamais un endpoint pour un moteur arrêté
	closeOnce sync.Once // garantit un seul appel réel à conn.Close() (voir closeConn)
}

// closeConn ferme réellement s.conn une seule fois, quel que soit lequel
// des deux chemins d'arrêt l'appelle en premier : la goroutine interne de
// Run() qui réagit à ctx.Done() (nécessaire quand l'appelant annule
// directement le contexte sans passer par Close()), ou Close() lui-même
// (appelé explicitement par Stop()/Restart()). Sans cette garde, les deux
// chemins appellent conn.Close() de façon concurrente sur le même fd —
// l'un des deux reçoit alors "use of closed network connection", ce qui
// remontait auparavant comme une erreur de Close() (donc de Stop()/
// Restart()) même quand l'arrêt s'est en réalité bien passé.
func (s *HysteriaServer) closeConn() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		conn := s.conn
		s.mu.Unlock()
		if conn != nil {
			err = conn.Close()
		}
	})
	return err
}

type fragBuffers struct {
	data     []byte
	expected int
	lastSeen time.Time
}

func NewHysteriaServer(cfg HysteriaConfig) (*HysteriaServer, error) {
	addr := fmt.Sprintf(":%d", cfg.Port)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("écoute UDP %s : %w", addr, err)
	}
	if cfg.Backend == "" {
		cfg.Backend = "127.0.0.1:22"
	}
	return &HysteriaServer{
		config:   cfg,
		conn:     conn,
		sessions: make(map[string]*HysteriaSession),
		frags:    make(map[string]*fragBuffers),
	}, nil
}

func loadHysteriaConfig(path string) (HysteriaConfig, error) {
	raw, err := readFileH(path)
	if err != nil {
		return HysteriaConfig{}, fmt.Errorf("lecture config Hysteria : %w", err)
	}
	var cfg HysteriaConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// Même tolérance que Configure : une config YAML émisé par la
		// plateforme (hysteriaV2Config) doit pouvoir démarrer le moteur.
		cfg, err = parseConfigYAML(raw)
		if err != nil {
			return HysteriaConfig{}, fmt.Errorf("config Hysteria illisible (ni JSON ni YAML toléré) : %w", err)
		}
	}
	if cfg.Port <= 0 {
		cfg.Port = defaultHysteriaPort
	}
	if cfg.Backend == "" {
		cfg.Backend = "127.0.0.1:22"
	}
	return cfg, nil
}

func readFileH(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (s *HysteriaServer) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	// s.cancel est protégé par s.mu (comme s.sessions) : Run() et Close()
	// peuvent s'exécuter dans des goroutines différentes (Close() est
	// typiquement appelé pendant que Run() tourne encore), et rien
	// d'autre ne garantit un ordre entre l'écriture ici et la lecture
	// dans Close() — confirmé par -race sur un test qui ferme le serveur
	// juste après l'avoir démarré.
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	log.Printf("✔ Hysteria Engine natif démarré sur :%d (obfs: %s, backend: %s)", s.config.Port, s.config.Obfs, s.config.Backend)

	go func() {
		<-ctx.Done()
		s.closeConn()
		s.mu.Lock()
		for _, sess := range s.sessions {
			if sess.Backend != nil {
				sess.Backend.Close()
			}
		}
		s.mu.Unlock()
	}()

	buf := make([]byte, 65535)
	for {
		n, remoteAddr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("erreur lecture UDP : %v", err)
				continue
			}
		}
		go s.handlePacket(buf[:n], remoteAddr)
	}
}

// Obscure le packet avec XOR sur les 16 premiers octets.
func obfuscate(data []byte, key []byte) {
	if len(key) == 0 {
		return
	}
	n := len(data)
	if n > 16 {
		n = 16
	}
	for i := 0; i < n; i++ {
		data[i] ^= key[i%len(key)]
	}
}

func (s *HysteriaServer) handlePacket(pkt []byte, remoteAddr *net.UDPAddr) {
	if len(pkt) < 8 {
		return
	}

	obfuscate(pkt, []byte(s.config.Obfs))

	magic := binary.BigEndian.Uint32(pkt[0:4])

	switch magic {
	case helloMagic:
		s.handleHello(pkt, remoteAddr)
	case authMagic:
		s.handleAuth(pkt, remoteAddr)
	case dataMagic:
		s.handleData(pkt, remoteAddr)
	case pingMagic:
		s.handlePing(pkt, remoteAddr)
	}
}

// handleHello traite le paquet HELLO initial. Format : [magic:4][sessionID:16].
// La largeur du sessionID (16 octets) doit être identique dans HELLO, AUTH
// et DATA : une version antérieure utilisait pkt[4:12] (8 octets) ici et
// dans handleAuth, mais pkt[4:20] (16 octets) dans handleData — les deux
// dérivaient donc des clés de map différentes pour ce qui devait être la
// même session, et handleData ne retrouvait jamais la session créée par
// handleHello/handleAuth : aucune donnée ne pouvait circuler après
// authentification.
func (s *HysteriaServer) handleHello(pkt []byte, remoteAddr *net.UDPAddr) {
	if len(pkt) < 4+16 {
		return
	}
	rawID := append([]byte(nil), pkt[4:20]...)
	sessionID := hex.EncodeToString(rawID)

	challenge := make([]byte, 32)
	rand.Read(challenge)

	s.mu.Lock()
	sess, exists := s.sessions[sessionID]
	if !exists {
		sess = &HysteriaSession{
			ID:        sessionID,
			rawID:     rawID,
			ClientIP:  remoteAddr.String(),
			StartedAt: time.Now(),
		}
		s.sessions[sessionID] = sess
	}
	sess.ClientIP = remoteAddr.String()
	s.mu.Unlock()

	authResp := make([]byte, 4+16+32)
	binary.BigEndian.PutUint32(authResp[0:4], authMagic)
	copy(authResp[4:20], rawID)
	copy(authResp[20:], challenge)
	obfuscate(authResp, []byte(s.config.Obfs))
	s.conn.WriteToUDP(authResp, remoteAddr)
}

// handleAuth traite le paquet AUTH. Format : [magic:4][sessionID:16]
// [clientNonce:16][HMAC(password,clientNonce):32][serverNonce:16] = 84
// octets minimum. sessionID fait 16 octets, comme dans handleHello et
// handleData (voir commentaire de handleHello sur l'incohérence corrigée).
func (s *HysteriaServer) handleAuth(pkt []byte, remoteAddr *net.UDPAddr) {
	const minLen = 4 + 16 + 16 + 32 + 16
	if len(pkt) < minLen {
		return
	}
	rawID := append([]byte(nil), pkt[4:20]...)
	sessionID := hex.EncodeToString(rawID)

	clientNonce := pkt[20:36]
	receivedHMAC := pkt[36:68]
	_ = pkt[68:84] // serverNonce

	// Vérifier HMAC - on itère sur les utilisateurs pour trouver le bon
	var user *HysteriaUser
	var ok bool
	for i := range s.config.Users {
		u := &s.config.Users[i]
		if !u.Enabled {
			continue
		}
		expectedHMAC := hmacSHA256([]byte(u.Password), clientNonce)
		if hmac.Equal(expectedHMAC, receivedHMAC) {
			user = u
			ok = true
			break
		}
	}
	if !ok {
		log.Printf("Hysteria : authentification HMAC refusée pour %s", remoteAddr)
		return
	}

	s.mu.Lock()
	sess, exists := s.sessions[sessionID]
	if !exists {
		sess = &HysteriaSession{
			ID:        sessionID,
			rawID:     rawID,
			ClientIP:  remoteAddr.String(),
			StartedAt: time.Now(),
		}
		s.sessions[sessionID] = sess
	}
	sess.User = user.Name
	s.mu.Unlock()

	// Connecter au backend
	backend, err := net.Dial("udp", s.config.Backend)
	if err != nil {
		log.Printf("Hysteria : impossible de joindre backend %s : %v", s.config.Backend, err)
		return
	}
	s.mu.Lock()
	sess.Backend = backend
	s.mu.Unlock()

	// Lancer la lecture du backend
	go s.backendLoop(sessionID, remoteAddr)

	log.Printf("✔ Hysteria : %s authentifié depuis %s, backend connecté", user.Name, remoteAddr)

	// Répondre OK
	okResp := make([]byte, 8)
	binary.BigEndian.PutUint32(okResp[0:4], authMagic)
	copy(okResp[4:8], []byte("OK"))
	obfuscate(okResp, []byte(s.config.Obfs))
	s.conn.WriteToUDP(okResp, remoteAddr)
}

func (s *HysteriaServer) authenticateByHMAC(receivedPassword string, clientNonce, receivedHMAC []byte) (*HysteriaUser, bool) {
	for i := range s.config.Users {
		u := &s.config.Users[i]
		if !u.Enabled {
			continue
		}
		expectedHMAC := hmacSHA256([]byte(u.Password), clientNonce)
		if hmac.Equal(expectedHMAC, receivedHMAC) {
			return u, true
		}
	}
	return nil, false
}

func (s *HysteriaServer) handleData(pkt []byte, remoteAddr *net.UDPAddr) {
	if len(pkt) < fragHeaderSz {
		return
	}

	// Format: [magic:4][sessionID:16][sequence:4][payload...]
	if len(pkt) < 4+16+4 {
		return
	}

	sessionID := hex.EncodeToString(pkt[4:20])
	sequence := binary.BigEndian.Uint32(pkt[20:24])
	payload := pkt[24:]

	s.mu.RLock()
	sess, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		return
	}

	// Vérifier HMAC du payload si présent. La clé utilisée DOIT être celle
	// de l'utilisateur authentifié pour CETTE session (sess.User, posé par
	// handleAuth) — une version antérieure prenait systématiquement le mot
	// de passe du premier utilisateur activé de la config, quelle que soit
	// la session, ce qui casse l'intégrité dès qu'il y a plus d'un compte.
	if len(payload) > 32 {
		hmacData := payload[len(payload)-32:]
		data := payload[:len(payload)-32]

		var expectedHMAC []byte
		for _, u := range s.config.Users {
			if u.Enabled && u.Name == sess.User {
				expectedHMAC = hmacSHA256([]byte(u.Password), data)
				break
			}
		}
		if expectedHMAC == nil || !hmac.Equal(expectedHMAC, hmacData) {
			return
		}
		payload = data
	}

	_ = sequence
	s.mu.Lock()
	sess, exists = s.sessions[sessionID]
	if !exists || sess.Backend == nil {
		s.mu.Unlock()
		return
	}
	sess.BytesIn += int64(len(payload))
	backend := sess.Backend
	s.mu.Unlock()

	// Écrire vers le backend
	if _, err := backend.Write(payload); err != nil {
		log.Printf("Hysteria : erreur écriture backend : %v", err)
	}
}

func (s *HysteriaServer) handlePing(pkt []byte, remoteAddr *net.UDPAddr) {
	if len(pkt) < 4 {
		return
	}
	nonce := pkt[4:]

	resp := make([]byte, 8+len(nonce))
	binary.BigEndian.PutUint32(resp[0:4], pingMagic)
	copy(resp[4:], nonce)
	obfuscate(resp, []byte(s.config.Obfs))
	s.conn.WriteToUDP(resp, remoteAddr)
}

func (s *HysteriaServer) backendLoop(sessionID string, remoteAddr *net.UDPAddr) {
	s.mu.RLock()
	sess, exists := s.sessions[sessionID]
	if !exists || sess.Backend == nil {
		s.mu.RUnlock()
		return
	}
	backend := sess.Backend
	rawID := sess.rawID
	s.mu.RUnlock()

	buf := make([]byte, 65535)
	for {
		n, err := backend.Read(buf)
		if n > 0 {
			data := buf[:n]

			// Construire packet data : [magic:4][sessionID:16][sequence:4][payload]
			seq := make([]byte, 4)
			binary.BigEndian.PutUint32(seq, 0) // simplifié : pas de suivi de séquence/réordonnancement

			dataPkt := make([]byte, 4+16+4+len(data))
			binary.BigEndian.PutUint32(dataPkt[0:4], dataMagic)
			// rawID (16 octets bruts), PAS hex.EncodeToString(sessionID)[:16] —
			// une version antérieure copiait les 16 premiers CARACTÈRES de la
			// représentation hexadécimale (donc seulement 8 octets réels de
			// session, mal encodés en ASCII), ce qui ne pouvait jamais
			// correspondre au sessionID brut envoyé par le client.
			copy(dataPkt[4:20], rawID)
			copy(dataPkt[20:24], seq)
			copy(dataPkt[24:], data)

			obfuscate(dataPkt, []byte(s.config.Obfs))
			s.conn.WriteToUDP(dataPkt, remoteAddr)

			s.mu.Lock()
			if sess, ok := s.sessions[sessionID]; ok {
				sess.BytesOut += int64(n)
			}
			s.mu.Unlock()
		}
		if err != nil {
			break
		}
	}

	// Fermer le backend AVANT de supprimer la session de la map : vérifier
	// après coup si la session est "toujours dans la map" pour décider de
	// fermer (comme le faisait une version antérieure) est toujours faux
	// juste après avoir appelé delete() sur cette même map — le backend
	// n'était donc jamais fermé ici (fuite de connexion).
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	if sess.Backend != nil {
		sess.Backend.Close()
	}
}

func (s *HysteriaServer) Sessions() []HysteriaSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]HysteriaSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, *sess)
	}
	return out
}

// Addr retourne l'adresse UDP réellement liée par ce serveur, et true tant
// qu'il n'a pas été arrêté. conn est ouvert de façon synchrone dans
// NewHysteriaServer (avant même que Run() ne soit lancé en goroutine), donc
// l'adresse est déjà réelle et disponible dès que le constructeur a réussi.
func (s *HysteriaServer) Addr() (*net.UDPAddr, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.conn == nil {
		return nil, false
	}
	addr, ok := s.conn.LocalAddr().(*net.UDPAddr)
	return addr, ok
}

func (s *HysteriaServer) Close() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.mu.Lock()
	for _, sess := range s.sessions {
		if sess.Backend != nil {
			sess.Backend.Close()
		}
	}
	s.mu.Unlock()
	return s.closeConn()
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

var _ = hmacSHA256
