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

	helloMagic   = 0x4848414C
	authMagic    = 0x48484154
	dataMagic    = 0x48484144
	pingMagic    = 0x48484150
	fragHeaderSz = 16
)

type HysteriaConfig struct {
	Port    int            `json:"port"`
	Obfs    string         `json:"obfs"`
	Users   []HysteriaUser `json:"users"`
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
	return cfg, nil
}

func readFileH(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (s *HysteriaServer) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	log.Printf("✔ Hysteria Engine natif démarré sur :%d (obfs: %s)", s.config.Port, s.config.Obfs)

	go func() {
		<-ctx.Done()
		s.conn.Close()
	}()

	go s.cleanupFragments()

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

	password := string(pkt[12:])

	user, ok := s.authenticate(password)
	if !ok {
		log.Printf("Hysteria : authentification refusée pour %s", remoteAddr)
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

	log.Printf("✔ Hysteria : %s authentifié depuis %s", user.Name, remoteAddr)
}

func (s *HysteriaServer) handleData(pkt []byte, remoteAddr *net.UDPAddr) {
	if len(pkt) < fragHeaderSz {
		return
	}

	fragID := hex.EncodeToString(pkt[0:16])
	_ = fragID

	s.mu.Lock()
	var user string
	for _, sess := range s.sessions {
		if sess.ClientIP == remoteAddr.String() && sess.User != "" {
			user = sess.User
			sess.BytesIn += int64(len(pkt) - fragHeaderSz)
			break
		}
	}
	s.mu.Unlock()

	_ = user
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

func (s *HysteriaServer) authenticate(password string) (*HysteriaUser, bool) {
	for i := range s.config.Users {
		u := &s.config.Users[i]
		if !u.Enabled {
			continue
		}
		if u.Password == password {
			return u, true
		}
	}
	return nil, false
}

func (s *HysteriaServer) cleanupFragments() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for id, fb := range s.frags {
			if now.Sub(fb.lastSeen) > 10*time.Minute {
				delete(s.frags, id)
			}
		}
		s.mu.Unlock()
	}
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
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
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

var _ = hmacSHA256
