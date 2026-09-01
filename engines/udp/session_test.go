package main

import (
	"net"
	"testing"
	"time"
)

func TestSessionCreateAndGet(t *testing.T) {
	sm := NewSessionManager(time.Minute)

	created := sm.CreateWithAddrAndUser("clientA", "client1", nil)
	if created == nil {
		t.Fatal("Create doit retourner une session")
	}

	if !created.Authenticated {
		t.Fatal("une session créée doit être authentifiée")
	}

	got, ok := sm.Get("clientA")
	if !ok {
		t.Fatal("la session doit être récupérable")
	}

	if got.Username != "client1" {
		t.Fatalf("username attendu client1, obtenu %q", got.Username)
	}

	if got.ClientID != "clientA" {
		t.Fatalf("ClientID attendu clientA, obtenu %q", got.ClientID)
	}
}

func TestSessionSetUserConfig(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.CreateWithAddrAndUser("clientA", "client1", nil)

	cfg := UserConfig{
		Password:       "CHANGE_ME",
		QuotaBytes:     1000,
		MaxConnections: 2,
		MaxIPs:         3,
		Enabled:        true,
	}

	if !sm.SetUserConfig("clientA", cfg) {
		t.Fatal("SetUserConfig doit réussir pour une session existante")
	}

	got, _ := sm.Get("clientA")
	if got.UserConfig.QuotaBytes != 1000 || got.UserConfig.MaxConnections != 2 || got.UserConfig.MaxIPs != 3 {
		t.Fatalf("la session doit conserver la config du compte : %+v", got.UserConfig)
	}

	if sm.SetUserConfig("inconnu", cfg) {
		t.Fatal("SetUserConfig doit échouer pour une session absente")
	}
}

func TestSessionBytesAndUsage(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.Create("clientA")

	sm.AddBytesIn("clientA", 100)
	sm.AddBytesIn("clientA", 50)
	sm.AddBytesOut("clientA", 200)

	in, out, total, ok := sm.Usage("clientA")
	if !ok {
		t.Fatal("Usage doit réussir")
	}

	if in != 150 {
		t.Fatalf("BytesIn attendu 150, obtenu %d", in)
	}

	if out != 200 {
		t.Fatalf("BytesOut attendu 200, obtenu %d", out)
	}

	if total != 350 {
		t.Fatalf("total attendu 350, obtenu %d", total)
	}
}

func TestSessionUsageByUser(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.CreateWithUser("a:1", "u", UserConfig{Enabled: true}, udpAddr("10.0.0.1", 1))
	sm.CreateWithUser("a:2", "u", UserConfig{Enabled: true}, udpAddr("10.0.0.2", 2))
	sm.CreateWithUser("b:1", "autre", UserConfig{Enabled: true}, udpAddr("10.0.0.3", 1))

	sm.AddBytesIn("a:1", 100)
	sm.AddBytesOut("a:1", 50)
	sm.AddBytesIn("a:2", 10)
	sm.AddBytesOut("a:2", 5)
	sm.AddBytesIn("b:1", 999)

	in, out, total := sm.UsageByUser("u")
	if in != 110 || out != 55 || total != 165 {
		t.Fatalf("agrégat de u incorrect : in=%d out=%d total=%d", in, out, total)
	}
}

func TestSessionTimeoutExpires(t *testing.T) {
	sm := NewSessionManager(10 * time.Millisecond)
	sm.Create("clientA")

	time.Sleep(80 * time.Millisecond)

	if _, ok := sm.Get("clientA"); ok {
		t.Fatal("une session inactive au-delà du timeout ne doit plus être récupérable")
	}

	if sm.Touch("clientA") {
		t.Fatal("Touch d'une session expirée doit échouer")
	}
}

func TestSessionTouchKeepsAlive(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.Create("clientA")

	// Vieillit artificiellement la session, mais dans la fenêtre de timeout.
	sm.mu.Lock()
	sm.sessions["clientA"].LastActivity = time.Now().Add(-30 * time.Second)
	sm.mu.Unlock()

	if !sm.Touch("clientA") {
		t.Fatal("Touch doit réussir sur une session encore valide")
	}

	// Après Touch, l'activité doit avoir été rafraîchie.
	sm.mu.Lock()
	age := time.Since(sm.sessions["clientA"].LastActivity)
	sm.mu.Unlock()

	if age > 5*time.Second {
		t.Fatalf("Touch doit rafraîchir LastActivity, âge obtenu %s", age)
	}

	if _, ok := sm.Get("clientA"); !ok {
		t.Fatal("une session touchée doit rester active")
	}
}

func TestSessionRemove(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.Create("clientA")

	sm.Remove("clientA")

	if _, ok := sm.Get("clientA"); ok {
		t.Fatal("une session supprimée ne doit plus être récupérable")
	}
}

func TestSessionCleanupRemovesStale(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.Create("frais")
	sm.Create("perime")

	// On force l'ancienneté de "perime" bien au-delà du timeout.
	sm.mu.Lock()
	sm.sessions["perime"].LastActivity = time.Now().Add(-time.Hour)
	sm.mu.Unlock()

	sm.Cleanup()

	if _, ok := sm.Get("perime"); ok {
		t.Fatal("Cleanup doit supprimer les sessions périmées")
	}

	if _, ok := sm.Get("frais"); !ok {
		t.Fatal("Cleanup ne doit pas supprimer les sessions récentes")
	}
}

func TestSessionRemoteAddr(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.Create("clientA")

	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}

	if !sm.SetRemoteAddr("clientA", addr) {
		t.Fatal("SetRemoteAddr doit réussir")
	}

	got, ok := sm.RemoteAddr("clientA")
	if !ok {
		t.Fatal("RemoteAddr doit être récupérable")
	}

	if got.Port != 5000 || !got.IP.Equal(addr.IP) {
		t.Fatalf("adresse distante incorrecte : %v", got)
	}

	// L'adresse retournée doit être une copie (isolation).
	got.Port = 9999
	again, _ := sm.RemoteAddr("clientA")
	if again.Port != 5000 {
		t.Fatal("RemoteAddr doit retourner une copie défensive de l'adresse")
	}
}

func TestSessionCount(t *testing.T) {
	sm := NewSessionManager(time.Minute)

	if sm.Count() != 0 {
		t.Fatalf("compte initial attendu 0, obtenu %d", sm.Count())
	}

	sm.Create("a")
	sm.Create("b")

	if sm.Count() != 2 {
		t.Fatalf("compte attendu 2, obtenu %d", sm.Count())
	}
}

func TestSessionAuthorizeAllowed(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.CreateWithUser("c", "client1", UserConfig{Enabled: true}, nil)

	if d := sm.Authorize("c"); d != TrafficAllowed {
		t.Fatalf("attendu autorisé, obtenu %q", d)
	}
}

func TestSessionAuthorizeNoSession(t *testing.T) {
	sm := NewSessionManager(time.Minute)

	if d := sm.Authorize("absent"); d != TrafficNoSession {
		t.Fatalf("attendu NO_SESSION, obtenu %q", d)
	}
}

func TestSessionAuthorizeExpiredAccount(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	sm.CreateWithUser("c", "client1", UserConfig{ExpiresAt: past, Enabled: true}, nil)

	if d := sm.Authorize("c"); d != TrafficExpired {
		t.Fatalf("attendu ACCOUNT_EXPIRED, obtenu %q", d)
	}
}

func TestSessionAuthorizeExpiresDuringSession(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.CreateWithUser("c", "client1", UserConfig{Enabled: true}, nil)

	// Le compte expire pendant que la session est active.
	sm.mu.Lock()
	sm.sessions["c"].Expiry = time.Now().Add(-time.Second)
	sm.mu.Unlock()

	if d := sm.Authorize("c"); d != TrafficExpired {
		t.Fatalf("attendu ACCOUNT_EXPIRED en cours de session, obtenu %q", d)
	}
}

func TestSessionAuthorizeQuotaReached(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.CreateWithUser("c", "client1", UserConfig{QuotaBytes: 100, Enabled: true}, nil)

	sm.AddBytesIn("c", 60)
	sm.AddBytesOut("c", 40) // total 100 >= 100

	if d := sm.Authorize("c"); d != TrafficQuota {
		t.Fatalf("attendu QUOTA_EXCEEDED, obtenu %q", d)
	}
}

func TestSessionQuotaUnlimited(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.CreateWithUser("c", "client1", UserConfig{QuotaBytes: 0, Enabled: true}, nil)

	sm.AddBytesIn("c", 1_000_000)

	if d := sm.Authorize("c"); d != TrafficAllowed {
		t.Fatalf("quota 0 = illimité, attendu autorisé, obtenu %q", d)
	}
}

func TestSessionExpiryParsedFromConfig(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	sm.CreateWithUser("c", "client1", UserConfig{ExpiresAt: future, Enabled: true}, nil)

	got, _ := sm.Get("c")
	if got.Expiry.IsZero() {
		t.Fatal("Expiry doit être renseigné depuis ExpiresAt")
	}

	if got.expired(time.Now()) {
		t.Fatal("un compte au futur ne doit pas être expiré")
	}
}

func udpAddr(ip string, port int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
}

func TestSessionAdmitMaxConnections(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	cfg := UserConfig{MaxConnections: 2, MaxIPs: 100, Enabled: true}

	sm.CreateWithUser("10.0.0.1:1", "u", cfg, udpAddr("10.0.0.1", 1))
	sm.CreateWithUser("10.0.0.2:1", "u", cfg, udpAddr("10.0.0.2", 1))

	// Deux connexions existent déjà ; une 3e dépasse MaxConnections=2.
	if d := sm.Admit("10.0.0.3:1", "u", cfg, udpAddr("10.0.0.3", 1)); d != AdmitMaxConnections {
		t.Fatalf("attendu MAX_CONNECTIONS, obtenu %q", d)
	}

	cfg.MaxConnections = 3
	if d := sm.Admit("10.0.0.3:1", "u", cfg, udpAddr("10.0.0.3", 1)); d != AdmitOK {
		t.Fatalf("attendu autorisé avec MaxConnections=3, obtenu %q", d)
	}
}

func TestSessionAdmitPerUser(t *testing.T) {
	// Les sessions d'un autre utilisateur ne comptent pas dans la limite.
	sm := NewSessionManager(time.Minute)
	cfg := UserConfig{MaxConnections: 1, MaxIPs: 100, Enabled: true}

	sm.CreateWithUser("10.0.0.9:1", "autre", cfg, udpAddr("10.0.0.9", 1))

	if d := sm.Admit("10.0.0.1:1", "u", cfg, udpAddr("10.0.0.1", 1)); d != AdmitOK {
		t.Fatalf("les sessions d'un autre compte ne doivent pas compter, obtenu %q", d)
	}
}

func TestSessionAdmitMaxIPs(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	cfg := UserConfig{MaxConnections: 100, MaxIPs: 2, Enabled: true}

	sm.CreateWithUser("10.0.0.1:1", "u", cfg, udpAddr("10.0.0.1", 1))
	sm.CreateWithUser("10.0.0.2:1", "u", cfg, udpAddr("10.0.0.2", 1))

	// Une 3e IP distincte dépasse MaxIPs=2.
	if d := sm.Admit("10.0.0.3:1", "u", cfg, udpAddr("10.0.0.3", 1)); d != AdmitMaxIPs {
		t.Fatalf("attendu MAX_IPS, obtenu %q", d)
	}
}

func TestSessionAdmitSameIPNotCountedTwice(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	cfg := UserConfig{MaxConnections: 100, MaxIPs: 2, Enabled: true}

	sm.CreateWithUser("10.0.0.1:1", "u", cfg, udpAddr("10.0.0.1", 1))
	sm.CreateWithUser("10.0.0.2:1", "u", cfg, udpAddr("10.0.0.2", 1))

	// Même IP qu'une session existante : n'ajoute pas de nouvelle IP,
	// donc autorisé même au plafond MaxIPs.
	if d := sm.Admit("10.0.0.1:9999", "u", cfg, udpAddr("10.0.0.1", 9999)); d != AdmitOK {
		t.Fatalf("une IP déjà présente ne doit pas être recomptée, obtenu %q", d)
	}
}

func TestSessionAdmitSameIPMultipleConnections(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	cfg := UserConfig{MaxConnections: 2, MaxIPs: 1, Enabled: true}

	sm.CreateWithUser("10.0.0.1:1000", "u", cfg, udpAddr("10.0.0.1", 1000))

	// 2e connexion depuis la même IP (nouveau port) : autorisée
	// (MaxConnections=2, MaxIPs=1 mais IP identique).
	if d := sm.Admit("10.0.0.1:2000", "u", cfg, udpAddr("10.0.0.1", 2000)); d != AdmitOK {
		t.Fatalf("2e connexion même IP attendue OK, obtenu %q", d)
	}
	sm.CreateWithUser("10.0.0.1:2000", "u", cfg, udpAddr("10.0.0.1", 2000))

	// 3e connexion : dépasse MaxConnections=2.
	if d := sm.Admit("10.0.0.1:3000", "u", cfg, udpAddr("10.0.0.1", 3000)); d != AdmitMaxConnections {
		t.Fatalf("3e connexion attendue MAX_CONNECTIONS, obtenu %q", d)
	}
}

func TestSessionAdmitReplacesSameClientID(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	cfg := UserConfig{MaxConnections: 1, MaxIPs: 1, Enabled: true}

	sm.CreateWithUser("10.0.0.1:1000", "u", cfg, udpAddr("10.0.0.1", 1000))

	// Ré-authentification depuis le MÊME point de terminaison : autorisée
	// (remplace la session existante, pas une connexion supplémentaire).
	if d := sm.Admit("10.0.0.1:1000", "u", cfg, udpAddr("10.0.0.1", 1000)); d != AdmitOK {
		t.Fatalf("ré-auth même clientID attendue OK, obtenu %q", d)
	}
}
