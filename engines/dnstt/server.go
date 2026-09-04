package dnstt

import (
	"context"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultDNSTTPort = 53
	defaultBackend   = "127.0.0.1:22"

	sessionIDLen = 8
	headerLen    = 1 + sessionIDLen + 4
)

type DNSTTConfig struct {
	Domain  string       `json:"domain"`
	Port    int          `json:"port"`
	Backend string       `json:"backend"`
	Users   []DNSTTUser  `json:"users"`
}

type DNSTTUser struct {
	User       string `json:"user"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	Enabled    bool   `json:"enabled"`
}

func loadDNSTTConfig(path string) (DNSTTConfig, error) {
	raw, err := readFile(path)
	if err != nil {
		return DNSTTConfig{}, fmt.Errorf("lecture config DTNSTT : %w", err)
	}
	var cfg DNSTTConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DNSTTConfig{}, fmt.Errorf("config DNSTT invalide : %w", err)
	}
	if cfg.Port <= 0 {
		cfg.Port = defaultDNSTTPort
	}
	if cfg.Backend == "" {
		cfg.Backend = defaultBackend
	}
	return cfg, nil
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

type DNSTTSession struct {
	ID        string
	User      string
	ClientIP  string
	StartedAt time.Time
	BytesIn   int64
	BytesOut  int64
	Sequence  uint32
	Backend   net.Conn
}

type DNSTTServer struct {
	config   DNSTTConfig
	conn     *net.UDPConn
	sessions map[string]*DNSTTSession
	mu       sync.RWMutex
	cancel   context.CancelFunc
}

func NewDNSTTServer(cfg DNSTTConfig) (*DNSTTServer, error) {
	addr := fmt.Sprintf(":%d", cfg.Port)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("écoute UDP %s : %w", addr, err)
	}
	return &DNSTTServer{
		config:   cfg,
		conn:     conn,
		sessions: make(map[string]*DNSTTSession),
	}, nil
}

func (s *DNSTTServer) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	log.Printf("✔ DNSTT Engine natif démarré sur :%d (domaine: %s)", s.config.Port, s.config.Domain)

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
				log.Printf("erreur lecture DNS : %v", err)
				continue
			}
		}
		go s.handleQuery(buf[:n], remoteAddr)
	}
}

func (s *DNSTTServer) handleQuery(query []byte, remoteAddr *net.UDPAddr) {
	if len(query) < 12 {
		return
	}

	subdomain := extractSubdomain(query[12:])
	if subdomain == "" {
		s.sendNXDOMAIN(query, remoteAddr)
		return
	}

	data, err := decodeSubdomainB32(subdomain)
	if err != nil {
		s.sendNXDOMAIN(query, remoteAddr)
		return
	}

	if len(data) < headerLen {
		s.sendNXDOMAIN(query, remoteAddr)
		return
	}

	sessionID := string(data[0:sessionIDLen])
	psn := binary.BigEndian.Uint32(data[sessionIDLen:headerLen])
	payload := data[headerLen:]

	s.mu.Lock()
	sess, exists := s.sessions[sessionID]
	if !exists {
		backend, err := net.Dial("tcp", s.config.Backend)
		if err != nil {
			log.Printf("DNSTT : impossible de joindre le backend %s : %v", s.config.Backend, err)
			s.mu.Unlock()
			s.sendServerError(query, remoteAddr)
			return
		}
		sess = &DNSTTSession{
			ID:        sessionID,
			User:      "dnstt-tunnel",
			ClientIP:  remoteAddr.String(),
			StartedAt: time.Now(),
			Backend:   backend,
		}
		s.sessions[sessionID] = sess
		go s.backendLoop(sess, remoteAddr)
		go s.backendToClient(sess, remoteAddr)
	}
	sess.Sequence = psn
	sess.BytesIn += int64(len(payload))
	if len(payload) > 0 {
		if _, err := sess.Backend.Write(payload); err != nil {
			log.Printf("DNSTT : erreur écriture backend : %v", err)
		}
	}
	s.mu.Unlock()

	ack := make([]byte, headerLen)
	copy(ack, data[:headerLen])
	s.sendDNSResponse(query, ack, remoteAddr)
}

func (s *DNSTTServer) backendLoop(sess *DNSTTSession, remoteAddr *net.UDPAddr) {
	buf := make([]byte, 32*1024)
	for {
		n, err := sess.Backend.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			s.mu.Lock()
			sess.BytesOut += int64(n)
			nonce := time.Now().UnixNano()
			pkt := make([]byte, headerLen+len(chunk))
			copy(pkt[0:sessionIDLen], []byte(sess.ID))
			binary.BigEndian.PutUint32(pkt[sessionIDLen:headerLen], uint32(nonce&0xFFFFFFFF))
			copy(pkt[headerLen:], chunk)
			s.mu.Unlock()

			subdomain := encodeSubdomainB32(pkt)
			fullQuery := []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
			fullQuery = append(fullQuery, []byte(subdomain+"."+s.config.Domain+".")...)
			fullQuery = append(fullQuery, 0x00)
			tail := []byte{0x00, 0x10, 0x00, 0x01}
			fullQuery = append(fullQuery, tail...)

			s.conn.WriteToUDP(fullQuery, remoteAddr)

			if err != nil {
				s.mu.Lock()
				sess.Backend.Close()
				delete(s.sessions, sess.ID)
				s.mu.Unlock()
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *DNSTTServer) backendToClient(sess *DNSTTSession, remoteAddr *net.UDPAddr) {
}

func (s *DNSTTServer) sendDNSResponse(query, answerData []byte, remoteAddr *net.UDPAddr) {
	resp := buildDNSResponse(query, answerData, 16)
	if resp != nil {
		s.conn.WriteToUDP(resp, remoteAddr)
	}
}

func (s *DNSTTServer) sendNXDOMAIN(query []byte, remoteAddr *net.UDPAddr) {
	if len(query) < 12 {
		return
	}
	resp := make([]byte, 12)
	copy(resp, query[:12])
	binary.BigEndian.PutUint16(resp[2:], 0x8183)
	s.conn.WriteToUDP(resp, remoteAddr)
}

func (s *DNSTTServer) sendServerError(query []byte, remoteAddr *net.UDPAddr) {
	s.sendNXDOMAIN(query, remoteAddr)
}

func (s *DNSTTServer) Sessions() []DNSTTSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DNSTTSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, *sess)
	}
	return out
}

func (s *DNSTTServer) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func encodeSubdomainB32(data []byte) string {
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)
	var parts []string
	for i := 0; i < len(encoded); i += 63 {
		end := i + 63
		if end > len(encoded) {
			end = len(encoded)
		}
		parts = append(parts, strings.ToLower(encoded[i:end]))
	}
	return strings.Join(parts, ".")
}

func decodeSubdomainB32(subdomain string) ([]byte, error) {
	cleaned := strings.ReplaceAll(subdomain, ".", "")
	cleaned = strings.ToUpper(cleaned)
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleaned)
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

func buildDNSResponse(query []byte, answerData []byte, typeCode uint16) []byte {
	if len(query) < 12 {
		return nil
	}

	qdCount := binary.BigEndian.Uint16(query[4:])
	if qdCount == 0 {
		return nil
	}

	qnameEnd := 12
	for qnameEnd < len(query) && query[qnameEnd] != 0 {
		if query[qnameEnd] == 0xC0 {
			qnameEnd += 2
			break
		}
		qnameEnd += int(query[qnameEnd]) + 1
	}
	if qnameEnd < len(query) && query[qnameEnd] == 0 {
		qnameEnd++
	}
	qnameEnd += 4

	response := make([]byte, qnameEnd+4+2+2+4+2+len(answerData))

	copy(response, query[:qnameEnd])

	binary.BigEndian.PutUint16(response[2:], 0x8180)
	binary.BigEndian.PutUint16(response[4:], 1)
	binary.BigEndian.PutUint16(response[6:], 1)
	binary.BigEndian.PutUint16(response[8:], 0)
	binary.BigEndian.PutUint16(response[10:], 0)

	pos := qnameEnd
	response[pos] = 0xC0
	response[pos+1] = 0x0C
	binary.BigEndian.PutUint16(response[pos+2:], typeCode)
	binary.BigEndian.PutUint16(response[pos+4:], 1)
	binary.BigEndian.PutUint32(response[pos+6:], 300)
	binary.BigEndian.PutUint16(response[pos+10:], uint16(len(answerData)))
	copy(response[pos+12:], answerData)

	return response
}

func init() {
	_ = hex.EncodeToString
}
