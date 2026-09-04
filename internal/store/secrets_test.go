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

func TestEnsureSecretsUnknownGrant(t *testing.T) {
	s := setStoreTempDir(t)
	s.CreateAccount(Account{ID: "c4", Enabled: true})
	s.AddGrant("c4", EngineSSH, map[string]any{})
	// pour un moteur sans secret spécifique, pas d'erreur.
	if _, err := s.EnsureEngineSecrets("c4", EngineSSH); err != nil {
		t.Fatalf("EnsureEngineSecrets(ssh): %v", err)
	}
}