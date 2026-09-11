package hysteria2

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
)

// TestAssetTargetMapping fige le mapping entre l'architecture normalisée
// LABOSURF (engineutil.DetectArch) et le suffixe d'asset officiel publié
// par HyNetworks/hysteria — non-régression du même type que
// engines/tuic.TestRustTargetMapping.
func TestAssetTargetMapping(t *testing.T) {
	cases := map[string]string{
		"amd64": "linux-amd64",
		"arm64": "linux-arm64",
	}
	for arch, want := range cases {
		if got := assetTarget(arch); got != want {
			t.Errorf("assetTarget(%q) = %q, attendu %q", arch, got, want)
		}
	}
}

// TestExpectedSHA256Format vérifie que le SHA-256 pinné pour chaque cible
// supportée est un hash 32 octets valide (non vide, non désactivé) — ces
// valeurs ont été obtenues en récupérant réellement le fichier officiel
// "hashes.txt" publié à côté des binaires de la release app/v2.12.2.
func TestExpectedSHA256Format(t *testing.T) {
	for _, target := range []string{"linux-amd64", "linux-arm64"} {
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
	if !engine.Has("hysteria2") {
		t.Fatal("le moteur 'hysteria2' n'est pas enregistré dans le registre global")
	}
	e, err := engine.Get("hysteria2")
	if err != nil {
		t.Fatalf("engine.Get(hysteria2) : %v", err)
	}
	if e.Name() != "hysteria2" {
		t.Fatalf("nom attendu 'hysteria2', obtenu %q", e.Name())
	}
	if e.Version() != hysteria2ServerVersion {
		t.Fatalf("version attendue %q, obtenue %q", hysteria2ServerVersion, e.Version())
	}
	if e.Description() == "" {
		t.Fatal("description vide")
	}
}

// TestEngineNameIsHysteria2NotHysteria verrouille explicitement le nom
// d'enregistrement exigé par la mission P1 : jamais "hysteria" (déjà pris
// par le moteur maison, non importé par ce paquet — voir
// internal/engineutil.TestHysteria2CapabilityDistinctFromHysteria pour la
// vérification croisée des DEUX moteurs enregistrés ensemble).
func TestEngineNameIsHysteria2NotHysteria(t *testing.T) {
	e, err := engine.Get("hysteria2")
	if err != nil {
		t.Fatalf("engine.Get(hysteria2) : %v", err)
	}
	if e.Name() == "hysteria" {
		t.Fatal("le moteur officiel ne doit jamais s'enregistrer sous le nom 'hysteria' (déjà le moteur maison)")
	}
	if e.Name() != "hysteria2" {
		t.Fatalf("nom attendu 'hysteria2', obtenu %q", e.Name())
	}
	if !strings.Contains(strings.ToLower(e.Description()), "officiel") {
		t.Fatalf("la description devrait clairement s'identifier comme le protocole officiel : %q", e.Description())
	}
}

// TestConfigureRejectsEmpty vérifie qu'une configuration vide est refusée
// explicitement plutôt que d'écrire un fichier vide.
func TestConfigureRejectsEmpty(t *testing.T) {
	w := &Hysteria2Engine{configPath: filepath.Join(t.TempDir(), "config.yaml")}
	if err := w.Configure(context.Background(), engine.EngineConfig{}); err == nil {
		t.Fatal("attendu une erreur pour une configuration vide")
	}
}

// TestConfigureRequiresListen vérifie que Configure() refuse une config
// sans clé "listen".
func TestConfigureRequiresListen(t *testing.T) {
	w := &Hysteria2Engine{configPath: filepath.Join(t.TempDir(), "config.yaml")}
	cfg := "auth:\n  type: userpass\n  userpass:\n    alice: secret\n"
	if err := w.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfg)}); err == nil {
		t.Fatal("attendu une erreur pour une config sans 'listen:'")
	}
}

// TestConfigureRequiresUsers vérifie que Configure() refuse une config sans
// aucun utilisateur sous auth.userpass — démarrer un serveur Hysteria2 sans
// utilisateur serait silencieusement inutilisable.
func TestConfigureRequiresUsers(t *testing.T) {
	w := &Hysteria2Engine{configPath: filepath.Join(t.TempDir(), "config.yaml")}
	cfg := "listen: :443\n\nauth:\n  type: userpass\n  userpass:\n"
	if err := w.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfg)}); err == nil {
		t.Fatal("attendu une erreur pour une config sans utilisateur")
	}
}

// TestConfigureWritesFile vérifie que Configure() écrit bien le fichier
// fourni tel quel (pas de réécriture/injection — voir le commentaire de
// Configure), et que configuredAddr() relit correctement la valeur écrite.
func TestConfigureWritesFile(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	w := &Hysteria2Engine{configPath: cfgPath}
	cfg := "listen: :4443\n\ntls:\n  cert: /custom/cert.pem\n  key: /custom/key.pem\n\n" +
		"auth:\n  type: userpass\n  userpass:\n    alice: secret\n"
	if err := w.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfg)}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("lecture config écrite : %v", err)
	}
	if !strings.Contains(string(data), "listen: :4443") {
		t.Fatalf("contenu écrit incorrect :\n%s", data)
	}
	if !strings.Contains(string(data), "/custom/cert.pem") {
		t.Fatalf("chemin de certificat fourni non préservé :\n%s", data)
	}

	addr, ok := w.configuredAddr()
	if !ok {
		t.Fatal("configuredAddr() devrait retrouver l'adresse écrite")
	}
	if addr != ":4443" {
		t.Fatalf("adresse attendue ':4443', obtenue %q", addr)
	}
}

// TestNormalizeListenAddr vérifie la complétion d'une adresse courte YAML
// (forme officielle ":443") en une adresse pleinement résolvable.
func TestNormalizeListenAddr(t *testing.T) {
	if got := normalizeListenAddr(":443"); got != "0.0.0.0:443" {
		t.Fatalf("attendu 0.0.0.0:443, obtenu %q", got)
	}
	if got := normalizeListenAddr("127.0.0.1:443"); got != "127.0.0.1:443" {
		t.Fatalf("adresse déjà complète ne doit pas être modifiée, obtenu %q", got)
	}
}

// TestEndpointFalseWhenNotStarted vérifie qu'Endpoint() ne renvoie jamais un
// endpoint (même partiel) tant que le moteur n'a pas été démarré — jamais un
// placeholder.
func TestEndpointFalseWhenNotStarted(t *testing.T) {
	w := &Hysteria2Engine{configPath: filepath.Join(t.TempDir(), "config.yaml")}
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

	w := &Hysteria2Engine{configPath: filepath.Join(t.TempDir(), "config.yaml")}
	st := w.Status()
	if st.Installed {
		t.Fatal("Installed ne doit pas être vrai sans binaire déployé")
	}
	if st.Running {
		t.Fatal("Running ne doit pas être vrai avant Start()")
	}
}

// TestEnsureObfsPasswordPersists vérifie que le secret d'obfuscation
// Salamander est généré une fois, aléatoire (pas une valeur codée en dur),
// puis relu identique à l'appel suivant (idempotence).
func TestEnsureObfsPasswordPersists(t *testing.T) {
	dir := t.TempDir()
	pw1, err := EnsureObfsPassword(dir)
	if err != nil {
		t.Fatalf("EnsureObfsPassword: %v", err)
	}
	if pw1 == "" {
		t.Fatal("mot de passe d'obfuscation vide")
	}
	if pw1 == "labosurf-sal4mander-obfs-secret" {
		t.Fatal("le secret ne doit jamais être la valeur codée en dur du moteur maison")
	}

	pw2, err := EnsureObfsPassword(dir)
	if err != nil {
		t.Fatalf("EnsureObfsPassword (second appel) : %v", err)
	}
	if pw1 != pw2 {
		t.Fatalf("le secret doit être stable entre deux appels : %q != %q", pw1, pw2)
	}

	dir2 := t.TempDir()
	pw3, err := EnsureObfsPassword(dir2)
	if err != nil {
		t.Fatalf("EnsureObfsPassword (répertoire différent) : %v", err)
	}
	if pw3 == pw1 {
		t.Fatal("deux répertoires de données différents ne devraient pas partager le même secret généré")
	}
}

// --- Cycle de vie réel (Start/Endpoint/Restart/Stop) avec un faux binaire ---
//
// Le vrai binaire `hysteria` (Go, téléchargé) n'est pas exécuté dans les
// tests unitaires (pas d'accès réseau garanti en CI). Pour tester la
// supervision de processus et le bind-probe UDP de Endpoint() avec un
// process RÉEL (pas un mock), ce test déploie un faux "hysteria" qui lit
// `server -c <config.yaml>`, extrait la ligne "listen:", lie réellement une
// socket UDP dessus, puis attend d'être tué — même patron que
// engines/tuic.TestTUICEngineLifecycleWithRealProcess.
const fakeHysteria2ServerScript = `#!/usr/bin/env python3
import socket, sys, time

cfg_path = sys.argv[sys.argv.index("-c") + 1]
listen = None
with open(cfg_path) as f:
    for line in f:
        line = line.strip()
        if line.startswith("listen:"):
            listen = line.split(":", 1)[1].strip()
            break
host, port = listen.rsplit(":", 1)
host = host.strip("[]") or "0.0.0.0"
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
sock.bind((host, int(port)))
time.sleep(3600)
`

func deployFakeHysteria2Server(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		if _, err := os.Stat("/usr/local/bin/python3"); err != nil {
			t.Skip("python3 indisponible : impossible de simuler hysteria pour ce test")
		}
	}

	orig := engineutil.DefaultBinaryDir
	engineutil.DefaultBinaryDir = t.TempDir()
	t.Cleanup(func() { engineutil.DefaultBinaryDir = orig })

	dir := filepath.Join(engineutil.BinaryDir(), "hysteria2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir faux binaire : %v", err)
	}
	scriptPath := filepath.Join(dir, "hysteria")
	if err := os.WriteFile(scriptPath, []byte(fakeHysteria2ServerScript), 0o755); err != nil {
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

func waitHysteria2Endpoint(t *testing.T, w *Hysteria2Engine) engine.Endpoint {
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

// TestHysteria2EngineLifecycleWithRealProcess couvre Start -> Endpoint réel
// -> bind-probe indépendant -> Restart -> Stop -> port libéré, avec un vrai
// sous-processus (pas de mock d'exec.Cmd) — contrôlé et hors réseau externe
// (faux binaire local, aucun téléchargement).
func TestHysteria2EngineLifecycleWithRealProcess(t *testing.T) {
	deployFakeHysteria2Server(t)

	port := freeUDPPort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := "listen: " + addr + "\n\nauth:\n  type: userpass\n  userpass:\n    alice: secret\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}

	w := &Hysteria2Engine{configPath: cfgPath}
	ctx := context.Background()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}

	ep := waitHysteria2Endpoint(t, w)
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
	ep2 := waitHysteria2Endpoint(t, w)
	assertUDPPortBusy(t, ep2.Addr)

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop : %v", err)
	}
	if _, ready := w.Endpoint(); ready {
		t.Fatal("Endpoint() doit redevenir indisponible après Stop()")
	}
	assertUDPPortFree(t, addr)
}
