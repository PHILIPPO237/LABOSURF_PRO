package service

import (
	"fmt"
	"os"
	"testing"
)

// setupSecretsDir crée un répertoire access temporaire et retourne une
// fonction de nettoyage.
func setupSecretsDir(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LABOSURF_DATA_DIR", dir)
	return func() { os.Unsetenv("LABOSURF_DATA_DIR") }
}

func newTestAccess(engine string) *Access {
	a := NewAccess("acc-1", "svc-1", engine)
	a.ID = "test-access-" + engine
	return &a
}

// ============================================================
// T1 — Moteur xray : UUID généré
// ============================================================

func TestEnsureAccessSecretsXray(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("xray")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("xray secrets: %v", err)
	}
	if secStr(a.Secrets["uuid"]) == "" {
		t.Fatal("uuid attendu pour xray, absent")
	}
}

// ============================================================
// T2 — Moteur hysteria : password généré
// ============================================================

func TestEnsureAccessSecretsHysteria(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("hysteria")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("hysteria secrets: %v", err)
	}
	if secStr(a.Secrets["password"]) == "" {
		t.Fatal("password attendu pour hysteria, absent")
	}
}

// ============================================================
// T3 — Moteur hysteria2 : password généré
// ============================================================

func TestEnsureAccessSecretsHysteria2(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("hysteria2")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("hysteria2 secrets: %v", err)
	}
	if secStr(a.Secrets["password"]) == "" {
		t.Fatal("password attendu pour hysteria2, absent")
	}
}

// ============================================================
// T4 — Moteur tuic : uuid + password générés
// ============================================================

func TestEnsureAccessSecretsTUIC(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("tuic")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("tuic secrets: %v", err)
	}
	if secStr(a.Secrets["uuid"]) == "" {
		t.Fatal("uuid attendu pour tuic, absent")
	}
	if secStr(a.Secrets["password"]) == "" {
		t.Fatal("password attendu pour tuic, absent")
	}
}

// ============================================================
// T5 — Moteur dnstt : clés Ed25519 générées
// ============================================================

func TestEnsureAccessSecretsDNSTT(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("dnstt")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("dnstt secrets: %v", err)
	}
	if secStr(a.Secrets["public_key"]) == "" {
		t.Fatal("public_key attendu pour dnstt, absent")
	}
	if secStr(a.Secrets["private_key"]) == "" {
		t.Fatal("private_key attendu pour dnstt, absent")
	}
}

// ============================================================
// T6 — Moteur slowdns : clés Ed25519 générées
// ============================================================

func TestEnsureAccessSecretsSlowDNS(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("slowdns")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("slowdns secrets: %v", err)
	}
	if secStr(a.Secrets["public_key"]) == "" {
		t.Fatal("public_key attendu pour slowdns, absent")
	}
	if secStr(a.Secrets["private_key"]) == "" {
		t.Fatal("private_key attendu pour slowdns, absent")
	}
}

// ============================================================
// T7 — Moteur ssh : clés Ed25519 générées
// ============================================================

func TestEnsureAccessSecretsSSH(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("ssh")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("ssh secrets: %v", err)
	}
	if secStr(a.Secrets["public_key"]) == "" {
		t.Fatal("public_key attendu pour ssh, absent")
	}
	if secStr(a.Secrets["private_key"]) == "" {
		t.Fatal("private_key attendu pour ssh, absent")
	}
}

// ============================================================
// T8 — Moteur wireguard : clés X25519 + adresse générées
// ============================================================

func TestEnsureAccessSecretsWireGuard(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("wireguard")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("wireguard secrets: %v", err)
	}
	if secStr(a.Secrets["private_key"]) == "" {
		t.Fatal("private_key attendu pour wireguard, absent")
	}
	if secStr(a.Secrets["public_key"]) == "" {
		t.Fatal("public_key attendu pour wireguard, absent")
	}
	addr := secStr(a.Secrets["address"])
	if addr == "" {
		t.Fatal("address attendu pour wireguard, absent")
	}
	var n int
	if _, err := fmt.Sscanf(addr, wireguardAddrFormat, &n); err != nil {
		t.Fatalf("address WireGuard format invalide: %q", addr)
	}
	if n < 2 || n > 254 {
		t.Fatalf("octet WireGuard %d hors plage [2,254]", n)
	}
}

// ============================================================
// T9 — Moteur udp : password généré
// ============================================================

func TestEnsureAccessSecretsUDP(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("udp")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("udp secrets: %v", err)
	}
	if secStr(a.Secrets["password"]) == "" {
		t.Fatal("password attendu pour udp, absent")
	}
}

// ============================================================
// T10 — Idempotence : secrets déjà présents non remplacés
// ============================================================

func TestEnsureAccessSecretsIdempotent(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("xray")
	a.Secrets = map[string]any{"uuid": "uuid-original-fixe"}

	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("idempotence xray: %v", err)
	}
	if secStr(a.Secrets["uuid"]) != "uuid-original-fixe" {
		t.Fatalf("uuid remplacé alors qu'il était déjà présent : %q", secStr(a.Secrets["uuid"]))
	}
}

// ============================================================
// T11 — Idempotence WireGuard : adresse déjà présente non remplacée
// ============================================================

func TestEnsureAccessSecretsWireGuardIdempotent(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("wireguard")
	// Fournir des clés pré-existantes ET une adresse
	a.Secrets = map[string]any{
		"private_key": "priv-existant",
		"public_key":  "pub-existant",
		"address":     "10.66.0.42/32",
	}

	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("idempotence wireguard: %v", err)
	}
	if secStr(a.Secrets["address"]) != "10.66.0.42/32" {
		t.Fatalf("adresse WireGuard remplacée : %q", secStr(a.Secrets["address"]))
	}
	if secStr(a.Secrets["private_key"]) != "priv-existant" {
		t.Fatal("private_key WireGuard remplacé alors qu'il était déjà présent")
	}
}

// ============================================================
// T12 — Unicité WireGuard : deux accès obtiennent des adresses différentes
// ============================================================

func TestWireGuardAddressUniqueness(t *testing.T) {
	_ = setupSecretsDir(t)

	a1 := newTestAccess("wireguard")
	a1.ID = "wg-acc-1"
	if err := EnsureAccessSecrets(a1, nil); err != nil {
		t.Fatalf("a1: %v", err)
	}
	// Sauvegarder a1 pour que nextAccessWireGuardAddress le trouve
	if err := SaveAccess(a1); err != nil {
		t.Fatalf("SaveAccess a1: %v", err)
	}

	a2 := newTestAccess("wireguard")
	a2.ID = "wg-acc-2"
	if err := EnsureAccessSecrets(a2, nil); err != nil {
		t.Fatalf("a2: %v", err)
	}

	if a1.Secrets["address"] == a2.Secrets["address"] {
		t.Fatalf("collision adresse WireGuard : a1=%q a2=%q",
			a1.Secrets["address"], a2.Secrets["address"])
	}
}

// ============================================================
// T13 — Transition compat : extraUsedWGOctets exclut des adresses existantes
// ============================================================

func TestWireGuardExtraUsedOctets(t *testing.T) {
	_ = setupSecretsDir(t)

	// Simuler que les octets 2, 3, 4 sont déjà utilisés par d'anciens Grants
	extra := map[int]bool{2: true, 3: true, 4: true}

	a := newTestAccess("wireguard")
	if err := EnsureAccessSecrets(a, extra); err != nil {
		t.Fatalf("extra used: %v", err)
	}

	var n int
	addr := secStr(a.Secrets["address"])
	if _, err := fmt.Sscanf(addr, wireguardAddrFormat, &n); err != nil {
		t.Fatalf("format adresse: %v", err)
	}
	if n == 2 || n == 3 || n == 4 {
		t.Fatalf("octet %d déjà marqué comme utilisé dans extraUsed mais quand même attribué", n)
	}
}

// ============================================================
// T14 — Hybride dnstt-xray : secrets des deux composants générés
// ============================================================

func TestEnsureAccessSecretsHybridDnsttXray(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("dnstt-xray")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("dnstt-xray secrets: %v", err)
	}
	// Secrets dnstt
	if secStr(a.Secrets["public_key"]) == "" {
		t.Fatal("public_key attendu pour composant dnstt, absent")
	}
	if secStr(a.Secrets["private_key"]) == "" {
		t.Fatal("private_key attendu pour composant dnstt, absent")
	}
	// Secrets xray
	if secStr(a.Secrets["uuid"]) == "" {
		t.Fatal("uuid attendu pour composant xray, absent")
	}
}

// ============================================================
// T15 — Hybride slowdns-xray : secrets des deux composants générés
// ============================================================

func TestEnsureAccessSecretsHybridSlowdnsXray(t *testing.T) {
	_ = setupSecretsDir(t)
	a := newTestAccess("slowdns-xray")
	if err := EnsureAccessSecrets(a, nil); err != nil {
		t.Fatalf("slowdns-xray secrets: %v", err)
	}
	if secStr(a.Secrets["public_key"]) == "" {
		t.Fatal("public_key attendu pour slowdns, absent")
	}
	if secStr(a.Secrets["uuid"]) == "" {
		t.Fatal("uuid attendu pour xray, absent")
	}
}

// ============================================================
// T16 — splitEngineComponents : moteur simple retourné tel quel
// ============================================================

func TestSplitEngineComponentsSimple(t *testing.T) {
	cases := []string{"xray", "wireguard", "hysteria2", "tuic", "udp", "ssh"}
	for _, eng := range cases {
		parts := splitEngineComponents(eng)
		if len(parts) != 1 || parts[0] != eng {
			t.Errorf("splitEngineComponents(%q) = %v, attendu [%q]", eng, parts, eng)
		}
	}
}

// ============================================================
// T17 — splitEngineComponents : hybride reconnu et décomposé
// ============================================================

func TestSplitEngineComponentsHybrid(t *testing.T) {
	parts := splitEngineComponents("dnstt-xray")
	if len(parts) != 2 || parts[0] != "dnstt" || parts[1] != "xray" {
		t.Errorf("splitEngineComponents(dnstt-xray) = %v, attendu [dnstt xray]", parts)
	}
	parts = splitEngineComponents("slowdns-xray")
	if len(parts) != 2 || parts[0] != "slowdns" || parts[1] != "xray" {
		t.Errorf("splitEngineComponents(slowdns-xray) = %v, attendu [slowdns xray]", parts)
	}
}

// ============================================================
// T18 — splitEngineComponents : nom inconnu avec tiret non décomposé
// ============================================================

func TestSplitEngineComponentsUnknown(t *testing.T) {
	// "foo-bar" n'est pas composé de moteurs connus → retourné tel quel
	parts := splitEngineComponents("foo-bar")
	if len(parts) != 1 || parts[0] != "foo-bar" {
		t.Errorf("splitEngineComponents(foo-bar) = %v, attendu [foo-bar]", parts)
	}
}
