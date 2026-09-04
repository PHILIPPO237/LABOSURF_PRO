// Package dnstt fournit le moteur dnstt natif de la plateforme LABOSURF PRO.
//
// Tunnel DNS quasi-indétectable implémenté directement en Go : les données
// sont encodées en base32 dans les sous-domaines de requêtes DNS, et
// transférées vers un backend TCP (SSH par défaut).
package dnstt

import (
	"context"
	"fmt"
	"log"
	"os"

	"labosurf/internal/engine"
)

const (
	engineName = "dnstt"
	engineDesc = "Serveur dnstt natif : tunnel DNS quasi-indétectable, comptes gérés depuis le store central."
	engineVer  = "1.0.0"
)

type DNSTTEngineWrapper struct {
	configPath string
	server     *DNSTTServer
	cancel     context.CancelFunc
	done       chan error
}

func New() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/dnstt/config.json"
	if p := os.Getenv("LABOSURF_DNSTT_CONFIG"); p != "" {
		cfgPath = p
	}
	return &DNSTTEngineWrapper{configPath: cfgPath}, nil
}

func (e *DNSTTEngineWrapper) Name() string       { return engineName }
func (e *DNSTTEngineWrapper) Version() string     { return engineVer }
func (e *DNSTTEngineWrapper) Description() string { return engineDesc }

func (e *DNSTTEngineWrapper) Install(ctx context.Context, cfg engine.InstallConfig) error {
	dir := "/etc/labosurf/engines/dnstt"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	log.Printf("✔ DNSTT : répertoire %s créé", dir)
	return nil
}

func (e *DNSTTEngineWrapper) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration DNSTT vide")
	}
	dir := "/etc/labosurf/engines/dnstt"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, cfg.JSON, 0o600); err != nil {
		return fmt.Errorf("écriture config DNSTT : %w", err)
	}
	log.Printf("✔ DNSTT : configuration écrite dans %s", e.configPath)
	return nil
}

func (e *DNSTTEngineWrapper) Start(ctx context.Context) error {
	cfg, err := loadDNSTTConfig(e.configPath)
	if err != nil {
		return err
	}
	srv, err := NewDNSTTServer(cfg)
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

func (e *DNSTTEngineWrapper) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	return <-e.done
}

func (e *DNSTTEngineWrapper) Stop() error {
	if e.cancel != nil {
		e.cancel()
	}
	if e.server != nil {
		return e.server.Close()
	}
	return nil
}

func (e *DNSTTEngineWrapper) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

func (e *DNSTTEngineWrapper) Status() engine.EngineStatus {
	if e.server == nil {
		return engine.EngineStatus{Installed: true}
	}
	return engine.EngineStatus{Installed: true, Running: true}
}

func (e *DNSTTEngineWrapper) HealthCheck() error {
	if e.server == nil {
		return fmt.Errorf("serveur dnstt non démarré")
	}
	return nil
}

func (e *DNSTTEngineWrapper) Logs(lines int) ([]string, error) {
	return []string{"dnstt Engine : logging via stdout/stderr du service systemd"}, nil
}

func (e *DNSTTEngineWrapper) Update() error {
	return nil
}

func (e *DNSTTEngineWrapper) Uninstall() error {
	_ = e.Stop()
	_ = os.Remove(e.configPath)
	return nil
}

func init() {
	engine.Register(engineName, New)
}
