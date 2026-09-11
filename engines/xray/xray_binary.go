package xray

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"labosurf/internal/engine"
)

// XrayBinaryConfig holds configuration for the Xray-core binary
type XrayBinaryConfig struct {
	Version       string `json:"version"`
	BinaryURL     string `json:"binary_url"`
	BinarySHA256  string `json:"binary_sha256"`
	ConfigPath    string `json:"config_path"`
	BinaryPath    string `json:"binary_path"`
	AssetDir      string `json:"asset_dir"`
}

// XrayCoreEngine wraps the real Xray-core binary
type XrayCoreEngine struct {
	configPath string
	binaryPath string
	assetDir   string
	version    string

	mu     sync.Mutex // protège cmd/cancel/done contre Start/Stop/Endpoint concurrents
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan error
}

// XrayCoreConfig holds the Xray-core configuration
type XrayCoreConfig struct {
	Inbounds  []json.RawMessage `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
	Routing   json.RawMessage   `json:"routing,omitempty"`
	DNS       json.RawMessage   `json:"dns,omitempty"`
	Policy    json.RawMessage   `json:"policy,omitempty"`
}

const (
	xrayCoreVersion      = "v26.3.27"
	xrayCoreBaseURL      = "https://github.com/XTLS/Xray-core/releases/download"
	xrayCoreBinaryName   = "xray"
	xrayCoreAssetDirName = "assets"
)

// getArchSuffix returns the architecture suffix for Xray-core assets.
// Doit correspondre EXACTEMENT au nom d'asset publié par XTLS/Xray-core
// (vérifié pour xrayCoreVersion via les fichiers .dgst officiels de la
// release) — "linux-arm64" (sans le "-v8a") n'a jamais existé comme asset
// et faisait échouer le téléchargement (404) sur toute machine arm64.
func getArchSuffix() string {
	return archSuffixFor(runtime.GOARCH)
}

// archSuffixFor est la version paramétrée de getArchSuffix, pour pouvoir
// tester le mapping pour toutes les architectures cibles depuis un binaire
// de test qui ne tourne que sur une seule (runtime.GOARCH est fixe).
func archSuffixFor(goarch string) string {
	switch goarch {
	case "amd64":
		return "linux-64"
	case "arm64":
		return "linux-arm64-v8a"
	default:
		return "linux-64"
	}
}

// getAssetURL returns the download URL for the current architecture
func getAssetURL() string {
	return fmt.Sprintf("%s/%s/Xray-%s.zip", xrayCoreBaseURL, xrayCoreVersion, getArchSuffix())
}

// getExpectedSHA256 returns the expected SHA-256 of the Xray-core release
// zip for the current architecture, pinned to xrayCoreVersion.
//
// Valeurs relevées manuellement depuis les fichiers .dgst signés publiés
// par XTLS/Xray-core pour la release xrayCoreVersion (champ "SHA2-256" de
// https://github.com/XTLS/Xray-core/releases/download/<version>/Xray-<arch>.zip.dgst) :
//   - Xray-linux-64.zip.dgst
//   - Xray-linux-arm64-v8a.zip.dgst
//
// IMPORTANT : si xrayCoreVersion est mis à jour, ces deux valeurs DOIVENT
// être remises à jour depuis les .dgst de la nouvelle release — sinon
// downloadAndInstallBinary() rejettera systématiquement le binaire
// téléchargé (SHA256 mismatch), ce qui est le comportement sûr par défaut
// (échec explicite) plutôt que d'installer un binaire non vérifié.
func getExpectedSHA256() string {
	return expectedSHA256For(getArchSuffix())
}

// expectedSHA256For est la version paramétrée de getExpectedSHA256, pour
// pouvoir tester les deux checksums connus depuis un seul binaire de test.
func expectedSHA256For(archSuffix string) string {
	switch archSuffix {
	case "linux-64":
		return "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae"
	case "linux-arm64-v8a":
		return "4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c"
	default:
		return ""
	}
}

// RealityDir retourne le répertoire où sont stockées les clés REALITY
// (générées par Install, utilisées par Configure) — surchargeable via
// LABOSURF_XRAY_REALITY_DIR comme les autres chemins de ce moteur.
func RealityDir() string {
	if p := os.Getenv("LABOSURF_XRAY_REALITY_DIR"); p != "" {
		return p
	}
	return "/etc/labosurf/engines/xray/reality"
}

// NewXrayCoreEngine creates a new Xray engine using the real Xray-core binary
func NewXrayCoreEngine() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/xray/config.json"
	if p := os.Getenv("LABOSURF_XRAY_CONFIG"); p != "" {
		cfgPath = p
	}

	binaryDir := "/usr/local/bin"
	if p := os.Getenv("LABOSURF_XRAY_BINARY_DIR"); p != "" {
		binaryDir = p
	}
	binaryPath := filepath.Join(binaryDir, "xray")

	assetDir := "/usr/local/share/xray"
	if p := os.Getenv("LABOSURF_XRAY_ASSET_DIR"); p != "" {
		assetDir = p
	}

	return &XrayCoreEngine{
		configPath: cfgPath,
		binaryPath: binaryPath,
		assetDir:   assetDir,
		version:    xrayCoreVersion,
	}, nil
}

// Install downloads and installs the Xray-core binary
func (e *XrayCoreEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	// Create directories
	dirs := []string{
		filepath.Dir(e.configPath),
		filepath.Dir(e.binaryPath),
		e.assetDir,
		RealityDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("création répertoire %s: %w", dir, err)
		}
	}

	// Generate or load REALITY keys
	realityDir := RealityDir()
	realityKeys, err := EnsureRealityKeys(realityDir)
	if err != nil {
		return fmt.Errorf("clés REALITY: %w", err)
	}
	log.Printf("✔ Clés REALITY prêtes (public key: %s...)", realityKeys.PublicKeyHex()[:16])

	// Check if binary already exists and is correct version
	if e.isBinaryInstalled() {
		log.Printf("✔ Xray-core %s déjà installé", e.version)
		return nil
	}

	// Download and install Xray-core binary
	log.Printf("⬇ Téléchargement Xray-core %s pour %s...", e.version, getArchSuffix())
	if err := e.downloadAndInstallBinary(ctx); err != nil {
		return fmt.Errorf("installation Xray-core: %w", err)
	}

	// Download and install geoip/geosite assets
	if err := e.downloadAndInstallAssets(ctx); err != nil {
		log.Printf("⚠ Impossible de télécharger les assets geoip/geosite: %v", err)
		// Non-fatal, Xray can work without them
	}

	log.Printf("✔ Xray-core %s installé avec succès", e.version)
	return nil
}

// isBinaryInstalled checks if the binary exists and is the correct version
func (e *XrayCoreEngine) isBinaryInstalled() bool {
	if _, err := os.Stat(e.binaryPath); err != nil {
		return false
	}

	// Check version
	cmd := exec.Command(e.binaryPath, "-version")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), e.version)
}

// downloadAndInstallBinary downloads and installs the Xray-core binary
func (e *XrayCoreEngine) downloadAndInstallBinary(ctx context.Context) error {
	url := getAssetURL()
	tmpDir, err := os.MkdirTemp("", "xray-download-*")
	if err != nil {
		return fmt.Errorf("répertoire temporaire: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, "xray.zip")

	// Download with retries
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("création requête: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("téléchargement: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	// Write zip file
	out, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("création fichier zip: %w", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return fmt.Errorf("écriture zip: %w", err)
	}
	out.Close()

	// Verify SHA256 if expected
	if expectedSHA := getExpectedSHA256(); expectedSHA != "" {
		if err := verifySHA256(zipPath, expectedSHA); err != nil {
			return fmt.Errorf("vérification SHA256 échouée: %w", err)
		}
	}

	// Extract binary
	if err := extractXrayBinary(zipPath, tmpDir); err != nil {
		return fmt.Errorf("extraction binaire: %w", err)
	}

	// Move binary to destination
	binarySrc := filepath.Join(tmpDir, xrayCoreBinaryName)
	if err := os.Rename(binarySrc, e.binaryPath); err != nil {
		// Try copy if rename fails (cross-device)
		if err := copyFile(binarySrc, e.binaryPath); err != nil {
			return fmt.Errorf("installation binaire: %w", err)
		}
	}

	// Make executable
	if err := os.Chmod(e.binaryPath, 0o755); err != nil {
		return fmt.Errorf("chmod binaire: %w", err)
	}

	return nil
}

// downloadAndInstallAssets downloads geoip.dat and geosite.dat
func (e *XrayCoreEngine) downloadAndInstallAssets(ctx context.Context) error {
	assetBaseURL := fmt.Sprintf("%s/%s", xrayCoreBaseURL, e.version)
	assets := []string{"geoip.dat", "geosite.dat"}

	for _, asset := range assets {
		url := fmt.Sprintf("%s/%s", assetBaseURL, asset)
		dst := filepath.Join(e.assetDir, asset)

		if _, err := os.Stat(dst); err == nil {
			continue // Already exists
		}

		log.Printf("⬇ Téléchargement asset %s...", asset)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}

		client := &http.Client{Timeout: 2 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d pour %s", resp.StatusCode, asset)
		}

		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, resp.Body); err != nil {
			out.Close()
			return err
		}
		out.Close()
	}

	return nil
}

// Configure applies the configuration to the Xray engine
func (e *XrayCoreEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration Xray vide")
	}

	// Parse and update config with REALITY keys
	var config map[string]any
	if err := json.Unmarshal(cfg.JSON, &config); err != nil {
		return fmt.Errorf("JSON invalide: %w", err)
	}

	// Injecter la vraie clé privée REALITY si la config l'utilise.
	// Xray-core ne peut pas effectuer le handshake REALITY sans
	// streamSettings.realitySettings.privateKey : le laisser vide (c'était
	// le cas auparavant — clientcfg.go génère `"privateKey": ""` avec le
	// commentaire "will be generated at install time", mais rien ne le
	// remplissait jamais) produit un serveur qui ne peut jamais valider de
	// handshake REALITY, sans aucune erreur visible avant l'échec en
	// production. Le champ "publicKey" injecté précédemment ici n'existe
	// pas dans le schéma des inbounds Xray-core (seul le client en a
	// besoin, via le lien VLESS — voir clientcfg.vlessLink) ; il n'était
	// donc de toute façon d'aucune utilité côté serveur.
	realityDir := RealityDir()
	if inbounds, ok := config["inbounds"].([]any); ok && len(inbounds) > 0 {
		if inbound, ok := inbounds[0].(map[string]any); ok {
			if streamSettings, ok := inbound["streamSettings"].(map[string]any); ok {
				if security, _ := streamSettings["security"].(string); security == "reality" {
					realitySettings, ok := streamSettings["realitySettings"].(map[string]any)
					if !ok {
						return fmt.Errorf("configuration Xray : security=reality mais streamSettings.realitySettings absent")
					}
					keys, err := LoadRealityKeys(realityDir)
					if err != nil {
						return fmt.Errorf("clés REALITY introuvables (%s) — lancez 'install' avant 'configure' : %w", realityDir, err)
					}
					realitySettings["privateKey"] = keys.PrivateKeyBase64()
				}
			}
		}
	}

	// Write updated config
	updatedJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("sérialisation config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, updatedJSON, 0o600); err != nil {
		return fmt.Errorf("écriture config: %w", err)
	}

	log.Printf("✔ Xray-core : configuration écrite (%d octets)", len(updatedJSON))
	return nil
}

// Start starts the Xray-core process
func (e *XrayCoreEngine) Start(ctx context.Context) error {
	if _, err := os.Stat(e.binaryPath); err != nil {
		return fmt.Errorf("binaire Xray-core non trouvé: %s (lancer 'install' d'abord)", e.binaryPath)
	}
	if _, err := os.Stat(e.configPath); err != nil {
		return fmt.Errorf("config non trouvée: %s (lancer 'configure' d'abord)", e.configPath)
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	cmd := exec.CommandContext(runCtx, e.binaryPath, "run", "-config", e.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	e.mu.Lock()
	e.cancel = cancel
	e.done = done
	e.cmd = cmd
	e.mu.Unlock()

	go func() {
		done <- cmd.Run()
	}()

	// Wait a moment for startup
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-done:
		return fmt.Errorf("Xray-core s'est arrêté immédiatement: %w", err)
	default:
	}

	log.Printf("✔ Xray-core démarré (PID: %d)", cmd.Process.Pid)
	return nil
}

// RunForeground runs the engine in foreground (for systemd)
func (e *XrayCoreEngine) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	return <-done
}

// Stop stops the Xray-core process
func (e *XrayCoreEngine) Stop() error {
	e.mu.Lock()
	cancel := e.cancel
	cmd := e.cmd
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		// Give it a moment to shutdown gracefully
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if err := cmd.Process.Kill(); err != nil {
				return fmt.Errorf("kill forcé: %w", err)
			}
			<-done
		}
	}
	return nil
}

// Restart restarts the engine
func (e *XrayCoreEngine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

// Status returns the engine status
func (e *XrayCoreEngine) Status() engine.EngineStatus {
	installed := false
	if _, err := os.Stat(e.binaryPath); err == nil {
		installed = true
	}

	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()

	running := false
	pid := 0
	if cmd != nil && cmd.Process != nil {
		running = true
		pid = cmd.Process.Pid
	}

	st := engine.EngineStatus{
		Installed:  installed,
		Running:    running,
		PID:        pid,
		ListenAddr: "",
		Port:       0,
		Health:     map[bool]string{true: "healthy", false: "unknown"}[running],
		ErrorCode:  "",
	}
	if running {
		if ep, ok := e.Endpoint(); ok {
			st.ListenAddr = ep.Addr
			if _, portStr, err := net.SplitHostPort(ep.Addr); err == nil {
				fmt.Sscanf(portStr, "%d", &st.Port)
			}
		}
	}
	return st
}

// configuredPort lit inbounds[0].port depuis le fichier de configuration
// actuellement écrit sur disque (celui que le process Xray-core a
// réellement chargé au démarrage, écrit par Configure()).
func (e *XrayCoreEngine) configuredPort() (int, bool) {
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		return 0, false
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, false
	}
	inbounds, ok := cfg["inbounds"].([]any)
	if !ok || len(inbounds) == 0 {
		return 0, false
	}
	inbound, ok := inbounds[0].(map[string]any)
	if !ok {
		return 0, false
	}
	portVal, ok := inbound["port"].(float64)
	if !ok || portVal <= 0 {
		return 0, false
	}
	return int(portVal), true
}

// Endpoint implémente engine.Endpointer. Contrairement aux moteurs Go
// natifs (hysteria/dnstt/slowdns/ssh), Xray-core est un processus externe
// qui lit son port d'écoute dans le fichier de config (pas un socket
// net.Listen géré par ce process Go) : Endpoint() lit ce port déjà écrit
// par Configure(), puis vérifie par une vraie tentative de connexion TCP
// que le process écoute réellement dessus avant de déclarer l'endpoint
// prêt — jamais une déduction optimiste basée sur le seul délai de Start().
func (e *XrayCoreEngine) Endpoint() (engine.Endpoint, bool) {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return engine.Endpoint{}, false
	}

	port, ok := e.configuredPort()
	if !ok {
		return engine.Endpoint{}, false
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return engine.Endpoint{}, false
	}
	conn.Close()
	return engine.Endpoint{Network: "tcp", Addr: addr}, true
}

// HealthCheck verifies the engine is operational
func (e *XrayCoreEngine) HealthCheck() error {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("Xray-core non démarré")
	}

	port, ok := e.configuredPort()
	if !ok {
		// Pas de port configuré (ou config illisible) : rien à sonder.
		return nil
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("port %d non accessible: %w", port, err)
	}
	conn.Close()
	return nil
}

// Logs returns recent logs (from stdout/stderr)
func (e *XrayCoreEngine) Logs(lines int) ([]string, error) {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return []string{"Xray-core non démarré"}, nil
	}
	return []string{fmt.Sprintf("Xray-core (PID %d) : logs via stdout/stderr systemd", cmd.Process.Pid)}, nil
}

// Update updates the Xray-core binary
func (e *XrayCoreEngine) Update() error {
	// Stop if running
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		if err := e.Stop(); err != nil {
			return err
		}
	}

	// Remove old binary
	os.Remove(e.binaryPath)

	// Reinstall
	ctx := context.Background()
	if err := e.downloadAndInstallBinary(ctx); err != nil {
		return err
	}
	if err := e.downloadAndInstallAssets(ctx); err != nil {
		log.Printf("⚠ Assets update failed: %v", err)
	}

	return nil
}

// Uninstall removes the engine
func (e *XrayCoreEngine) Uninstall() error {
	_ = e.Stop()
	os.Remove(e.binaryPath)
	os.Remove(e.configPath)
	os.RemoveAll(e.assetDir)
	return nil
}

// Name returns the engine name
func (e *XrayCoreEngine) Name() string { return "xray" }

// Version returns the version, without the leading "v" of the upstream
// release tag — every other engine's Version() already omits it, and
// callers (menu headers, dashboards) prepend their own "v".
func (e *XrayCoreEngine) Version() string { return strings.TrimPrefix(e.version, "v") }

// Description returns the description
func (e *XrayCoreEngine) Description() string {
	return fmt.Sprintf("Serveur Xray-core officiel %s (VLESS, Trojan, VMess, Shadowsocks, etc.)", e.version)
}

// Helper functions

func extractXrayBinary(zipPath, destDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("ouverture zip: %w", err)
	}
	defer reader.Close()

	for _, f := range reader.File {
		if filepath.Base(f.Name) == xrayCoreBinaryName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			outPath := filepath.Join(destDir, xrayCoreBinaryName)
			out, err := os.Create(outPath)
			if err != nil {
				return err
			}
			defer out.Close()

			if _, err := io.Copy(out, rc); err != nil {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("binaire %s non trouvé dans l'archive", xrayCoreBinaryName)
}

func verifySHA256(filePath, expected string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}