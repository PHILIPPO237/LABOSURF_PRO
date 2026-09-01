package main

import (
	"net"
	"sync"
	"time"
)

type Session struct {
	ClientID      string
	Username      string
	UserConfig    UserConfig
	Authenticated bool

	RemoteAddr *net.UDPAddr

	// TunnelIP est l'adresse IPv4 virtuelle attribuée à cette session VPN.
	TunnelIP net.IP

	CreatedAt    time.Time
	LastActivity time.Time

	// Expiry est la date d'expiration du compte, pré-calculée à la
	// création de la session (zéro = pas d'expiration). Évite de
	// re-parser ExpiresAt à chaque paquet.
	Expiry time.Time

	BytesIn  uint64
	BytesOut uint64
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	timeout  time.Duration
}

func NewSessionManager(timeout time.Duration) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		timeout:  timeout,
	}
}

func (sm *SessionManager) Create(clientID string) *Session {
	return sm.CreateWithUser(clientID, "", UserConfig{}, nil)
}

func (sm *SessionManager) CreateWithAddr(
	clientID string,
	addr *net.UDPAddr,
) *Session {
	return sm.CreateWithUser(clientID, "", UserConfig{}, addr)
}

// CreateWithAddrAndUser reste disponible pour compatibilité : elle crée
// une session associée à un nom d'utilisateur mais sans détail de
// configuration (règles). Préférer CreateWithUser après authentification.
func (sm *SessionManager) CreateWithAddrAndUser(
	clientID string,
	username string,
	addr *net.UDPAddr,
) *Session {
	return sm.CreateWithUser(clientID, username, UserConfig{}, addr)
}

// CreateWithUser crée une session en la liant à son compte complet :
// le nom d'utilisateur ET les règles du compte (UserConfig).
//
// C'est la voie utilisée après une authentification réussie afin que la
// session sache en permanence :
//   - « Quel compte possède cette session ? »   (Username)
//   - « Quelles règles ce compte doit-il respecter ? »  (UserConfig)
func (sm *SessionManager) CreateWithUser(
	clientID string,
	username string,
	config UserConfig,
	addr *net.UDPAddr,
) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()

	var remoteAddr *net.UDPAddr

	if addr != nil {
		copyAddr := *addr
		remoteAddr = &copyAddr
	}

	session := &Session{
		ClientID:      clientID,
		Username:      username,
		UserConfig:    config,
		Authenticated: true,
		RemoteAddr:    remoteAddr,
		CreatedAt:     now,
		LastActivity:  now,
		Expiry:        parseExpiry(config.ExpiresAt),
	}

	sm.sessions[clientID] = session

	return session
}

// parseExpiry convertit ExpiresAt (RFC3339) en time.Time.
//
//   - vide      => zéro (pas d'expiration).
//   - illisible => date passée (compte considéré expiré, par prudence).
func parseExpiry(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}

	expires, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Unix(1, 0)
	}

	return expires
}

// expired indique si le compte lié à la session est expiré à l'instant now.
// L'appelant doit détenir le verrou du SessionManager.
func (s *Session) expired(now time.Time) bool {
	if s.Expiry.IsZero() {
		return false
	}

	return now.After(s.Expiry)
}

// quotaExceeded indique si le quota de trafic du compte est atteint.
// QuotaBytes == 0 signifie « illimité ».
// L'appelant doit détenir le verrou du SessionManager.
func (s *Session) quotaExceeded() bool {
	if s.UserConfig.QuotaBytes == 0 {
		return false
	}

	return s.BytesIn+s.BytesOut >= s.UserConfig.QuotaBytes
}

// TrafficDecision décrit le résultat d'un contrôle des règles de compte.
// La chaîne vide signifie « autorisé » ; les autres valeurs sont des codes
// de contrôle envoyés au client puis suivies d'une coupure propre.
type TrafficDecision string

const (
	TrafficAllowed   TrafficDecision = ""
	TrafficNoSession TrafficDecision = "NO_SESSION"
	TrafficExpired   TrafficDecision = "ACCOUNT_EXPIRED"
	TrafficQuota     TrafficDecision = "QUOTA_EXCEEDED"
)

// Authorize applique les règles du compte pour décider si la session peut
// encore faire transiter du trafic (expiration, quota). Le contrôle est
// atomique (sous verrou) afin d'être cohérent avec la comptabilisation.
func (sm *SessionManager) Authorize(clientID string) TrafficDecision {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[clientID]
	if !ok {
		return TrafficNoSession
	}

	if sm.timeout > 0 &&
		time.Since(session.LastActivity) > sm.timeout {
		return TrafficNoSession
	}

	if session.expired(time.Now()) {
		return TrafficExpired
	}

	if session.quotaExceeded() {
		return TrafficQuota
	}

	return TrafficAllowed
}

// AdmitDecision décrit le résultat d'un contrôle d'admission d'une nouvelle
// session (limites par utilisateur).
type AdmitDecision string

const (
	AdmitOK             AdmitDecision = ""
	AdmitMaxConnections AdmitDecision = "MAX_CONNECTIONS"
	AdmitMaxIPs         AdmitDecision = "MAX_IPS"
)

// sessionIP retourne l'adresse IP (sans port) associée à une session.
func sessionIP(clientID string, s *Session) string {
	if s != nil && s.RemoteAddr != nil {
		return s.RemoteAddr.IP.String()
	}

	if host, _, err := net.SplitHostPort(clientID); err == nil {
		return host
	}

	return clientID
}

// Admit vérifie si une nouvelle session peut être créée pour username depuis
// addr, en respectant les limites du compte (par utilisateur) :
//
//   - MaxConnections : nombre maximum de sessions simultanées.
//   - MaxIPs         : nombre maximum d'adresses IP distinctes ; une même
//     IP n'est jamais comptée deux fois.
//
// La session dont le clientID est identique est exclue du décompte car une
// nouvelle authentification depuis le même point de terminaison remplace la
// session existante (ce n'est pas une connexion supplémentaire).
// Une valeur de limite <= 0 signifie « illimité ».
func (sm *SessionManager) Admit(
	clientID string,
	username string,
	cfg UserConfig,
	addr *net.UDPAddr,
) AdmitDecision {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	newIP := ""
	if addr != nil {
		newIP = addr.IP.String()
	} else if host, _, err := net.SplitHostPort(clientID); err == nil {
		newIP = host
	}

	now := time.Now()

	count := 0
	ips := make(map[string]struct{})

	for id, sess := range sm.sessions {
		if sess.Username != username {
			continue
		}

		if id == clientID {
			continue // sera remplacée, pas une connexion supplémentaire
		}

		if sm.timeout > 0 &&
			now.Sub(sess.LastActivity) > sm.timeout {
			continue // session périmée : ne compte pas
		}

		count++
		ips[sessionIP(id, sess)] = struct{}{}
	}

	// MaxConnections : la nouvelle session incluse.
	if cfg.MaxConnections > 0 && count+1 > cfg.MaxConnections {
		return AdmitMaxConnections
	}

	// MaxIPs : seulement si l'IP n'est pas déjà utilisée par le compte.
	if cfg.MaxIPs > 0 {
		if _, exists := ips[newIP]; !exists {
			if len(ips)+1 > cfg.MaxIPs {
				return AdmitMaxIPs
			}
		}
	}

	return AdmitOK
}

func (sm *SessionManager) SetUserConfig(clientID string, config UserConfig) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	session, ok := sm.sessions[clientID]
	if !ok {
		return false
	}
	session.UserConfig = config
	return true
}

func (sm *SessionManager) Get(clientID string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[clientID]
	if !ok {
		return nil, false
	}

	if sm.timeout > 0 &&
		time.Since(session.LastActivity) > sm.timeout {
		return nil, false
	}

	return session, true
}

func (sm *SessionManager) Touch(clientID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[clientID]
	if !ok {
		return false
	}

	if sm.timeout > 0 &&
		time.Since(session.LastActivity) > sm.timeout {
		delete(sm.sessions, clientID)
		return false
	}

	session.LastActivity = time.Now()

	return true
}

func (sm *SessionManager) SetRemoteAddr(
	clientID string,
	addr *net.UDPAddr,
) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[clientID]
	if !ok {
		return false
	}

	if addr == nil {
		session.RemoteAddr = nil
		return true
	}

	copyAddr := *addr
	session.RemoteAddr = &copyAddr
	session.LastActivity = time.Now()

	return true
}

func (sm *SessionManager) RemoteAddr(
	clientID string,
) (*net.UDPAddr, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[clientID]
	if !ok || session.RemoteAddr == nil {
		return nil, false
	}

	if sm.timeout > 0 &&
		time.Since(session.LastActivity) > sm.timeout {
		return nil, false
	}

	addr := *session.RemoteAddr

	return &addr, true
}

func (sm *SessionManager) AddBytesIn(
	clientID string,
	n uint64,
) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[clientID]
	if !ok {
		return false
	}

	session.BytesIn += n
	session.LastActivity = time.Now()

	return true
}

func (sm *SessionManager) AddBytesOut(
	clientID string,
	n uint64,
) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[clientID]
	if !ok {
		return false
	}

	session.BytesOut += n
	session.LastActivity = time.Now()

	return true
}

func (sm *SessionManager) Usage(
	clientID string,
) (uint64, uint64, uint64, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[clientID]
	if !ok {
		return 0, 0, 0, false
	}

	total := session.BytesIn + session.BytesOut

	return session.BytesIn, session.BytesOut, total, true
}

// UsageByUser agrège la consommation de toutes les sessions actives d'un
// même compte (BytesIn, BytesOut, total).
func (sm *SessionManager) UsageByUser(
	username string,
) (in uint64, out uint64, total uint64) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, session := range sm.sessions {
		if session.Username != username {
			continue
		}

		in += session.BytesIn
		out += session.BytesOut
	}

	total = in + out

	return in, out, total
}

// ActiveIPs retourne le nombre de connexions actives et la liste des IPs
// distinctes pour un compte donné. Utilisé par le portail pour afficher
// l'état réel des connexions.
func (sm *SessionManager) ActiveIPs(username string) (count int, ips []string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	seen := make(map[string]struct{})
	for _, session := range sm.sessions {
		if session.Username != username {
			continue
		}
		count++
		ip := sessionIP("", session)
		if ip != "" {
			if _, exists := seen[ip]; !exists {
				seen[ip] = struct{}{}
				ips = append(ips, ip)
			}
		}
	}
	return count, ips
}

func (sm *SessionManager) Username(clientID string) (string, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[clientID]
	if !ok {
		return "", false
	}

	return session.Username, true
}

func (sm *SessionManager) Remove(clientID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.sessions, clientID)
}

func (sm *SessionManager) Cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.timeout <= 0 {
		return
	}

	now := time.Now()

	for clientID, session := range sm.sessions {
		if now.Sub(session.LastActivity) > sm.timeout {
			delete(sm.sessions, clientID)
		}
	}
}

func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.sessions)
}
