// Package freewaygate fournit le moteur freeway-gate pour la plateforme LABOSURF PRO.
//
// Ce moteur télécharge, configure et supervise le binaire freeway-gate
// (https://github.com/PHILIPPO237/freeway-gate) : reverse proxy
// multi-opérateur zero-rating (MTN Free Basics + Orange Maxit), chaînage
// CONNECT côte serveur (round-robin + failover), injection d'en-têtes,
// rate limit et health.
//
// Le moteur pilote freeway-gate comme un binaire externe (pattern identique
// au moteur Xray/TUIC) : aucune recompilation de freeway-gate n'est
// nécessaire, sa version est interchangeable sans toucher au code.
package freewaygate

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"labosurf/internal/engine"
)

const (
	engineName = "freeway-gate"
	engineDesc = "Reverse proxy multi-operateur zero-rating (MTN Free Basics + Orange Maxit), chainage CONNECT serveur"
	engineVer  = "0.1.0"

	defaultDataDir = "/etc/labosurf/engines/freeway-gate"
	defaultBinDir  = "/opt/labosurf"
)

// FreewayGateEngine pilote le binaire freeway-gate.
type FreewayGateEngine struct {
	mu         sync.Mutex // protège cmd/cancel/done/buffer contre Start/Stop/Status concurrents
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	done       chan error
	binaryPath string
	configPath string
	listen     string
	logs       *logRing
}

// New construit le moteur.
func New() (engine.Engine, error) {
	return &FreewayGateEngine{
		listen: "127.0.0.1:8080",
		logs:   newLogRing(200),
	}, nil
}

func init() {
	engine.Register(engineName, New)
}

func (e *FreewayGateEngine) Name() string        { return engineName }
func (e *FreewayGateEngine) Version() string     { return engineVer }
func (e *FreewayGateEngine) Description() string { return engineDesc }

// Install déploie le binaire freeway-gate sur le système.
func (e *FreewayGateEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	binaryDir := cfg.BinaryDir
	if binaryDir == "" {
		binaryDir = defaultBinDir
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(binaryDir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(dataDir, "config.json")
	if err := os.WriteFile(configPath, []byte(DefaultConfigJSON), 0o644); err != nil {
		return err
	}
	binaryPath, err := installBinary(ctx, binaryDir)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.binaryPath = binaryPath
	e.configPath = configPath
	e.listen = listenFrom(configPath)
	e.mu.Unlock()
	return nil
}

// Configure applique la configuration du moteur.
func (e *FreewayGateEngine) Configure(_ context.Context, cfg engine.EngineConfig) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.configPath == "" {
		e.configPath = filepath.Join(defaultDataDir, "config.json")
	}
	if e.binaryPath == "" {
		e.binaryPath = filepath.Join(defaultBinDir, "freeway-gate")
	}
	data := cfg.JSON
	if len(data) == 0 {
		data = []byte(DefaultConfigJSON)
	}
	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, data, 0o644); err != nil {
		return err
	}
	e.listen = listenFromData(data)
	return nil
}

// Start démarre freeway-gate (bloquant jusqu'à l'arrêt).
func (e *FreewayGateEngine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.cmd != nil {
		e.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.done = make(chan error, 1)
	cmd := exec.CommandContext(ctx, e.binaryPath, "-config", e.configPath)
	cmd.Stdout = e.logs
	cmd.Stderr = e.logs
	e.cmd = cmd
	e.mu.Unlock()

	err := cmd.Run()
	e.mu.Lock()
	done := e.done
	e.cmd = nil
	e.mu.Unlock()
	done <- err
	return err
}

// RunForeground lance le moteur en avant-plan (systemd Type=simple).
func (e *FreewayGateEngine) RunForeground(ctx context.Context) error {
	return e.Start(ctx)
}

// Stop arrête freeway-gate.
func (e *FreewayGateEngine) Stop() error {
	e.mu.Lock()
	cancel := e.cancel
	cmd := e.cmd
	done := e.done
	e.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if cancel != nil {
		cancel()
		if done != nil {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	return nil
}

// Restart redémarre freeway-gate.
func (e *FreewayGateEngine) Restart(ctx context.Context) error {
	_ = e.Stop()
	go func() {
		_ = e.Start(ctx)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		up := e.cmd != nil && e.cmd.Process != nil && e.cmd.Process.Pid > 0
		e.mu.Unlock()
		if up {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

// Status retourne l'état courant du moteur.
func (e *FreewayGateEngine) Status() engine.EngineStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := engine.EngineStatus{
		Installed:  fileExists(e.binaryPath),
		Running:    e.cmd != nil && e.cmd.Process != nil,
		ListenAddr: e.listen,
		Health:     "unknown",
		ErrorCode:  engine.ErrCodeUnknown,
	}
	if st.Running {
		st.PID = e.cmd.Process.Pid
		st.Health = "healthy"
		st.Port = portOf(e.listen)
	}
	return st
}

// HealthCheck vérifie que le moteur est opérationnel via /health.
func (e *FreewayGateEngine) HealthCheck() error {
	e.mu.Lock()
	addr := e.listen
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	e.mu.Unlock()
	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: /health -> %s", engine.ErrCodeHealthCheckFailed, resp.Status)
	}
	return nil
}

// Logs retourne les dernières lignes du journal du moteur.
func (e *FreewayGateEngine) Logs(lines int) ([]string, error) {
	return e.logs.Last(lines), nil
}

// Update re-télécharge le dernier binaire freeway-gate.
func (e *FreewayGateEngine) Update() error {
	e.mu.Lock()
	binDir := filepath.Dir(e.binaryPath)
	e.mu.Unlock()
	path, err := installBinary(context.Background(), binDir)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.binaryPath = path
	e.mu.Unlock()
	return nil
}

// Uninstall supprime le binaire et la configuration du moteur.
func (e *FreewayGateEngine) Uninstall() error {
	e.mu.Lock()
	binaryPath := e.binaryPath
	configPath := e.configPath
	e.mu.Unlock()
	_ = e.Stop()
	if binaryPath != "" {
		_ = os.Remove(binaryPath)
	}
	if configPath != "" {
		_ = os.Remove(filepath.Dir(configPath)) // répertoire de config du moteur
	}
	return nil
}

// Endpoint expose l'adresse d'écoute du moteur (implémente engine.Endpointer).
func (e *FreewayGateEngine) Endpoint() (engine.Endpoint, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd == nil || e.cmd.Process == nil {
		return engine.Endpoint{}, false
	}
	return engine.Endpoint{Network: "tcp", Addr: e.listen}, true
}

// logRing est un tampon circulaire des sorties du binaire.
type logRing struct {
	mu  sync.Mutex
	buf []string
	max int
}

func newLogRing(max int) *logRing {
	return &logRing{max: max}
}

func (l *logRing) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range strings.Split(string(p), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		l.buf = append(l.buf, line)
		if len(l.buf) > l.max {
			l.buf = l.buf[len(l.buf)-l.max:]
		}
	}
	return len(p), nil
}

// Last retourne les n dernières lignes dans l'ordre chronologique.
func (l *logRing) Last(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.buf) {
		n = len(l.buf)
	}
	out := make([]string, n)
	copy(out, l.buf[len(l.buf)-n:])
	return out
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func portOf(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
}

// listenFrom extrait "listen" d'un fichier de config (retourne le défaut si absent).
func listenFrom(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "127.0.0.1:8080"
	}
	return listenFromData(data)
}

func listenFromData(data []byte) string {
	var c struct {
		Listen string `json:"listen"`
	}
	if err := json.Unmarshal(data, &c); err != nil || c.Listen == "" {
		return "127.0.0.1:8080"
	}
	return c.Listen
}

// DefaultConfigJSON est la configuration par défaut écrite à l'installation.
// Surchargée à la volée via Configure (même modèle que config.example.json
// du repo freeway-gate).
const DefaultConfigJSON = `{
  "listen": "127.0.0.1:8080",
  "hosts": {
    "mtn": ["mtn.proxy.", "mtn."],
    "orange": ["orange.proxy.", "orange."]
  },
  "profiles": {
    "mtn": {
      "target": "http://127.0.0.1:80",
      "headers": {
        "user-agent": "Mozilla/5.0 (Linux; Android 11; %MODEL%) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/104.0.0.0 Mobile Safari/537.36 [FBAN/FB4A;FBAV/419.0.0.45.100;FBPN/com.facebook.katana;FBDV/%MODEL%;FBLC/fr_FR;FBCR/MTN]",
        "accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
        "accept-encoding": "gzip, deflate, br",
        "accept-language": "en-US,en;q=0.9",
        "x-requested-with": "com.facebook.katana",
        "referer": "https://m.facebook.com/",
        "connection": "keep-alive"
      },
      "ua_models": ["SM-A515F", "SM-A125F", "SM-G780F", "M2101K6G", "CPH2399"],
      "bsids": ["@AK_QL", "@mtnplaycom"],
      "chains": [],
      "chain_target": ""
    },
    "orange": {
      "target": "http://127.0.0.1:81",
      "headers": {
        "user-agent": "OrangeMaxit/4.0.0 (Linux; Android 13; %MODEL%)",
        "accept": "application/json,text/plain,*/*",
        "accept-encoding": "gzip, deflate, br",
        "accept-language": "fr-CM,fr;q=0.9,en-US;q=0.8",
        "x-requested-with": "com.orange.myorange.cm",
        "x-caller-app-id": "maxit-cm-android",
        "referer": "https://maxit.orange.cm/",
        "connection": "keep-alive"
      },
      "ua_models": ["SM-A515F", "SM-A125F", "Pixel 7", "Redmi Note 12"],
      "auto_device_id": true,
      "chains": []
    }
  },
  "default_operator": "mtn",
  "rate_limit_max": 300
}`
