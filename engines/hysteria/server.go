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
	User      string
	ClientIP  string
	StartedAt time.Time
	BytesIn   int64
	BytesOut  int64
	Backend   net.Conn
}

type HysteriaServer struct {
	config   HysteriaConfig
	conn     *net.UDPConn
	mu       sync.RWMutex
	sessions map[string]*HysteriaSession
	frags    map[string]*fragBuffers
	cancel   context.CancelFunc
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
		return HysteriaConfig{}, fmt.Errorf("config Hysteria invalide : %w", err)
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
	s.cancel = cancel
	log.Printf("✔ Hysteria Engine natif démarré sur :%d (obfs: %s, backend: %s)", s.config.Port, s.config.Obfs, s.config.Backend)

	go func() {
		<-ctx.Done()
		s.conn.Close()
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

func (s *HysteriaServer) handleHello(pkt []byte, remoteAddr *net.UDPAddr) {
	if len(pkt) < 12 {
		return
	}
	sessionID := hex.EncodeToString(pkt[4:12])

	challenge := make([]byte, 32)
	rand.Read(challenge)

	s.mu.Lock()
	sess, exists := s.sessions[sessionID]
	if !exists {
		sess = &HysteriaSession{
			ID:        sessionID,
			ClientIP:  remoteAddr.String(),
			StartedAt: time.Now(),
		}
		s.sessions[sessionID] = sess
	}
	sess.ClientIP = remoteAddr.String()
	s.mu.Unlock()

	authResp := make([]byte, 8+32)
	binary.BigEndian.PutUint32(authResp[0:4], authMagic)
	copy(authResp[4:8], []byte(sessionID[:4]))
	copy(authResp[8:], challenge)
	obfuscate(authResp, []byte(s.config.Obfs))
	s.conn.WriteToUDP(authResp, remoteAddr)
}

func (s *HysteriaServer) handleAuth(pkt []byte, remoteAddr *net.UDPAddr) {
	if len(pkt) < 12+64 {
		return
	}
	sessionID := hex.EncodeToString(pkt[4:12])

	// Le payload après 12 octets = 16 bytes client nonce + 32 bytes HMAC(password, client_nonce) + 16 bytes server_nonce
	if len(pkt) < 12+16+32+16 {
		return
	}

	clientNonce := pkt[12:28]
	receivedHMAC := pkt[28:60]
	_ = pkt[60:76] // serverNonce

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

	// Vérifier HMAC du payload si présent
	if len(payload) > 32 {
		hmacData := payload[len(payload)-32:]
		data := payload[:len(payload)-32]
		// Trouver l'utilisateur pour la clé HMAC
		var expectedHMAC []byte
		for _, u := range s.config.Users {
			if u.Enabled {
				expectedHMAC = hmacSHA256([]byte(u.Password), data)
				break
			}
		}
		if !hmac.Equal(expectedHMAC, hmacData) {
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
	s.mu.RUnlock()

	buf := make([]byte, 65535)
	for {
		n, err := backend.Read(buf)
		if n > 0 {
			data := buf[:n]

			// Construire packet data : [magic:4][sessionID:16][sequence:4][payload]
			seq := make([]byte, 4)
			binary.BigEndian.PutUint32(seq, 0) // simplifié

			dataPkt := make([]byte, 4+16+4+len(data))
			binary.BigEndian.PutUint32(dataPkt[0:4], dataMagic)
			copy(dataPkt[4:20], []byte(sessionID)[:16])
			copy(dataPkt[20:24], seq)
			copy(dataPkt[24:], data)

			obfuscate(dataPkt, []byte(s.config.Obfs))
			s.conn.WriteToUDP(dataPkt, remoteAddr)

			s.mu.Lock()
			if s, ok := s.sessions[sessionID]; ok {
				s.BytesOut += int64(n)
			}
			s.mu.Unlock()
		}
		if err != nil {
			break
		}
	}

	s.mu.Lock()
	delete(s.sessions, sessionID)
	if sess, ok := s.sessions[sessionID]; ok && sess.Backend != nil {
		sess.Backend.Close()
	}
	s.mu.Unlock()
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

func (s *HysteriaServer) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	for _, sess := range s.sessions {
		if sess.Backend != nil {
			sess.Backend.Close()
		}
	}
	s.mu.Unlock()
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

var _ = hmacSHA256
