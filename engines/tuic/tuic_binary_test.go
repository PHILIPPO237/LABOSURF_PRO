package tuic

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
)

// TestRustTargetMapping fige le mapping entre l'architecture normalisée
// LABOSURF (engineutil.DetectArch) et la cible Rust officielle publiée par
// tuic-protocol/tuic — non-régression du même type que
// engines/xray.TestGetArchSuffixMatchesRealAssetNames.
func TestRustTargetMapping(t *testing.T) {
	cases := map[string]string{
		"amd64": "x86_64-unknown-linux-musl",
		"arm64": "aarch64-unknown-linux-musl",
	}
	for arch, want := range cases {
		if got := rustTarget(arch); got != want {
			t.Errorf("rustTarget(%q) = %q, attendu %q", arch, got, want)
		}
	}
}

// TestExpectedSHA256Format vérifie que le SHA-256 pinné pour chaque cible
// supportée est bien un hash 32 octets valide (non vide, non désactivé) —
// ces valeurs ont été recalculées en téléchargeant réellement les deux
// binaires depuis la release officielle (voir AUDIT_TUIC_INTEGRATION.md §2).
func TestExpectedSHA256Format(t *testing.T) {
	for _, target := range []string{"x86_64-unknown-linux-musl", "aarch64-unknown-linux-musl"} {
		sum := expectedSHA256For(target)
		if sum == "" {
			t.Errorf("SHA256 vide pour %q — vérification désactivée", target)
			continue
		}
		raw, err := hex.DecodeString(sum)
		if err != nil {
			t.Errorf("%q n'est pas un hex valide pour %q : %v", sum, target, err)
			continue
		}
		if len(raw) != 32 {
			t.Errorf("%q pour %q : %d octets décodés, 32 attendus (SHA-256)", sum, target, len(raw))
		}
	}
}

// TestExpectedSHA256UnknownTarget vérifie qu'une cible inconnue désactive
// explicitement la vérification (chaîne vide) plutôt que de renvoyer un
// hash inventé.
func TestExpectedSHA256UnknownTarget(t *testing.T) {
	if got := expectedSHA256For("mips-nonexistent"); got != "" {
		t.Fatalf("attendu chaîne vide pour cible inconnue, obtenu %q", got)
	}
}

// TestEngineIdentity vérifie Name/Version/Description et l'enregistrement
// dans le registre global (effet de bord du init() de engine.go).
func TestEngineIdentity(t *testing.T) {
	if !engine.Has("tuic") {
		t.Fatal("le moteur 'tuic' n'est pas enregistré dans le registre global")
	}
	e, err := engine.Get("tuic")
	if err != nil {
		t.Fatalf("engine.Get(tuic) : %v", err)
	}
	if e.Name() != "tuic" {
		t.Fatalf("nom attendu 'tuic', obtenu %q", e.Name())
	}
	if e.Version() != tuicServerVersion {
		t.Fatalf("version attendue %q, obtenue %q", tuicServerVersion, e.Version())
	}
	if e.Description() == "" {
		t.Fatal("description vide")
	}
}

// TestConfigureRejectsEmpty vérifie qu'une configuration vide est refusée
// explicitement plutôt que d'écrire un fichier vide.
func TestConfigureRejectsEmpty(t *testing.T) {
	w := &TUICEngine{configPath: filepath.Join(t.TempDir(), "config.json")}
	if err := w.Configure(context.Background(), engine.EngineConfig{}); err == nil {
		t.Fatal("attendu une erreur pour une configuration vide")
	}
}

// TestConfigureRequiresUsers vérifie que Configure() refuse une config sans
// aucun utilisateur — démarrer un serveur TUIC sans utilisateur serait
// silencieusement inutilisable (aucun client ne pourrait s'authentifier).
func TestConfigureRequiresUsers(t *testing.T) {
	w := &TUICEngine{configPath: filepath.Join(t.TempDir(), "config.json")}
	cfg := map[string]any{
		"server":      "127.0.0.1:4433",
		"certificate": "/dev/null",
		"private_key": "/dev/null",
		"users":       map[string]any{},
	}
	raw, _ := json.Marshal(cfg)
	if err := w.Configure(context.Background(), engine.EngineConfig{JSON: raw}); err == nil {
		t.Fatal("attendu une erreur pour une config sans utilisateur")
	}
}

// TestConfigureWritesFile vérifie que Configure() écrit bien le fichier avec
// les valeurs fournies, sans écraser un certificat/clé déjà renseignés.
func TestConfigureWritesFile(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	w := &TUICEngine{configPath: cfgPath}
	cfg := map[string]any{
		"server":      "127.0.0.1:4433",
		"certificate": "/custom/cert.pem",
		"private_key": "/custom/key.pem",
		"users":       map[string]any{"uuid-1": "pw-1"},
	}
	raw, _ := json.Marshal(cfg)
	if err := w.Configure(context.Background(), engine.EngineConfig{JSON: raw}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("lecture config écrite : %v", err)
	}
	var written map[string]any
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("config écrite invalide : %v", err)
	}
	if written["certificate"] != "/custom/cert.pem" || written["private_key"] != "/custom/key.pem" {
		t.Fatalf("certificate/private_key fournis ne doivent pas être écrasés : %v", written)
	}
	if written["server"] != "127.0.0.1:4433" {
		t.Fatalf("server attendu 127.0.0.1:4433, obtenu %v", written["server"])
	}
}

// TestConfigureDefaultsListenAddr vérifie que Configure() fournit une
// adresse d'écoute par défaut si absente.
func TestConfigureDefaultsListenAddr(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	w := &TUICEngine{configPath: cfgPath}
	cfg := map[string]any{
		"certificate": "/dev/null",
		"private_key": "/dev/null",
		"users":       map[string]any{"uuid-1": "pw-1"},
	}
	raw, _ := json.Marshal(cfg)
	if err := w.Configure(context.Background(), engine.EngineConfig{JSON: raw}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	data, _ := os.ReadFile(cfgPath)
	var written map[string]any
	json.Unmarshal(data, &written)
	if written["server"] != "[::]:443" {
		t.Fatalf("adresse d'écoute par défaut attendue [::]:443, obtenu %v", written["server"])
	}
}

// TestEndpointFalseWhenNotStarted vérifie qu'Endpoint() ne renvoie jamais un
// endpoint (même partiel) tant que le moteur n'a pas été démarré — jamais un
// placeholder.
func TestEndpointFalseWhenNotStarted(t *testing.T) {
	w := &TUICEngine{configPath: filepath.Join(t.TempDir(), "config.json")}
	if ep, ok := w.Endpoint(); ok {
		t.Fatalf("endpoint inattendu avant Start() : %+v", ep)
	}
	if err := w.HealthCheck(); err == nil {
		t.Fatal("HealthCheck doit échouer avant Start()")
	}
}

// TestStatusNotInstalledNotRunning vérifie l'état par défaut d'un moteur
// jamais installé ni démarré (répertoire binaire vide dédié au test).
func TestStatusNotInstalledNotRunning(t *testing.T) {
	orig := engineutil.DefaultBinaryDir
	engineutil.DefaultBinaryDir = t.TempDir()
	t.Cleanup(func() { engineutil.DefaultBinaryDir = orig })

	w := &TUICEngine{configPath: filepath.Join(t.TempDir(), "config.json")}
	st := w.Status()
	if st.Installed {
		t.Fatal("Installed ne doit pas être vrai sans binaire déployé")
	}
	if st.Running {
		t.Fatal("Running ne doit pas être vrai avant Start()")
	}
}

// --- Cycle de vie réel (Start/Endpoint/Restart/Stop) avec un faux binaire ---
//
// Le vrai binaire tuic-server (Rust, téléchargé) n'est pas exécuté dans les
// tests unitaires (pas d'accès réseau garanti en CI). Pour tester la
// supervision de processus et le bind-probe UDP de Endpoint() avec un
// process RÉEL (pas un mock), ce test déploie un faux "tuic-server" qui lit
// -c <config>, extrait le champ "server", lie réellement une socket UDP
// dessus, puis attend d'être tué — exactement le contrat observable dont
// TUICEngine a besoin (process vivant + port réellement occupé).

const fakeTUICServerScript = `#!/usr/bin/env python3
import json, socket, sys, time

cfg_path = sys.argv[sys.argv.index("-c") + 1]
with open(cfg_path) as f:
    cfg = json.load(f)
host, port = cfg["server"].rsplit(":", 1)
host = host.strip("[]") or "0.0.0.0"
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
sock.bind((host, int(port)))
time.sleep(3600)
`

func deployFakeTUICServer(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		if _, err := os.Stat("/usr/local/bin/python3"); err != nil {
			t.Skip("python3 indisponible : impossible de simuler tuic-server pour ce test")
		}
	}

	orig := engineutil.DefaultBinaryDir
	engineutil.DefaultBinaryDir = t.TempDir()
	t.Cleanup(func() { engineutil.DefaultBinaryDir = orig })

	dir := filepath.Join(engineutil.BinaryDir(), "tuic")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir faux binaire : %v", err)
	}
	scriptPath := filepath.Join(dir, "tuic-server")
	if err := os.WriteFile(scriptPath, []byte(fakeTUICServerScript), 0o755); err != nil {
		t.Fatalf("écriture faux binaire : %v", err)
	}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("sondage port UDP libre : %v", err)
	}
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	return port
}

func assertUDPPortBusy(t *testing.T, addr string) {
	t.Helper()
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("résolution %s : %v", addr, err)
	}
	l, err := net.ListenUDP("udp", udpAddr)
	if err == nil {
		l.Close()
		t.Fatalf("endpoint annoncé comme réel mais rebindable : %s n'était pas réellement occupé", addr)
	}
}

func assertUDPPortFree(t *testing.T, addr string) {
	t.Helper()
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("résolution %s : %v", addr, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		l, err := net.ListenUDP("udp", udpAddr)
		if err == nil {
			l.Close()
			return
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("le port %s n'a pas été libéré après Stop() : %v", addr, lastErr)
}

func waitTUICEndpoint(t *testing.T, w *TUICEngine) engine.Endpoint {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ep, ok := w.Endpoint(); ok {
			return ep
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Endpoint() jamais devenu prêt après Start()")
	return engine.Endpoint{}
}

// TestTUICEngineLifecycleWithRealProcess couvre Start -> Endpoint réel ->
// bind-probe indépendant -> Restart -> Stop -> port libéré, avec un vrai
// sous-processus (pas de mock d'exec.Cmd).
func TestTUICEngineLifecycleWithRealProcess(t *testing.T) {
	deployFakeTUICServer(t)

	port := freeUDPPort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := map[string]any{
		"server":      addr,
		"certificate": "/dev/null",
		"private_key": "/dev/null",
		"users":       map[string]any{"uuid-1": "pw-1"},
	}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}

	w := &TUICEngine{configPath: cfgPath}
	ctx := context.Background()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}

	ep := waitTUICEndpoint(t, w)
	if ep.Network != "udp" {
		t.Fatalf("réseau attendu udp, obtenu %q", ep.Network)
	}
	if ep.Addr != addr {
		t.Fatalf("adresse attendue %q, obtenue %q", addr, ep.Addr)
	}
	assertUDPPortBusy(t, addr)
	if err := w.HealthCheck(); err != nil {
		t.Fatalf("HealthCheck après Start : %v", err)
	}

	if err := w.Restart(ctx); err != nil {
		t.Fatalf("Restart : %v", err)
	}
	ep2 := waitTUICEndpoint(t, w)
	assertUDPPortBusy(t, ep2.Addr)

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop : %v", err)
	}
	if _, ready := w.Endpoint(); ready {
		t.Fatal("Endpoint() doit redevenir indisponible après Stop()")
	}
	assertUDPPortFree(t, addr)
}

