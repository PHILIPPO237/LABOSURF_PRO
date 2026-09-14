// Package freewaygate fournit le moteur freeway-gate pour la plateforme LABOSURF PRO.
//
// Ce moteur télécharge, configure et supervise le binaire freeway-gate
// (https://github.com/PHILIPPO237/freeway-gate) : reverse proxy
// multi-opérateur zero-rating (MTN + Orange Maxit), chaînage
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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"labosurf/internal/engine"
)

const (
	engineName = "freeway-gate"
	engineDesc = "Reverse proxy multi-operateur zero-rating (MTN + Orange Maxit), chainage CONNECT serveur"
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
	if err := os.WriteFile(configPath, []byte(DefaultConfigJSON), 0o600); err != nil {
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
	if !json.Valid(data) {
		return fmt.Errorf("configuration freeway-gate : JSON invalide")
	}
	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, data, 0o600); err != nil {
		return err
	}
	e.listen = listenFromData(data)
	return nil
}

// Start démarre freeway-gate de façon NON bloquante : le binaire tourne en
// goroutine, et Start rend la main dès que le process tient (ou après une
// grâce anti-échec immédiat), en signalant une sortie immédiate (config
// invalide, port déjà pris…) comme une erreur — jamais un silence.
func (e *FreewayGateEngine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.cmd != nil {
		e.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	cmd := exec.CommandContext(runCtx, e.binaryPath, "-config", e.configPath)
	cmd.Stdout = e.logs
	cmd.Stderr = e.logs
	e.cmd = cmd
	e.cancel = cancel
	e.done = done
	e.mu.Unlock()

	go func() {
		done <- cmd.Run()
		e.mu.Lock()
		if e.cmd == cmd {
			e.cmd = nil
		}
		e.mu.Unlock()
	}()

	// Grâce anti-échec immédiat : sur un hôte lent (WSL, charge), le process
	// peut mettre un peu plus de 500 ms à être planifié avant de mourir. Au
	// lieu d'un sleep fixe, on sonde la sortie réelle (done) et la vie du
	// process ; le vrai signal de santé, en exploitation, reste les sondes
	// Endpoint()/HealthCheck().
	startFail := func(err error) error {
		e.mu.Lock()
		e.cmd = nil
		e.mu.Unlock()
		return fmt.Errorf("freeway-gate s'est arrêté immédiatement: %w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return startFail(err)
		default:
		}
		e.mu.Lock()
		pid := 0
		if e.cmd == cmd && cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		e.mu.Unlock()
		if pid > 0 && !processAlive(pid) {
			select {
			case err := <-done:
				return startFail(err)
			case <-time.After(2 * time.Second):
				return startFail(fmt.Errorf("process %d mort (sortie non collectée)", pid))
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// processAlive rapporte si le process est encore dans la table des process
// (signal 0 POSIX : jamais de signal envoyé, juste un test d'existence).
// Sur Windows la sémantique du signal 0 n'existe pas : on s'appuie alors
// uniquement sur done, position conservative (jamais de faux négatif).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

// RunForeground lance le moteur en avant-plan (systemd Type=simple).
func (e *FreewayGateEngine) RunForeground(ctx context.Context) error {
	return e.Start(ctx)
}

// Stop arrête freeway-gate : annulation du contexte (SIGKILL via
// exec.CommandContext) après une courte attente gracieuse.
func (e *FreewayGateEngine) Stop() error {
	e.mu.Lock()
	cancel := e.cancel
	cmd := e.cmd
	done := e.done
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
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

// HealthCheck vérifie que le moteur est opérationnel via /health, avec un
// timeout explicite (jamais http.Get sans limite).
func (e *FreewayGateEngine) HealthCheck() error {
	e.mu.Lock()
	addr := e.listen
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	e.mu.Unlock()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
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

// Uninstall supprime le binaire et le fichier de configuration du moteur
// (jamais le répertoire parent, qui peut contenir d'autres données).
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
		_ = os.Remove(configPath)
	}
	return nil
}

// Endpoint expose l'adresse d'écoute du moteur (implémente engine.Endpointer),
// mais ne la déclare prête qu'après une VRAIE tentative de connexion TCP sur
// l'adresse configurée — jamais une déduction optimiste basée sur la seule
// présence du process.
func (e *FreewayGateEngine) Endpoint() (engine.Endpoint, bool) {
	e.mu.Lock()
	cmd := e.cmd
	addr := e.listen
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return engine.Endpoint{}, false
	}
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return engine.Endpoint{}, false
	}
	conn.Close()
	return engine.Endpoint{Network: "tcp", Addr: addr}, true
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
    "mtn": ["proxy-mtn.", "mtn."],
    "orange": ["proxy-orange.", "orange."]
  },
  "profiles": {
    "mtn": {
      "target": "http://127.0.0.1:80",
      "headers": {
        "user-agent": "Mozilla/5.0 (Linux; Android 14; %MODEL%) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.7871.46 Mobile Safari/537.36",
        "accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
        "accept-encoding": "gzip, deflate, br",
        "accept-language": "fr-CM,fr;q=0.9,en-US;q=0.8",
        "referer": "https://one.mtn.cm/",
        "connection": "keep-alive",
        "sec-ch-ua": "\"Chromium\";v=\"150\", \"Google Chrome\";v=\"150\", \"Not?A_Brand\";v=\"99\"",
        "sec-ch-ua-mobile": "?1",
        "sec-ch-ua-platform": "\"Android\"",
        "sec-ch-ua-platform-version": "\"14.0.0\"",
        "sec-ch-ua-model": "\"%MODEL%\""
      },
      "ua_models": ["SM-A515F", "SM-A125F", "SM-G780F", "M2101K6G", "CPH2399"],
      "chains": [],
      "chain_target": ""
    },
    "orange": {
      "target": "http://127.0.0.1:81",
      "headers": {
        "user-agent": "OrangeMaxit/8.0.0 (Linux; Android 13; %MODEL%)",
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
