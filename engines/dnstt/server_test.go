package dnstt

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================
// CLIENT DE TEST — construit et parse les octets du protocole DNSTT
// propriétaire ACTUEL de LABOSURF_PRO (pas le vrai dnstt : pas de Noise,
// pas de KCP). Aucun client indépendant n'existe dans ce dépôt pour ce
// moteur. Sockets UDP/TCP réels, vérification ed25519 réelle, backend TCP
// réel et indépendant. Test loopback protocolaire réel, pas un mock.
// ============================================================

func buildDNSTTQuery(t *testing.T, domain string, sessionID [8]byte, psn uint32, payload []byte) []byte {
	t.Helper()

	raw := make([]byte, 0, headerLen+len(payload))
	raw = append(raw, sessionID[:]...)
	seq := make([]byte, 4)
	binary.BigEndian.PutUint32(seq, psn)
	raw = append(raw, seq...)
	raw = append(raw, 0x00) // octet de padding (headerLen = 1+8+4)
	raw = append(raw, payload...)

	encoded := encodeSubdomainB32(raw)
	fullName := encoded + "." + strings.Trim(domain, ".")

	var qname []byte
	for _, label := range strings.Split(fullName, ".") {
		if label == "" {
			continue
		}
		qname = append(qname, byte(len(label)))
		qname = append(qname, []byte(label)...)
	}
	qname = append(qname, 0x00)

	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[0:], 0x5678)
	binary.BigEndian.PutUint16(header[2:], 0x0100)
	binary.BigEndian.PutUint16(header[4:], 1) // QDCOUNT

	q := append(header, qname...)
	q = append(q, 0x00, 0x10, 0x00, 0x01) // QTYPE=16, QCLASS=1
	return q
}

// startCountingTCPBackend démarre un serveur TCP réel qui compte les
// connexions acceptées (pour prouver qu'aucune connexion n'est ouverte
// avant authentification réussie) et répond "ACK:<données>" à ce qu'il
// reçoit.
func startCountingTCPBackend(t *testing.T) (addr string, accepted *int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("démarrage backend TCP de test : %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	var count int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&count, 1)
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
	return ln.Addr().String(), &count
}

func newTestDNSTTServer(t *testing.T, backendAddr string, users []DNSTTUser) *DNSTTServer {
	t.Helper()
	cfg := DNSTTConfig{
		Domain:  "t.example.com",
		Port:    0,
		Backend: backendAddr,
		Users:   users,
	}
	srv, err := NewDNSTTServer(cfg)
	if err != nil {
		t.Fatalf("NewDNSTTServer : %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx) }()
	t.Cleanup(func() { srv.Close() })
	return srv
}

// TestDNSTTValidKeyAuthorized : une signature ed25519 valide (clé privée
// correspondant à une clé publique enregistrée) crée une session, atteint
// le backend TCP réel, et la réponse du backend revient au client.
func TestDNSTTValidKeyAuthorized(t *testing.T) {
	backendAddr, accepted := startCountingTCPBackend(t)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}
	srv := newTestDNSTTServer(t, backendAddr, []DNSTTUser{
		{User: "alice", PublicKey: hex.EncodeToString(pub), Enabled: true},
	})

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)
	client, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("DialUDP : %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	var sessionID [8]byte
	copy(sessionID[:], []byte("sess0001"))

	signature := ed25519.Sign(priv, sessionID[:])
	innerPayload := []byte("HELLO-BACKEND")
	firstPayload := append(append([]byte(nil), signature...), innerPayload...)

	q1 := buildDNSTTQuery(t, "t.example.com", sessionID, 1, firstPayload)
	if _, err := client.Write(q1); err != nil {
		t.Fatalf("envoi requête 1 : %v", err)
	}
	buf := make([]byte, 4096)
	if _, err := client.Read(buf); err != nil {
		t.Fatalf("lecture ack : %v", err)
	}

	// La réception de l'ack ne garantit pas que la goroutine Accept() du
	// backend de test a déjà incrémenté son compteur (net.Dial côté
	// serveur peut réussir une poignée de main TCP légèrement avant que
	// ln.Accept() ne retourne côté backend) : sous charge système plus
	// lourde (ex : suite complète en parallèle d'autres packages), cette
	// fenêtre est parfois observable et faisait échouer ce test de façon
	// intermittente sans rapport avec une régression réelle. On sonde
	// plutôt que de vérifier une seule fois immédiatement.
	deadlineAccepted := time.Now().Add(2 * time.Second)
	var got int32
	for time.Now().Before(deadlineAccepted) {
		got = atomic.LoadInt32(accepted)
		if got == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got != 1 {
		t.Fatalf("attendu 1 connexion backend après authentification réussie, obtenu %d", got)
	}
	if len(srv.Sessions()) != 1 {
		t.Fatalf("attendu 1 session après authentification réussie, obtenu %d", len(srv.Sessions()))
	}
	sess := srv.Sessions()[0]
	if sess.User != "alice" {
		t.Fatalf("session attribuée au mauvais utilisateur : %q", sess.User)
	}

	// Chemin retour : le backend a répondu "ACK:HELLO-BACKEND", relayé de
	// façon asynchrone par backendLoop, sous forme d'un paquet "query-shaped"
	// dont le QNAME encode [sessionID:8][nonce:4][chunk] en base32.
	//
	// Le SetDeadline posé au tout début du test (avant l'envoi de q1) peut
	// avoir presque expiré au moment d'atteindre cette boucle (sous
	// -race, le scheduling est nettement plus lent) : sans le renouveler
	// ici, client.Read() ne bloquerait plus du tout une fois ce délai
	// dépassé (retour immédiat en erreur), transformant la boucle en
	// spin CPU qui n'attend jamais réellement le paquet poussé de façon
	// asynchrone — flaky observé et corrigé.
	deadline := time.Now().Add(4 * time.Second)
	_ = client.SetDeadline(deadline)
	for time.Now().Before(deadline) {
		n, err := client.Read(buf)
		if err != nil {
			continue
		}
		pushPkt := buf[:n]
		if len(pushPkt) < 12 {
			continue
		}
		sub := extractSubdomain(pushPkt[12:], "t.example.com")
		raw, err := decodeSubdomainB32(sub)
		if err != nil || len(raw) < headerLen {
			continue
		}
		chunk := string(raw[headerLen:])
		if chunk == "ACK:HELLO-BACKEND" {
			t.Logf("✓ DATA PATH DNSTT BIDIRECTIONNEL FONCTIONNEL (auth valide) : backend -> serveur -> client = %q", chunk)
			return
		}
	}
	t.Fatal("✗ réponse du backend jamais reçue (ou mal formée) par le client après authentification réussie")
}

// TestDNSTTInvalidKeyRejected : une signature invalide (mauvaise clé
// privée, ou clé publique non enregistrée côté serveur) est refusée :
// aucune session n'est créée, et — point central de cette priorité —
// AUCUNE connexion n'est ouverte vers le backend.
func TestDNSTTInvalidKeyRejected(t *testing.T) {
	backendAddr, accepted := startCountingTCPBackend(t)

	_, wrongPriv, err := ed25519.GenerateKey(rand.Reader) // jamais enregistrée côté serveur
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}
	registeredPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 (enregistrée) : %v", err)
	}
	srv := newTestDNSTTServer(t, backendAddr, []DNSTTUser{
		{User: "bob", PublicKey: hex.EncodeToString(registeredPub), Enabled: true},
	})

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)
	client, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("DialUDP : %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(500 * time.Millisecond))

	var sessionID [8]byte
	copy(sessionID[:], []byte("sess0002"))

	// Signé avec une clé privée dont la clé publique n'est PAS enregistrée
	// côté serveur : la vérification doit échouer pour tous les
	// utilisateurs configurés.
	signature := ed25519.Sign(wrongPriv, sessionID[:])
	payload := append(append([]byte(nil), signature...), []byte("SHOULD-NOT-REACH-BACKEND")...)

	q := buildDNSTTQuery(t, "t.example.com", sessionID, 1, payload)
	if _, err := client.Write(q); err != nil {
		t.Fatalf("envoi requête : %v", err)
	}

	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("lecture réponse : %v", err)
	}
	resp := buf[:n]
	if len(resp) < 4 || resp[2] != 0x81 || resp[3] != 0x83 {
		t.Fatalf("attendu NXDOMAIN pour signature invalide, obtenu resp[2:4]=%x", resp[2:4])
	}

	// Laisser une marge : si une connexion backend devait être ouverte à
	// tort, elle le serait déjà à ce stade.
	time.Sleep(100 * time.Millisecond)

	got := atomic.LoadInt32(accepted)
	if got != 0 {
		t.Fatalf("✗ AUCUNE DONNÉE NE DOIT ATTEINDRE LE BACKEND AVANT AUTHENTIFICATION : %d connexion(s) backend ouverte(s) malgré une signature invalide", got)
	}
	if len(srv.Sessions()) != 0 {
		t.Fatalf("une session a été créée malgré une signature invalide : %+v", srv.Sessions())
	}
	t.Logf("✓ signature invalide correctement rejetée : aucune session, aucune connexion backend (%d)", got)
}
