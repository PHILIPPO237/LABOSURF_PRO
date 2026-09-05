package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"labosurf/internal/store"
)

const (
	sessionTimeout = time.Minute
	tcpDialTimeout = 10 * time.Second
)

type Server struct {
	config     Config
	conn       *net.UDPConn
	auth       *AuthManager
	sessions   *SessionManager
	tun        TunnelDevice
	tunnelPool *TunnelIPPool

	mu      sync.Mutex
	streams map[string]net.Conn

	closeOnce sync.Once
	store     *store.Store // pour persistance quota
}

func NewServer(config Config, st *store.Store) (*Server, error) {
	addr, err := net.ResolveUDPAddr("udp", config.Listen)
	if err != nil {
		return nil, fmt.Errorf("adresse UDP invalide : %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf(
			"impossible d'ouvrir UDP %s : %w",
			config.Listen,
			err,
		)
	}

	var tunnelPool *TunnelIPPool
	if config.TUN.Address != "" {
		tunnelPool, err = NewTunnelIPPool(config.TUN.Address)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf(
				"initialisation du pool IP tunnel : %w",
				err,
			)
		}
	}

	return &Server{
		config:     config,
		conn:       conn,
		auth:       NewAuthManager(config.Auth.Users),
		sessions:   NewSessionManager(sessionTimeout),
		streams:    make(map[string]net.Conn),
		tunnelPool: tunnelPool,
		store:      st,
	}, nil
}

func (s *Server) backendAddress() string {
	if address := os.Getenv("LABOSURF_TCP_BACKEND"); address != "" {
		return address
	}

	return "127.0.0.1:22"
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}

	var closeErr error

	s.closeOnce.Do(func() {
		s.mu.Lock()

		for clientID, conn := range s.streams {
			_ = conn.Close()
			delete(s.streams, clientID)
		}

		s.mu.Unlock()

		if s.tun != nil {
			if err := s.tun.Close(); err != nil {
				log.Printf("fermeture TUN : %v", err)
			}
			s.tun = nil
		}

		if s.conn != nil {
			// On ferme la socket mais on ne remet PAS s.conn à nil :
			// des goroutines (readTCPStream, forwardToTCP) peuvent encore
			// l'utiliser. Une socket fermée renvoie une erreur au lieu de
			// provoquer un déréférencement de pointeur nil. closeOnce
			// garantit que la fermeture n'a lieu qu'une seule fois.
			closeErr = s.conn.Close()
		}
	})

	return closeErr
}

func (s *Server) Run(ctx context.Context) error {
	if s == nil || s.conn == nil {
		return net.ErrClosed
	}

	log.Println("==========================================")
	log.Println("       LABOSURF PRO — UDP Engine")
	log.Println("       LABORATOIRE DU FREESURF")
	log.Println("==========================================")
	log.Printf("Serveur UDP : %s", s.conn.LocalAddr())

	if s.tun != nil {
		log.Printf("Mode VPN    : TUN (%s)", s.tun.Name())
		log.Printf("Backend TCP : %s (désactivé en mode VPN)", s.backendAddress())
	} else {
		log.Printf("Mode        : Proxy TCP → %s", s.backendAddress())
	}

	log.Printf("Mode auth   : %s", s.config.Auth.Mode)
	log.Printf(
		"Utilisateurs configurés : %d",
		len(s.config.Auth.Users),
	)
	log.Printf("Expiration session : %s", sessionTimeout)
	log.Println("UDP Engine démarré.")

	// Démarrer le loop TUN si l'interface est disponible.
	// Ce goroutine lit les paquets IP depuis TUN et les envoie aux clients UDP.
	if s.tun != nil {
		go s.tunLoop(ctx)
	}

	buffer := make([]byte, maxUDPPacketSize)

	for {
		select {
		case <-ctx.Done():
			log.Println("Arrêt UDP Engine...")
			return nil
		default:
		}

		_ = s.conn.SetReadDeadline(
			time.Now().Add(500 * time.Millisecond),
		)

		n, client, err := s.conn.ReadFromUDP(buffer)

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				for _, clientID := range s.sessions.Cleanup() {
					s.removeSession(clientID)
				}
				s.cleanupExpiredStreams()
				continue
			}

			if ctx.Err() != nil {
				return nil
			}

			return fmt.Errorf("lecture UDP : %w", err)
		}

		if n == 0 {
			continue
		}

		packet := append([]byte(nil), buffer[:n]...)

		s.handlePacket(client, packet)
	}
}

// tunLoop lit les paquets IP depuis l'interface TUN et les envoie aux clients
// UDP appropriés. C'est le chemin retour du VPN : Internet → TUN → Serveur → Client.
//
// Chaque paquet lu depuis TUN contient un en-tête IP. La destination IP détermine
// quel client doit recevoir le paquet (via le pool d'IP virtuelles).
func (s *Server) tunLoop(ctx context.Context) {
	log.Println("[tunLoop] Démarrage du loop TUN → UDP")

	// Buffer de 65535 octets pour les paquets IP (max théorique)
	buffer := make([]byte, 65535)

	for {
		select {
		case <-ctx.Done():
			log.Println("[tunLoop] Arrêt du loop TUN")
			return
		default:
		}

		// Lire un paquet IP depuis le TUN
		n, err := s.tun.Read(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[tunLoop] Erreur lecture TUN : %v", err)
			continue
		}

		if n == 0 {
			continue
		}

		pkt := buffer[:n]

		// Extraire l'adresse IP destination du paquet
		dstIP := parseDstIP(pkt)
		if dstIP == nil {
			continue
		}

		// Trouver le client UDP associé à cette IP virtuelle
		if s.tunnelPool == nil {
			continue
		}

		clientID, found := s.tunnelPool.Lookup(dstIP)
		if !found {
			// Paquet pour une IP non allouée — ignoré
			continue
		}

		// Récupérer la session pour obtenir l'adresse UDP du client
		session, ok := s.sessions.Get(clientID)
		if !ok || session.RemoteAddr == nil {
			continue
		}

		// Appliquer les règles du compte avant d'envoyer
		if decision := s.sessions.Authorize(clientID); decision != TrafficAllowed {
			continue
		}

		// Encoder le paquet avec le protocole tunnel
		tunnelPkt, encErr := EncodeTunnelPacket(tunnelClientID(clientID), pkt)
		if encErr != nil {
			log.Printf("[tunLoop] Encodage tunnel échoué : %v", encErr)
			continue
		}

		// Envoyer au client via UDP
		if _, err := s.conn.WriteToUDP(tunnelPkt, session.RemoteAddr); err != nil {
			log.Printf("[tunLoop] Envoi UDP vers %s échoué : %v", clientID, err)
			continue
		}

		// Comptabiliser le trafic sortant
		s.sessions.AddBytesOut(clientID, uint64(n))
	}
}

// parseSrcIP extrait l'adresse IP source d'un paquet IPv4.
// Retourne nil si le paquet n'est pas un paquet IPv4 valide.
func parseSrcIP(pkt []byte) net.IP {
	if len(pkt) < 20 {
		return nil
	}

	// Vérifier la version IP (4 bits haute du premier octet)
	version := pkt[0] >> 4
	if version != 4 {
		return nil // IPv6 non supporté pour l'instant
	}

	// L'en-tête IPv4 fait au minimum 20 octets
	ihl := int(pkt[0]&0x0F) * 4
	if ihl < 20 || len(pkt) < ihl {
		return nil
	}

	// L'adresse IP source est aux octets 12-15
	return net.IP(pkt[12:16])
}

// parseDstIP extrait l'adresse IP destination d'un paquet IPv4.
// Retourne nil si le paquet n'est pas un paquet IPv4 valide.
func parseDstIP(pkt []byte) net.IP {
	if len(pkt) < 20 {
		return nil
	}

	// Vérifier la version IP (4 bits haute du premier octet)
	version := pkt[0] >> 4
	if version != 4 {
		return nil // IPv6 non supporté pour l'instant
	}

	// L'en-tête IPv4 fait au minimum 20 octets
	ihl := int(pkt[0]&0x0F) * 4
	if ihl < 20 || len(pkt) < ihl {
		return nil
	}

	// L'adresse IP destination est aux octets 16-19
	return net.IP(pkt[16:20])
}

func (s *Server) handlePacket(
	client *net.UDPAddr,
	rawPacket []byte,
) {
	clientID := client.String()

	log.Printf(
		"Paquet reçu de %s : %d octets",
		clientID,
		len(rawPacket),
	)

	if s.handleControlPacket(client, clientID, rawPacket) {
		return
	}

	if !s.sessions.Touch(clientID) {
		log.Printf(
			"Paquet refusé : session absente ou expirée pour %s",
			clientID,
		)
		return
	}

	_ = s.sessions.SetRemoteAddr(clientID, client)

	// Application des règles du compte (expiration, quota) à chaque
	// paquet entrant. Si le compte n'est plus autorisé, on coupe proprement.
	if decision := s.sessions.Authorize(clientID); decision != TrafficAllowed {
		s.denyTraffic(client, clientID, decision)
		return
	}

	if len(rawPacket) >= tunnelHeaderSz &&
		rawPacket[0] == tunnelVersion {

		s.handleTunnelPacket(
			client,
			clientID,
			rawPacket,
		)

		return
	}

	s.forwardToTCP(
		client,
		clientID,
		rawPacket,
	)
}

// denyTraffic coupe proprement le trafic d'une session qui ne respecte plus
// les règles de son compte : notification au client (sauf simple absence de
// session), fermeture du backend TCP et retrait de la session.
func (s *Server) denyTraffic(
	client *net.UDPAddr,
	clientID string,
	decision TrafficDecision,
) {
	log.Printf(
		"Trafic refusé pour %s : %s",
		clientID,
		decision,
	)

	if decision == TrafficExpired || decision == TrafficQuota {
		_, _ = s.conn.WriteToUDP([]byte(string(decision)), client)
	}

	// Ferme le backend TCP éventuel et retire la session.
	s.removeStream(clientID, nil)
	s.removeSession(clientID)
}

func (s *Server) removeSession(clientID string) {
	if s == nil {
		return
	}

	// Persister le quota consommé avant de supprimer la session.
	if s.store != nil && s.sessions != nil {
		if sess, ok := s.sessions.Get(clientID); ok {
			total := sess.BytesIn + sess.BytesOut
			if total > 0 {
				_, _ = s.store.UpdateUsedBytes(sess.Username, int64(total))
			}
		}
	}

	if s.tunnelPool != nil {
		s.tunnelPool.Release(clientID)
	}

	if s.sessions != nil {
		s.sessions.Remove(clientID)
	}
}

func (s *Server) handleControlPacket(
	client *net.UDPAddr,
	clientID string,
	rawPacket []byte,
) bool {
	packet := string(rawPacket)

	switch packet {
	case "HELLO":
		challenge, err := s.auth.NewChallenge(clientID)
		if err != nil {
			log.Printf(
				"Erreur challenge pour %s : %v",
				clientID,
				err,
			)
			return true
		}

		_, _ = s.conn.WriteToUDP(
			[]byte("CHALLENGE "+challenge),
			client,
		)

		log.Printf("Challenge envoyé à %s", clientID)

		return true

	case "PING":
		// Keepalive : maintient la session active et le mapping NAT côté
		// client ouvert. La réponse PONG informe le client que le serveur
		// est toujours joignable. Le PING est traité ici (avant le contrôle
		// de session) afin de pouvoir répondre même si la session vient
		// d'expirer — le client détectera alors l'absence de PONG.
		if s.sessions.Touch(clientID) {
			_, _ = s.conn.WriteToUDP([]byte("PONG"), client)
		}
		return true
	}

	const prefix = "AUTH "

	if len(packet) >= len(prefix) &&
		packet[:len(prefix)] == prefix {

		// Le service recharge le store avant chaque authentification : les comptes
		// créés/bloqués depuis le menu sont donc pris en compte sans redémarrer.
		if s.config.Store != "" {
			if err := s.auth.ReloadFromStore(s.config.Store); err != nil {
				log.Printf("Impossible de recharger les comptes : %v", err)
			}
		}

		response := packet[len(prefix):]

		authUser, ok := s.auth.Verify(clientID, response)

		if ok {
			// Contrôle des limites du compte (par utilisateur) :
			// MaxConnections et MaxIPs.
			if decision := s.sessions.Admit(
				clientID,
				authUser.Username,
				authUser.Config,
				client,
			); decision != AdmitOK {
				_, _ = s.conn.WriteToUDP(
					[]byte(string(decision)),
					client,
				)

				log.Printf(
					"Connexion refusée : %s | Compte : %s | Raison : %s",
					clientID,
					authUser.Username,
					decision,
				)

				return true
			}

			session := s.sessions.CreateWithUser(
				clientID,
				authUser.Username,
				authUser.Config,
				client,
			)

			// Attribution d'une adresse IPv4 virtuelle unique.
			if s.tunnelPool != nil {
				tunnelIP, ipErr := s.tunnelPool.Allocate(clientID)
				if ipErr != nil {
					s.sessions.Remove(clientID)

					_, _ = s.conn.WriteToUDP(
						[]byte("TUNNEL_IP_UNAVAILABLE"),
						client,
					)

					log.Printf(
						"IP tunnel indisponible pour %s : %v",
						clientID,
						ipErr,
					)
					return true
				}

			// Enregistrer l'adresse virtuelle dans la session.
			s.sessions.SetTunnelIP(clientID, tunnelIP)

				_, _ = s.conn.WriteToUDP(
					[]byte("AUTH_OK "+tunnelIP.String()),
					client,
				)
			} else {
				// Compatibilité si aucun réseau TUN n'est configuré.
				_, _ = s.conn.WriteToUDP(
					[]byte("AUTH_OK"),
					client,
				)
			}
			log.Printf(
				"Utilisateur authentifié : %s | Compte : %s | Session : %s",
				clientID,
				authUser.Username,
				session.CreatedAt.Format(time.RFC3339),
			)
		} else {
			_, _ = s.conn.WriteToUDP(
				[]byte("AUTH_FAIL"),
				client,
			)

			log.Printf(
				"Authentification refusée : %s",
				clientID,
			)
		}

		return true
	}

	return false
}

func (s *Server) handleTunnelPacket(
	client *net.UDPAddr,
	clientID string,
	rawPacket []byte,
) {
	packet, err := DecodeTunnelPacket(rawPacket)
	if err != nil {
		log.Printf(
			"Paquet tunnel invalide de %s : %v",
			clientID,
			err,
		)
		return
	}

	expectedClientID := tunnelClientID(clientID)

	if packet.ClientID != expectedClientID {
		log.Printf(
			"Tunnel refusé : ClientID incorrect pour %s",
			clientID,
		)
		return
	}

	if len(packet.Payload) == 0 {
		return
	}

	// MODE VPN : écrire le paquet IP brut dans le TUN
	if s.tun != nil {
		// Anti-spoofing : l'adresse IP source du paquet doit correspondre
		// à l'adresse tunnel allouée à cette session. Sans cette vérification,
		// un client authentifié pourrait usurper l'IP d'un autre abonné.
		if s.tunnelPool != nil {
			srcIP := parseSrcIP(packet.Payload)
			if srcIP == nil {
				// Paquet non-IPv4 : rejeté en mode VPN
				return
			}

			expectedIP, hasIP := s.sessions.TunnelIP(clientID)
			if !hasIP || expectedIP == nil {
				log.Printf(
					"Tunnel refusé : pas d'IP allouée pour %s",
					clientID,
				)
				return
			}

			if !srcIP.Equal(expectedIP) {
				log.Printf(
					"Tunnel refusé (spoofing) : %s émet depuis %s au lieu de %s",
					clientID,
					srcIP,
					expectedIP,
				)
				return
			}
		}

		s.sessions.AddBytesIn(clientID, uint64(len(packet.Payload)))

		if _, err := s.tun.Write(packet.Payload); err != nil {
			log.Printf(
				"écriture TUN pour %s : %v",
				clientID,
				err,
			)
		}
		return
	}

	// MODE PROXY TCP : forwarder vers le backend (comportement historique)
	s.forwardToTCP(
		client,
		clientID,
		packet.Payload,
	)
}

func (s *Server) getStream(
	clientID string,
	client *net.UDPAddr,
) (net.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if conn, ok := s.streams[clientID]; ok {
		return conn, nil
	}

	conn, err := net.DialTimeout(
		"tcp",
		s.backendAddress(),
		tcpDialTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"connexion TCP %s : %w",
			s.backendAddress(),
			err,
		)
	}

	s.streams[clientID] = conn

	log.Printf(
		"Backend TCP connecté : %s -> %s",
		clientID,
		s.backendAddress(),
	)

	go s.readTCPStream(clientID, client, conn)

	return conn, nil
}

func (s *Server) forwardToTCP(
	client *net.UDPAddr,
	clientID string,
	payload []byte,
) {
	if len(payload) == 0 {
		return
	}

	// Comptabilisation du trafic entrant (client -> backend) pour les deux
	// voies : paquets tunnel ET paquets bruts. Comptage unique ici.
	s.sessions.AddBytesIn(clientID, uint64(len(payload)))

	conn, err := s.getStream(clientID, client)
	if err != nil {
		log.Printf(
			"TCP indisponible pour %s : %v",
			clientID,
			err,
		)

		_, _ = s.conn.WriteToUDP(
			[]byte("BACKEND_UNAVAILABLE"),
			client,
		)

		return
	}

	if _, err := conn.Write(payload); err != nil {
		log.Printf(
			"écriture TCP pour %s : %v",
			clientID,
			err,
		)

		s.removeStream(clientID, conn)
		return
	}

	s.sessions.Touch(clientID)
}

func (s *Server) readTCPStream(
	clientID string,
	client *net.UDPAddr,
	conn net.Conn,
) {
	buffer := make([]byte, maxUDPPacketSize)

	for {
		n, err := conn.Read(buffer)

		if n > 0 {
			// Application des règles avant de renvoyer du trafic au client.
			if decision := s.sessions.Authorize(clientID); decision != TrafficAllowed {
				log.Printf(
					"Sortie refusée pour %s : %s",
					clientID,
					decision,
				)

				if decision == TrafficExpired || decision == TrafficQuota {
					_, _ = s.conn.WriteToUDP([]byte(string(decision)), client)
				}

				s.removeStream(clientID, conn)
				return
			}

			payload := buffer[:n]

			if _, writeErr := s.conn.WriteToUDP(
				payload,
				client,
			); writeErr != nil {
				log.Printf(
					"retour UDP vers %s : %v",
					clientID,
					writeErr,
				)

				s.removeStream(clientID, conn)
				return
			}

			s.sessions.AddBytesOut(
				clientID,
				uint64(n),
			)
		}

		if err != nil {
			if err != io.EOF {
				log.Printf(
					"lecture TCP pour %s : %v",
					clientID,
					err,
				)
			}

			s.removeStream(clientID, conn)
			return
		}
	}
}

func (s *Server) removeStream(
	clientID string,
	expected net.Conn,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	conn, ok := s.streams[clientID]
	if !ok {
		return
	}

	if expected != nil && conn != expected {
		return
	}

	_ = conn.Close()
	delete(s.streams, clientID)

	s.removeSession(clientID)

	log.Printf(
		"Connexion TCP fermée : %s",
		clientID,
	)
}

func (s *Server) cleanupExpiredStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for clientID, conn := range s.streams {
		if _, ok := s.sessions.Get(clientID); !ok {
			_ = conn.Close()
			delete(s.streams, clientID)

			log.Printf(
				"Session expirée, TCP fermé : %s",
				clientID,
			)
		}
	}
}

func runServerContext(
	configPath string,
	ctx context.Context,
	devMode bool,
) error {
	config, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf(
			"lecture de la configuration : %w",
			err,
		)
	}

	// Vérification de licence AVANT tout démarrage réseau. En production la
	// vérification est obligatoire ; seul le mode développement (-dev ou
	// LABOSURF_DEV=1) permet de la contourner.
	if devMode {
		log.Println("⚠ MODE DÉVELOPPEMENT : vérification de licence ignorée")
	} else if err := checkLicense(config); err != nil {
		return fmt.Errorf("démarrage refusé : %w", err)
	}

	// Le store est la source de vérité des comptes. S'il contient au moins
	// un compte, il fait autorité sur la section auth de la configuration.
	store, storeErr := LoadStore(config.Store)
	if storeErr != nil {
		log.Printf(
			"Store %s non chargé (%v) — utilisation des utilisateurs de la config",
			config.Store,
			storeErr,
		)
	} else if users := store.UserConfigs(); len(users) > 0 {
		config.Auth.Users = users
		log.Printf(
			"Comptes chargés depuis le store %s : %d",
			config.Store,
			len(users),
		)
	}

	server, err := NewServer(config, store)
	if err != nil {
		return err
	}

	defer server.Close()

	if config.TUN.Enabled {
		tun, err := NewTUNDevice(config.TUN.Name)
		if err != nil {
			return fmt.Errorf("initialisation TUN %q : %w", config.TUN.Name, err)
		}
		server.tun = tun
		log.Printf("TUN activé : %s (%s)", tun.Name(), config.TUN.Address)

		// Configurer le réseau : adresse IP, forwarding, NAT, routes
		netCfg := DefaultNetworkConfig()
		netCfg.TUNName = tun.Name()
		netCfg.TUNAddress = config.TUN.Address
		if config.TUN.Address != "" {
			// Extraire le CIDR réseau depuis l'adresse (ex: 10.77.0.1/24 → 10.77.0.0/24)
			_, ipnet, parseErr := net.ParseCIDR(config.TUN.Address)
			if parseErr == nil {
				netCfg.VPNRange = ipnet.String()
			}
		}
		cleanup, netErr := ConfigureNetwork(netCfg)
		if netErr != nil {
			log.Printf("Avertissement réseau : %v", netErr)
			log.Println("Le serveur continue en mode dégradé (pas de NAT)")
		} else {
			defer cleanup()
		}
	} else {
		log.Println("TUN désactivé.")
	}

	// Portail intégré : quand activé, démarre dans le même processus et
	// partage le SessionManager du moteur. L'état affiché est l'état réel.
	if config.Portal.Enabled {
		ps := NewPortalServerShared(config.Portal.Listen, store, server.sessions)

		portalCtx, portalCancel := context.WithCancel(ctx)
		defer portalCancel()

		portalErrCh := make(chan error, 1)
		go func() {
			portalErrCh <- ps.ListenAndServe(portalCtx)
		}()

		go func() {
			select {
			case <-ctx.Done():
				portalCancel()
			case pErr := <-portalErrCh:
				if pErr != nil {
					log.Printf("portail : %v", pErr)
				}
			}
		}()
	}

	return server.Run(ctx)
}

func runServer(configPath string, devMode bool) error {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	return runServerContext(configPath, ctx, devMode)
}
