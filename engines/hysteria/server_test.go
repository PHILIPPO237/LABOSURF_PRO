package hysteria

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// ============================================================
// AVERTISSEMENT DE PROTOCOLE — à lire avant de faire confiance à ces tests
//
// Ce moteur n'implémente PAS Hysteria2 officiel (pas de QUIC, pas de
// TLS 1.3, pas de congestion control BBR, pas de Salamander obfuscation).
// C'est un protocole UDP propriétaire distinct, avec ses propres magic
// numbers (helloMagic/authMagic/dataMagic/pingMagic) et une obfuscation
// XOR simple sur 16 octets. Le client réel qui pourrait parler ce
// protocole n'existe pas dans ce dépôt (contrairement à engines/udp qui a
// un client.go). Les tests ci-dessous construisent et parsent donc les
// octets du protocole ACTUEL de LABOSURF_PRO directement — ce sont des
// tests loopback protocolaires réels (vrais sockets UDP, vraie vérif HMAC,
// vrai backend UDP indépendant), pas un test contre un Hysteria2 officiel,
// et pas un mock du moteur.
// ============================================================

// startEchoBackendUDP démarre un serveur UDP réel qui répond à tout paquet
// reçu par "ACK:<données>" — un backend distinct du serveur Hysteria et du
// client, pour démontrer sans ambiguïté un aller-retour réel.
func startEchoBackendUDP(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("démarrage backend UDP de test : %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	go func() {
		buf := make([]byte, 4096)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			reply := append([]byte("ACK:"), buf[:n]...)
			_, _ = conn.WriteToUDP(reply, addr)
		}
	}()
	return conn.LocalAddr().String()
}

// TestHysteriaRoundTrip démontre le chemin complet :
//
//	HELLO -> challenge -> AUTH (HMAC) -> OK -> DATA (client->backend)
//	-> backend UDP réel -> DATA retour (backend->client)
//
// Test loopback protocolaire réel sur le protocole ACTUEL du dépôt (voir
// avertissement de package ci-dessus) — pas un test contre Hysteria2
// officiel.
func TestHysteriaRoundTrip(t *testing.T) {
	backendAddr := startEchoBackendUDP(t)

	const obfsKey = "test-obfs-key"
	const username = "alice"
	const password = "s3cret-hysteria-password"

	cfg := HysteriaConfig{
		Port:    0,
		Obfs:    obfsKey,
		Backend: backendAddr,
		Users: []HysteriaUser{
			{Name: username, Password: password, Enabled: true},
		},
	}
	srv, err := NewHysteriaServer(cfg)
	if err != nil {
		t.Fatalf("NewHysteriaServer : %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)
	client, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("DialUDP : %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	rawID := make([]byte, 16)
	copy(rawID, []byte("hysteria-test-id"))

	// 1) HELLO
	hello := make([]byte, 4+16)
	binary.BigEndian.PutUint32(hello[0:4], helloMagic)
	copy(hello[4:20], rawID)
	obfuscate(hello, []byte(obfsKey))
	if _, err := client.Write(hello); err != nil {
		t.Fatalf("envoi HELLO : %v", err)
	}

	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("lecture challenge : %v", err)
	}
	challengeResp := append([]byte(nil), buf[:n]...)
	obfuscate(challengeResp, []byte(obfsKey))
	if len(challengeResp) < 4+16+32 {
		t.Fatalf("réponse challenge trop courte : %d octets", len(challengeResp))
	}
	if magic := binary.BigEndian.Uint32(challengeResp[0:4]); magic != authMagic {
		t.Fatalf("magic inattendu dans la réponse challenge : %x", magic)
	}
	if !bytesEq(challengeResp[4:20], rawID) {
		t.Fatalf("sessionID non échoé dans le challenge : attendu %x, obtenu %x", rawID, challengeResp[4:20])
	}

	// 2) AUTH : HMAC-SHA256(password, clientNonce)
	clientNonce := make([]byte, 16)
	copy(clientNonce, []byte("client-nonce-16b"))
	serverNonce := make([]byte, 16)
	mac := hmacSHA256([]byte(password), clientNonce)

	auth := make([]byte, 4+16+16+32+16)
	binary.BigEndian.PutUint32(auth[0:4], authMagic)
	copy(auth[4:20], rawID)
	copy(auth[20:36], clientNonce)
	copy(auth[36:68], mac)
	copy(auth[68:84], serverNonce)
	obfuscate(auth, []byte(obfsKey))
	if _, err := client.Write(auth); err != nil {
		t.Fatalf("envoi AUTH : %v", err)
	}

	n, err = client.Read(buf)
	if err != nil {
		t.Fatalf("lecture réponse AUTH : %v", err)
	}
	okResp := append([]byte(nil), buf[:n]...)
	obfuscate(okResp, []byte(obfsKey))
	if len(okResp) < 8 || binary.BigEndian.Uint32(okResp[0:4]) != authMagic || string(okResp[4:6]) != "OK" {
		t.Fatalf("réponse AUTH inattendue : % x", okResp)
	}
	t.Logf("✓ authentification HMAC acceptée, session créée")

	// 3) DATA aller : payload="PING" + HMAC(password, "PING") en fin de
	// paquet (déclenche la vérification HMAC de handleData, avec la clé du
	// bon utilisateur — c'était le bug corrigé).
	payload := []byte("PING")
	payloadHMAC := hmacSHA256([]byte(password), payload)

	data := make([]byte, 4+16+4+len(payload)+len(payloadHMAC))
	binary.BigEndian.PutUint32(data[0:4], dataMagic)
	copy(data[4:20], rawID)
	binary.BigEndian.PutUint32(data[20:24], 1) // sequence
	copy(data[24:24+len(payload)], payload)
	copy(data[24+len(payload):], payloadHMAC)
	obfuscate(data, []byte(obfsKey))
	if _, err := client.Write(data); err != nil {
		t.Fatalf("envoi DATA : %v", err)
	}

	// 4) DATA retour : le backend UDP réel a répondu "ACK:PING", relayé de
	// façon asynchrone par backendLoop dès qu'il arrive.
	n, err = client.Read(buf)
	if err != nil {
		t.Fatalf("lecture DATA retour : %v", err)
	}
	back := append([]byte(nil), buf[:n]...)
	obfuscate(back, []byte(obfsKey))
	if len(back) < 4+16+4 {
		t.Fatalf("paquet DATA retour trop court : %d octets", len(back))
	}
	if magic := binary.BigEndian.Uint32(back[0:4]); magic != dataMagic {
		t.Fatalf("magic inattendu dans DATA retour : %x", magic)
	}
	if !bytesEq(back[4:20], rawID) {
		t.Fatalf("✗ sessionID retour incorrect : attendu %x, obtenu %x (bug de troncature hex ?)", rawID, back[4:20])
	}
	gotPayload := string(back[24:])
	if gotPayload != "ACK:PING" {
		t.Fatalf("✗ DATA PATH Hysteria NON FONCTIONNEL : payload attendu \"ACK:PING\" (produit par le backend UDP réel), obtenu %q", gotPayload)
	}
	t.Logf("✓ DATA PATH Hysteria BIDIRECTIONNEL FONCTIONNEL : client -> Hysteria -> backend UDP -> Hysteria -> client : %q", gotPayload)
}

// TestHysteriaRejectsBadHMAC vérifie qu'une authentification avec un
// mauvais mot de passe (donc un mauvais HMAC) est refusée : aucune session
// n'est créée et aucune réponse OK n'est renvoyée.
func TestHysteriaRejectsBadHMAC(t *testing.T) {
	backendAddr := startEchoBackendUDP(t)
	const obfsKey = "test-obfs-key"

	cfg := HysteriaConfig{
		Port:    0,
		Obfs:    obfsKey,
		Backend: backendAddr,
		Users: []HysteriaUser{
			{Name: "alice", Password: "correct-password", Enabled: true},
		},
	}
	srv, err := NewHysteriaServer(cfg)
	if err != nil {
		t.Fatalf("NewHysteriaServer : %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)
	client, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("DialUDP : %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(500 * time.Millisecond))

	rawID := make([]byte, 16)
	copy(rawID, []byte("bad-hmac-test-id"))

	clientNonce := make([]byte, 16)
	copy(clientNonce, []byte("client-nonce-16b"))
	wrongMAC := hmacSHA256([]byte("WRONG-PASSWORD"), clientNonce)

	auth := make([]byte, 4+16+16+32+16)
	binary.BigEndian.PutUint32(auth[0:4], authMagic)
	copy(auth[4:20], rawID)
	copy(auth[20:36], clientNonce)
	copy(auth[36:68], wrongMAC)
	obfuscate(auth, []byte(obfsKey))
	if _, err := client.Write(auth); err != nil {
		t.Fatalf("envoi AUTH : %v", err)
	}

	buf := make([]byte, 4096)
	_, err = client.Read(buf)
	if err == nil {
		t.Fatal("une réponse a été reçue pour une authentification avec mauvais mot de passe — attendu : aucune réponse (timeout)")
	}
	if len(srv.Sessions()) != 0 {
		t.Fatalf("une session existe malgré l'échec d'authentification : %+v", srv.Sessions())
	}
	t.Logf("✓ authentification avec mauvais mot de passe correctement refusée (pas de réponse, pas de session)")
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
