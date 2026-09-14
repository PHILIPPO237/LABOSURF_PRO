package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"labosurf/internal/engine"
	"labosurf/internal/store"
)

// UDPServerEngine est le moteur UDP implémentant l'interface engine.Engine
// de LABOSURF PRO. Il wrapper le serveur UDP existant (Server) pour le
// rendre pilotable par la plateforme multi-moteurs (install/start/stop/
// status/... à travers internal/engine).
type UDPServerEngine struct {
	configPath string

	server *Server
	store  *store.Store
	cancel context.CancelFunc
	done   chan error
	logs   *logRing
}

// UDPEngineFactory fabrique des instances du moteur UDP.
func UDPEngineFactory() (engine.Engine, error) {
	return &UDPServerEngine{
		configPath: defaultConfigPath(),
		logs:       newLogRing(200),
	}, nil
}

func defaultConfigPath() string {
	if p := os.Getenv("LABOSURF_CONFIG"); p != "" {
		return p
	}
	// Pendant la migration, on retombe sur le fichier historique.
	if _, err := os.Stat("config.json"); err == nil {
		return "config.json"
	}
	return filepath.Join("/etc", "labosurf", "config.json")
}

// Name retourne l'identifiant unique du moteur.
func (e *UDPServerEngine) Name() string {
	return "udp"
}

// Version retourne la version du moteur UDP.
func (e *UDPServerEngine) Version() string {
	return engineVersion
}

// Description fournit une courte description du moteur.
func (e *UDPServerEngine) Description() string {
	return "Moteur VPN UDP natif LABOSURF PRO (transport UDP personnalisé, chiffré uniquement côté proxy)."
}

// Install déploie le moteur UDP sur le système : crée le répertoire de
// données et écrit une configuration par défaut si elle est absente.
// Le moteur UDP est natif (aucun binaire tiers à télécharger).
func (e *UDPServerEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	dir := filepath.Dir(e.configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("création répertoire UDP (%s) : %w", dir, err)
	}
	if _, err := os.Stat(e.configPath); os.IsNotExist(err) {
		defaultCfg := fmt.Sprintf(`{
  "listen": "%s",
  "store": "%s",
  "portal": { "enabled": false, "listen": "%s" },
  "auth": { "mode": "store", "users": {} }
}`, defaultListen, store.StorePath(), defaultPortalListen)
		if err := os.WriteFile(e.configPath, []byte(defaultCfg), 0o600); err != nil {
			return fmt.Errorf("écriture configuration UDP par défaut : %w", err)
		}
	}
	log.Printf("✔ UDP Engine : répertoire %s prêt, configuration %s", dir, e.configPath)
	return nil
}

// Configure applique la configuration au moteur.
func (e *UDPServerEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration JSON vide")
	}
	if err := os.WriteFile(e.configPath, cfg.JSON, 0o600); err != nil {
		return fmt.Errorf("écriture configuration : %w", err)
	}
	return nil
}

// Start démarre le moteur UDP et redirige les logs vers le ring buffer.
func (e *UDPServerEngine) Start(ctx context.Context) error {
	config, err := loadConfig(e.configPath)
	if err != nil {
		return fmt.Errorf("erreur de configuration : %w", err)
	}

	st, err := store.LoadStore(store.StorePath())
	if err != nil {
		return fmt.Errorf("chargement store pour quota : %w", err)
	}
	e.store = st

	// Redirige la sortie standard du package log vers le ring buffer pour que
	// Logs() puisse retourner les vraies lignes de journal du serveur.
	log.SetOutput(e.logs)

	server, err := NewServer(config, st)
	if err != nil {
		return fmt.Errorf("démarrage UDP Engine : %w", err)
	}
	e.server = server

	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.done = make(chan error, 1)

	go func() {
		e.done <- server.Run(runCtx)
	}()

	return nil
}

// RunForeground lance le moteur UDP en avant-plan (utilisé par systemd).
func (e *UDPServerEngine) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	return <-e.done
}

// Stop arrête le moteur UDP.
func (e *UDPServerEngine) Stop() error {
	if e.cancel != nil {
		e.cancel()
	}
	if e.server != nil {
		return e.server.Close()
	}
	return nil
}

// Restart redémarre le moteur UDP.
func (e *UDPServerEngine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

// Status retourne l'état courant du moteur (PID, adresse, port inclus).
func (e *UDPServerEngine) Status() engine.EngineStatus {
	if e.server == nil {
		return engine.EngineStatus{Installed: true}
	}
	st := engine.EngineStatus{
		Installed: true,
		Running:   true,
		PID:       os.Getpid(),
	}
	if e.server.conn != nil {
		addr := e.server.conn.LocalAddr()
		st.ListenAddr = addr.String()
		if udpAddr, ok := addr.(*net.UDPAddr); ok {
			st.Port = udpAddr.Port
		}
	}
	return st
}

// HealthCheck vérifie que le moteur est opérationnel.
func (e *UDPServerEngine) HealthCheck() error {
	if e.server == nil {
		return fmt.Errorf("moteur UDP non démarré")
	}
	return nil
}

// Logs retourne les dernières lignes du journal du moteur depuis le ring buffer.
func (e *UDPServerEngine) Logs(lines int) ([]string, error) {
	return e.logs.Last(lines), nil
}

// Update : le moteur UDP est natif (aucun binaire tiers à re-télécharger).
func (e *UDPServerEngine) Update() error {
	return nil
}

// Uninstall désinstalle le moteur du système.
func (e *UDPServerEngine) Uninstall() error {
	_ = os.Remove(e.configPath)
	return nil
}

func init() {
	engine.Register("udp", UDPEngineFactory)
}

// logRing est un tampon circulaire thread-safe qui capture la sortie du
// package log standard (log.SetOutput) pour l'exposer via Logs().
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
	if n == 0 {
		return []string{}
	}
	out := make([]string, n)
	copy(out, l.buf[len(l.buf)-n:])
	return out
}
