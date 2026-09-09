package slowdns

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"
)

// ============================================================
// CLIENT DE TEST — pas un mock : construit et parse les VRAIS octets du
// protocole DNS-tunnel de ce moteur (aucun client SlowDNS indépendant
// n'existe dans ce dépôt). Les sockets (UDP client<->serveur, TCP
// serveur<->backend) sont réels. C'est un test loopback protocolaire réel,
// pas un test réseau entre deux machines distinctes, et pas un mock du
// moteur lui-même.
// ============================================================

// buildQuery construit une requête DNS-tunnel valide : en-tête DNS (12
// octets) + QNAME encodant [sessionID(16)][signature ed25519(64)][payload]
// en base32, suffixé par le domaine configuré + QTYPE/QCLASS.
func buildQuery(t *testing.T, domain string, sessionID [16]byte, priv ed25519.PrivateKey, payload []byte) []byte {
	t.Helper()

	signature := ed25519.Sign(priv, payload)

	raw := make([]byte, 0, 16+64+len(payload))
	raw = append(raw, sessionID[:]...)
	raw = append(raw, signature...)
	raw = append(raw, payload...)

	encoded := encodeSubdomain(raw) // labels séparés par "." (encodeSubdomain package-local)
	fullName := encoded + "." + strings.Trim(domain, ".")

	var qname []byte
	for _, label := range strings.Split(fullName, ".") {
		if label == "" {
			continue
		}
		qname = append(qname, byte(len(label)))
		qname = append(qname, []byte(label)...)
	}
	qname = append(qname, 0x00) // fin du QNAME

	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[0:], 0x1234) // transaction ID arbitraire
	binary.BigEndian.PutUint16(header[2:], 0x0100) // flags: standard query
	binary.BigEndian.PutUint16(header[4:], 1)      // QDCOUNT=1

	q := append(header, qname...)
	q = append(q, 0x00, 0x10) // QTYPE=16 (TXT)
	q = append(q, 0x00, 0x01) // QCLASS=1 (IN)
	return q
}

// parseResponse extrait (sessionID brut, payload) d'une réponse construite
// par buildDNSResponse — dont la disposition d'octets ([nom compressé 2o]
// [type 2o][classe 2o][pseudo-TTL 2o][rdlength 2o][rdata]) est propre à ce
// protocole et n'est PAS un enregistrement DNS conforme RFC1035 (le TTL y
// tient sur 2 octets, pas 4) : c'est un format DNS-like propriétaire, pas
// un vrai DNS. Documenté tel quel, pas "corrigé" vers du RFC1035 strict —
// hors périmètre de cette phase (le bug demandé concernait uniquement la
// construction jamais atteinte de cette réponse, pas son schéma d'octets).
func parseResponse(t *testing.T, query, resp []byte) (sessionID [16]byte, payload []byte) {
	t.Helper()
	if len(resp) < len(query)+10 {
		t.Fatalf("réponse trop courte : %d octets (requête = %d)", len(resp), len(query))
	}
	if !bytesEqual(resp[0:2], query[0:2]) {
		t.Fatalf("ID de transaction non échoé : requête=%x réponse=%x", query[0:2], resp[0:2])
	}
	pos := len(query)
	rdlen := int(binary.BigEndian.Uint16(resp[pos+8 : pos+10]))
	if len(resp) < pos+10+rdlen {
		t.Fatalf("rdlength (%d) dépasse la taille de la réponse", rdlen)
	}
	rdata := resp[pos+10 : pos+10+rdlen]
	if len(rdata) < 16 {
		t.Fatalf("rdata trop courte pour contenir un sessionID : %d octets", len(rdata))
	}
	copy(sessionID[:], rdata[:16])
	payload = append([]byte(nil), rdata[16:]...)
	return sessionID, payload
}

func bytesEqual(a, b []byte) bool {
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

// startEchoBackend démarre un serveur TCP réel qui, pour chaque connexion,
// répond à toute donnée reçue par "ACK:<données>" — un backend distinct du
// client et du serveur SlowDNS, pour démontrer sans ambiguïté que les
// données ont réellement transité serveur -> backend -> serveur -> client
// (et pas simplement été réfléchies par le serveur SlowDNS lui-même).
func startEchoBackend(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("démarrage backend TCP de test : %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						reply := append([]byte("ACK:"), buf[:n]...)
						if _, werr := c.Write(reply); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// TestSlowDNSRoundTrip démontre le chemin complet :
//
//	client -> SlowDNS (auth + décodage requête) -> backend TCP (écriture)
//	backend TCP (réponse) -> SlowDNS (mise en attente) -> client (poll suivant)
//
// Test loopback protocolaire réel : vrais sockets UDP et TCP sur
// 127.0.0.1, vraie vérification de signature ed25519, vrai backend TCP
// indépendant. Le "client" est une construction manuelle des octets du
// protocole (pas un binaire client séparé, qui n'existe pas dans ce
// dépôt) — voir commentaire de package ci-dessus.
func TestSlowDNSRoundTrip(t *testing.T) {
	backendAddr := startEchoBackend(t)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}

	cfg := SlowDNSConfig{
		Domain:  "t.example.com",
		Port:    0,
		Backend: backendAddr,
		Users: []SlowDNSUser{
			{User: "alice", PublicKey: hex.EncodeToString(pub), Enabled: true},
		},
	}

	srv, err := NewSlowDNSServer(cfg)
	if err != nil {
		t.Fatalf("NewSlowDNSServer : %v", err)
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

	var sessionID [16]byte
	copy(sessionID[:], []byte("test-session-1234"))

	// 1) Requête aller : le client envoie "HELLO" au backend via le tunnel.
	q1 := buildQuery(t, cfg.Domain, sessionID, priv, []byte("HELLO"))
	if _, err := client.Write(q1); err != nil {
		t.Fatalf("envoi requête 1 : %v", err)
	}
	resp1Buf := make([]byte, 4096)
	n1, err := client.Read(resp1Buf)
	if err != nil {
		t.Fatalf("lecture réponse 1 : %v", err)
	}
	gotSession1, payload1 := parseResponse(t, q1, resp1Buf[:n1])
	if gotSession1 != sessionID {
		t.Fatalf("sessionID non échoé correctement : attendu %x, obtenu %x", sessionID, gotSession1)
	}

	// 2) Poll jusqu'à obtenir la donnée backend->client mise en attente.
	// Combien de cycles de poll sont nécessaires avant que backendLoop
	// n'ait lu la réponse du backend TCP dépend de l'ordonnancement réel
	// des goroutines (accentué sous -race, qui ralentit tout) — la donnée
	// peut donc arriver dès la réponse 1 ou seulement après plusieurs
	// polls. On accumule ce qui arrive jusqu'à trouver l'ACK attendu, avec
	// un budget de temps borné plutôt qu'un nombre de cycles fixe supposé.
	got := string(payload1)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(got, "ACK:HELLO") && time.Now().Before(deadline) {
		q := buildQuery(t, cfg.Domain, sessionID, priv, nil)
		if _, err := client.Write(q); err != nil {
			t.Fatalf("envoi requête poll : %v", err)
		}
		buf := make([]byte, 4096)
		n, err := client.Read(buf)
		if err != nil {
			t.Fatalf("lecture réponse poll : %v", err)
		}
		gotSession, payload := parseResponse(t, q, buf[:n])
		if gotSession != sessionID {
			t.Fatalf("sessionID non échoé correctement (poll) : attendu %x, obtenu %x", sessionID, gotSession)
		}
		got += string(payload)
	}

	want := "ACK:HELLO"
	if !strings.Contains(got, want) {
		t.Fatalf("✗ DATA PATH SlowDNS NON FONCTIONNEL : payload attendu contenant %q (produit par le backend TCP réel), accumulé %q", want, got)
	}
	t.Logf("✓ DATA PATH SlowDNS BIDIRECTIONNEL FONCTIONNEL : client -> SlowDNS -> backend TCP -> SlowDNS -> client : %q", got)
}

// TestSlowDNSRejectsBadSignature vérifie qu'une signature invalide ne crée
// pas de session et n'atteint jamais le backend.
func TestSlowDNSRejectsBadSignature(t *testing.T) {
	backendAddr := startEchoBackend(t)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader) // clé PUBLIQUE différente enregistrée côté serveur
	if err != nil {
		t.Fatalf("génération clé ed25519 (autre) : %v", err)
	}

	cfg := SlowDNSConfig{
		Domain:  "t.example.com",
		Port:    0,
		Backend: backendAddr,
		Users: []SlowDNSUser{
			{User: "bob", PublicKey: hex.EncodeToString(otherPub), Enabled: true},
		},
	}
	srv, err := NewSlowDNSServer(cfg)
	if err != nil {
		t.Fatalf("NewSlowDNSServer : %v", err)
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
	_ = client.SetDeadline(time.Now().Add(1 * time.Second))

	var sessionID [16]byte
	copy(sessionID[:], []byte("bad-sig-session1"))

	// Signée avec `priv`, mais le serveur ne connaît que `otherPub` : la
	// vérification ed25519.Verify doit échouer.
	q := buildQuery(t, cfg.Domain, sessionID, priv, []byte("SHOULD-BE-REJECTED"))
	if _, err := client.Write(q); err != nil {
		t.Fatalf("envoi requête : %v", err)
	}
	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("lecture réponse : %v", err)
	}
	resp := buf[:n]
	// NXDOMAIN attendu : RCODE=3 dans le octet resp[3] (bits bas).
	if len(resp) < 4 || resp[3]&0x0F != 3 {
		t.Fatalf("attendu NXDOMAIN (RCODE=3) pour signature invalide, obtenu resp[3]=%x", resp[3])
	}
	if len(srv.Sessions()) != 0 {
		t.Fatalf("une session a été créée malgré une signature invalide : %+v", srv.Sessions())
	}
	t.Logf("✓ signature invalide correctement rejetée (NXDOMAIN), aucune session créée")
}

