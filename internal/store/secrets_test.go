package store

import (
	"os"
	"path/filepath"
	"testing"
)

func setStoreTempDir(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "labosurf-store-secrets-test")
	os.RemoveAll(dir)
	t.Setenv("LABOSURF_DATA_DIR", dir)
	s, err := LoadStore(StorePath())
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	return s
}

func TestEnsureSecretsXray(t *testing.T) {
	s := setStoreTempDir(t)
	s.CreateAccount(Account{ID: "c1", Enabled: true})
	s.AddGrant("c1", EngineXray, map[string]any{})
	acc, err := s.EnsureEngineSecrets("c1", EngineXray)
	if err != nil {
		t.Fatalf("EnsureEngineSecrets: %v", err)
	}
	uuid := acc.Grants[EngineXray].Config["uuid"].(string)
	if uuid == "" {
		t.Fatal("uuid xray absent")
	}
	// Idempotence : le uuid ne change pas au 2e appel.
	acc2, _ := s.EnsureEngineSecrets("c1", EngineXray)
	if acc2.Grants[EngineXray].Config["uuid"].(string) != uuid {
		t.Fatal("uuid régénéré alors que déjà présent")
	}
}

func TestEnsureSecretsDNSTT(t *testing.T) {
	s := setStoreTempDir(t)
	s.CreateAccount(Account{ID: "c2", Enabled: true})
	s.AddGrant("c2", EngineDNSTT, map[string]any{})
	acc, err := s.EnsureEngineSecrets("c2", EngineDNSTT)
	if err != nil {
		t.Fatalf("EnsureEngineSecrets: %v", err)
	}
	cfg := acc.Grants[EngineDNSTT].Config
	if cfg["public_key"].(string) == "" || cfg["private_key"].(string) == "" {
		t.Fatal("clés Ed25519 dnstt absentes")
	}
}

func TestEnsureSecretsHysteria(t *testing.T) {
	s := setStoreTempDir(t)
	s.CreateAccount(Account{ID: "c3", Enabled: true})
	s.AddGrant("c3", EngineHysteria, map[string]any{})
	acc, _ := s.EnsureEngineSecrets("c3", EngineHysteria)
	if acc.Grants[EngineHysteria].Config["password"].(string) == "" {
		t.Fatal("password hysteria absent")
	}
}

func TestEnsureSecretsTUIC(t *testing.T) {
	s := setStoreTempDir(t)
	s.CreateAccount(Account{ID: "c5", Enabled: true})
	s.AddGrant("c5", EngineTUIC, map[string]any{})
	acc, err := s.EnsureEngineSecrets("c5", EngineTUIC)
	if err != nil {
		t.Fatalf("EnsureEngineSecrets: %v", err)
	}
	cfg := acc.Grants[EngineTUIC].Config
	uuid := cfg["uuid"].(string)
	pw := cfg["password"].(string)
	if uuid == "" || pw == "" {
		t.Fatal("uuid et/ou mot de passe tuic absents")
	}
	// Idempotence : les deux secrets ne changent pas au 2e appel.
	acc2, _ := s.EnsureEngineSecrets("c5", EngineTUIC)
	cfg2 := acc2.Grants[EngineTUIC].Config
	if cfg2["uuid"].(string) != uuid || cfg2["password"].(string) != pw {
		t.Fatal("secrets tuic régénérés alors que déjà présents")
	}
}

func TestEnsureSecretsWireGuardGeneratesKeyAndAddress(t *testing.T) {
	s := setStoreTempDir(t)
	s.CreateAccount(Account{ID: "wg1", Enabled: true})
	s.AddGrant("wg1", EngineWireGuard, map[string]any{})
	acc, err := s.EnsureEngineSecrets("wg1", EngineWireGuard)
	if err != nil {
		t.Fatalf("EnsureEngineSecrets: %v", err)
	}
	cfg := acc.Grants[EngineWireGuard].Config
	priv := cfg["private_key"].(string)
	pub := cfg["public_key"].(string)
	addr := cfg["address"].(string)
	if priv == "" || pub == "" {
		t.Fatal("clés WireGuard absentes")
	}
	if priv == pub {
		t.Fatal("clé privée et clé publique identiques — génération incorrecte")
	}
	if addr != "10.66.0.2/32" {
		t.Fatalf("première adresse allouée attendue 10.66.0.2/32, obtenu %q", addr)
	}

	// Idempotence : rien ne change au 2e appel.
	acc2, _ := s.EnsureEngineSecrets("wg1", EngineWireGuard)
	cfg2 := acc2.Grants[EngineWireGuard].Config
	if cfg2["private_key"].(string) != priv || cfg2["address"].(string) != addr {
		t.Fatal("secrets WireGuard régénérés alors que déjà présents")
	}
}

// TestEnsureSecretsWireGuardAddressesUniqueAcrossAccounts vérifie que
// plusieurs comptes reçoivent des adresses VPN DIFFÉRENTES — condition
// nécessaire au fonctionnement de WireGuard (deux peers avec la même
// adresse casseraient le routage des deux), et raison d'être de cet ajout
// à EnsureEngineSecrets (voir son commentaire, mission P2).
func TestEnsureSecretsWireGuardAddressesUniqueAcrossAccounts(t *testing.T) {
	s := setStoreTempDir(t)
	ids := []string{"wgA", "wgB", "wgC"}
	seen := map[string]bool{}
	for _, id := range ids {
		s.CreateAccount(Account{ID: id, Enabled: true})
		s.AddGrant(id, EngineWireGuard, map[string]any{})
		acc, err := s.EnsureEngineSecrets(id, EngineWireGuard)
		if err != nil {
			t.Fatalf("EnsureEngineSecrets(%s): %v", id, err)
		}
		addr := acc.Grants[EngineWireGuard].Config["address"].(string)
		if seen[addr] {
			t.Fatalf("adresse WireGuard %q réutilisée pour plusieurs comptes", addr)
		}
		seen[addr] = true
	}
	if len(seen) != 3 {
		t.Fatalf("3 adresses distinctes attendues, obtenu %d : %v", len(seen), seen)
	}
	// Adresses séquentielles attendues : .2, .3, .4 (ordre de première
	// génération, .1 réservée au serveur).
	for _, want := range []string{"10.66.0.2/32", "10.66.0.3/32", "10.66.0.4/32"} {
		if !seen[want] {
			t.Fatalf("adresse attendue %q non allouée : %v", want, seen)
		}
	}
}

// TestEnsureSecretsWireGuardReusesFreedAddress vérifie que
// nextWireGuardAddress retrouve bien le plus petit numéro LIBRE (pas
// seulement "après le dernier") — utile après suppression d'un compte.
func TestEnsureSecretsWireGuardReusesFreedAddress(t *testing.T) {
	s := setStoreTempDir(t)
	s.CreateAccount(Account{ID: "wgX", Enabled: true})
	s.AddGrant("wgX", EngineWireGuard, map[string]any{"address": "10.66.0.2/32"})
	s.CreateAccount(Account{ID: "wgY", Enabled: true})
	s.AddGrant("wgY", EngineWireGuard, map[string]any{})

	acc, err := s.EnsureEngineSecrets("wgY", EngineWireGuard)
	if err != nil {
		t.Fatalf("EnsureEngineSecrets: %v", err)
	}
	got := acc.Grants[EngineWireGuard].Config["address"].(string)
	if got != "10.66.0.3/32" {
		t.Fatalf("adresse attendue 10.66.0.3/32 (0.2 déjà prise), obtenu %q", got)
	}
}

func TestEnsureSecretsUnknownGrant(t *testing.T) {
	s := setStoreTempDir(t)
	s.CreateAccount(Account{ID: "c4", Enabled: true})
	s.AddGrant("c4", EngineSSH, map[string]any{})
	// pour un moteur sans secret spécifique, pas d'erreur.
	if _, err := s.EnsureEngineSecrets("c4", EngineSSH); err != nil {
		t.Fatalf("EnsureEngineSecrets(ssh): %v", err)
	}
}