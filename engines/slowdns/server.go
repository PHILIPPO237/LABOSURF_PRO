package slowdns

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type SlowDNSServer struct {
	config    SlowDNSConfig
	conn      *net.UDPConn
	sessions  map[string]*SlowDNSSession
	mu        sync.RWMutex
	cancel    context.CancelFunc
}

type SlowDNSSession struct {
	ID        string
	User      string
	ClientIP  string
	StartedAt time.Time
	BytesIn   int64
	BytesOut  int64
}

func NewSlowDNSServer(cfg SlowDNSConfig) (*SlowDNSServer, error) {
	addr := fmt.Sprintf(":%d", cfg.Port)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("écoute UDP %s : %w", addr, err)
	}
	return &SlowDNSServer{
		config:   cfg,
		conn:     conn,
		sessions: make(map[string]*SlowDNSSession),
	}, nil
}

func (s *SlowDNSServer) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	log.Printf("✔ SlowDNS Engine natif démarré sur :%d (domaine: %s)", s.config.Port, s.config.Domain)

	go func() {
		<-ctx.Done()
		s.conn.Close()
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
		go s.handleDNSQuery(buf[:n], remoteAddr)
	}
}

func (s *SlowDNSServer) handleDNSQuery(query []byte, remoteAddr *net.UDPAddr) {
	if len(query) < 12 {
		return
	}

	qdCount := binary.BigEndian.Uint16(query[6:])
	if qdCount == 0 {
		return
	}

	nameEnd := 12
	for nameEnd < len(query) && query[nameEnd] != 0 {
		if query[nameEnd] == 0xC0 {
			nameEnd += 2
			break
		}
		nameEnd += int(query[nameEnd]) + 1
	}
	if nameEnd < len(query) && query[nameEnd] == 0 {
		nameEnd++
	}
	nameEnd += 4

	subdomain := extractSubdomain(query[12:])
	if subdomain == "" {
		s.sendNXDOMAIN(query, remoteAddr)
		return
	}

	data, err := decodeSubdomain(subdomain)
	if err != nil {
		s.sendNXDOMAIN(query, remoteAddr)
		return
	}

	if len(data) < 1 {
		s.sendNXDOMAIN(query, remoteAddr)
		return
	}

	sessionID := hex.EncodeToString(data[:16])
	s.mu.RLock()
	sess, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		sess = &SlowDNSSession{
			ID:        sessionID,
			User:      "unknown",
			ClientIP:  remoteAddr.String(),
			StartedAt: time.Now(),
		}
		s.mu.Lock()
		s.sessions[sessionID] = sess
		s.mu.Unlock()
	}

	payload := data[16:]
	sess.BytesIn += int64(len(payload))

	responseData := []byte{0x00}
	resp := buildDNSResponse(query, responseData)
	if resp != nil {
		s.conn.WriteToUDP(resp, remoteAddr)
	}
}

func (s *SlowDNSServer) sendNXDOMAIN(query []byte, remoteAddr *net.UDPAddr) {
	if len(query) < 12 {
		return
	}
	resp := make([]byte, len(query))
	copy(resp, query)
	resp[2] = 0x81
	resp[3] = 0x83
	binary.BigEndian.PutUint16(resp[6:], 0)
	binary.BigEndian.PutUint16(resp[10:], 0)
	s.conn.WriteToUDP(resp[:12], remoteAddr)
}

func extractSubdomain(qname []byte) string {
	var parts []string
	pos := 0
	for pos < len(qname) {
		length := int(qname[pos])
		if length == 0 {
			break
		}
		pos++
		if pos+length > len(qname) {
			break
		}
		parts = append(parts, string(qname[pos:pos+length]))
		pos += length
	}
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func (s *SlowDNSServer) Sessions() []SlowDNSSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SlowDNSSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, *sess)
	}
	return out
}

func (s *SlowDNSServer) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func generateEd25519Keypair() (pubHex, privHex string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(pub), hex.EncodeToString(priv), nil
}
