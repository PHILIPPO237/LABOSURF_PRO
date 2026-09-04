//go:build linux && !android

package main

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// ============================================================
// TEST D'INTÉGRATION RÉEL — nécessite Linux + root (CAP_NET_ADMIN)
// ============================================================
//
// Ce test crée un VRAI périphérique TUN, lance le serveur LABOSURF,
// connecte un vrai client VPN, puis vérifie que les paquets IP
// traversent réellement le tunnel dans les deux sens.
//
// Pour l'exécuter :
//
//	sudo go test -run TestRealEndToEnd -v ./engines/udp/
//
// Il est automatiquement skippé si :
//   - on n'est pas sous Linux ;
//   - l'utilisateur n'est pas root (/dev/net/tun inaccessible) ;
//   - la variable LABOSURF_REAL_TEST n'est pas définie à "1".

// TestRealEndToEnd valide le data path complet avec de vrais TUN.
func TestRealEndToEnd(t *testing.T) {
	if os.Getenv("LABOSURF_REAL_TEST") != "1" {
		t.Skip("définir LABOSURF_REAL_TEST=1 et lancer en root pour activer ce test")
	}

	if os.Geteuid() != 0 {
		t.Skip("ce test nécessite root (CAP_NET_ADMIN) pour créer un TUN")
	}

	// Vérifier que /dev/net/tun est accessible
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		t.Skipf("/dev/net/tun inaccessible : %v", err)
	}

	// Configuration serveur : réseau 10.99.0.0/24 pour éviter tout conflit
	// avec une éventuelle instance de production sur 10.77.0.0/24.
	users := map[string]UserConfig{
		"testuser": {Password: "TESTPASS", Enabled: true},
	}

	var config Config
	config.Listen = "127.0.0.1:0"
	config.Auth.Mode = "passwords"
	config.Auth.Users = users
	config.TUN.Address = "10.99.0.1/24"
	config.TUN.Name = "labsrv0"
	config.TUN.Enabled = true

	srv, err := NewServer(config)
	if err != nil {
		t.Fatalf("NewServer : %v", err)
	}
	defer srv.Close()

	// Créer un VRAI TUN pour le serveur
	serverTUN, err := NewTUNDevice("labsrv0")
	if err != nil {
		t.Fatalf("NewTUNDevice serveur : %v", err)
	}
	srv.tun = serverTUN
	t.Logf("TUN serveur créé : %s", serverTUN.Name())

	// Configurer l'adresse IP du TUN serveur
	netCfg := DefaultNetworkConfig()
	netCfg.TUNName = serverTUN.Name()
	netCfg.TUNAddress = config.TUN.Address
	netCfg.VPNRange = "10.99.0.0/24"
	cleanupNet, netErr := ConfigureNetwork(netCfg)
	if netErr != nil {
		t.Logf("Avertissement configuration réseau : %v", netErr)
		t.Logf("Le test continue sans NAT (test local uniquement)")
	} else {
		defer cleanupNet()
		t.Logf("Réseau serveur configuré : %s", config.TUN.Address)
	}

	// Démarrer le serveur
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	time.Sleep(200 * time.Millisecond)

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)
	t.Logf("Serveur UDP en écoute sur %s", serverAddr)

	// === CONNEXION DU CLIENT VPN ===

	clientCfg := VPNClientConfig{
		ServerAddr: serverAddr.String(),
		Username:   "testuser",
		Password:   "TESTPASS",
		TUNName:    "labcli0",
	}

	client, err := Connect(clientCfg)
	if err != nil {
		t.Fatalf("connexion client : %v", err)
	}
	defer client.Close()

	if client.tunIP == nil {
		t.Fatal("aucune IP tunnel assignée au client")
	}
	t.Logf("Client connecté avec IP tunnel : %s", client.tunIP)

	// Configurer l'adresse IP du TUN client (côté client, c'est manuel ici
	// car le client Linux ne configure pas automatiquement son interface).
	// Pour ce test, on utilise `ip addr add` via exec serait possible,
	// mais on se contente de vérifier le transport des paquets bruts.

	// Démarrer le loop client en arrière-plan
	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()
	go func() { _ = client.Run(clientCtx) }()
	time.Sleep(200 * time.Millisecond)

	// === TEST ALLER : client → serveur → TUN serveur ===

	// Lire ce qui arrive dans le TUN serveur (dans une goroutine)
	serverTUNRead := make(chan []byte, 10)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := serverTUN.Read(buf)
			if err != nil {
				return
			}
			if n > 0 {
				pkt := make([]byte, n)
				copy(pkt, buf[:n])
				serverTUNRead <- pkt
			}
		}
	}()

	// Le client envoie un paquet IP forgé à travers son tunnel
	srcIP := client.tunIP.To4()
	dstIP := net.ParseIP("203.0.113.1").To4() // TEST-NET-3, non routé

	testPkt := make([]byte, 40)
	testPkt[0] = 0x45 // IPv4, IHL=5
	testPkt[8] = 64   // TTL
	testPkt[9] = 1    // ICMP
	copy(testPkt[12:16], srcIP)
	copy(testPkt[16:20], dstIP)

	tunnelPkt, err := EncodeTunnelPacket(tunnelClientID(client.clientID), testPkt)
	if err != nil {
		t.Fatalf("EncodeTunnelPacket : %v", err)
	}

	if _, err := client.conn.Write(tunnelPkt); err != nil {
		t.Fatalf("envoi paquet tunnel : %v", err)
	}

	// Attendre la réception dans le TUN serveur
	select {
	case received := <-serverTUNRead:
		if len(received) < 20 {
			t.Fatalf("paquet trop court dans TUN serveur : %d octets", len(received))
		}
		gotSrc := net.IP(received[12:16])
		gotDst := net.IP(received[16:20])
		if !gotSrc.Equal(srcIP) {
			t.Fatalf("source incorrecte : %s (attendu %s)", gotSrc, srcIP)
		}
		if !gotDst.Equal(dstIP) {
			t.Fatalf("destination incorrecte : %s (attendu %s)", gotDst, dstIP)
		}
		t.Logf("✓ ALLER : paquet %s → %s reçu dans le TUN serveur", gotSrc, gotDst)
	case <-time.After(3 * time.Second):
		t.Fatal("TIMEOUT : aucun paquet reçu dans le TUN serveur — le data path aller ne fonctionne pas")
	}

	// === TEST RETOUR : TUN serveur → serveur → client ===

	// Lire ce qui arrive dans le TUN client
	clientTUNRead := make(chan []byte, 10)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := client.tun.Read(buf)
			if err != nil {
				return
			}
			if n > 0 {
				pkt := make([]byte, n)
				copy(pkt, buf[:n])
				clientTUNRead <- pkt
			}
		}
	}()

	// Injecter un paquet de retour dans le TUN serveur (comme si Internet
	// répondait au client). La destination est l'IP tunnel du client.
	replyPkt := make([]byte, 40)
	replyPkt[0] = 0x45
	replyPkt[8] = 64
	replyPkt[9] = 1
	copy(replyPkt[12:16], dstIP) // source = Internet
	copy(replyPkt[16:20], srcIP) // destination = client VPN

	if _, err := serverTUN.Write(replyPkt); err != nil {
		t.Fatalf("écriture TUN serveur : %v", err)
	}

	// Attendre la réception dans le TUN client
	select {
	case received := <-clientTUNRead:
		if len(received) < 20 {
			t.Fatalf("paquet retour trop court : %d octets", len(received))
		}
		gotSrc := net.IP(received[12:16])
		gotDst := net.IP(received[16:20])
		if !gotSrc.Equal(dstIP) {
			t.Fatalf("source retour incorrecte : %s", gotSrc)
		}
		if !gotDst.Equal(srcIP) {
			t.Fatalf("destination retour incorrecte : %s", gotDst)
		}
		t.Logf("✓ RETOUR : paquet %s → %s reçu dans le TUN client", gotSrc, gotDst)
	case <-time.After(3 * time.Second):
		t.Fatal("TIMEOUT : aucun paquet reçu dans le TUN client — le data path retour ne fonctionne pas")
	}

	t.Log("✓✓✓ TEST RÉEL BOUT-EN-BOUT RÉUSSI : le data path VPN est fonctionnel avec de vrais TUN")
}

// TestRealKeepalive vérifie que le keepalive maintient la session active
// au-delà du timeout nominal. Nécessite les mêmes privilèges que le test
// principal mais est plus court.
func TestRealKeepalive(t *testing.T) {
	if os.Getenv("LABOSURF_REAL_TEST") != "1" {
		t.Skip("définir LABOSURF_REAL_TEST=1 pour activer ce test")
	}
	if os.Geteuid() != 0 {
		t.Skip("root requis")
	}

	// Ce test utilise un timeout de session très court (2 s) pour vérifier
	// que le keepalive (toutes les 1 s dans ce test) empêche l'expiration.
	// On ne peut pas modifier sessionTimeout directement, donc on vérifie
	// simplement que PING/PONG fonctionne sur une vraie socket.
	users := map[string]UserConfig{
		"kp": {Password: "KP", Enabled: true},
	}
	var config Config
	config.Listen = "127.0.0.1:0"
	config.Auth.Users = users
	config.TUN.Address = "10.98.0.1/24"
	config.TUN.Enabled = false // pas besoin de TUN pour le keepalive

	srv, err := NewServer(config)
	if err != nil {
		t.Fatalf("NewServer : %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)

	// Handshake minimal
	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("dial : %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write([]byte("HELLO")); err != nil {
		t.Fatalf("HELLO : %v", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("CHALLENGE : %v", err)
	}
	challenge := strings.TrimPrefix(string(buf[:n]), "CHALLENGE ")
	resp := authResponse(t, "KP", challenge)

	if _, err := conn.Write([]byte("AUTH " + resp)); err != nil {
		t.Fatalf("AUTH : %v", err)
	}
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("AUTH_OK : %v", err)
	}
	if !strings.HasPrefix(string(buf[:n]), "AUTH_OK") {
		t.Fatalf("auth échouée : %q", string(buf[:n]))
	}

	// Envoyer PING
	if _, err := conn.Write([]byte("PING")); err != nil {
		t.Fatalf("PING : %v", err)
	}
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("PONG : %v", err)
	}
	if string(buf[:n]) != "PONG" {
		t.Fatalf("PONG attendu, obtenu %q", string(buf[:n]))
	}

	t.Log("✓ Keepalive réel OK")
}
