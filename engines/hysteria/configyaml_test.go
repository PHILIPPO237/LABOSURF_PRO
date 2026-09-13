package hysteria

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"labosurf/internal/engine"
)

// yamlSample reproduit EXACTEMENT la forme émise par la plateforme
// (internal/clientcfg.hysteriaV2Config) : listen/tls/auth.userpass/
// obfs.salamander/masquerade, indentation 2 espaces, userpass sous la clé
// `userpass:` (niveau 2, entrées niveau 4).
const yamlSample = `listen: 8443

tls:
  cert: /etc/labosurf/hysteria/cert.pem
  key: /etc/labosurf/hysteria/key.pem

auth:
  type: userpass
  userpass:
    c1: sec1
    c2: sec2

obfs:
  type: salamander
  salamander:
    password: labosurf-sal4mander-obfs-secret

masquerade:
  type: proxy
  proxy:
    url: https://www.bing.com
    rewriteHost: true
`

// TestParseConfigYAML vérifie que la config YAML émise par la plateforme
// est réellement utilisable par le moteur : sans ce pas de secours,
// Configure() échouait sur cette forme et le moteur hysteria ne pouvait
// pas démarrer depuis la configuration groupée.
func TestParseConfigYAML(t *testing.T) {
	cfg, err := parseConfigYAML([]byte(yamlSample))
	if err != nil {
		t.Fatalf("parseConfigYAML: %v", err)
	}
	if cfg.Port != 8443 {
		t.Fatalf("port attendu 8443, obtenu %d", cfg.Port)
	}
	if len(cfg.Users) != 2 {
		t.Fatalf("2 utilisateurs attendus, obtenus %d", len(cfg.Users))
	}
	for _, u := range cfg.Users {
		if u.Name == "" || u.Password == "" || !u.Enabled {
			t.Fatalf("utilisateur incomplet : %+v", u)
		}
	}
	if cfg.Obfs != "labosurf-sal4mander-obfs-secret" {
		t.Fatalf("obfs attendu, obtenu %q", cfg.Obfs)
	}
}

// TestParseConfigYAMLBadShape garantit qu'une forme YAML inattendue renvoie
// une erreur explicite (jamais une lecture partielle silencieuse).
func TestParseConfigYAMLBadShape(t *testing.T) {
	if _, err := parseConfigYAML([]byte("listen: 8443\n!!!pas yaml")); err == nil {
		t.Fatal("forme invalide devrait être rejetée")
	}
	if _, err := parseConfigYAML([]byte("listen: 999999")); err == nil {
		t.Fatal("port hors bornes devrait être rejeté")
	}
}

// TestConfigureAcceptsGeneratedYAML exerce le chemin réel : Configure()
// (appelé par ApplyServerConfig avec la forme YAML groupée) doit
// normaliser la config, puis Start()/loadHysteriaConfig doit relire le
// fichier normalisé sans erreur.
func TestConfigureAcceptsGeneratedYAML(t *testing.T) {
	path := t.TempDir() + "/config.json"
	t.Setenv("LABOSURF_HYSTERIA_CONFIG", path)
	ew, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := ew.Configure(nil, engine.EngineConfig{JSON: []byte(yamlSample)}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fichier de config non écrit : %v", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("la config écrite doit être du JSON normalisé, obtenu :\n%s", trimmed)
	}

	cfg, err := loadHysteriaConfig(path)
	if err != nil {
		t.Fatalf("loadHysteriaConfig: %v", err)
	}
	var want struct {
		Port  int    `json:"port"`
		Obfs  string `json:"obfs"`
		Users []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"users"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("l'écrit n'est pas du JSON : %v", err)
	}
	if want.Port != cfg.Port || cfg.Port != 8443 {
		t.Fatalf("port attendu 8443, obtenu %d", cfg.Port)
	}
	if len(cfg.Users) != 2 {
		t.Fatalf("2 utilisateurs attendus, obtenus %d", len(cfg.Users))
	}
	if cfg.Obfs != "labosurf-sal4mander-obfs-secret" {
		t.Fatalf("obfs mal propagé : %q", cfg.Obfs)
	}
}