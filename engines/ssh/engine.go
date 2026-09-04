// Package ssh fournit le moteur SSH natif de la plateforme LABOSURF PRO.
//
// Le serveur SSH est implémenté directement en Go (golang.org/x/crypto/ssh).
// Il authentifie les comptes du store central via clés Ed25519 et fournit
// un accès shell complet, sans aucune dépendance à un binaire tiers.
package ssh

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"labosurf/internal/engine"
)

const (
	name      = "ssh"
	desc      = "Serveur SSH natif : accès shell par clé Ed25519, comptes gérés depuis le store central."
	version   = "1.0.0"
)

type SSHEngine struct {
	configPath string
	server     *Server
	cancel     context.CancelFunc
	done       chan error
}

func New() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/ssh/config.json"
	if p := os.Getenv("LABOSURF_SSH_CONFIG"); p != "" {
		cfgPath = p
	}
	return &SSHEngine{configPath: cfgPath}, nil
}

func (e *SSHEngine) Name() string        { return name }
func (e *SSHEngine) Version() string      { return version }
func (e *SSHEngine) Description() string  { return desc }

func (e *SSHEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	if err := ensureSSHDir(""); err != nil {
		return fmt.Errorf("création répertoire SSH : %w", err)
	}
	return nil
}

func (e *SSHEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration SSH vide")
	}
	if err := ensureSSHDir(""); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, cfg.JSON, 0o600); err != nil {
		return fmt.Errorf("écriture config SSH : %w", err)
	}
	var sshCfg SSHConfig
	if err := json.Unmarshal(cfg.JSON, &sshCfg); err != nil {
		return err
	}
	akPath := authorizedKeysPath(sshCfg.Dir)
	if err := os.WriteFile(akPath, sshCfg.AuthorizedKeysBytes(), 0o600); err != nil {
		return fmt.Errorf("écriture authorized_keys : %w", err)
	}
	log.Printf("✔ SSH : %d clés autorisées écrites dans %s", len(sshCfg.Users), akPath)
	return nil
}

func (e *SSHEngine) Start(ctx context.Context) error {
	cfg, err := loadSSHConfig(e.configPath)
	if err != nil {
		return err
	}
	srv, err := NewServer(cfg)
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

func (e *SSHEngine) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	return <-e.done
}

func (e *SSHEngine) Stop() error {
	if e.cancel != nil {
		e.cancel()
	}
	if e.server != nil {
		return e.server.Close()
	}
	return nil
}

func (e *SSHEngine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

func (e *SSHEngine) Status() engine.EngineStatus {
	if e.server == nil {
		return engine.EngineStatus{Installed: true}
	}
	return engine.EngineStatus{Installed: true, Running: true}
}

func (e *SSHEngine) HealthCheck() error {
	if e.server == nil {
		return fmt.Errorf("serveur SSH non démarré")
	}
	return nil
}

func (e *SSHEngine) Logs(lines int) ([]string, error) {
	return []string{"SSH Engine : logging via stdout/stderr du service systemd"}, nil
}

func (e *SSHEngine) Update() error {
	return nil
}

func (e *SSHEngine) Uninstall() error {
	_ = e.Stop()
	_ = os.Remove(e.configPath)
	return nil
}

func init() {
	engine.Register(name, New)
}
