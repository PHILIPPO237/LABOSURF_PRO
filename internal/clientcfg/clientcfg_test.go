package clientcfg

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

func tempStore(t *testing.T) *store.Store {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "labosurf-clientcfg-test")
	os.RemoveAll(dir)
	t.Setenv("LABOSURF_DATA_DIR", dir)
	s, err := store.LoadStore(store.StorePath())
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	return s
}

func prof(host string) srvcfg.Profile {
	p := srvcfg.Default()
	p.Host = host
	return p
}

func TestGenerateUDP(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "u1", Username: "alice", Password: "s3cret", Enabled: true})
	acc, _ := s.GetAccount("u1")
	s.AddGrant("u1", store.EngineUDP, nil)
	acc, _ = s.GetAccount("u1")

	res, err := Generate(acc, store.EngineUDP, prof("10.0.0.1"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(res.ClientLink, "udp://alice@10.0.0.1:5667") {
		t.Fatalf("lien UDP incorrect : %s", res.ClientLink)
	}
	if !strings.Contains(string(res.ServerConfig), `"listen"`) {
		t.Fatalf("config serveur UDP absente : %s", res.ServerConfig)
	}
}

func TestGenerateXrayLink(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "x1", Username: "bob", Enabled: true})
	acc, _ := s.GetAccount("x1")
	s.AddGrant("x1", store.EngineXray, map[string]any{"uuid": "abc-123"})
	acc, _ = s.GetAccount("x1")

	res, err := Generate(acc, store.EngineXray, prof("vpn.example.com"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(res.ClientLink, "vless://abc-123@vpn.example.com:443") {
		t.Fatalf("lien xray incorrect : %s", res.ClientLink)
	}
}

func TestGenerateRequiresGrant(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "n1", Enabled: true})
	s.AddGrant("n1", store.EngineSSH, nil)
	acc, _ := s.GetAccount("n1")

	if _, err := Generate(acc, store.EngineXray, prof("1.1.1.1")); err == nil {
		t.Fatal("attendu une erreur pour un moteur sans grant")
	}
}

func TestGenerateRequiresHost(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "n2", Enabled: true})
	s.AddGrant("n2", store.EngineUDP, nil)
	acc, _ := s.GetAccount("n2")

	if _, err := Generate(acc, store.EngineUDP, prof("")); err == nil {
		t.Fatal("attendu une erreur si l'hôte serveur n'est pas défini")
	}
}

func TestGenerateSSH(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "s1", Username: "carole", Enabled: true})
	s.AddGrant("s1", store.EngineSSH, nil)
	acc, _ := s.GetAccount("s1")

	res, err := Generate(acc, store.EngineSSH, prof("203.0.113.7"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.ClientLink != "ssh carole@203.0.113.7 -p 22" {
		t.Fatalf("lien ssh incorrect : %s", res.ClientLink)
	}
}

// TestGroupedXray vérifie que buildGroupedConfig rassemble TOUS les comptes
// autorisés sur le moteur, pas un seul.
func TestGroupedXray(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "a1", Enabled: true})
	s.CreateAccount(store.Account{ID: "a2", Enabled: true})
	s.AddGrant("a1", store.EngineXray, map[string]any{"uuid": "u1"})
	s.AddGrant("a2", store.EngineXray, map[string]any{"uuid": "u2"})

	p := prof("vpn.example.com")
	out := buildGroupedConfig(store.EngineXray, authorizedAccounts(s, store.EngineXray), p)
	json := string(out)
	if n := strings.Count(json, "a1@labosurf"); n != 1 {
		t.Fatalf("compte a1 attendu 1 fois dans la config, obtenu %d :\n%s", n, json)
	}
	if n := strings.Count(json, "a2@labosurf"); n != 1 {
		t.Fatalf("compte a2 attendu 1 fois dans la config, obtenu %d :\n%s", n, json)
	}
	if !strings.Contains(json, "\"u1\"") || !strings.Contains(json, "\"u2\"") {
		t.Fatalf("UUIDS des deux comptes attendus dans la config :\n%s", json)
	}
}

// TestAuthorizedAccounts exclut les comptes sans grant sur le moteur.
func TestAuthorizedAccounts(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "onlyudp", Enabled: true})
	s.CreateAccount(store.Account{ID: "onlyssh", Enabled: true})
	s.AddGrant("onlyudp", store.EngineUDP, nil)
	s.AddGrant("onlyssh", store.EngineSSH, nil)

	got := authorizedAccounts(s, store.EngineUDP)
	if len(got) != 1 || got[0].ID != "onlyudp" {
		t.Fatalf("seul le compte avec grant UDP est attendu, obtenu %v", got)
	}
}

// TestHysteriaYAML vérifie que la config serveur Hysteria v2 est émit en YAML
// valide (et non en JSON) avec tous les comptes autorisés.
func TestHysteriaYAML(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "c1", Enabled: true, Password: "pw1"})
	s.CreateAccount(store.Account{ID: "c2", Enabled: true})
	s.AddGrant("c1", store.EngineHysteria, map[string]any{"password": "sec1"})
	s.AddGrant("c2", store.EngineHysteria, map[string]any{"password": "sec2"})

	p := srvcfg.Default()
	y := string(hysteriaV2Config(authorizedAccounts(s, store.EngineHysteria), p))

	// Pas de JSON.
	if strings.Contains(y, "{") {
		t.Fatalf("config hysteria ne doit pas être du JSON :\n%s", y)
	}
	// Structure YAML v2.
	if !strings.Contains(y, "listen: "+strconv.Itoa(p.Port(store.EngineHysteria))) {
		t.Fatalf("listen manquant :\n%s", y)
	}
	if !strings.Contains(y, "tls:") || !strings.Contains(y, "  cert:") || !strings.Contains(y, "  key:") {
		t.Fatalf("tls cert/key manquants :\n%s", y)
	}
	if !strings.Contains(y, "auth:") || !strings.Contains(y, "type: userpass") {
		t.Fatalf("auth userpass manquant :\n%s", y)
	}
	if !strings.Contains(y, "c1: sec1") || !strings.Contains(y, "c2: sec2") {
		t.Fatalf("mots de passe des comptes manquants :\n%s", y)
	}
	if !strings.Contains(y, "type: salamander") {
		t.Fatalf("obfs salamander manquant :\n%s", y)
	}
	if !strings.Contains(y, "type: proxy") {
		t.Fatalf("masquerade manquant :\n%s", y)
	}
}

// TestHysteriaPassword défaut utilise le mot de passe du compte si le grant
// n'en porte pas.
func TestHysteriaPasswordDefault(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "c3", Enabled: true, Password: "fallbackpw"})
	s.AddGrant("c3", store.EngineHysteria, nil)
	y := string(hysteriaV2Config(authorizedAccounts(s, store.EngineHysteria), srvcfg.Default()))
	if !strings.Contains(y, "c3: fallbackpw") {
		t.Fatalf("mot de passe fallback attendu :\n%s", y)
	}
}

// TestSSHAuthorizedKeys vérifie la génération authorized_keys en clés
// OpenSSH base64.
func TestSSHAuthorizedKeys(t *testing.T) {
	out := SSHAuthorizedKeys([]SSHUser{
		{Username: "alice", PublicKey: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"},
		{Username: "bob",   PublicKey: "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"},
	})
	if !strings.Contains(out, "ssh-ed25519 ") {
		t.Fatalf("ligne ssh-ed25519 attendue :\n%q", out)
	}
	if !strings.Contains(out, "alice@labosurf") || !strings.Contains(out, "bob@labosurf") {
		t.Fatalf("users attendus :\n%q", out)
	}
	// Deux lignes distinctes.
	if strings.Count(out, "ssh-ed25519") != 2 {
		t.Fatalf("2 clés attendues :\n%q", out)
	}
}

// TestSSHDConfig verrouille le serveur.
func TestSSHDConfig(t *testing.T) {
	out := SSHDConfig(22)
	for _, need := range []string{
		"Port 22",
		"PasswordAuthentication no",
		"PubkeyAuthentication yes",
		"PermitRootLogin no",
		"AllowUsers labosurf",
	} {
		if !strings.Contains(out, need) {
			t.Fatalf("config sshd doit contenir %s :\n%s", need, out)
		}
	}
}

// TestSSHGrouped vérifie que la config groupée SSH liste users + clés.
func TestSSHGrouped(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "g1", Username: "alice", Enabled: true})
	s.CreateAccount(store.Account{ID: "g2", Username: "bob", Enabled: true})
	s.AddGrant("g1", store.EngineSSH, map[string]any{"public_key": "00" + strings.Repeat("11", 31)})
	s.AddGrant("g2", store.EngineSSH, map[string]any{"public_key": "ff" + strings.Repeat("00", 31)})

	out := string(buildGroupedConfig(store.EngineSSH, authorizedAccounts(s, store.EngineSSH), srvcfg.Default()))
	if !strings.Contains(out, "alice") || !strings.Contains(out, "bob") {
		t.Fatalf("users ssh attendus dans la config :\n%s", out)
	}
	if !strings.Contains(out, "authorized_keys") {
		t.Fatalf("mode authorized_keys attendu :\n%s", out)
	}
	if !strings.Contains(out, "\"port\": 22") {
		t.Fatalf("port ssh doit transiter dans la config :\n%s", out)
	}
}