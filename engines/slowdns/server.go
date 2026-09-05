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
	ID         string
	User       string
	PublicKey  ed25519.PublicKey
	ClientIP   string
	StartedAt  time.Time
	BytesIn    int64
	BytesOut   int64
	Backend    net.Conn
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

	// Format: [16 bytes sessionID][64 bytes signature][payload...]
	if len(data) < 16+64 {
		s.sendNXDOMAIN(query, remoteAddr)
		return
	}

	sessionID := hex.EncodeToString(data[:16])
	signature := data[16:80]
	payload := data[80:]

	s.mu.RLock()
	sess, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		// Nouvelle session : vérifier la signature avec la clé publique de l'utilisateur
		user, pubKey := s.findUserBySessionID(sessionID)
		if user == "" || pubKey == nil || !ed25519.Verify(pubKey, payload, signature) {
			s.sendNXDOMAIN(query, remoteAddr)
			return
		}

		// Connecter au backend TCP
		backend, err := net.Dial("tcp", s.config.Backend)
		if err != nil {
			log.Printf("SlowDNS : impossible de joindre backend %s : %v", s.config.Backend, err)
			s.sendServerError(query, remoteAddr)
			return
		}

		sess = &SlowDNSSession{
			ID:        sessionID,
			User:      user,
			PublicKey: pubKey,
			ClientIP:  remoteAddr.String(),
			StartedAt: time.Now(),
			Backend:   backend,
		}
		s.mu.Lock()
		s.sessions[sessionID] = sess
		s.mu.Unlock()

		// Lancer la boucle de lecture du backend
		go s.backendLoop(sess, remoteAddr)
	}

	// Vérifier la signature pour chaque paquet
	if !ed25519.Verify(sess.PublicKey, payload, signature) {
		log.Printf("SlowDNS : signature invalide pour session %s", sessionID)
		s.sendNXDOMAIN(query, remoteAddr)
		return
	}

	sess.BytesIn += int64(len(payload))

	// Écrire le payload vers le backend
	if sess.Backend != nil && len(payload) > 0 {
		if _, err := sess.Backend.Write(payload); err != nil {
			log.Printf("SlowDNS : erreur écriture backend : %v", err)
			s.sendServerError(query, remoteAddr)
			return
		}
	}

	// Lire la réponse du backend (non-bloquant via backendLoop)
	// La réponse sera envoyée via backendLoop -> sendDNSResponse
	_ = remoteAddr // utilisé dans backendLoop
}

func (s *SlowDNSServer) backendLoop(sess *SlowDNSSession, remoteAddr *net.UDPAddr) {
	buf := make([]byte, 65535)
	for {
		n, err := sess.Backend.Read(buf)
		if n > 0 {
			responseData := buf[:n]
			resp := buildDNSResponseForPayload(sess.ID, responseData)
			if resp != nil {
				s.conn.WriteToUDP(resp, remoteAddr)
			}
			sess.BytesOut += int64(n)
		}
		if err != nil {
			break
		}
	}
	s.mu.Lock()
	delete(s.sessions, sess.ID)
	if sess.Backend != nil {
		sess.Backend.Close()
	}
	s.mu.Unlock()
}

func (s *SlowDNSServer) findUserBySessionID(sessionID string) (string, ed25519.PublicKey) {
	// Le sessionID est les 16 premiers bytes encodés en hex (32 chars)
	// On cherche l'utilisateur dont la clé publique correspond
	for _, u := range s.config.Users {
		if !u.Enabled {
			continue
		}
		pubKeyBytes, err := hex.DecodeString(u.PublicKey)
		if err != nil {
			continue
		}
		if len(pubKeyBytes) != ed25519.PublicKeySize {
			continue
		}
		// Pour simplifier, on accepte le premier utilisateur valide
		// Dans un vrai déploiement, le sessionID serait dérivé de la clé publique
		return u.User, ed25519.PublicKey(pubKeyBytes)
	}
	return "", nil
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

func (s *SlowDNSServer) sendServerError(query []byte, remoteAddr *net.UDPAddr) {
	resp := make([]byte, 12)
	copy(resp, query[:12])
	resp[2] = 0x81
	resp[3] = 0x85
	s.conn.WriteToUDP(resp, remoteAddr)
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

func generateEd25519Keypair() (pubHex, privHex string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(pub), hex.EncodeToString(priv), nil
}
