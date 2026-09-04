//go:build linux && !android

package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// ============================================================
// CLIENT VPN LABOSURF PRO — TEST BOUT EN BOUT
// ============================================================
//
// Client Go capable de parler au protocole LABOSURF.
// Crée un TUN local, s'authentifie, et transporte les paquets IP
// à travers le tunnel UDP.
//
// Usage :
//   labosurf vpn connect --server VPS_IP:51820 --user client1 --password secret

// VPNClient représente un client VPN connecté au serveur.
type VPNClient struct {
	conn       *net.UDPConn
	serverAddr *net.UDPAddr
	tun        *TUNDevice
	tunIP      net.IP
	clientID   string
	username   string
}

// VPNClientConfig contient les paramètres de connexion du client.
type VPNClientConfig struct {
	ServerAddr string // "VPS_IP:51820"
	Username   string
	Password   string
	TUNName    string // "labosurf1" (client-side)
	TUNAddress string // "10.77.0.2/24" (assigné par le serveur)
}

// Connect établit la connexion au serveur VPN et effectue le handshake.
func Connect(cfg VPNClientConfig) (*VPNClient, error) {
	// Résoudre l'adresse du serveur
	serverAddr, err := net.ResolveUDPAddr("udp", cfg.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("adresse serveur invalide : %w", err)
	}

	// Ouvrir la socket UDP
	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		return nil, fmt.Errorf("connexion UDP : %w", err)
	}

	// Identifier le client par son ip:port local
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	clientID := localAddr.String()

	client := &VPNClient{
		conn:       conn,
		serverAddr: serverAddr,
		clientID:   clientID,
		username:   cfg.Username,
	}

	// Handshake : HELLO → CHALLENGE → AUTH → AUTH_OK
	if err := client.handshake(cfg.Username, cfg.Password); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake échoué : %w", err)
	}

	// Créer le TUN local
	tunName := cfg.TUNName
	if tunName == "" {
		tunName = "labosurf1"
	}

	tun, err := NewTUNDevice(tunName)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("création TUN : %w", err)
	}
	client.tun = tun

	log.Printf("Client connecté : %s → %s (TUN: %s)", clientID, cfg.ServerAddr, tun.Name())

	return client, nil
}

// handshake effectue le protocole HELLO → CHALLENGE → AUTH → AUTH_OK.
func (c *VPNClient) handshake(username, password string) error {
	// Envoyer HELLO
	if _, err := c.conn.Write([]byte("HELLO")); err != nil {
		return fmt.Errorf("envoi HELLO : %w", err)
	}

	// Lire CHALLENGE
	buffer := make([]byte, 1024)
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := c.conn.Read(buffer)
	if err != nil {
		return fmt.Errorf("lecture CHALLENGE : %w", err)
	}

	response := string(buffer[:n])
	if !strings.HasPrefix(response, "CHALLENGE ") {
		return fmt.Errorf("réponse inattendue : %s", response)
	}

	challenge := strings.TrimPrefix(response, "CHALLENGE ")

	// Calculer la réponse HMAC
	hmacResponse := computeHMAC(challenge, password)

	// Envoyer AUTH
	authMsg := fmt.Sprintf("AUTH %s", hmacResponse)
	if _, err := c.conn.Write([]byte(authMsg)); err != nil {
		return fmt.Errorf("envoi AUTH : %w", err)
	}

	// Lire AUTH_OK ou AUTH_FAIL
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err = c.conn.Read(buffer)
	if err != nil {
		return fmt.Errorf("lecture réponse AUTH : %w", err)
	}

	authResponse := string(buffer[:n])

	if strings.HasPrefix(authResponse, "AUTH_OK") {
		// Extraire l'IP tunnel si fournie
		parts := strings.SplitN(authResponse, " ", 2)
		if len(parts) == 2 {
			c.tunIP = net.ParseIP(parts[1])
			log.Printf("IP tunnel assignée : %s", c.tunIP)
		}
		return nil
	}

	if authResponse == "AUTH_FAIL" {
		return fmt.Errorf("authentification refusée")
	}

	return fmt.Errorf("réponse inattendue : %s", authResponse)
}

// keepaliveInterval est la période d'envoi des PING de keepalive.
// Doit être inférieure au timeout de session serveur (1 minute).
const keepaliveInterval = 25 * time.Second

// Run démarre le client VPN bidirectionnel.
// Trois goroutines : TUN→UDP, UDP→TUN, et keepalive.
func (c *VPNClient) Run(ctx context.Context) error {
	if c.tun == nil {
		return fmt.Errorf("TUN non initialisé")
	}

	log.Println("Client VPN démarré")
	log.Printf("  Serveur  : %s", c.serverAddr)
	log.Printf("  Client   : %s", c.clientID)
	log.Printf("  TUN      : %s", c.tun.Name())
	if c.tunIP != nil {
		log.Printf("  IP VPN   : %s", c.tunIP)
	}
	log.Println("Ctrl+C pour arrêter")

	// Goroutine 1 : TUN → UDP (envoyer les paquets du téléphone au serveur)
	go c.tunToUDP(ctx)

	// Goroutine 2 : UDP → TUN (recevoir les paquets du serveur vers le téléphone)
	go c.udpToTUN(ctx)

	// Goroutine 3 : keepalive (maintient la session et le NAT ouverts)
	go c.keepalive(ctx)

	// Attendre l'arrêt
	<-ctx.Done()
	log.Println("Arrêt du client VPN...")
	return nil
}

// keepalive envoie un PING périodique au serveur pour maintenir la session
// active (le serveur expire les sessions après 1 minute d'inactivité) et
// garder le mapping NAT du client ouvert (certains NAT ferment les bindings
// UDP après 30 à 60 secondes sans trafic).
func (c *VPNClient) keepalive(ctx context.Context) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.conn.Write([]byte("PING")); err != nil {
				log.Printf("[keepalive] envoi PING échoué : %v", err)
			}
		}
	}
}

// tunToUDP lit les paquets depuis le TUN local et les envoie au serveur.
func (c *VPNClient) tunToUDP(ctx context.Context) {
	buffer := make([]byte, 65535)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := c.tun.Read(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		if n == 0 {
			continue
		}

		pkt := buffer[:n]

		// Encoder avec le protocole tunnel
		tunnelPkt, err := EncodeTunnelPacket(tunnelClientID(c.clientID), pkt)
		if err != nil {
			log.Printf("Encodage échoué : %v", err)
			continue
		}

		// Envoyer au serveur
		if _, err := c.conn.Write(tunnelPkt); err != nil {
			log.Printf("Envoi UDP échoué : %v", err)
			continue
		}
	}
}

// udpToTUN reçoit les paquets du serveur et les écrit dans le TUN local.
func (c *VPNClient) udpToTUN(ctx context.Context) {
	buffer := make([]byte, 65535)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		c.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := c.conn.Read(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			log.Printf("Lecture UDP échouée : %v", err)
			continue
		}

		if n == 0 {
			continue
		}

		// Décoder le paquet tunnel
		tunnelPkt, err := DecodeTunnelPacket(buffer[:n])
		if err != nil {
			// Ce pourrait être une réponse de contrôle (AUTH_OK, etc.)
			// ou un paquet invalide — ignorer
			continue
		}

		// Vérifier que le paquet est bien destiné à ce client.
		expectedClientID := tunnelClientID(c.clientID)
		if tunnelPkt.ClientID != expectedClientID {
			log.Printf("Paquet tunnel rejeté : ClientID inattendu")
			continue
		}

		// Écrire le paquet IP brut dans le TUN
		if _, err := c.tun.Write(tunnelPkt.Payload); err != nil {
			log.Printf("Écriture TUN échouée : %v", err)
			continue
		}
	}
}

// Close ferme proprement la connexion.
func (c *VPNClient) Close() error {
	if c.tun != nil {
		_ = c.tun.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// computeHMAC calcule HMAC-SHA256(nonce_bytes, password).
// Identique à auth.go côté serveur.
func computeHMAC(challengeHex, password string) string {
	nonceBytes, err := hex.DecodeString(challengeHex)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(password))
	mac.Write(nonceBytes)
	return hex.EncodeToString(mac.Sum(nil))
}

// runVPNClient est le point d'entrée de la commande `labosurf vpn connect`.
func runVPNClient(args []string) {
	var serverAddr, username, password, tunName string

	// Parser les arguments
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server", "-s":
			if i+1 < len(args) {
				serverAddr = args[i+1]
				i++
			}
		case "--user", "-u":
			if i+1 < len(args) {
				username = args[i+1]
				i++
			}
		case "--password", "-p":
			if i+1 < len(args) {
				password = args[i+1]
				i++
			}
		case "--tun":
			if i+1 < len(args) {
				tunName = args[i+1]
				i++
			}
		}
	}

	if serverAddr == "" || username == "" || password == "" {
		fmt.Println("Usage : labosurf vpn connect --server IP:PORT --user USER --password PASS")
		fmt.Println()
		fmt.Println("Options :")
		fmt.Println("  --server, -s   Adresse du serveur (IP:PORT)")
		fmt.Println("  --user, -u     Nom d'utilisateur")
		fmt.Println("  --password, -p Mot de passe")
		fmt.Println("  --tun          Nom du TUN local (défaut: labosurf1)")
		os.Exit(1)
	}

	cfg := VPNClientConfig{
		ServerAddr: serverAddr,
		Username:   username,
		Password:   password,
		TUNName:    tunName,
	}

	client, err := Connect(cfg)
	if err != nil {
		log.Fatalf("Connexion échouée : %v", err)
	}
	defer client.Close()

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	if err := client.Run(ctx); err != nil {
		log.Fatalf("Client VPN : %v", err)
	}
}
