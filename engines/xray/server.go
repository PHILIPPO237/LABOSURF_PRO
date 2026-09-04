package xray

import (
	"context"
	"crypto/cipher"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	defaultXrayPort = 443
	ProtocolVLESS   = "vless"
	ProtocolVMess   = "vmess"
	ProtocolTrojan  = "trojan"
	ProtocolShadowsocks = "shadowsocks"
)

type XrayConfig struct {
	Port     int        `json:"port"`
	Protocol string     `json:"protocol"`
	Users    []XrayUser `json:"users"`
}

type XrayUser struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Flow    string `json:"flow"`
	Enabled bool   `json:"enabled"`
	Password string `json:"password,omitempty"`
	Method  string `json:"method,omitempty"`
}

type XraySession struct {
	ID        string
	User      string
	RemoteAddr string
	StartedAt time.Time
	BytesIn   int64
	BytesOut  int64
}

type XrayServer struct {
	config   XrayConfig
	listener net.Listener
	mu       sync.RWMutex
	sessions map[string]*XraySession
	cancel   context.CancelFunc
}

func NewXrayServer(cfg XrayConfig) (*XrayServer, error) {
	return &XrayServer{
		config:   cfg,
		sessions: make(map[string]*XraySession),
	}, nil
}

func loadXrayConfig(path string) (XrayConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return XrayConfig{}, fmt.Errorf("lecture config Xray : %w", err)
	}

	var cfg XrayConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return XrayConfig{}, fmt.Errorf("config Xray invalide : %w", err)
	}

	if cfg.Port <= 0 {
		cfg.Port = defaultXrayPort
	}
	if cfg.Protocol == "" {
		cfg.Protocol = ProtocolVLESS
	}

	return cfg, nil
}

func (s *XrayServer) findUser(id string) (*XrayUser, bool) {
	for i := range s.config.Users {
		u := &s.config.Users[i]
		if !u.Enabled {
			continue
		}
		if u.ID == id {
			return u, true
		}
		if u.Password == id {
			return u, true
		}
	}
	return nil, false
}

func (s *XrayServer) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	addr := fmt.Sprintf(":%d", s.config.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("écoute TCP %s : %w", addr, err)
	}
	s.listener = ln
	log.Printf("✔ Xray-like Engine natif démarré sur %s (protocole: %s)", addr, s.config.Protocol)

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
				log.Printf("erreur accept Xray : %v", err)
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *XrayServer) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	switch s.config.Protocol {
	case ProtocolVLESS:
		s.handleVLESS(conn)
	case ProtocolVMess:
		s.handleVMess(conn)
	case ProtocolTrojan:
		s.handleTrojan(conn)
	case ProtocolShadowsocks:
		s.handleShadowsocks(conn)
	default:
		log.Printf("Xray : protocole inconnu : %s", s.config.Protocol)
	}
}

func (s *XrayServer) handleVLESS(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	header := make([]byte, 1)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if header[0] != 0 {
		return
	}

	uuidLen := make([]byte, 1)
	if _, err := io.ReadFull(conn, uuidLen); err != nil {
		return
	}
	if uuidLen[0] != 16 {
		return
	}

	uuidBytes := make([]byte, 16)
	if _, err := io.ReadFull(conn, uuidBytes); err != nil {
		return
	}
	uuid := formatUUID(uuidBytes)

	user, ok := s.findUser(uuid)
	if !ok {
		log.Printf("Xray : accès refusé pour l'UUID %s", uuid)
		return
	}

	addon := make([]byte, 1)
	if _, err := io.ReadFull(conn, addon); err != nil {
		return
	}

	cmd := make([]byte, 16)
	if _, err := io.ReadFull(conn, cmd); err != nil {
		return
	}

	if cmd[0] != 1 {
		return
	}

	port := binary.BigEndian.Uint16(cmd[1:3])
	addrType := cmd[3]

	var host string
	switch addrType {
	case 1:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 2:
		dlen := int(cmd[4])
		domain := make([]byte, dlen)
		if _, err := io.ReadFull(conn, domain); err != nil {
			return
		}
		host = string(domain)
	case 3:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	default:
		return
	}

	conn.SetReadDeadline(time.Time{})

	sess := &XraySession{
		ID:         fmt.Sprintf("%s-%d", user.ID, time.Now().UnixNano()),
		User:       user.Email,
		RemoteAddr: conn.RemoteAddr().String(),
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

	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	backend, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("Xray : impossible de joindre %s : %v", target, err)
		return
	}
	defer backend.Close()

	log.Printf("✔ Xray : %s → %s (via %s)", user.Email, target, conn.RemoteAddr())

	done := make(chan struct{}, 2)
	go pipeConn(conn, backend, &sess.BytesIn, done)
	go pipeConn(backend, conn, &sess.BytesOut, done)
	<-done
}

func (s *XrayServer) handleVMess(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	ver := make([]byte, 1)
	if _, err := io.ReadFull(conn, ver); err != nil {
		return
	}
	_ = ver

	ivLen := make([]byte, 1)
	if _, err := io.ReadFull(conn, ivLen); err != nil {
		return
	}
	iv := make([]byte, ivLen[0])
	if _, err := io.ReadFull(conn, iv); err != nil {
		return
	}

	keyLen := make([]byte, 1)
	if _, err := io.ReadFull(conn, keyLen); err != nil {
		return
	}
	key := make([]byte, keyLen[0])
	if _, err := io.ReadFull(conn, key); err != nil {
		return
	}

	_ = key
	_ = iv
	s.handleFallbackProxy(conn)
}

func (s *XrayServer) handleTrojan(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	payload := make([]byte, 56)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return
	}

	password := string(payload[:56])

	user, ok := s.findUser(password)
	if !ok {
		log.Printf("Trojan : mot de passe invalide")
		return
	}

	crlf := make([]byte, 2)
	if _, err := io.ReadFull(conn, crlf); err != nil {
		return
	}

	cmd := make([]byte, 1)
	if _, err := io.ReadFull(conn, cmd); err != nil {
		return
	}

	if cmd[0] != 1 {
		return
	}

	addrType := make([]byte, 1)
	if _, err := io.ReadFull(conn, addrType); err != nil {
		return
	}

	var host string
	switch addrType[0] {
	case 1:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 2:
		dlen := make([]byte, 1)
		if _, err := io.ReadFull(conn, dlen); err != nil {
			return
		}
		domain := make([]byte, dlen[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return
		}
		host = string(domain)
	case 3:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	default:
		return
	}

	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBytes)

	crlf2 := make([]byte, 2)
	if _, err := io.ReadFull(conn, crlf2); err != nil {
		return
	}

	conn.SetReadDeadline(time.Time{})

	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	backend, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("Trojan : impossible de joindre %s : %v", target, err)
		return
	}
	defer backend.Close()

	log.Printf("✔ Trojan : %s → %s", user.ID, target)

	sess := &XraySession{
		ID:        fmt.Sprintf("trojan-%d", time.Now().UnixNano()),
		User:      user.ID,
		RemoteAddr: conn.RemoteAddr().String(),
		StartedAt: time.Now(),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sessions, sess.ID)
		s.mu.Unlock()
	}()

	done := make(chan struct{}, 2)
	go pipeConn(conn, backend, &sess.BytesIn, done)
	go pipeConn(backend, conn, &sess.BytesOut, done)
	<-done
}

func (s *XrayServer) handleShadowsocks(conn net.Conn) {
	s.handleFallbackProxy(conn)
}

func (s *XrayServer) handleFallbackProxy(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	addrType := make([]byte, 1)
	if _, err := io.ReadFull(conn, addrType); err != nil {
		return
	}

	var host string
	switch addrType[0] {
	case 1:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 2:
		dlen := make([]byte, 1)
		if _, err := io.ReadFull(conn, dlen); err != nil {
			return
		}
		domain := make([]byte, dlen[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return
		}
		host = string(domain)
	case 3:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	default:
		return
	}

	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBytes)

	conn.SetReadDeadline(time.Time{})

	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	backend, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("Proxy : impossible de joindre %s : %v", target, err)
		return
	}
	defer backend.Close()

	done := make(chan struct{}, 2)
	go pipeConn(conn, backend, nil, done)
	go pipeConn(backend, conn, nil, done)
	<-done
}

func formatUUID(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func md5sum(data []byte) []byte {
	h := md5.Sum(data)
	return h[:]
}

var _ cipher.Stream

func pipeConn(src, dst net.Conn, counter *int64, done chan struct{}) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if counter != nil {
				*counter += int64(n)
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	done <- struct{}{}
}

func (s *XrayServer) Sessions() []XraySession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]XraySession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, *sess)
	}
	return out
}

func (s *XrayServer) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}
