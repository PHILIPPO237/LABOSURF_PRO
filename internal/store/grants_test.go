package store

import (
	"path/filepath"
	"testing"
)

func storeIn(t *testing.T) *Store {
	t.Helper()
	s, err := LoadStore(filepath.Join(t.TempDir(), "users_db.json"))
	if err != nil {
		t.Fatalf("LoadStore : %v", err)
	}
	return s
}

// Un compte peut se connecter à PLUSIEURS moteurs : le store central porte
// un grant par moteur/protocole (udp, ssh, ...) sur le même compte.
func TestAccountMultiEngineGrants(t *testing.T) {
	s := storeIn(t)

	acc, err := s.CreateAccount(Account{ID: "client-multi", Password: "secret"})
	if err != nil {
		t.Fatalf("CreateAccount : %v", err)
	}
	if acc.HasEngine(EngineUDP) {
		t.Fatal("grant udp ne devrait pas encore exister")
	}

	// Rattaché à plusieurs moteurs : udp et ssh.
	if _, err := s.AddGrant(acc.ID, EngineUDP, map[string]any{"engine": EngineUDP}); err != nil {
		t.Fatalf("AddGrant udp : %v", err)
	}
	if _, err := s.AddGrant(acc.ID, EngineSSH, map[string]any{"engine": EngineSSH}); err != nil {
		t.Fatalf("AddGrant ssh : %v", err)
	}

	got, ok := s.GetAccount(acc.ID)
	if !ok {
		t.Fatal("compte introuvable après grants")
	}

	if !got.HasEngine(EngineUDP) || !got.HasEngine(EngineSSH) {
		t.Fatalf("le compte doit avoir accès à udp et ssh : %v", got.LinkedEngines())
	}

	engs := got.LinkedEngines()
	if len(engs) != 2 {
		t.Fatalf("attendu 2 moteurs, obtenu %v", engs)
	}

	// Le grant UDP est durable : reload depuis le disque.
	reloaded, err := LoadStore(s.path)
	if err != nil {
		t.Fatalf("reload : %v", err)
	}
	r, _ := reloaded.GetAccount(acc.ID)
	if !r.HasEngine(EngineUDP) || !r.HasEngine(EngineSSH) {
		t.Fatalf("grants non persistés : %v", r.LinkedEngines())
	}
}

// Seuls les comptes avec un grant UDP apparaissent dans UserConfigs() que le
// moteur UDP consomme pour l'authentification.
func TestUserConfigsFiltersByUDPGrant(t *testing.T) {
	s := storeIn(t)

	if _, err := s.CreateAccount(Account{ID: "udp-user", Password: "p", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAccount(Account{ID: "ssh-user", Password: "s", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// udp-user a un grant udp ; ssh-user a seulement un grant ssh.
	if _, err := s.AddGrant("udp-user", EngineUDP, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddGrant("ssh-user", EngineSSH, nil); err != nil {
		t.Fatal(err)
	}

	cfg := s.UserConfigs()
	if _, ok := cfg["udp-user"]; !ok {
		t.Fatal("udp-user devrait être dans UserConfigs (grant udp)")
	}
	if _, ok := cfg["ssh-user"]; ok {
		t.Fatal("ssh-user ne devrait PAS être dans UserConfigs (pas de grant udp)")
	}
}

// AddGrant ne doit rien faire si l'accès existe déjà ; RemoveGrant détache.
func TestGrantLifecycle(t *testing.T) {
	s := storeIn(t)
	if _, err := s.CreateAccount(Account{ID: "a", Password: "p", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AddGrant("a", EngineSSH, nil); err != nil {
		t.Fatal(err)
	}
	acc, _ := s.GetAccount("a")
	if !acc.HasEngine(EngineSSH) {
		t.Fatal("grant ssh attendu")
	}

	// Détacher SSH : le compte n'a plus l'accès.
	if _, err := s.RemoveGrant("a", EngineSSH); err != nil {
		t.Fatal(err)
	}
	acc, _ = s.GetAccount("a")
	if acc.HasEngine(EngineSSH) {
		t.Fatal("grant ssh devrait être retiré")
	}
}