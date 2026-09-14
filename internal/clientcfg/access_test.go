package clientcfg

// Tests de GenerateFromAccess (M2).
//
// Ce fichier compile dans le même binaire que clientcfg_test.go — il partage
// donc l'import _ "labosurf/engines/ssh" qui y est déclaré et la failure de
// build Windows PRÉ-EXISTANTE (syscall.Credential Linux-only) qui en découle.
// C'est délibéré : les helpers tempRealityKeys / setEngineDataDir sont dans
// clientcfg_test.go (même package), et éviter de les dupliquer est plus
// important que d'introduire un package de test externe supplémentaire.

import (
	"strings"
	"testing"

	"labosurf/internal/service"
	"labosurf/internal/store"
)

// makeAccess crée un Access de test avec des secrets pré-remplis.
func makeAccess(engine string, secrets map[string]any) service.Access {
	a := service.NewAccess("acc-test", "svc-test", engine)
	a.ID = "access-" + engine
	a.Secrets = secrets
	return a
}

// makeSvc crée un Service de test avec host et port.
func makeSvc(engine, host string, port int, components []string) service.Service {
	s := service.Service{
		ID:         "svc-test",
		Name:       "Test Service",
		Engine:     engine,
		Host:       host,
		Ports:      map[string]int{"listen": port},
		Components: components,
	}
	return s
}

// ============================================================
// T1 — GenerateFromAccess xray : lien VLESS généré
// ============================================================

func TestGenerateFromAccessXray(t *testing.T) {
	tempRealityKeys(t)
	acc := makeAccess(store.EngineXray, map[string]any{"uuid": "test-uuid-xray"})
	svc := makeSvc(store.EngineXray, "vpn.example.com", 443, nil)

	res, err := GenerateFromAccess(acc, svc, "alice")
	if err != nil {
		t.Fatalf("GenerateFromAccess xray: %v", err)
	}
	if !strings.HasPrefix(res.ClientLink, "vless://test-uuid-xray@vpn.example.com:443") {
		t.Fatalf("lien VLESS incorrect : %s", res.ClientLink)
	}
	if res.Engine != store.EngineXray {
		t.Fatalf("engine attendu %q, obtenu %q", store.EngineXray, res.Engine)
	}
}

// ============================================================
// T2 — GenerateFromAccess hysteria : lien hysteria://
// ============================================================

func TestGenerateFromAccessHysteria(t *testing.T) {
	acc := makeAccess(store.EngineHysteria, map[string]any{"password": "monpasshysteria"})
	svc := makeSvc(store.EngineHysteria, "vpn.example.com", 8443, nil)

	res, err := GenerateFromAccess(acc, svc, "alice")
	if err != nil {
		t.Fatalf("GenerateFromAccess hysteria: %v", err)
	}
	want := "hysteria://monpasshysteria@vpn.example.com:8443"
	if res.ClientLink != want {
		t.Fatalf("lien hysteria incorrect, attendu %q, obtenu %q", want, res.ClientLink)
	}
}

// ============================================================
// T3 — GenerateFromAccess hysteria2 : lien hysteria2://
// ============================================================

func TestGenerateFromAccessHysteria2(t *testing.T) {
	setEngineDataDir(t)
	acc := makeAccess(store.EngineHysteria2, map[string]any{"password": "monpasshy2"})
	svc := makeSvc(store.EngineHysteria2, "vpn.example.com", 443, nil)

	res, err := GenerateFromAccess(acc, svc, "alice")
	if err != nil {
		t.Fatalf("GenerateFromAccess hysteria2: %v", err)
	}
	if !strings.HasPrefix(res.ClientLink, "hysteria2://alice:monpasshy2@vpn.example.com:443?") {
		t.Fatalf("lien hysteria2 incorrect : %s", res.ClientLink)
	}
	for _, want := range []string{"obfs=salamander", "insecure=1"} {
		if !strings.Contains(res.ClientLink, want) {
			t.Fatalf("lien hysteria2 devrait contenir %q : %s", want, res.ClientLink)
		}
	}
}

// ============================================================
// T4 — GenerateFromAccess tuic : lien tuic://
// ============================================================

func TestGenerateFromAccessTUIC(t *testing.T) {
	acc := makeAccess(store.EngineTUIC, map[string]any{
		"uuid":     "tuic-uuid-123",
		"password": "tuic-pass",
	})
	svc := makeSvc(store.EngineTUIC, "vpn.example.com", 443, nil)

	res, err := GenerateFromAccess(acc, svc, "alice")
	if err != nil {
		t.Fatalf("GenerateFromAccess tuic: %v", err)
	}
	want := "tuic://tuic-uuid-123:tuic-pass@vpn.example.com:443?"
	if !strings.HasPrefix(res.ClientLink, want) {
		t.Fatalf("lien tuic incorrect, attendu préfixe %q, obtenu %q", want, res.ClientLink)
	}
}

// ============================================================
// T5 — GenerateFromAccess wireguard : config wg-quick complète
// ============================================================

func TestGenerateFromAccessWireGuard(t *testing.T) {
	setEngineDataDir(t)
	// Générer de vraies clés X25519 pour un client test
	acc := makeAccess(store.EngineWireGuard, map[string]any{
		"private_key": "test-priv-key-base64",
		"public_key":  "test-pub-key-base64",
		"address":     "10.66.0.10/32",
	})
	svc := makeSvc(store.EngineWireGuard, "vpn.example.com", 51820, nil)

	res, err := GenerateFromAccess(acc, svc, "alice")
	if err != nil {
		t.Fatalf("GenerateFromAccess wireguard: %v", err)
	}
	for _, want := range []string{
		"[Interface]",
		"PrivateKey = test-priv-key-base64",
		"Address = 10.66.0.10/32",
		"[Peer]",
		"AllowedIPs = 0.0.0.0/0, ::/0",
		"Endpoint = vpn.example.com:51820",
		"PersistentKeepalive",
	} {
		if !strings.Contains(res.ClientLink, want) {
			t.Fatalf("config WireGuard devrait contenir %q :\n%s", want, res.ClientLink)
		}
	}
}

// ============================================================
// T6 — GenerateFromAccess ssh : commande ssh + ServerConfig
// ============================================================

func TestGenerateFromAccessSSH(t *testing.T) {
	acc := makeAccess(store.EngineSSH, map[string]any{
		"public_key":  "aabbccdd" + strings.Repeat("ef", 28),
		"private_key": "aabbccdd" + strings.Repeat("ef", 28),
	})
	svc := makeSvc(store.EngineSSH, "vpn.example.com", 22, nil)

	res, err := GenerateFromAccess(acc, svc, "carol")
	if err != nil {
		t.Fatalf("GenerateFromAccess ssh: %v", err)
	}
	if res.ClientLink != "ssh carol@vpn.example.com -p 22" {
		t.Fatalf("lien ssh incorrect : %q", res.ClientLink)
	}
	sc := string(res.ServerConfig)
	if !strings.Contains(sc, "authorized_keys") {
		t.Fatalf("ServerConfig SSH devrait contenir 'authorized_keys' : %s", sc)
	}
	if !strings.Contains(sc, "carol") {
		t.Fatalf("ServerConfig SSH devrait contenir le nom d'utilisateur : %s", sc)
	}
}

// ============================================================
// T7 — GenerateFromAccess dnstt : lien dnstt://
// ============================================================

func TestGenerateFromAccessDNSTT(t *testing.T) {
	acc := makeAccess(store.EngineDNSTT, map[string]any{
		"public_key":  "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
		"private_key": "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
	})
	svc := makeSvc(store.EngineDNSTT, "dns.example.com", 53, nil)

	res, err := GenerateFromAccess(acc, svc, "dave")
	if err != nil {
		t.Fatalf("GenerateFromAccess dnstt: %v", err)
	}
	if !strings.HasPrefix(res.ClientLink, "dnstt://dave@dns.example.com?key=") {
		t.Fatalf("lien dnstt incorrect : %q", res.ClientLink)
	}
}

// ============================================================
// T8 — GenerateFromAccess slowdns : lien slowdns://
// ============================================================

func TestGenerateFromAccessSlowDNS(t *testing.T) {
	acc := makeAccess(store.EngineSlowDNS, map[string]any{
		"public_key":  "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
		"private_key": "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
	})
	svc := makeSvc(store.EngineSlowDNS, "dns.example.com", 53, nil)

	res, err := GenerateFromAccess(acc, svc, "eve")
	if err != nil {
		t.Fatalf("GenerateFromAccess slowdns: %v", err)
	}
	if !strings.HasPrefix(res.ClientLink, "slowdns://eve@dns.example.com?key=") {
		t.Fatalf("lien slowdns incorrect : %q", res.ClientLink)
	}
}

// ============================================================
// T9 — GenerateFromAccess udp : lien udp://
// ============================================================

func TestGenerateFromAccessUDP(t *testing.T) {
	acc := makeAccess(store.EngineUDP, map[string]any{"password": "udp-secret-pw"})
	svc := makeSvc(store.EngineUDP, "vpn.example.com", 5667, nil)

	res, err := GenerateFromAccess(acc, svc, "frank")
	if err != nil {
		t.Fatalf("GenerateFromAccess udp: %v", err)
	}
	want := "udp://frank@vpn.example.com:5667?pass=udp-secret-pw"
	if res.ClientLink != want {
		t.Fatalf("lien UDP incorrect, attendu %q, obtenu %q", want, res.ClientLink)
	}
}

// ============================================================
// T10 — GenerateFromAccess hybride dnstt-xray : lien VLESS du VPN principal
// ============================================================

func TestGenerateFromAccessHybridDnsttXray(t *testing.T) {
	tempRealityKeys(t)
	acc := makeAccess("dnstt-xray", map[string]any{
		"uuid":        "hybrid-uuid-123",
		"public_key":  "aa" + strings.Repeat("11", 31),
		"private_key": "aa" + strings.Repeat("11", 31),
	})
	svc := makeSvc("dnstt-xray", "vpn.example.com", 443, []string{"dnstt", "xray"})

	res, err := GenerateFromAccess(acc, svc, "grace")
	if err != nil {
		t.Fatalf("GenerateFromAccess dnstt-xray: %v", err)
	}
	// Le lien client est celui du composant VPN (xray = VLESS)
	if !strings.HasPrefix(res.ClientLink, "vless://hybrid-uuid-123@vpn.example.com:443") {
		t.Fatalf("lien hybride dnstt-xray incorrect, attendu VLESS : %s", res.ClientLink)
	}
	// Le nom du moteur dans le résultat est le nom hybride complet.
	if res.Engine != "dnstt-xray" {
		t.Fatalf("engine attendu 'dnstt-xray', obtenu %q", res.Engine)
	}
}

// ============================================================
// T11 — GenerateFromAccess hybride slowdns-xray : lien VLESS du VPN principal
// ============================================================

func TestGenerateFromAccessHybridSlowdnsXray(t *testing.T) {
	tempRealityKeys(t)
	acc := makeAccess("slowdns-xray", map[string]any{
		"uuid":        "hybrid-slowdns-uuid",
		"public_key":  "bb" + strings.Repeat("22", 31),
		"private_key": "bb" + strings.Repeat("22", 31),
	})
	svc := makeSvc("slowdns-xray", "vpn.example.com", 443, []string{"slowdns", "xray"})

	res, err := GenerateFromAccess(acc, svc, "hank")
	if err != nil {
		t.Fatalf("GenerateFromAccess slowdns-xray: %v", err)
	}
	if !strings.HasPrefix(res.ClientLink, "vless://hybrid-slowdns-uuid@vpn.example.com:443") {
		t.Fatalf("lien hybride slowdns-xray incorrect : %s", res.ClientLink)
	}
}

// ============================================================
// T12 — Erreur : hôte non défini
// ============================================================

func TestGenerateFromAccessRequiresHost(t *testing.T) {
	acc := makeAccess(store.EngineUDP, map[string]any{"password": "pw"})
	svc := makeSvc(store.EngineUDP, "", 5667, nil)

	if _, err := GenerateFromAccess(acc, svc, "alice"); err == nil {
		t.Fatal("attendu une erreur : hôte du service non défini")
	}
}

// ============================================================
// T13 — Erreur : secrets manquants pour xray (uuid absent)
// ============================================================

func TestGenerateFromAccessMissingSecrets(t *testing.T) {
	tempRealityKeys(t)
	// Xray sans uuid → vlessLink() retourne un lien avec "UUID-MANQUANT" mais
	// sans erreur. Vérification de la robustesse : le lien est quand même généré.
	acc := makeAccess(store.EngineXray, map[string]any{})
	svc := makeSvc(store.EngineXray, "vpn.example.com", 443, nil)

	res, err := GenerateFromAccess(acc, svc, "alice")
	if err != nil {
		t.Fatalf("GenerateFromAccess xray (uuid absent) : %v", err)
	}
	// UUID manquant → placeholder
	if !strings.Contains(res.ClientLink, "UUID-MANQUANT") {
		t.Fatalf("lien xray sans uuid devrait contenir UUID-MANQUANT : %s", res.ClientLink)
	}
}

// ============================================================
// T14 — Erreur : hysteria sans password → erreur explicite
// ============================================================

func TestGenerateFromAccessHysteriaMissingPassword(t *testing.T) {
	acc := makeAccess(store.EngineHysteria, map[string]any{})
	svc := makeSvc(store.EngineHysteria, "vpn.example.com", 8443, nil)

	if _, err := GenerateFromAccess(acc, svc, "alice"); err == nil {
		t.Fatal("attendu une erreur : password hysteria absent")
	}
}

// ============================================================
// T15 — Erreur : moteur inconnu → erreur explicite
// ============================================================

func TestGenerateFromAccessUnknownEngine(t *testing.T) {
	acc := makeAccess("moteur-inconnu", map[string]any{})
	svc := makeSvc("moteur-inconnu", "vpn.example.com", 443, nil)

	if _, err := GenerateFromAccess(acc, svc, "alice"); err == nil {
		t.Fatal("attendu une erreur pour un moteur inconnu")
	}
}

// ============================================================
// T16 — Port par défaut utilisé si le service n'a pas de port défini
// ============================================================

func TestGenerateFromAccessDefaultPort(t *testing.T) {
	acc := makeAccess(store.EngineHysteria, map[string]any{"password": "pw"})
	// Service sans port configuré — doit fallback sur le port par défaut du moteur
	svc := service.Service{
		ID:     "svc-noport",
		Engine: store.EngineHysteria,
		Host:   "vpn.example.com",
		Ports:  map[string]int{},
	}

	res, err := GenerateFromAccess(acc, svc, "alice")
	if err != nil {
		t.Fatalf("GenerateFromAccess hysteria (sans port): %v", err)
	}
	// Le port par défaut d'hysteria est 8443
	if !strings.Contains(res.ClientLink, "vpn.example.com:8443") {
		t.Fatalf("port par défaut 8443 attendu : %s", res.ClientLink)
	}
}
