package dnstt

import (
	"context"
	"crypto/ed25519"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	mrand "math/rand"
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

	// JitterMs : délai aléatoire maximal (millisecondes) appliqué avant
	// chaque réponse ACK, pour décorréler le rythme des échanges DNS et
	// rester sous les seuils de détection des DPI. 0 = pas de jitter
	// (comportement historique). Valeur recommandée : 30-80 ms.
	JitterMs int `json:"jitter_ms,omitempty"`
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
	config    DNSTTConfig
	conn      *net.UDPConn
	sessions  map[string]*DNSTTSession
	mu        sync.RWMutex
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
// l'un des deux reçoit "use of closed network connection", ce qui
// remontait auparavant comme une erreur de Close() (donc de Stop()/
// Restart()) même quand l'arrêt s'est en réalité bien passé.
func (s *DNSTTServer) closeConn() error {
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
	// s.cancel protégé par s.mu : Close() peut être appelé depuis une autre
	// goroutine juste après le démarrage de Run() (même correctif que
	// engines/hysteria et engines/slowdns, où -race l'a mis en évidence).
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	log.Printf("✔ DNSTT Engine natif démarré sur :%d (domaine: %s)", s.config.Port, s.config.Domain)

	go func() {
		<-ctx.Done()
		s.closeConn()
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

	subdomain := extractSubdomain(query[12:], s.config.Domain)
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
		// Authentification : le premier paquet d'une nouvelle session doit
		// porter, en tête du payload, une signature ed25519 de sessionID
		// vérifiable avec la clé publique d'un utilisateur activé. Sans
		// cela, AUCUNE session n'est créée et le backend n'est jamais
		// contacté — une version antérieure ignorait entièrement
		// PublicKey/PrivateKey et acceptait tout paquet correctement
		// formé, de n'importe quel client non authentifié.
		if len(payload) < ed25519.SignatureSize {
			s.mu.Unlock()
			s.sendNXDOMAIN(query, remoteAddr)
			return
		}
		signature := payload[:ed25519.SignatureSize]
		innerPayload := payload[ed25519.SignatureSize:]

		user := s.findUserBySessionSignature(sessionID, signature)
		if user == "" {
			s.mu.Unlock()
			log.Printf("DNSTT : authentification refusée pour %s (signature invalide ou clé inconnue)", remoteAddr)
			s.sendNXDOMAIN(query, remoteAddr)
			return
		}

		backend, err := net.Dial("tcp", s.config.Backend)
		if err != nil {
			log.Printf("DNSTT : impossible de joindre le backend %s : %v", s.config.Backend, err)
			s.mu.Unlock()
			s.sendServerError(query, remoteAddr)
			return
		}
		sess = &DNSTTSession{
			ID:        sessionID,
			User:      user,
			ClientIP:  remoteAddr.String(),
			StartedAt: time.Now(),
			Backend:   backend,
		}
		s.sessions[sessionID] = sess
		go s.backendLoop(sess, remoteAddr)
		payload = innerPayload
		log.Printf("✔ DNSTT : %s authentifié depuis %s, backend connecté", user, remoteAddr)
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
	time.Sleep(jitterDelay(s.config.JitterMs))
	s.sendDNSResponse(query, ack, remoteAddr)
}

// jitterDelay retourne un délai uniformément aléatoire dans [0, maxMs]
// millisecondes, appliqué avant chaque réponse DNS lorsque le serveur est
// configuré avec jitter_ms > 0. Un rythme de réponses trop régulier est le
// premier signal que les DPI utilisent pour identifier un tunnel DNS ; la
// variance aléatoire maintient le trafic sous ce seuil. maxMs <= 0 (valeur
// par défaut) préserve le comportement historique : aucune latence ajoutée.
func jitterDelay(maxMs int) time.Duration {
	if maxMs <= 0 {
		return 0
	}
	return time.Duration(mrand.Intn(maxMs+1)) * time.Millisecond
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

			// Paquet "query-shaped" poussé de façon asynchrone au client
			// (comme pour engines/hysteria, ce n'est pas du vrai DNS
			// standard consommé par un résolveur — juste une trame au
			// format DNS-like propriétaire). Le nom DOIT être encodé au
			// format fil DNS (labels préfixés par leur longueur) : une
			// version antérieure concaténait directement la chaîne
			// "sous-domaine.domaine." avec ses points ASCII littéraux,
			// ce qui ne peut être interprété comme un QNAME par aucun
			// analyseur — les données poussées au client n'étaient donc
			// jamais exploitables.
			subdomain := encodeSubdomainB32(pkt)
			fullQuery := []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
			fullQuery = append(fullQuery, encodeDNSName(subdomain+"."+s.config.Domain)...)
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

// findUserBySessionSignature retourne le nom de l'utilisateur activé dont
// la clé publique valide la signature (ed25519) du sessionID — ou "" si
// aucune ne correspond. Contrairement à un mécanisme qui accepterait "le
// premier utilisateur valide" indépendamment de la signature reçue (un
// anti-pattern déjà identifié ailleurs dans ce dépôt, pour SlowDNS), la
// signature est réellement vérifiée contre CHAQUE clé publique activée :
// seul le détenteur de la clé privée correspondante peut passer.
func (s *DNSTTServer) findUserBySessionSignature(sessionID string, signature []byte) string {
	msg := []byte(sessionID)
	for _, u := range s.config.Users {
		if !u.Enabled {
			continue
		}
		pubKeyBytes, err := hex.DecodeString(u.PublicKey)
		if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
			continue
		}
		if ed25519.Verify(ed25519.PublicKey(pubKeyBytes), msg, signature) {
			return u.User
		}
	}
	return ""
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

// Addr retourne l'adresse UDP réellement liée par ce serveur, et true tant
// qu'il n'a pas été arrêté. conn est ouvert de façon synchrone dans
// NewDNSTTServer (avant même que Run() ne soit lancé en goroutine), donc
// l'adresse est déjà réelle et disponible dès que le constructeur a réussi.
func (s *DNSTTServer) Addr() (*net.UDPAddr, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.conn == nil {
		return nil, false
	}
	addr, ok := s.conn.LocalAddr().(*net.UDPAddr)
	return addr, ok
}

func (s *DNSTTServer) Close() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.closeConn()
}

// encodeDNSName encode un nom pointillé ("a.b.c") au format fil DNS :
// une suite de labels préfixés par leur longueur, terminée par un octet
// nul. Chaque label doit faire au plus 63 octets (respecté ici car
// encodeSubdomainB32 découpe déjà ses labels de données à 63 caractères).
func encodeDNSName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			continue
		}
		out = append(out, byte(len(label)))
		out = append(out, []byte(label)...)
	}
	return append(out, 0x00)
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

// extractSubdomain reconstruit la chaîne base32 complète à partir du QNAME,
// en retirant les labels correspondant au domaine configuré. Une version
// antérieure ne retournait que parts[0] (le premier label DNS, 63
// caractères max) : dès que le payload dépasse ~39 octets (courant une
// fois la signature ed25519 d'authentification de 64 octets ajoutée), les
// données encodées débordent sur plusieurs labels et étaient tronquées
// silencieusement. Voir le même correctif dans engines/slowdns.
func extractSubdomain(qname []byte, domain string) string {
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
	if len(parts) == 0 {
		return ""
	}

	dataLabels := parts
	if domain != "" {
		suffix := strings.Split(strings.Trim(domain, "."), ".")
		if len(parts) > len(suffix) {
			matches := true
			offset := len(parts) - len(suffix)
			for i, want := range suffix {
				if !strings.EqualFold(parts[offset+i], want) {
					matches = false
					break
				}
			}
			if matches {
				dataLabels = parts[:offset]
			}
		}
	}
	if len(dataLabels) > 0 {
		return strings.Join(dataLabels, ".")
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

