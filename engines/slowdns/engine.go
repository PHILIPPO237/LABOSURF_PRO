// Package slowdns fournit le moteur SlowDNS natif de la plateforme LABOSURF PRO.
//
// Le tunnel DNS est implémenté directement en Go. Le serveur écoute sur le
// port UDP 53 (DNS), décode les requêtes DNS contenant des données tunnel
// dans les sous-domaines, et les transfère vers un backend TCP (SSH).
package slowdns

import (
	"context"
	"fmt"
	"log"
	"os"

	"labosurf/internal/engine"
)

const (
	engineName = "slowdns"
	engineDesc = "SlowDNS natif : tunnel DNS vers backend SSH, comptes gérés depuis le store central."
	engineVer  = "1.0.0"
)

type SlowDNSEngineWrapper struct {
	configPath string
	server     *SlowDNSServer
	cancel     context.CancelFunc
	done       chan error
}

func New() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/slowdns/config.json"
	if p := os.Getenv("LABOSURF_SLOWDNS_CONFIG"); p != "" {
		cfgPath = p
	}
	return &SlowDNSEngineWrapper{configPath: cfgPath}, nil
}

func (e *SlowDNSEngineWrapper) Name() string        { return engineName }
func (e *SlowDNSEngineWrapper) Version() string      { return engineVer }
func (e *SlowDNSEngineWrapper) Description() string  { return engineDesc }

func (e *SlowDNSEngineWrapper) Install(ctx context.Context, cfg engine.InstallConfig) error {
	dir := "/etc/labosurf/engines/slowdns"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	log.Printf("✔ SlowDNS : répertoire %s créé", dir)
	return nil
}

func (e *SlowDNSEngineWrapper) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration SlowDNS vide")
	}
	dir := "/etc/labosurf/engines/slowdns"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, cfg.JSON, 0o600); err != nil {
		return fmt.Errorf("écriture config SlowDNS : %w", err)
	}
	log.Printf("✔ SlowDNS : configuration écrite dans %s", e.configPath)
	return nil
}

func (e *SlowDNSEngineWrapper) Start(ctx context.Context) error {
	cfg, err := loadSlowDNSConfig(e.configPath)
	if err != nil {
		return err
	}
	srv, err := NewSlowDNSServer(cfg)
	if err != nil {
		return err
	}
	e.server = srv
	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.done = make(chan error, 1)
	go func() {
		e.done <- srv.Run(runCtx)
	}()
	return nil
}

func (e *SlowDNSEngineWrapper) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	return <-e.done
}

func (e *SlowDNSEngineWrapper) Stop() error {
	if e.cancel != nil {
		e.cancel()
	}
	if e.server != nil {
		return e.server.Close()
	}
	return nil
}

func (e *SlowDNSEngineWrapper) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

func (e *SlowDNSEngineWrapper) Status() engine.EngineStatus {
	if e.server == nil {
		return engine.EngineStatus{Installed: true}
	}
	return engine.EngineStatus{Installed: true, Running: true}
}

func (e *SlowDNSEngineWrapper) HealthCheck() error {
	if e.server == nil {
		return fmt.Errorf("serveur SlowDNS non démarré")
	}
	return nil
}

func (e *SlowDNSEngineWrapper) Logs(lines int) ([]string, error) {
	return []string{"SlowDNS Engine : logging via stdout/stderr du service systemd"}, nil
}

func (e *SlowDNSEngineWrapper) Update() error {
	return nil
}

func (e *SlowDNSEngineWrapper) Uninstall() error {
	_ = e.Stop()
	_ = os.Remove(e.configPath)
	return nil
}

func init() {
	engine.Register(engineName, New)
}
