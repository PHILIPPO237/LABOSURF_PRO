package clientcfg

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "labosurf/engines/dnstt"
	_ "labosurf/engines/slowdns"
	_ "labosurf/engines/ssh"
	"labosurf/engines/xray"
	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// tempRealityKeys génère de vraies clés REALITY dans un répertoire
// temporaire et pointe le moteur xray dessus (LABOSURF_XRAY_REALITY_DIR),
// pour que vlessLink() puisse charger une clé publique réelle sans
// dépendre d'une installation Xray sur la machine de test.
func tempRealityKeys(t *testing.T) *xray.RealityKeyPair {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LABOSURF_XRAY_REALITY_DIR", dir)
	keys, err := xray.EnsureRealityKeys(dir)
	if err != nil {
		t.Fatalf("EnsureRealityKeys: %v", err)
	}
	return keys
}

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
	keys := tempRealityKeys(t)
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
	// Non-régression : le lien ne doit plus jamais contenir la clé
	// placeholder — il doit porter la vraie clé publique REALITY générée
	// pour ce serveur (format base64url attendu par un client VLESS réel).
	if strings.Contains(res.ClientLink, "PUBLIC_KEY_PLACEHOLDER") {
		t.Fatalf("le lien contient encore la clé placeholder : %s", res.ClientLink)
	}
	wantPbk := "pbk=" + keys.PublicKeyForVLESS()
	if !strings.Contains(res.ClientLink, wantPbk) {
		t.Fatalf("le lien ne contient pas la vraie clé publique REALITY (%s) : %s", wantPbk, res.ClientLink)
	}
}

// TestGenerateXrayLinkWithoutRealityKeys vérifie que Generate() échoue
// explicitement (au lieu de renvoyer un lien avec une clé factice) quand
// les clés REALITY du serveur n'existent pas encore.
func TestGenerateXrayLinkWithoutRealityKeys(t *testing.T) {
	s := tempStore(t)
	t.Setenv("LABOSURF_XRAY_REALITY_DIR", filepath.Join(t.TempDir(), "absent"))
	s.CreateAccount(store.Account{ID: "x2", Username: "bob2", Enabled: true})
	acc, _ := s.GetAccount("x2")
	s.AddGrant("x2", store.EngineXray, map[string]any{"uuid": "def-456"})
	acc, _ = s.GetAccount("x2")

	if _, err := Generate(acc, store.EngineXray, prof("vpn.example.com")); err == nil {
		t.Fatal("attendu une erreur : clés REALITY absentes")
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

// TestGenerateHysteria2Link vérifie le format du lien client hysteria2://
// (schéma officiel) et que la config serveur générée porte bien l'utilisateur.
func TestGenerateHysteria2Link(t *testing.T) {
	s := tempStore(t)
	t.Setenv("LABOSURF_DATA_DIR", t.TempDir())
	s.CreateAccount(store.Account{ID: "hy1", Username: "iris", Enabled: true})
	s.AddGrant("hy1", store.EngineHysteria2, map[string]any{"password": "sekret2"})
	acc, _ := s.GetAccount("hy1")

	res, err := Generate(acc, store.EngineHysteria2, prof("vpn.example.com"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(res.ClientLink, "hysteria2://iris:sekret2@vpn.example.com:443?") {
		t.Fatalf("lien hysteria2 incorrect : %s", res.ClientLink)
	}
	for _, want := range []string{"obfs=salamander", "obfs-password=", "sni=vpn.example.com", "insecure=1"} {
		if !strings.Contains(res.ClientLink, want) {
			t.Fatalf("lien hysteria2 devrait contenir %q : %s", want, res.ClientLink)
		}
	}
	sc := string(res.ServerConfig)
	// La clé userpass est l'ID du compte (hy1), pas son Username (iris) —
	// même convention que hysteriaV2Config (voir TestHysteriaYAML : "c1: sec1").
	if !strings.Contains(sc, "hy1: sekret2") {
		t.Fatalf("config serveur hysteria2 ne porte pas l'utilisateur :\n%s", sc)
	}
	if !strings.Contains(sc, "type: salamander") || !strings.Contains(sc, "type: proxy") {
		t.Fatalf("config serveur hysteria2 incomplète (obfs/masquerade attendus) :\n%s", sc)
	}
}

// TestGenerateHysteria2PasswordDefault vérifie le repli sur le mot de passe
// du compte quand le grant n'en porte pas (même convention que Hysteria/TUIC).
func TestGenerateHysteria2PasswordDefault(t *testing.T) {
	s := tempStore(t)
	t.Setenv("LABOSURF_DATA_DIR", t.TempDir())
	s.CreateAccount(store.Account{ID: "hy2", Username: "jack", Enabled: true, Password: "fallbackpw2"})
	s.AddGrant("hy2", store.EngineHysteria2, nil)
	acc, _ := s.GetAccount("hy2")

	res, err := Generate(acc, store.EngineHysteria2, prof("vpn.example.com"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.ClientLink, "jack:fallbackpw2@") {
		t.Fatalf("mot de passe fallback attendu dans le lien : %s", res.ClientLink)
	}
}

// TestGroupedHysteria2 vérifie que buildGroupedConfig rassemble TOUS les
// comptes autorisés pour hysteria2, au format officiel (YAML), et jamais au
// format JSON du moteur "hysteria" maison.
func TestGroupedHysteria2(t *testing.T) {
	s := tempStore(t)
	t.Setenv("LABOSURF_DATA_DIR", t.TempDir())
	s.CreateAccount(store.Account{ID: "hg1", Enabled: true})
	s.CreateAccount(store.Account{ID: "hg2", Enabled: true})
	s.AddGrant("hg1", store.EngineHysteria2, map[string]any{"password": "p1"})
	s.AddGrant("hg2", store.EngineHysteria2, map[string]any{"password": "p2"})

	p := prof("vpn.example.com")
	out := string(buildGroupedConfig(store.EngineHysteria2, authorizedAccounts(s, store.EngineHysteria2), p))

	if strings.Contains(out, "{") {
		t.Fatalf("config hysteria2 ne doit pas être du JSON :\n%s", out)
	}
	if !strings.Contains(out, "listen: :443") {
		t.Fatalf("listen sur le port officiel 443 attendu :\n%s", out)
	}
	if !strings.Contains(out, "hg1: p1") || !strings.Contains(out, "hg2: p2") {
		t.Fatalf("mots de passe des deux comptes attendus :\n%s", out)
	}
	if !strings.Contains(out, "/hysteria2/cert.pem") {
		t.Fatalf("certificat propre au moteur hysteria2 attendu (jamais celui du moteur maison) :\n%s", out)
	}
	if strings.Contains(out, "fuckGFW") {
		t.Fatalf("config hysteria2 ne doit jamais contenir l'obfuscation du moteur maison :\n%s", out)
	}
}

// TestGenerateWireGuardClientConfig vérifie que Generate() produit un
// fichier .conf client WireGuard réellement exploitable (format standard),
// avec la clé privée du COMPTE, la clé publique du SERVEUR, l'endpoint réel
// et les AllowedIPs — jamais un pseudo-format (mission P2, Étape 9).
func TestGenerateWireGuardClientConfig(t *testing.T) {
	s := tempStore(t)
	t.Setenv("LABOSURF_DATA_DIR", t.TempDir())
	s.CreateAccount(store.Account{ID: "wgc1", Username: "karim", Enabled: true})
	if _, err := s.AddGrant("wgc1", store.EngineWireGuard, nil); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}
	if _, err := s.EnsureEngineSecrets("wgc1", store.EngineWireGuard); err != nil {
		t.Fatalf("EnsureEngineSecrets: %v", err)
	}
	acc, _ := s.GetAccount("wgc1")

	res, err := Generate(acc, store.EngineWireGuard, prof("vpn.example.com"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cfg := res.ClientLink
	accPriv := acc.Grants[store.EngineWireGuard].Config["private_key"].(string)
	_, serverPub, err := wireguardServerKeys()
	if err != nil {
		t.Fatalf("wireguardServerKeys: %v", err)
	}

	for _, want := range []string{
		"[Interface]",
		"PrivateKey = " + accPriv,
		"Address = 10.66.0.2/32",
		"[Peer]",
		"PublicKey = " + serverPub,
		"AllowedIPs = 0.0.0.0/0, ::/0",
		"Endpoint = vpn.example.com:51820",
		"PersistentKeepalive",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config client WireGuard devrait contenir %q :\n%s", want, cfg)
		}
	}
	// La clé privée du COMPTE ne doit jamais être celle du serveur.
	if strings.Contains(cfg, serverPub) && strings.Contains(cfg, "PrivateKey = "+serverPub) {
		t.Fatal("la clé privée client ne doit jamais être la clé publique serveur")
	}
}

// TestGenerateWireGuardMissingSecretsErrors vérifie qu'un compte sans
// secrets WireGuard générés produit une erreur explicite, jamais un
// placeholder silencieux.
func TestGenerateWireGuardMissingSecretsErrors(t *testing.T) {
	s := tempStore(t)
	t.Setenv("LABOSURF_DATA_DIR", t.TempDir())
	s.CreateAccount(store.Account{ID: "wgc2", Enabled: true})
	s.AddGrant("wgc2", store.EngineWireGuard, nil) // pas d'EnsureEngineSecrets
	acc, _ := s.GetAccount("wgc2")

	if _, err := Generate(acc, store.EngineWireGuard, prof("vpn.example.com")); err == nil {
		t.Fatal("attendu une erreur : secrets WireGuard jamais générés")
	}
}

// TestGroupedWireGuardMultiplePeers vérifie que buildGroupedConfig produit
// un bloc [Peer] par compte autorisé, chacun avec SA PROPRE clé publique et
// adresse — jamais mélangés, jamais celle du serveur dans un bloc [Peer].
func TestGroupedWireGuardMultiplePeers(t *testing.T) {
	s := tempStore(t)
	t.Setenv("LABOSURF_DATA_DIR", t.TempDir())
	s.CreateAccount(store.Account{ID: "wgg1", Enabled: true})
	s.CreateAccount(store.Account{ID: "wgg2", Enabled: true})
	s.AddGrant("wgg1", store.EngineWireGuard, nil)
	s.AddGrant("wgg2", store.EngineWireGuard, nil)
	s.EnsureEngineSecrets("wgg1", store.EngineWireGuard)
	s.EnsureEngineSecrets("wgg2", store.EngineWireGuard)
	acc1, _ := s.GetAccount("wgg1")
	acc2, _ := s.GetAccount("wgg2")
	pub1 := acc1.Grants[store.EngineWireGuard].Config["public_key"].(string)
	pub2 := acc2.Grants[store.EngineWireGuard].Config["public_key"].(string)
	addr1 := acc1.Grants[store.EngineWireGuard].Config["address"].(string)
	addr2 := acc2.Grants[store.EngineWireGuard].Config["address"].(string)

	p := prof("vpn.example.com")
	out := string(buildGroupedConfig(store.EngineWireGuard, authorizedAccounts(s, store.EngineWireGuard), p))

	if n := strings.Count(out, "[Peer]"); n != 2 {
		t.Fatalf("2 blocs [Peer] attendus, obtenu %d :\n%s", n, out)
	}
	for _, want := range []string{pub1, pub2, addr1, addr2, "ListenPort = 51820", "Address = 10.66.0.1/24"} {
		if !strings.Contains(out, want) {
			t.Fatalf("config groupée devrait contenir %q :\n%s", want, out)
		}
	}
	_, serverPub, _ := wireguardServerKeys()
	if strings.Contains(out, "PublicKey = "+serverPub) {
		t.Fatalf("la clé publique du serveur ne doit jamais apparaître dans un bloc [Peer] :\n%s", out)
	}
}

// TestGenerateTUICLink vérifie le format du lien client tuic:// et que la
// config serveur générée porte bien l'utilisateur (uuid -> mot de passe).
func TestGenerateTUICLink(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "t1", Enabled: true})
	s.AddGrant("t1", store.EngineTUIC, map[string]any{"uuid": "aaaa-bbbb", "password": "sekret"})
	acc, _ := s.GetAccount("t1")

	res, err := Generate(acc, store.EngineTUIC, prof("vpn.example.com"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := "tuic://aaaa-bbbb:sekret@vpn.example.com:443?"
	if !strings.HasPrefix(res.ClientLink, want) {
		t.Fatalf("lien tuic incorrect : %s", res.ClientLink)
	}
	if !strings.Contains(string(res.ServerConfig), `"aaaa-bbbb": "sekret"`) {
		t.Fatalf("config serveur tuic ne porte pas l'utilisateur :\n%s", res.ServerConfig)
	}
}

// TestGenerateTUICPasswordDefault vérifie le repli sur le mot de passe du
// compte quand le grant n'en porte pas (même convention que Hysteria).
func TestGenerateTUICPasswordDefault(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "t2", Enabled: true, Password: "fallbackpw"})
	s.AddGrant("t2", store.EngineTUIC, map[string]any{"uuid": "uuid-2"})
	acc, _ := s.GetAccount("t2")

	res, err := Generate(acc, store.EngineTUIC, prof("vpn.example.com"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.ClientLink, "uuid-2:fallbackpw@") {
		t.Fatalf("mot de passe fallback attendu dans le lien : %s", res.ClientLink)
	}
}

// TestGroupedTUIC vérifie que buildGroupedConfig rassemble TOUS les comptes
// autorisés (table users JSON uuid -> password), et exclut un compte sans
// uuid plutôt que d'écrire une entrée invalide.
func TestGroupedTUIC(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "g1", Enabled: true})
	s.CreateAccount(store.Account{ID: "g2", Enabled: true})
	s.CreateAccount(store.Account{ID: "g3", Enabled: true}) // pas de grant tuic
	s.AddGrant("g1", store.EngineTUIC, map[string]any{"uuid": "u1", "password": "p1"})
	s.AddGrant("g2", store.EngineTUIC, map[string]any{"uuid": "u2", "password": "p2"})

	p := prof("vpn.example.com")
	out := string(buildGroupedConfig(store.EngineTUIC, authorizedAccounts(s, store.EngineTUIC), p))
	if !strings.Contains(out, `"u1": "p1"`) || !strings.Contains(out, `"u2": "p2"`) {
		t.Fatalf("users tuic attendus dans la config :\n%s", out)
	}
	if strings.Contains(out, "g3") {
		t.Fatalf("compte sans grant tuic ne doit pas apparaître :\n%s", out)
	}
	if !strings.Contains(out, `"server": "[::]:443"`) {
		t.Fatalf("adresse d'écoute par défaut attendue :\n%s", out)
	}
	if !strings.Contains(out, `"zero_rtt_handshake": false`) {
		t.Fatalf("zero_rtt_handshake doit rester désactivé par défaut :\n%s", out)
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

// ---------------------------------------------------------------------------
// Hybrides — buildComponentConfigs / ApplyServerConfig (correction P0 :
// avant cette correction, buildGroupedConfig d'un hybride perdait
// silencieusement la configuration du composant transport, voir
// ARCHITECTURE_HYBRIDES.md §12.5 et AUDIT_ETAT_ACTUEL_LABOSURF_PRO.md §8.1).
// ---------------------------------------------------------------------------

// TestApplyServerConfigSimpleEngineUnchanged verrouille la non-régression du
// chemin moteur SIMPLE (mission P0, étape 3 : "les moteurs simples doivent
// continuer de fonctionner exactement comme avant").
func TestApplyServerConfigSimpleEngineUnchanged(t *testing.T) {
	s := tempStore(t)
	tempRealityKeys(t)
	t.Setenv("LABOSURF_XRAY_CONFIG", filepath.Join(t.TempDir(), "xray.json"))

	s.CreateAccount(store.Account{ID: "simple1", Enabled: true})
	s.AddGrant("simple1", store.EngineXray, map[string]any{"uuid": "uuid-simple1"})

	p := prof("vpn.example.com")
	if err := ApplyServerConfig(context.Background(), s, store.EngineXray, p); err != nil {
		t.Fatalf("ApplyServerConfig (moteur simple) : %v", err)
	}
	raw, err := os.ReadFile(os.Getenv("LABOSURF_XRAY_CONFIG"))
	if err != nil {
		t.Fatalf("lecture config xray écrite : %v", err)
	}
	if !strings.Contains(string(raw), "uuid-simple1") {
		t.Fatalf("uuid attendu dans la config xray simple :\n%s", raw)
	}
}

// assertComponentConfig vérifie qu'un composant est représenté dans le
// résultat de buildComponentConfigs par SA PROPRE forme (pas un placeholder,
// pas la forme d'un autre composant) et contient chacune des sous-chaînes
// attendues.
func assertComponentConfig(t *testing.T, cfgs map[string]engine.EngineConfig, component string, mustContain ...string) string {
	t.Helper()
	cfg, ok := cfgs[component]
	if !ok {
		t.Fatalf("composant %q absent de la config générée (perte silencieuse) : %v", component, cfgs)
	}
	j := string(cfg.JSON)
	if strings.Contains(j, `"engine": "`) && strings.Count(j, "\"") <= 4 {
		t.Fatalf("composant %q réduit à un placeholder générique :\n%s", component, j)
	}
	for _, want := range mustContain {
		if !strings.Contains(j, want) {
			t.Fatalf("composant %q : attendu %q dans la config :\n%s", component, want, j)
		}
	}
	return j
}

// TestBuildComponentConfigsDNSTTSSH couvre dnstt -> ssh (mission P0, cas B).
func TestBuildComponentConfigsDNSTTSSH(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "h1", Username: "dave", Enabled: true})
	s.AddGrant("h1", "dnstt-ssh", map[string]any{"public_key": "aa" + strings.Repeat("11", 31)})
	acc, _ := s.GetAccount("h1")

	p := prof("vpn.example.com")
	cfgs := buildComponentConfigs("dnstt-ssh", []string{"dnstt", "ssh"}, []store.Account{acc}, p)
	if len(cfgs) != 2 {
		t.Fatalf("2 composants attendus, obtenu %d : %v", len(cfgs), cfgs)
	}

	dj := assertComponentConfig(t, cfgs, "dnstt", `"domain"`, `"port"`, `"users"`, "h1")
	if strings.Contains(dj, `"inbounds"`) {
		t.Fatalf("config dnstt contaminée par la forme xray :\n%s", dj)
	}

	assertComponentConfig(t, cfgs, "ssh", `"mode": "authorized_keys"`, `"port": 22`)
}

// TestBuildComponentConfigsSlowDNSSSH couvre slowdns -> ssh (mission P0, cas C).
func TestBuildComponentConfigsSlowDNSSSH(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "h2", Username: "erin", Enabled: true})
	s.AddGrant("h2", "slowdns-ssh", map[string]any{"public_key": "bb" + strings.Repeat("22", 31)})
	acc, _ := s.GetAccount("h2")

	p := prof("vpn.example.com")
	cfgs := buildComponentConfigs("slowdns-ssh", []string{"slowdns", "ssh"}, []store.Account{acc}, p)

	dj := assertComponentConfig(t, cfgs, "slowdns", `"domain"`, `"port"`, `"users"`, "h2")
	if strings.Contains(dj, `"inbounds"`) {
		t.Fatalf("config slowdns contaminée par la forme xray :\n%s", dj)
	}
	assertComponentConfig(t, cfgs, "ssh", `"mode": "authorized_keys"`, `"port": 22`)
}

// TestBuildComponentConfigsDNSTTXray couvre dnstt -> xray (mission P0, cas D).
func TestBuildComponentConfigsDNSTTXray(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "h3", Username: "finn", Enabled: true})
	s.AddGrant("h3", "dnstt-xray", map[string]any{"uuid": "uuid-h3"})
	acc, _ := s.GetAccount("h3")

	p := prof("vpn.example.com")
	cfgs := buildComponentConfigs("dnstt-xray", []string{"dnstt", "xray"}, []store.Account{acc}, p)

	dj := assertComponentConfig(t, cfgs, "dnstt", `"domain"`, `"users"`)
	if strings.Contains(dj, `"inbounds"`) {
		t.Fatalf("config dnstt contaminée par la forme xray (perte du composant transport) :\n%s", dj)
	}

	xj := assertComponentConfig(t, cfgs, "xray", `"inbounds"`, `"vless"`, "uuid-h3")
	if strings.Contains(xj, `"domain"`) {
		t.Fatalf("config xray contaminée par un champ dnstt :\n%s", xj)
	}
}

// TestBuildComponentConfigsSlowDNSXray couvre slowdns -> xray (mission P0, cas E).
func TestBuildComponentConfigsSlowDNSXray(t *testing.T) {
	s := tempStore(t)
	s.CreateAccount(store.Account{ID: "h4", Username: "gwen", Enabled: true})
	s.AddGrant("h4", "slowdns-xray", map[string]any{"uuid": "uuid-h4"})
	acc, _ := s.GetAccount("h4")

	p := prof("vpn.example.com")
	cfgs := buildComponentConfigs("slowdns-xray", []string{"slowdns", "xray"}, []store.Account{acc}, p)

	dj := assertComponentConfig(t, cfgs, "slowdns", `"domain"`, `"users"`)
	if strings.Contains(dj, `"inbounds"`) {
		t.Fatalf("config slowdns contaminée par la forme xray (perte du composant transport) :\n%s", dj)
	}

	xj := assertComponentConfig(t, cfgs, "xray", `"inbounds"`, `"vless"`, "uuid-h4")
	if strings.Contains(xj, `"domain"`) {
		t.Fatalf("config xray contaminée par un champ slowdns :\n%s", xj)
	}
}

// TestApplyServerConfigHybridWritesRealFiles vérifie, de bout en bout (via le
// registre réel, pas seulement la fonction pure buildComponentConfigs), que
// ApplyServerConfig peuple ComponentConfig sur la VRAIE instance
// CompositeEngine et que CHAQUE composant écrit sur disque sa propre forme —
// c'est l'effet durable réel (voir analyse : engine.Get retourne toujours
// une instance neuve, la source de vérité durable est le fichier écrit par
// chaque sous-moteur, pas un état en mémoire partagé entre deux appels
// séparés).
func TestApplyServerConfigHybridWritesRealFiles(t *testing.T) {
	s := tempStore(t)
	tempRealityKeys(t)
	t.Setenv("LABOSURF_DNSTT_CONFIG", filepath.Join(t.TempDir(), "dnstt.json"))
	t.Setenv("LABOSURF_XRAY_CONFIG", filepath.Join(t.TempDir(), "xray.json"))

	name, err := engineutil.RegisterHybrid([]string{"dnstt", "xray"})
	if err != nil {
		t.Fatalf("RegisterHybrid: %v", err)
	}

	s.CreateAccount(store.Account{ID: "h5", Username: "hank", Enabled: true})
	s.AddGrant("h5", name, map[string]any{"uuid": "uuid-h5", "public_key": "cc" + strings.Repeat("33", 31)})

	p := prof("vpn.example.com")
	if err := ApplyServerConfig(context.Background(), s, name, p); err != nil {
		t.Fatalf("ApplyServerConfig: %v", err)
	}

	dRaw, err := os.ReadFile(os.Getenv("LABOSURF_DNSTT_CONFIG"))
	if err != nil {
		t.Fatalf("lecture config dnstt écrite sur disque : %v", err)
	}
	dj := string(dRaw)
	if !strings.Contains(dj, `"domain"`) || !strings.Contains(dj, `"users"`) {
		t.Fatalf("fichier dnstt écrit sur disque incomplet (perte silencieuse du transport) :\n%s", dj)
	}
	if strings.Contains(dj, `"inbounds"`) {
		t.Fatalf("fichier dnstt écrit sur disque contaminé par la forme xray :\n%s", dj)
	}

	xRaw, err := os.ReadFile(os.Getenv("LABOSURF_XRAY_CONFIG"))
	if err != nil {
		t.Fatalf("lecture config xray écrite sur disque : %v", err)
	}
	xj := string(xRaw)
	if !strings.Contains(xj, "uuid-h5") {
		t.Fatalf("fichier xray écrit sur disque ne porte pas l'uuid du compte :\n%s", xj)
	}
}