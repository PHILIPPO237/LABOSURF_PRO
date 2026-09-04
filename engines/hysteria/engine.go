// Package hysteria fournit le moteur Hysteria natif de la plateforme LABOSURF PRO.
//
// Relais UDP haute vitesse implémenté directement en Go : protocole
// authentifié par mot de passe, sessions suivies, sans binaire tiers.
package hysteria

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"labosurf/internal/engine"
)

const (
	engineName = "hysteria"
	engineDesc = "Serveur de relais UDP natif haute vitesse, authentifié par mot de passe. Sans binaire tiers."
	engineVer  = "1.0.0"
)

type HysteriaEngineWrapper struct {
	configPath string
	server     *HysteriaServer
	cancel     context.CancelFunc
	done       chan error
}

func New() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/hysteria/config.json"
	if p := os.Getenv("LABOSURF_HYSTERIA_CONFIG"); p != "" {
		cfgPath = p
	}
	return &HysteriaEngineWrapper{configPath: cfgPath}, nil
}

func (e *HysteriaEngineWrapper) Name() string        { return engineName }
func (e *HysteriaEngineWrapper) Version() string      { return engineVer }
func (e *HysteriaEngineWrapper) Description() string  { return engineDesc }

func (e *HysteriaEngineWrapper) Install(ctx context.Context, cfg engine.InstallConfig) error {
	dir := "/etc/labosurf/engines/hysteria"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	log.Printf("✔ Hysteria : répertoire %s créé", dir)
	return nil
}

func (e *HysteriaEngineWrapper) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration Hysteria vide")
	}

	var raw map[string]any
	if err := json.Unmarshal(cfg.JSON, &raw); err != nil {
		return err
	}

	port := 8443
	if p, ok := raw["port"].(float64); ok && int(p) > 0 {
		port = int(p)
	}

	var obfs string
	if o, ok := raw["obfs"].(string); ok {
		obfs = o
	}
	if obfs == "" {
		obfs = "labosurf"
	}

	var users []HysteriaUser
	if auth, ok := raw["auth"].(map[string]any); ok {
		if userpass, ok := auth["userpass"].(map[string]any); ok {
			for name, pw := range userpass {
				if pws, ok := pw.(string); ok && pws != "" {
					users = append(users, HysteriaUser{Name: name, Password: pws, Enabled: true})
				}
			}
		}
		if pw, ok := auth["password"].(string); ok && pw != "" {
			users = append(users, HysteriaUser{Name: "default", Password: pw, Enabled: true})
		}
	}
	if usersList, ok := raw["users"].([]any); ok {
		for _, u := range usersList {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			hu := HysteriaUser{Enabled: true}
			if name, ok := um["name"].(string); ok {
				hu.Name = name
			}
			if pw, ok := um["password"].(string); ok {
				hu.Password = pw
			}
			if enabled, ok := um["enabled"].(bool); ok {
				hu.Enabled = enabled
			}
			users = append(users, hu)
		}
	}

	cfgJSON := HysteriaConfig{
		Port:  port,
		Obfs:  obfs,
		Users: users,
	}

	out, err := json.MarshalIndent(cfgJSON, "", "  ")
	if err != nil {
		return err
	}

	dir := "/etc/labosurf/engines/hysteria"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, out, 0o600); err != nil {
		return fmt.Errorf("écriture config Hysteria : %w", err)
	}
	log.Printf("✔ Hysteria : configuration écrite (%d utilisateurs, port %d)", len(cfgJSON.Users), cfgJSON.Port)
	return nil
}

func (e *HysteriaEngineWrapper) Start(ctx context.Context) error {
	cfg, err := loadHysteriaConfig(e.configPath)
	if err != nil {
		return err
	}
	srv, err := NewHysteriaServer(cfg)
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

func (e *HysteriaEngineWrapper) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	return <-e.done
}

func (e *HysteriaEngineWrapper) Stop() error {
	if e.cancel != nil {
		e.cancel()
	}
	if e.server != nil {
		return e.server.Close()
	}
	return nil
}

func (e *HysteriaEngineWrapper) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

func (e *HysteriaEngineWrapper) Status() engine.EngineStatus {
	if e.server == nil {
		return engine.EngineStatus{Installed: true}
	}
	return engine.EngineStatus{Installed: true, Running: true}
}

func (e *HysteriaEngineWrapper) HealthCheck() error {
	if e.server == nil {
		return fmt.Errorf("serveur Hysteria non démarré")
	}
	return nil
}

func (e *HysteriaEngineWrapper) Logs(lines int) ([]string, error) {
	return []string{"Hysteria Engine : logging via stdout/stderr du service systemd"}, nil
}

func (e *HysteriaEngineWrapper) Update() error {
	return nil
}

func (e *HysteriaEngineWrapper) Uninstall() error {
	_ = e.Stop()
	_ = os.Remove(e.configPath)
	return nil
}

func init() {
	engine.Register(engineName, New)
}
