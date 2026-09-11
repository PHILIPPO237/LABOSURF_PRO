package wireguard

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
)

// ---------------------------------------------------------------------------
// Tests unitaires purs (aucun exec, aucun accès réseau réel).
// ---------------------------------------------------------------------------

func TestEngineIdentity(t *testing.T) {
	if !engine.Has("wireguard") {
		t.Fatal("le moteur 'wireguard' n'est pas enregistré dans le registre global")
	}
	e, err := engine.Get("wireguard")
	if err != nil {
		t.Fatalf("engine.Get(wireguard) : %v", err)
	}
	if e.Name() != "wireguard" {
		t.Fatalf("nom attendu 'wireguard', obtenu %q", e.Name())
	}
	if e.Description() == "" {
		t.Fatal("description vide")
	}
}

func TestIfaceNameDerivedFromConfigPath(t *testing.T) {
	e := &WireGuardEngine{configPath: "/etc/labosurf/engines/wireguard/wg-labosurf.conf"}
	if got := e.ifaceName(); got != "wg-labosurf" {
		t.Fatalf("nom d'interface attendu 'wg-labosurf', obtenu %q", got)
	}
}

func TestConfiguredListenPort(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "wg-labosurf.conf")
	content := "[Interface]\nPrivateKey = abc\nAddress = 10.66.0.1/24\nListenPort = 51820\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}
	e := &WireGuardEngine{configPath: cfgPath}
	port, ok := e.configuredListenPort()
	if !ok {
		t.Fatal("port attendu trouvé")
	}
	if port != 51820 {
		t.Fatalf("port attendu 51820, obtenu %d", port)
	}
}

func TestConfiguredListenPortAbsent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "wg-labosurf.conf")
	os.WriteFile(cfgPath, []byte("[Interface]\nPrivateKey = abc\n"), 0o600)
	e := &WireGuardEngine{configPath: cfgPath}
	if _, ok := e.configuredListenPort(); ok {
		t.Fatal("aucun port ne devrait être trouvé")
	}
}

func TestNormalizeListenAddr(t *testing.T) {
	if got := normalizeListenAddr(":51820"); got != "0.0.0.0:51820" {
		t.Fatalf("attendu 0.0.0.0:51820, obtenu %q", got)
	}
	if got := normalizeListenAddr("127.0.0.1:51820"); got != "127.0.0.1:51820" {
		t.Fatalf("adresse déjà complète ne doit pas être modifiée, obtenu %q", got)
	}
}

func TestOwnedByLabosurf(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "wg-labosurf.conf")
	e := &WireGuardEngine{configPath: cfgPath}

	os.WriteFile(cfgPath, []byte(LabosurfMarker+"\n[Interface]\n"), 0o600)
	if !e.ownedByLabosurf() {
		t.Fatal("fichier avec le marqueur LABOSURF devrait être reconnu comme possédé")
	}

	os.WriteFile(cfgPath, []byte("[Interface]\n# fichier étranger\n"), 0o600)
	if e.ownedByLabosurf() {
		t.Fatal("fichier sans le marqueur ne devrait jamais être reconnu comme possédé")
	}

	os.Remove(cfgPath)
	if e.ownedByLabosurf() {
		t.Fatal("fichier absent ne devrait jamais être reconnu comme possédé")
	}
}

func TestConfigureRejectsEmpty(t *testing.T) {
	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	if err := e.Configure(context.Background(), engine.EngineConfig{}); err == nil {
		t.Fatal("attendu une erreur pour une configuration vide")
	}
}

func TestConfigureRequiresInterfaceBlock(t *testing.T) {
	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	cfg := "PrivateKey = abc\nListenPort = 51820\n[Peer]\nPublicKey = xyz\nAllowedIPs = 10.66.0.2/32\n"
	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfg)}); err == nil {
		t.Fatal("attendu une erreur pour une config sans [Interface]")
	}
}

func TestConfigureRequiresPrivateKey(t *testing.T) {
	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	cfg := "[Interface]\nListenPort = 51820\n\n[Peer]\nPublicKey = xyz\nAllowedIPs = 10.66.0.2/32\n"
	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfg)}); err == nil {
		t.Fatal("attendu une erreur pour une config sans PrivateKey")
	}
}

func TestConfigureRequiresListenPort(t *testing.T) {
	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	cfg := "[Interface]\nPrivateKey = abc\n\n[Peer]\nPublicKey = xyz\nAllowedIPs = 10.66.0.2/32\n"
	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfg)}); err == nil {
		t.Fatal("attendu une erreur pour une config sans ListenPort")
	}
}

func TestConfigureRequiresPeer(t *testing.T) {
	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	cfg := "[Interface]\nPrivateKey = abc\nListenPort = 51820\n"
	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfg)}); err == nil {
		t.Fatal("attendu une erreur pour une config sans [Peer]")
	}
}

// TestConfigureWritesFileWithRestrictedPermissions vérifie que le fichier
// écrit (qui contient la clé privée du serveur) n'est jamais lisible par
// d'autres utilisateurs (mission P2, Étape 15).
func TestConfigureWritesFileWithRestrictedPermissions(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "wg-labosurf.conf")
	e := &WireGuardEngine{configPath: cfgPath}
	cfg := LabosurfMarker + "\n[Interface]\nPrivateKey = abc\nListenPort = 51820\n\n[Peer]\nPublicKey = xyz\nAllowedIPs = 10.66.0.2/32\n"
	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfg)}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat : %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissions attendues 0600, obtenu %o", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "PrivateKey = abc") {
		t.Fatalf("contenu écrit incorrect :\n%s", data)
	}
}

// TestEndpointFalseWhenNoToolsAvailable vérifie qu'Endpoint()/HealthCheck()
// ne renvoient jamais un endpoint réel si les outils système sont absents
// (PATH vide) — jamais un placeholder.
func TestEndpointFalseWhenNoToolsAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // PATH sans wg/wg-quick
	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	if ep, ok := e.Endpoint(); ok {
		t.Fatalf("endpoint inattendu sans outils système : %+v", ep)
	}
	if err := e.HealthCheck(); err == nil {
		t.Fatal("HealthCheck doit échouer sans interface active")
	}
	if err := toolsAvailable(); err == nil {
		t.Fatal("toolsAvailable doit échouer sans wg/wg-quick dans PATH")
	}
}

func TestInstallFailsWithClearErrorWithoutTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	err := e.Install(context.Background(), engine.InstallConfig{})
	if err == nil {
		t.Fatal("attendu une erreur sans wg/wg-quick disponibles")
	}
	if !strings.Contains(err.Error(), "wg") {
		t.Fatalf("message d'erreur devrait mentionner l'outil manquant : %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests de cycle de vie avec de FAUX outils système wg/wg-quick.
//
// Comme pour TUIC/Hysteria2 (faux process simulant le binaire tiers), ces
// tests substituent de faux scripts "wg"/"wg-quick" en tête de PATH plutôt
// que d'exécuter les vrais outils système — aucune interface réseau réelle
// n'est créée, aucun module noyau requis, aucun accès root nécessaire (voir
// mission P2, Étape 16 : "ne jamais modifier la machine réseau réelle
// pendant un test unitaire").
// ---------------------------------------------------------------------------

const fakeWgScript = `#!/bin/sh
if [ "$1" = "show" ] && [ -n "$2" ]; then
  if [ -f "$FAKE_WG_STATE_DIR/$2.up" ]; then
    echo "interface: $2"
    exit 0
  fi
  echo "No such device" 1>&2
  exit 1
fi
exit 1
`

const fakeWgQuickScript = `#!/bin/sh
action="$1"
cfgpath="$2"
iface=$(basename "$cfgpath")
iface=${iface%.conf}
case "$action" in
  up)
    touch "$FAKE_WG_STATE_DIR/$iface.up"
    exit 0
    ;;
  down)
    rm -f "$FAKE_WG_STATE_DIR/$iface.up"
    exit 0
    ;;
esac
exit 1
`

// deployFakeWireGuardTools installe de faux "wg"/"wg-quick" en tête de PATH
// pour la durée du test, et retourne le répertoire d'état partagé (marqueurs
// "<iface>.up").
func deployFakeWireGuardTools(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh indisponible : impossible de simuler wg/wg-quick pour ce test")
	}

	toolsDir := t.TempDir()
	stateDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(toolsDir, "wg"), []byte(fakeWgScript), 0o755); err != nil {
		t.Fatalf("écriture faux wg : %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "wg-quick"), []byte(fakeWgQuickScript), 0o755); err != nil {
		t.Fatalf("écriture faux wg-quick : %v", err)
	}

	t.Setenv("PATH", toolsDir+":"+os.Getenv("PATH"))
	t.Setenv("FAKE_WG_STATE_DIR", stateDir)
	return stateDir
}

func freeUDPPort(t *testing.T) (int, *net.UDPConn) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("sondage port UDP libre : %v", err)
	}
	return conn.LocalAddr().(*net.UDPAddr).Port, conn
}

// TestInstallDetectsToolsAndGeneratesServerKey couvre Install() avec de faux
// outils disponibles : succès, clé serveur générée et persistée.
func TestInstallDetectsToolsAndGeneratesServerKey(t *testing.T) {
	deployFakeWireGuardTools(t)
	origData := engineutil.DefaultDataDir
	engineutil.DefaultDataDir = t.TempDir()
	t.Cleanup(func() { engineutil.DefaultDataDir = origData })

	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	if err := e.Install(context.Background(), engine.InstallConfig{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(serverKeyPath(engineutil.DefaultDataDir)); err != nil {
		t.Fatalf("clé serveur non créée par Install() : %v", err)
	}
}

// TestWireGuardEngineLifecycleWithFakeTools couvre Start -> Endpoint réel
// (bind-probe UDP + `wg show` simulé) -> Status -> Stop -> Endpoint
// indisponible, avec les faux outils — sans toucher au réseau réel au-delà
// d'un simple socket UDP loopback ouvert par le test lui-même (simulant le
// port que `wg-quick up` aurait réellement ouvert).
func TestWireGuardEngineLifecycleWithFakeTools(t *testing.T) {
	deployFakeWireGuardTools(t)

	port, conn := freeUDPPort(t)
	defer conn.Close() // simule le port réellement occupé par l'interface

	cfgPath := filepath.Join(t.TempDir(), "wg-labosurf.conf")
	cfg := LabosurfMarker + "\n[Interface]\nPrivateKey = abc\n" +
		"ListenPort = " + strconv.Itoa(port) + "\n\n[Peer]\nPublicKey = xyz\nAllowedIPs = 10.66.0.2/32\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}

	e := &WireGuardEngine{configPath: cfgPath}
	ctx := context.Background()

	if _, ok := e.Endpoint(); ok {
		t.Fatal("endpoint inattendu avant Start()")
	}

	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}

	ep, ok := e.Endpoint()
	if !ok {
		t.Fatal("endpoint attendu après Start()")
	}
	if ep.Network != "udp" {
		t.Fatalf("réseau attendu udp, obtenu %q", ep.Network)
	}
	wantAddr := "0.0.0.0:" + strconv.Itoa(port)
	if ep.Addr != wantAddr {
		t.Fatalf("adresse attendue %q, obtenue %q", wantAddr, ep.Addr)
	}

	st := e.Status()
	if !st.Running {
		t.Fatal("Status().Running devrait être vrai après Start()")
	}
	if st.Health != "healthy" {
		t.Fatalf("santé attendue 'healthy', obtenu %q", st.Health)
	}

	if err := e.HealthCheck(); err != nil {
		t.Fatalf("HealthCheck après Start : %v", err)
	}

	logs, err := e.Logs(10)
	if err != nil {
		t.Fatalf("Logs : %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("logs vides alors que l'interface est active")
	}

	if err := e.Stop(); err != nil {
		t.Fatalf("Stop : %v", err)
	}
	if _, ok := e.Endpoint(); ok {
		t.Fatal("endpoint ne devrait plus être disponible après Stop()")
	}
	if e.Status().Running {
		t.Fatal("Status().Running devrait être faux après Stop()")
	}
}

// TestStopNoopWhenIfaceAbsent vérifie qu'arrêter un moteur jamais démarré ne
// retourne pas d'erreur (mission P2, Étape 4 : "gérer proprement le cas où
// l'interface n'existe plus").
func TestStopNoopWhenIfaceAbsent(t *testing.T) {
	deployFakeWireGuardTools(t)
	e := &WireGuardEngine{configPath: filepath.Join(t.TempDir(), "wg-labosurf.conf")}
	if err := e.Stop(); err != nil {
		t.Fatalf("Stop() sur une interface jamais démarrée devrait être un no-op, obtenu : %v", err)
	}
}

// TestStopRefusesWithoutOwnershipMarker vérifie que Stop() refuse d'agir si
// le fichier de configuration ne porte plus le marqueur LABOSURF — jamais
// détruire une interface dont on ne peut plus confirmer la possession
// (mission P2, Étape 4).
func TestStopRefusesWithoutOwnershipMarker(t *testing.T) {
	stateDir := deployFakeWireGuardTools(t)

	cfgPath := filepath.Join(t.TempDir(), "wg-labosurf.conf")
	// Config SANS le marqueur LABOSURF.
	os.WriteFile(cfgPath, []byte("[Interface]\nPrivateKey = abc\nListenPort = 51820\n"), 0o600)

	// Simule une interface déjà "active" (comme si wg-quick up avait
	// déjà tourné une fois, puis que le fichier avait été remplacé).
	os.WriteFile(filepath.Join(stateDir, "wg-labosurf.up"), []byte("1"), 0o644)

	e := &WireGuardEngine{configPath: cfgPath}
	if err := e.Stop(); err == nil {
		t.Fatal("Stop() aurait dû refuser d'agir sur une configuration sans marqueur LABOSURF")
	}

	// Vérifie que wg-quick down n'a PAS été appelé : le marqueur "up" est
	// toujours là.
	if _, err := os.Stat(filepath.Join(stateDir, "wg-labosurf.up")); err != nil {
		t.Fatal("l'interface simulée ne devrait pas avoir été arrêtée")
	}
}
