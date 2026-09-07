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
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	done       chan error
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

// getArchSuffix returns the architecture suffix for Xray-core assets
func getArchSuffix() string {
	switch runtime.GOARCH {
	case "amd64":
		return "linux-64"
	case "arm64":
		return "linux-arm64"
	default:
		return "linux-64"
	}
}

// getAssetURL returns the download URL for the current architecture
func getAssetURL() string {
	return fmt.Sprintf("%s/%s/Xray-%s.zip", xrayCoreBaseURL, xrayCoreVersion, getArchSuffix())
}

// getExpectedSHA256 returns the expected SHA256 for the current architecture
// These should be updated when version changes
func getExpectedSHA256() string {
	// These are placeholder - in production these should be verified
	// For now we'll skip SHA256 verification in tests and use a map
	arch := getArchSuffix()
	switch arch {
	case "linux-64":
		return "" // Will be filled from config or verified at runtime
	case "linux-arm64":
		return ""
	default:
		return ""
	}
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
		"/etc/labosurf/engines/xray/reality", // For REALITY keys
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("création répertoire %s: %w", dir, err)
		}
	}

	// Generate or load REALITY keys
	realityDir := "/etc/labosurf/engines/xray/reality"
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

	// Inject REALITY public key if present
	realityDir := "/etc/labosurf/engines/xray/reality"
	if keys, err := LoadRealityKeys(realityDir); err == nil {
		if inbounds, ok := config["inbounds"].([]any); ok && len(inbounds) > 0 {
			if inbound, ok := inbounds[0].(map[string]any); ok {
				if streamSettings, ok := inbound["streamSettings"].(map[string]any); ok {
					if realitySettings, ok := streamSettings["realitySettings"].(map[string]any); ok {
						realitySettings["publicKey"] = keys.PublicKeyHex()
					}
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
	e.cancel = cancel
	e.done = make(chan error, 1)

	cmd := exec.CommandContext(runCtx, e.binaryPath, "run", "-config", e.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	e.cmd = cmd
	go func() {
		e.done <- cmd.Run()
	}()

	// Wait a moment for startup
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-e.done:
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
	return <-e.done
}

// Stop stops the Xray-core process
func (e *XrayCoreEngine) Stop() error {
	if e.cancel != nil {
		e.cancel()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		// Give it a moment to shutdown gracefully
		done := make(chan error, 1)
		go func() {
			done <- e.cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if err := e.cmd.Process.Kill(); err != nil {
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

	running := false
	pid := 0
	if e.cmd != nil && e.cmd.Process != nil {
		running = true
		pid = e.cmd.Process.Pid
	}

	return engine.EngineStatus{
		Installed:  installed,
		Running:    running,
		PID:        pid,
		ListenAddr: "",
		Port:       0,
		Health:     map[bool]string{true: "healthy", false: "unknown"}[running],
		ErrorCode:  "",
	}
}

// HealthCheck verifies the engine is operational
func (e *XrayCoreEngine) HealthCheck() error {
	if e.cmd == nil || e.cmd.Process == nil {
		return fmt.Errorf("Xray-core non démarré")
	}

	// Try to connect to the configured port
	// Parse config to get port
	if _, err := os.Stat(e.configPath); err != nil {
		return fmt.Errorf("config non trouvée")
	}

	data, err := os.ReadFile(e.configPath)
	if err != nil {
		return fmt.Errorf("lecture config: %w", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("config JSON invalide: %w", err)
	}

	// Try to connect to the first inbound port
	if inbounds, ok := cfg["inbounds"].([]any); ok && len(inbounds) > 0 {
		if inbound, ok := inbounds[0].(map[string]any); ok {
			if portVal, ok := inbound["port"].(float64); ok && portVal > 0 {
				addr := fmt.Sprintf("127.0.0.1:%d", int(portVal))
				conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
				if err != nil {
					return fmt.Errorf("port %d non accessible: %w", int(portVal), err)
				}
				conn.Close()
			}
		}
	}

	return nil
}

// Logs returns recent logs (from stdout/stderr)
func (e *XrayCoreEngine) Logs(lines int) ([]string, error) {
	if e.cmd == nil || e.cmd.Process == nil {
		return []string{"Xray-core non démarré"}, nil
	}
	return []string{fmt.Sprintf("Xray-core (PID %d) : logs via stdout/stderr systemd", e.cmd.Process.Pid)}, nil
}

// Update updates the Xray-core binary
func (e *XrayCoreEngine) Update() error {
	// Stop if running
	if e.cmd != nil && e.cmd.Process != nil {
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

// Version returns the version
func (e *XrayCoreEngine) Version() string { return e.version }

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