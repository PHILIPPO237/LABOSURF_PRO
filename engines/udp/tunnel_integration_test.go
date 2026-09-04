package main

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// ============================================================
// MOCK TUN DEVICE — pour tests d'intégration sans vrai TUN
// ============================================================

type mockTUN struct {
	name    string
	written [][]byte // paquets écrits dans le TUN (client → serveur)
	readCh  chan []byte // paquets à lire depuis le TUN (serveur → client)
}

func newMockTUN(name string) *mockTUN {
	return &mockTUN{
		name:   name,
		readCh: make(chan []byte, 100),
	}
}

func (m *mockTUN) Name() string { return m.name }

func (m *mockTUN) Read(p []byte) (int, error) {
	pkt := <-m.readCh
	n := copy(p, pkt)
	return n, nil
}

func (m *mockTUN) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	m.written = append(m.written, cp)
	return len(p), nil
}

func (m *mockTUN) Close() error { return nil }

// inject simule un paquet IP sortant du TUN (réponse Internet → client).
func (m *mockTUN) inject(pkt []byte) {
	cp := make([]byte, len(pkt))
	copy(cp, pkt)
	m.readCh <- cp
}

// ============================================================
// PAQUET IP DE TEST — ICMP ping 8.8.8.8 → 10.77.0.2
// ============================================================

func fakeIPPkt(src, dst net.IP) []byte {
	pkt := make([]byte, 40) // IP header (20) + ICMP echo (20)
	// IPv4 header
	pkt[0] = 0x45          // Version=4, IHL=5
	pkt[1] = 0x00          // DSCP/ECN
	binary.BigEndian.PutUint16(pkt[2:4], 40) // Total length
	pkt[8] = 64            // TTL
	pkt[9] = 1             // Protocol: ICMP
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	// ICMP Echo Request
	pkt[20] = 8            // Type: Echo Request
	pkt[21] = 0            // Code
	// Checksum (omitted for test — server doesn't validate IP checksum)
	return pkt
}

// ============================================================
// TESTS D'INTÉGRATION
// ============================================================

// TestTunnelHandshakeAndIPPacket vérifie le chemin complet :
// handshake → allocation IP → envoi paquet IP → écriture dans TUN.
func TestTunnelHandshakeAndIPPacket(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {Password: "SECRET", Enabled: true},
	}

	var config Config
	config.Listen = "127.0.0.1:0"
	config.Auth.Mode = "passwords"
	config.Auth.Users = users
	config.TUN.Address = "10.77.0.1/24"

	srv, err := NewServer(config)
	if err != nil {
		t.Fatalf("NewServer : %v", err)
	}
	defer srv.Close()

	// Injecter le mock TUN
	mockTUN := newMockTUN("test0")
	srv.tun = mockTUN

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	// Connecter le client
	client, err := net.DialUDP("udp", nil, srv.conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP : %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	// === PHASE 1 : HANDSHAKE ===

	// HELLO
	if _, err := client.Write([]byte("HELLO")); err != nil {
		t.Fatalf("envoi HELLO : %v", err)
	}

	// Lire CHALLENGE
	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("lecture CHALLENGE : %v", err)
	}
	challengeMsg := string(buf[:n])
	if !strings.HasPrefix(challengeMsg, "CHALLENGE ") {
		t.Fatalf("CHALLENGE attendu, obtenu %q", challengeMsg)
	}
	challenge := strings.TrimPrefix(challengeMsg, "CHALLENGE ")

	// AUTH avec HMAC-SHA256 correct
	resp := authResponse(t, "SECRET", challenge)
	if _, err := client.Write([]byte("AUTH " + resp)); err != nil {
		t.Fatalf("envoi AUTH : %v", err)
	}

	// Lire AUTH_OK <tunnelIP>
	n, err = client.Read(buf)
	if err != nil {
		t.Fatalf("lecture AUTH_OK : %v", err)
	}
	authReply := string(buf[:n])
	if !strings.HasPrefix(authReply, "AUTH_OK ") {
		t.Fatalf("AUTH_OK attendu, obtenu %q", authReply)
	}

	tunnelIP := strings.TrimPrefix(authReply, "AUTH_OK ")
	if tunnelIP == "" {
		t.Fatal("IP tunnel vide dans AUTH_OK")
	}
	t.Logf("IP tunnel assignée : %s", tunnelIP)

	// Vérifier que l'IP est dans le range 10.77.0.x
	if !strings.HasPrefix(tunnelIP, "10.77.0.") {
		t.Fatalf("IP tunnel hors range : %s", tunnelIP)
	}

	// === PHASE 2 : ENVOI PAQUET IP CLIENT → SERVEUR ===

	// Créer un paquet IP simulé (ping de 10.77.0.2 vers 8.8.8.8)
	srcIP := net.ParseIP(tunnelIP)
	if srcIP == nil {
		t.Fatalf("IP invalide : %s", tunnelIP)
	}
	dstIP := net.ParseIP("8.8.8.8")
	fakePkt := fakeIPPkt(srcIP, dstIP)

	// Encoder dans le protocole tunnel
	clientID := client.LocalAddr().String()
	tunnelPkt, err := EncodeTunnelPacket(tunnelClientID(clientID), fakePkt)
	if err != nil {
		t.Fatalf("EncodeTunnelPacket : %v", err)
	}

	// Envoyer au serveur
	if _, err := client.Write(tunnelPkt); err != nil {
		t.Fatalf("envoi tunnel : %v", err)
	}

	// Attendre que le serveur écrive dans le mock TUN
	time.Sleep(200 * time.Millisecond)

	if len(mockTUN.written) == 0 {
		t.Fatal("aucun paquet écrit dans le TUN — le data path ne fonctionne pas")
	}

	received := mockTUN.written[len(mockTUN.written)-1]
	t.Logf("Paquet reçu dans TUN : %d octets", len(received))

	// Vérifier que c'est un paquet IPv4
	if len(received) < 20 {
		t.Fatalf("paquet trop court : %d octets", len(received))
	}
	if received[0]>>4 != 4 {
		t.Fatalf("pas un paquet IPv4 : version=%d", received[0]>>4)
	}

	// Vérifier les IP source/destination
	pktSrc := net.IP(received[12:16])
	pktDst := net.IP(received[16:20])
	if !pktSrc.Equal(srcIP) {
		t.Fatalf("IP source incorrecte : %s (attendu %s)", pktSrc, srcIP)
	}
	if !pktDst.Equal(dstIP) {
		t.Fatalf("IP destination incorrecte : %s (attendu %s)", pktDst, dstIP)
	}

	t.Logf("✓ Paquet IP correctement routé : %s → %s", pktSrc, pktDst)

	// === PHASE 3 : TRAFIC RETOUR TUN → CLIENT ===

	// Simuler un paquet réponse (8.8.8.8 → 10.77.0.2)
	respPkt := fakeIPPkt(dstIP, srcIP)
	mockTUN.inject(respPkt)

	// Le serveur devrait lire ce paquet et l'envoyer au client via UDP
	n, err = client.Read(buf)
	if err != nil {
		t.Fatalf("lecture paquet retour : %v", err)
	}

	// Décoder le paquet tunnel
	tunnelResp, err := DecodeTunnelPacket(buf[:n])
	if err != nil {
		t.Fatalf("décodage paquet tunnel retour : %v", err)
	}

	if len(tunnelResp.Payload) < 20 {
		t.Fatalf("paquet retour trop court : %d octets", len(tunnelResp.Payload))
	}

	respSrc := net.IP(tunnelResp.Payload[12:16])
	respDst := net.IP(tunnelResp.Payload[16:20])

	if !respSrc.Equal(dstIP) {
		t.Fatalf("IP source retour incorrecte : %s", respSrc)
	}
	if !respDst.Equal(srcIP) {
		t.Fatalf("IP destination retour incorrecte : %s", respDst)
	}

	t.Logf("✓ Paquet retour correctement routé : %s → %s", respSrc, respDst)
	t.Logf("✓ DATA PATH VPN BIDIRECTIONNEL FONCTIONNEL")
}

// TestTunnelAntiSpoofing vérifie qu'un client ne peut pas émettre de paquets
// avec une IP source différente de son IP tunnel allouée.
func TestTunnelAntiSpoofing(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {Password: "SECRET", Enabled: true},
	}

	var config Config
	config.Listen = "127.0.0.1:0"
	config.Auth.Mode = "passwords"
	config.Auth.Users = users
	config.TUN.Address = "10.77.0.1/24"

	srv, err := NewServer(config)
	if err != nil {
		t.Fatalf("NewServer : %v", err)
	}
	defer srv.Close()

	mockTUN := newMockTUN("test0")
	srv.tun = mockTUN

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)
	client, tunnelIP := connectClient(t, serverAddr, "client1", "SECRET")
	defer client.Close()

	t.Logf("Client authentifié avec IP tunnel : %s", tunnelIP)

	// === CAS 1 : paquet légitime (bonne source) ===
	legitPkt := fakeIPPkt(net.ParseIP(tunnelIP), net.ParseIP("8.8.8.8"))
	sendTunnelPacket(t, client, legitPkt)
	time.Sleep(150 * time.Millisecond)

	before := len(mockTUN.written)
	if before == 0 {
		t.Fatal("le paquet légitime aurait dû être écrit dans TUN")
	}
	t.Logf("✓ Paquet légitime accepté (%d dans TUN)", before)

	// === CAS 2 : paquet spoofé (mauvaise source) ===
	spoofedPkt := fakeIPPkt(net.ParseIP("10.77.0.99"), net.ParseIP("8.8.8.8"))
	sendTunnelPacket(t, client, spoofedPkt)
	time.Sleep(150 * time.Millisecond)

	after := len(mockTUN.written)
	if after != before {
		t.Fatalf("paquet spoofé accepté : %d écrits dans TUN, attendu %d", after, before)
	}
	t.Logf("✓ Paquet spoofé rejeté (TUN inchangé : %d)", after)

	// === CAS 3 : paquet non-IPv4 ===
	notIP := []byte{0x60, 0x00, 0x00, 0x00} // version 6
	notIP = append(notIP, make([]byte, 36)...) // padding
	sendTunnelPacket(t, client, notIP)
	time.Sleep(150 * time.Millisecond)

	final := len(mockTUN.written)
	if final != before {
		t.Fatalf("paquet non-IPv4 accepté : %d écrits dans TUN", final)
	}
	t.Logf("✓ Paquet non-IPv4 rejeté")
}

// TestTunnelKeepalive vérifie que le serveur répond PONG à un PING
// et que la session reste active.
func TestTunnelKeepalive(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {Password: "SECRET", Enabled: true},
	}

	var config Config
	config.Listen = "127.0.0.1:0"
	config.Auth.Mode = "passwords"
	config.Auth.Users = users
	config.TUN.Address = "10.77.0.1/24"

	srv, err := NewServer(config)
	if err != nil {
		t.Fatalf("NewServer : %v", err)
	}
	defer srv.Close()

	mockTUN := newMockTUN("test0")
	srv.tun = mockTUN

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)
	client, _ := connectClient(t, serverAddr, "client1", "SECRET")
	defer client.Close()

	clientID := client.LocalAddr().String()

	// Vérifier que la session existe
	if _, ok := srv.sessions.Get(clientID); !ok {
		t.Fatal("session absente après authentification")
	}

	// Envoyer PING
	if _, err := client.Write([]byte("PING")); err != nil {
		t.Fatalf("envoi PING : %v", err)
	}

	// Lire PONG
	buf := make([]byte, 64)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("lecture PONG : %v", err)
	}

	if string(buf[:n]) != "PONG" {
		t.Fatalf("PONG attendu, obtenu %q", string(buf[:n]))
	}

	t.Logf("✓ Keepalive PING/PONG fonctionnel")
}

// TestTunnelMultipleClients vérifie que plusieurs clients peuvent
// recevoir des IPs différentes et que le serveur route correctement.
func TestTunnelMultipleClients(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {Password: "PASS1", Enabled: true},
		"client2": {Password: "PASS2", Enabled: true},
	}

	var config Config
	config.Listen = "127.0.0.1:0"
	config.Auth.Mode = "passwords"
	config.Auth.Users = users
	config.TUN.Address = "10.77.0.1/24"

	srv, err := NewServer(config)
	if err != nil {
		t.Fatalf("NewServer : %v", err)
	}
	defer srv.Close()

	mockTUN := newMockTUN("test0")
	srv.tun = mockTUN

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)

	// Client 1
	c1, ip1 := connectClient(t, serverAddr, "client1", "PASS1")
	defer c1.Close()

	// Client 2
	c2, ip2 := connectClient(t, serverAddr, "client2", "PASS2")
	defer c2.Close()

	if ip1 == ip2 {
		t.Fatalf("les deux clients doivent avoir des IPs différentes : %s == %s", ip1, ip2)
	}

	t.Logf("Client 1 : %s", ip1)
	t.Logf("Client 2 : %s", ip2)

	// Envoyer un paquet depuis client 1
	pkt1 := fakeIPPkt(net.ParseIP(ip1), net.ParseIP("1.1.1.1"))
	sendTunnelPacket(t, c1, pkt1)
	time.Sleep(100 * time.Millisecond)

	// Envoyer un paquet depuis client 2
	pkt2 := fakeIPPkt(net.ParseIP(ip2), net.ParseIP("8.8.4.4"))
	sendTunnelPacket(t, c2, pkt2)
	time.Sleep(100 * time.Millisecond)

	if len(mockTUN.written) < 2 {
		t.Fatalf("2 paquets attendus dans TUN, obtenu %d", len(mockTUN.written))
	}

	t.Logf("✓ %d paquets correctement routés through TUN", len(mockTUN.written))
}

// connectHelper connecte un client et retourne l'IP tunnel assignée.
func connectClient(t *testing.T, serverAddr *net.UDPAddr, user, pass string) (*net.UDPConn, string) {
	t.Helper()

	client, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("DialUDP pour %s : %v", user, err)
	}
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	// HELLO
	if _, err := client.Write([]byte("HELLO")); err != nil {
		t.Fatalf("HELLO %s : %v", user, err)
	}

	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("CHALLENGE %s : %v", user, err)
	}

	challenge := strings.TrimPrefix(string(buf[:n]), "CHALLENGE ")
	resp := authResponse(t, pass, challenge)

	if _, err := client.Write([]byte("AUTH " + resp)); err != nil {
		t.Fatalf("AUTH %s : %v", user, err)
	}

	n, err = client.Read(buf)
	if err != nil {
		t.Fatalf("AUTH_OK %s : %v", user, err)
	}

	reply := string(buf[:n])
	if !strings.HasPrefix(reply, "AUTH_OK ") {
		t.Fatalf("%s : AUTH_OK attendu, obtenu %q", user, reply)
	}

	ip := strings.TrimPrefix(reply, "AUTH_OK ")
	return client, ip
}

// sendTunnelPacket encode et envoie un paquet IP via le tunnel.
func sendTunnelPacket(t *testing.T, conn *net.UDPConn, pkt []byte) {
	t.Helper()

	tunnelPkt, err := EncodeTunnelPacket(tunnelClientID(conn.LocalAddr().String()), pkt)
	if err != nil {
		t.Fatalf("encode tunnel : %v", err)
	}

	if _, err := conn.Write(tunnelPkt); err != nil {
		t.Fatalf("envoi tunnel : %v", err)
	}
}
