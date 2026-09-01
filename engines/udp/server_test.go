package main

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// startEchoBackend démarre un faux backend TCP qui renvoie (echo) tout ce
// qu'il reçoit. Retourne son adresse et une fonction d'arrêt.
func startEchoBackend(t *testing.T) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend TCP : %v", err)
	}

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
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String(), func() { _ = ln.Close() }
}

// newTestServer démarre un serveur UDP Engine sur un port UDP éphémère local.
func newTestServer(t *testing.T, users map[string]UserConfig) (*Server, *net.UDPAddr) {
	t.Helper()

	var config Config
	config.Listen = "127.0.0.1:0"
	config.Auth.Mode = "passwords"
	config.Auth.Users = users

	srv, err := NewServer(config)
	if err != nil {
		t.Fatalf("NewServer : %v", err)
	}

	addr, ok := srv.conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatal("adresse locale UDP inattendue")
	}

	return srv, addr
}

// doHandshake exécute HELLO -> CHALLENGE -> AUTH et retourne la réponse
// finale du serveur ainsi que le clientID vu côté serveur.
func doHandshake(t *testing.T, serverAddr *net.UDPAddr, password string) (*net.UDPConn, string, string) {
	t.Helper()

	client, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("DialUDP : %v", err)
	}

	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := client.Write([]byte("HELLO")); err != nil {
		t.Fatalf("envoi HELLO : %v", err)
	}

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
	resp := authResponse(t, password, challenge)

	if _, err := client.Write([]byte("AUTH " + resp)); err != nil {
		t.Fatalf("envoi AUTH : %v", err)
	}

	n, err = client.Read(buf)
	if err != nil {
		t.Fatalf("lecture réponse AUTH : %v", err)
	}

	return client, client.LocalAddr().String(), string(buf[:n])
}

// waitSession attend l'apparition d'une session pour clientID.
func waitSession(t *testing.T, srv *Server, clientID string) *Session {
	t.Helper()

	for i := 0; i < 100; i++ {
		if sess, ok := srv.sessions.Get(clientID); ok {
			return sess
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("aucune session pour %s", clientID)
	return nil
}

// TestServerAuthHandshakeBindsUserConfig valide toute la chaîne :
// AuthManager.Verify -> AuthUser -> Session -> UserConfig.
func TestServerAuthHandshakeBindsUserConfig(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {
			Password:       "CHANGE_ME",
			QuotaBytes:     12345,
			MaxConnections: 2,
			MaxIPs:         3,
			Enabled:        true,
		},
	}

	srv, serverAddr := newTestServer(t, users)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()

	client, clientID, reply := doHandshake(t, serverAddr, "CHANGE_ME")
	defer client.Close()

	if reply != "AUTH_OK" {
		t.Fatalf("AUTH_OK attendu, obtenu %q", reply)
	}

	sess := waitSession(t, srv, clientID)

	if sess.Username != "client1" {
		t.Fatalf("username attendu client1, obtenu %q", sess.Username)
	}

	if sess.UserConfig.QuotaBytes != 12345 ||
		sess.UserConfig.MaxConnections != 2 ||
		sess.UserConfig.MaxIPs != 3 {
		t.Fatalf(
			"la session doit être liée à la config du compte, obtenu %+v",
			sess.UserConfig,
		)
	}
}

// TestServerRejectsWrongPassword : un mauvais mot de passe -> AUTH_FAIL
// et aucune session créée.
func TestServerRejectsWrongPassword(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {Password: "CHANGE_ME", Enabled: true},
	}

	srv, serverAddr := newTestServer(t, users)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()

	client, clientID, reply := doHandshake(t, serverAddr, "MAUVAIS")
	defer client.Close()

	if reply != "AUTH_FAIL" {
		t.Fatalf("AUTH_FAIL attendu, obtenu %q", reply)
	}

	time.Sleep(50 * time.Millisecond)

	if _, ok := srv.sessions.Get(clientID); ok {
		t.Fatal("aucune session ne doit exister après un mauvais mot de passe")
	}
}

// TestServerBlocksExpiredMidSession : un compte qui expire pendant la
// session voit son trafic coupé proprement (ACCOUNT_EXPIRED).
func TestServerBlocksExpiredMidSession(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {Password: "CHANGE_ME", Enabled: true},
	}

	srv, serverAddr := newTestServer(t, users)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()

	client, clientID, reply := doHandshake(t, serverAddr, "CHANGE_ME")
	defer client.Close()

	if reply != "AUTH_OK" {
		t.Fatalf("AUTH_OK attendu, obtenu %q", reply)
	}

	waitSession(t, srv, clientID)

	// Simule l'expiration du compte pendant la session.
	srv.sessions.mu.Lock()
	srv.sessions.sessions[clientID].Expiry = time.Now().Add(-time.Hour)
	srv.sessions.mu.Unlock()

	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("DATA")); err != nil {
		t.Fatalf("envoi DATA : %v", err)
	}

	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("lecture réponse : %v", err)
	}

	if string(buf[:n]) != string(TrafficExpired) {
		t.Fatalf("ACCOUNT_EXPIRED attendu, obtenu %q", string(buf[:n]))
	}

	time.Sleep(50 * time.Millisecond)
	if _, ok := srv.sessions.Get(clientID); ok {
		t.Fatal("la session doit être retirée après expiration")
	}
}

// TestServerBlocksOverQuota : au-delà du quota, le trafic est refusé
// proprement (QUOTA_EXCEEDED).
func TestServerBlocksOverQuota(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {Password: "CHANGE_ME", QuotaBytes: 50, Enabled: true},
	}

	srv, serverAddr := newTestServer(t, users)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()

	client, clientID, reply := doHandshake(t, serverAddr, "CHANGE_ME")
	defer client.Close()

	if reply != "AUTH_OK" {
		t.Fatalf("AUTH_OK attendu, obtenu %q", reply)
	}

	waitSession(t, srv, clientID)

	// Dépasse le quota du compte.
	srv.sessions.mu.Lock()
	srv.sessions.sessions[clientID].BytesOut = 100
	srv.sessions.mu.Unlock()

	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("DATA")); err != nil {
		t.Fatalf("envoi DATA : %v", err)
	}

	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("lecture réponse : %v", err)
	}

	if string(buf[:n]) != string(TrafficQuota) {
		t.Fatalf("QUOTA_EXCEEDED attendu, obtenu %q", string(buf[:n]))
	}
}

// TestServerMaxConnections : la 2e connexion simultanée d'un même compte
// (MaxConnections=1) est refusée avec MAX_CONNECTIONS.
func TestServerMaxConnections(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {
			Password:       "CHANGE_ME",
			MaxConnections: 1,
			MaxIPs:         10,
			Enabled:        true,
		},
	}

	srv, serverAddr := newTestServer(t, users)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()

	// 1re connexion : acceptée.
	c1, _, reply1 := doHandshake(t, serverAddr, "CHANGE_ME")
	defer c1.Close()

	if reply1 != "AUTH_OK" {
		t.Fatalf("1re connexion : AUTH_OK attendu, obtenu %q", reply1)
	}

	// 2e connexion depuis la même IP (port éphémère différent) : refusée.
	c2, _, reply2 := doHandshake(t, serverAddr, "CHANGE_ME")
	defer c2.Close()

	if reply2 != string(AdmitMaxConnections) {
		t.Fatalf("2e connexion : MAX_CONNECTIONS attendu, obtenu %q", reply2)
	}
}

// TestServerAccountsTrafficBothDirections : le trafic entrant (voie brute,
// non-tunnel) ET sortant est comptabilisé.
func TestServerAccountsTrafficBothDirections(t *testing.T) {
	backendAddr, stop := startEchoBackend(t)
	defer stop()

	_ = os.Setenv("LABOSURF_TCP_BACKEND", backendAddr)
	defer os.Unsetenv("LABOSURF_TCP_BACKEND")

	users := map[string]UserConfig{
		"client1": {Password: "CHANGE_ME", Enabled: true},
	}

	srv, serverAddr := newTestServer(t, users)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()

	client, clientID, reply := doHandshake(t, serverAddr, "CHANGE_ME")
	defer client.Close()

	if reply != "AUTH_OK" {
		t.Fatalf("AUTH_OK attendu, obtenu %q", reply)
	}

	waitSession(t, srv, clientID)

	payload := []byte("BONJOUR_BACKEND")

	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("envoi données : %v", err)
	}

	// Réception de l'écho (preuve du transit aller-retour).
	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("lecture écho : %v", err)
	}

	if string(buf[:n]) != string(payload) {
		t.Fatalf("écho incorrect : %q", string(buf[:n]))
	}

	// La comptabilisation sortante est faite juste après l'écriture UDP :
	// on laisse un court instant puis on vérifie.
	var in, out uint64
	for i := 0; i < 100; i++ {
		var ok bool
		in, out, _, ok = srv.sessions.Usage(clientID)
		if ok && in >= uint64(len(payload)) && out >= uint64(len(payload)) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if in < uint64(len(payload)) {
		t.Fatalf("BytesIn attendu >= %d, obtenu %d", len(payload), in)
	}

	if out < uint64(len(payload)) {
		t.Fatalf("BytesOut attendu >= %d, obtenu %d", len(payload), out)
	}
}

// TestServerCleanupClosesStreamOnExpiredSession : lorsqu'une session
// expire, le backend TCP associé est fermé et la session retirée.
func TestServerCleanupClosesStreamOnExpiredSession(t *testing.T) {
	backendAddr, stop := startEchoBackend(t)
	defer stop()

	_ = os.Setenv("LABOSURF_TCP_BACKEND", backendAddr)
	defer os.Unsetenv("LABOSURF_TCP_BACKEND")

	users := map[string]UserConfig{
		"client1": {Password: "CHANGE_ME", Enabled: true},
	}

	srv, serverAddr := newTestServer(t, users)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()

	client, clientID, reply := doHandshake(t, serverAddr, "CHANGE_ME")
	defer client.Close()

	if reply != "AUTH_OK" {
		t.Fatalf("AUTH_OK attendu, obtenu %q", reply)
	}

	waitSession(t, srv, clientID)

	// Ouvre le backend TCP en envoyant des données.
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatalf("envoi : %v", err)
	}

	buf := make([]byte, 4096)
	if _, err := client.Read(buf); err != nil {
		t.Fatalf("lecture écho : %v", err)
	}

	// Attend que le stream TCP soit enregistré côté serveur.
	streamOpen := false
	for i := 0; i < 100; i++ {
		srv.mu.Lock()
		_, streamOpen = srv.streams[clientID]
		srv.mu.Unlock()
		if streamOpen {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !streamOpen {
		t.Fatal("le backend TCP aurait dû être ouvert")
	}

	// Force l'expiration de la session, puis déclenche le nettoyage.
	srv.sessions.mu.Lock()
	srv.sessions.sessions[clientID].LastActivity = time.Now().Add(-time.Hour)
	srv.sessions.mu.Unlock()

	srv.sessions.Cleanup()
	srv.cleanupExpiredStreams()

	srv.mu.Lock()
	_, stillOpen := srv.streams[clientID]
	srv.mu.Unlock()

	if stillOpen {
		t.Fatal("le backend TCP doit être fermé après expiration de la session")
	}

	if _, ok := srv.sessions.Get(clientID); ok {
		t.Fatal("la session doit être retirée après nettoyage")
	}
}
