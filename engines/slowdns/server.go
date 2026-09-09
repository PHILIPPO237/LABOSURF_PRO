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
	"strings"
	"sync"
	"time"
)

type SlowDNSServer struct {
	config    SlowDNSConfig
	conn      *net.UDPConn
	sessions  map[string]*SlowDNSSession
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
func (s *SlowDNSServer) closeConn() error {
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

type SlowDNSSession struct {
	ID         string
	rawID      []byte // les mêmes 16 octets que ID, mais bruts (pas hex)
	User       string
	PublicKey  ed25519.PublicKey
	ClientIP   string
	StartedAt  time.Time
	BytesIn    int64
	BytesOut   int64
	Backend    net.Conn

	// outMu protège pendingOut : DNS est requête/réponse, il n'y a donc
	// pas de moyen d'envoyer une donnée retour au client autrement qu'en
	// réponse à une de ses requêtes (poll). backendLoop empile les octets
	// lus du backend ici ; handleDNSQuery les dépile et les renvoie dans
	// la réponse à la PROCHAINE requête reçue du client pour cette session
	// (qui peut être une requête de poll sans nouvelle donnée à envoyer).
	// Pointeur (et non valeur) car Sessions() copie *SlowDNSSession par
	// valeur pour renvoyer un instantané en lecture seule — copier un
	// sync.Mutex par valeur est incorrect (go vet le signale).
	outMu      *sync.Mutex
	pendingOut []byte
}

// maxDNSAnswerPayload borne la taille d'un paquet retour inclus dans une
// réponse DNS unique : le champ de longueur RDATA de buildDNSResponse est
// un uint16 (65535 max), et rester sous cette taille pour un datagramme UDP
// évite la fragmentation IP. Le reste d'un pendingOut plus gros que ça
// reste en attente et part au prochain poll.
const maxDNSAnswerPayload = 4096

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
	// s.cancel protégé par s.mu : Close() peut être appelé depuis une autre
	// goroutine juste après le démarrage de Run() (voir le même correctif
	// dans engines/hysteria, où -race l'a mis en évidence).
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	log.Printf("✔ SlowDNS Engine natif démarré sur :%d (domaine: %s)", s.config.Port, s.config.Domain)

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
		go s.handleDNSQuery(buf[:n], remoteAddr)
	}
}

func (s *SlowDNSServer) handleDNSQuery(query []byte, remoteAddr *net.UDPAddr) {
	if len(query) < 12 {
		return
	}

	// En-tête DNS (RFC1035 §4.1.1) : ID(0-1) FLAGS(2-3) QDCOUNT(4-5)
	// ANCOUNT(6-7) NSCOUNT(8-9) ARCOUNT(10-11). QDCOUNT est donc à
	// l'offset 4, pas 6 (offset 6 = ANCOUNT, toujours 0 dans une requête
	// entrante légitime) — lire le mauvais offset faisait rejeter TOUTE
	// requête correctement formée dès cette première vérification.
	qdCount := binary.BigEndian.Uint16(query[4:])
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

	subdomain := extractSubdomain(query[12:], s.config.Domain)
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

		rawID := make([]byte, 16)
		copy(rawID, data[:16])
		sess = &SlowDNSSession{
			ID:        sessionID,
			rawID:     rawID,
			User:      user,
			PublicKey: pubKey,
			ClientIP:  remoteAddr.String(),
			StartedAt: time.Now(),
			Backend:   backend,
			outMu:     &sync.Mutex{},
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

	// Répondre à CETTE requête avec les données backend->client en attente
	// (s'il y en a). DNS étant requête/réponse, c'est le seul moyen de les
	// faire parvenir au client : elles ont été accumulées de façon
	// asynchrone par backendLoop et sont dépilées ici, dans la réponse à
	// la requête réellement reçue (query, remoteAddr) — jamais via un
	// paquet UDP non sollicité. Si rien n'est en attente, on répond quand
	// même avec un payload vide : une réponse DNS valide sans donnée,
	// pour que le client sache que le poll a réussi et puisse réessayer.
	out := sess.drainPendingOut()
	resp := buildDNSResponseForPayload(query, sess.rawID, out)
	if resp == nil {
		log.Printf("SlowDNS : construction de la réponse échouée pour %s", sessionID)
		return
	}
	if _, err := s.conn.WriteToUDP(resp, remoteAddr); err != nil {
		log.Printf("SlowDNS : erreur envoi réponse à %s : %v", remoteAddr, err)
	}
}

// backendLoop lit en continu les données que le backend TCP renvoie
// (SSH, ou tout autre service tunnelé) et les met en attente pour le
// client. Il ne peut PAS les envoyer directement : DNS est un protocole
// requête/réponse, il n'y a pas de socket "retour" vers le client tant
// qu'il n'a pas lui-même émis une nouvelle requête (voir handleDNSQuery,
// qui dépile pendingOut et répond avec la vraie requête DNS reçue).
func (s *SlowDNSServer) backendLoop(sess *SlowDNSSession, remoteAddr *net.UDPAddr) {
	buf := make([]byte, 65535)
	for {
		n, err := sess.Backend.Read(buf)
		if n > 0 {
			sess.outMu.Lock()
			sess.pendingOut = append(sess.pendingOut, buf[:n]...)
			sess.outMu.Unlock()
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

// drainPendingOut retire jusqu'à maxDNSAnswerPayload octets de
// sess.pendingOut (FIFO) — le reste, s'il y en a, attend le prochain poll.
func (sess *SlowDNSSession) drainPendingOut() []byte {
	sess.outMu.Lock()
	defer sess.outMu.Unlock()
	if len(sess.pendingOut) == 0 {
		return nil
	}
	n := len(sess.pendingOut)
	if n > maxDNSAnswerPayload {
		n = maxDNSAnswerPayload
	}
	chunk := make([]byte, n)
	copy(chunk, sess.pendingOut[:n])
	sess.pendingOut = sess.pendingOut[n:]
	return chunk
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

// extractSubdomain reconstruit la chaîne de données encodées en base32 à
// partir du QNAME de la requête DNS, en retirant les labels correspondant
// au domaine configuré (domain).
//
// Le payload minimum du protocole (16 octets de sessionID + 64 octets de
// signature = 80 octets, avant tout payload applicatif) encode en base32
// sur ~128 caractères — encodeSubdomain le répartit obligatoirement sur
// AU MOINS 3 labels DNS (limite de 63 caractères par label). Une version
// antérieure de cette fonction ne retournait QUE parts[0] (le premier
// label, ~63 caractères max, ~39 octets décodés) : la vérification
// `len(data) < 16+64` dans handleDNSQuery échouait alors systématiquement,
// pour TOUTE requête, quelle que soit sa taille — le chemin aller du
// tunnel n'a donc jamais pu fonctionner. On rejoint maintenant tous les
// labels de données (tous sauf le suffixe correspondant à domain).
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

func (s *SlowDNSServer) Sessions() []SlowDNSSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SlowDNSSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, *sess)
	}
	return out
}

// Addr retourne l'adresse UDP réellement liée par ce serveur, et true tant
// qu'il n'a pas été arrêté. conn est ouvert de façon synchrone dans
// NewSlowDNSServer (avant même que Run() ne soit lancé en goroutine), donc
// l'adresse est déjà réelle et disponible dès que le constructeur a réussi.
func (s *SlowDNSServer) Addr() (*net.UDPAddr, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.conn == nil {
		return nil, false
	}
	addr, ok := s.conn.LocalAddr().(*net.UDPAddr)
	return addr, ok
}

func (s *SlowDNSServer) Close() error {
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

func generateEd25519Keypair() (pubHex, privHex string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(pub), hex.EncodeToString(priv), nil
}
