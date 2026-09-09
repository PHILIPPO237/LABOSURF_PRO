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
	"sync"

	"labosurf/internal/engine"
)

const (
	name      = "ssh"
	desc      = "Serveur SSH natif : accès shell par clé Ed25519, comptes gérés depuis le store central."
	version   = "1.0.0"
)

type SSHEngine struct {
	configPath string

	mu     sync.Mutex // protège server/cancel/done contre Start/Stop/Endpoint concurrents
	server *Server
	cancel context.CancelFunc
	done   chan error
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
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	e.mu.Lock()
	e.server = srv
	e.cancel = cancel
	e.done = done
	e.mu.Unlock()

	go func() {
		done <- srv.Run(runCtx)
	}()
	return nil
}

func (e *SSHEngine) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	return <-done
}

func (e *SSHEngine) Stop() error {
	e.mu.Lock()
	cancel := e.cancel
	srv := e.server
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if srv != nil {
		return srv.Close()
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
	e.mu.Lock()
	srv := e.server
	e.mu.Unlock()
	if srv == nil {
		return engine.EngineStatus{Installed: true}
	}
	st := engine.EngineStatus{Installed: true, Running: true}
	if addr, ok := srv.AddrOk(); ok {
		st.ListenAddr = addr.String()
	}
	return st
}

func (e *SSHEngine) HealthCheck() error {
	e.mu.Lock()
	srv := e.server
	e.mu.Unlock()
	if srv == nil {
		return fmt.Errorf("serveur SSH non démarré")
	}
	return nil
}

// Endpoint implémente engine.Endpointer : retourne l'adresse TCP réelle
// liée par le serveur SSH, jamais un placeholder. Contrairement aux
// moteurs UDP (hysteria/dnstt/slowdns/udp), le listener SSH est ouvert de
// façon asynchrone à l'intérieur de Run() (goroutine) : juste après
// Start(), il peut ne pas être encore prêt — l'appelant qui chaîne des
// moteurs doit réessayer (voir engineutil.waitForEndpoint) plutôt que de
// supposer une disponibilité immédiate.
func (e *SSHEngine) Endpoint() (engine.Endpoint, bool) {
	e.mu.Lock()
	srv := e.server
	e.mu.Unlock()
	if srv == nil {
		return engine.Endpoint{}, false
	}
	addr, ok := srv.AddrOk()
	if !ok {
		return engine.Endpoint{}, false
	}
	return engine.Endpoint{Network: "tcp", Addr: addr.String()}, true
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
