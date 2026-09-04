// Package xray fournit le moteur de proxy natif de la plateforme LABOSURF PRO.
//
// Implémente directement en Go : VLESS (TCP), Trojan, VMess et Shadowsocks
// basique. Gère les comptes VLESS/Trojan depuis le store central via UUID
// et mots de passe.
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"labosurf/internal/engine"
)

const (
	engineName = "xray"
	engineDesc = "Serveur de proxy natif : VLESS, Trojan, VMess, Shadowsocks. Sans binaire tiers."
	engineVer  = "1.0.0"
)

type XrayEngineWrapper struct {
	configPath string
	server     *XrayServer
	cancel     context.CancelFunc
	done       chan error
}

func New() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/xray/config.json"
	if p := os.Getenv("LABOSURF_XRAY_CONFIG"); p != "" {
		cfgPath = p
	}
	return &XrayEngineWrapper{configPath: cfgPath}, nil
}

func (e *XrayEngineWrapper) Name() string        { return engineName }
func (e *XrayEngineWrapper) Version() string      { return engineVer }
func (e *XrayEngineWrapper) Description() string  { return engineDesc }

func (e *XrayEngineWrapper) Install(ctx context.Context, cfg engine.InstallConfig) error {
	dir := "/etc/labosurf/engines/xray"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	log.Printf("✔ Xray : répertoire %s créé", dir)
	return nil
}

func (e *XrayEngineWrapper) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration Xray vide")
	}

	var raw map[string]any
	if err := json.Unmarshal(cfg.JSON, &raw); err != nil {
		return err
	}

	port := 443
	if p, ok := raw["port"].(float64); ok && int(p) > 0 {
		port = int(p)
	}

	var users []XrayUser
	if inbounds, ok := raw["inbounds"].([]any); ok && len(inbounds) > 0 {
		if inbound, ok := inbounds[0].(map[string]any); ok {
			if settings, ok := inbound["settings"].(map[string]any); ok {
				if clients, ok := settings["clients"].([]any); ok {
					for _, c := range clients {
						cm, ok := c.(map[string]any)
						if !ok {
							continue
						}
						u := XrayUser{}
						if id, ok := cm["id"].(string); ok {
							u.ID = id
							u.Password = id
						}
						if pw, ok := cm["password"].(string); ok {
							u.Password = pw
						}
						if email, ok := cm["email"].(string); ok {
							u.Email = email
						}
						if flow, ok := cm["flow"].(string); ok {
							u.Flow = flow
						}
						if enabled, ok := cm["enabled"].(bool); ok {
							u.Enabled = enabled
						} else {
							u.Enabled = true
						}
						if u.Email == "" {
							u.Email = u.ID
						}
						users = append(users, u)
					}
				}
			}
		}
	}

	protocol := ProtocolVLESS
	if inbounds, ok := raw["inbounds"].([]any); ok && len(inbounds) > 0 {
		if inbound, ok := inbounds[0].(map[string]any); ok {
			if p, ok := inbound["protocol"].(string); ok {
				protocol = p
			}
		}
	}

	cfgJSON := XrayConfig{
		Port:     port,
		Protocol: protocol,
		Users:    users,
	}

	out, err := json.MarshalIndent(cfgJSON, "", "  ")
	if err != nil {
		return err
	}

	dir := "/etc/labosurf/engines/xray"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, out, 0o600); err != nil {
		return fmt.Errorf("écriture config Xray : %w", err)
	}
	log.Printf("✔ Xray : configuration écrite (%d utilisateurs, port %d, %s)", len(cfgJSON.Users), cfgJSON.Port, cfgJSON.Protocol)
	return nil
}

func (e *XrayEngineWrapper) Start(ctx context.Context) error {
	cfg, err := loadXrayConfig(e.configPath)
	if err != nil {
		return err
	}
	srv, err := NewXrayServer(cfg)
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

func (e *XrayEngineWrapper) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	return <-e.done
}

func (e *XrayEngineWrapper) Stop() error {
	if e.cancel != nil {
		e.cancel()
	}
	if e.server != nil {
		return e.server.Close()
	}
	return nil
}

func (e *XrayEngineWrapper) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

func (e *XrayEngineWrapper) Status() engine.EngineStatus {
	if e.server == nil {
		return engine.EngineStatus{Installed: true}
	}
	return engine.EngineStatus{Installed: true, Running: true}
}

func (e *XrayEngineWrapper) HealthCheck() error {
	if e.server == nil {
		return fmt.Errorf("serveur Xray non démarré")
	}
	return nil
}

func (e *XrayEngineWrapper) Logs(lines int) ([]string, error) {
	return []string{"Xray Engine : logging via stdout/stderr du service systemd"}, nil
}

func (e *XrayEngineWrapper) Update() error {
	return nil
}

func (e *XrayEngineWrapper) Uninstall() error {
	_ = e.Stop()
	_ = os.Remove(e.configPath)
	return nil
}

func init() {
	engine.Register(engineName, New)
}
