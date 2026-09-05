package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
}

// UDPEngineFactory fabrique des instances du moteur UDP.
func UDPEngineFactory() (engine.Engine, error) {
	return &UDPServerEngine{
		configPath: defaultConfigPath(),
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

// Install déploie le moteur sur le système. Pour l'instant, l'installation
// système complète (binaire, service systemd, réseau) est gérée par
// labosurf-pro.sh ; cette méthode vérifie la présence de la configuration.
func (e *UDPServerEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	// TODO(migration) : intégrer ici la logique de l'installeur
	// (téléchargement binaire, SHA-256, service systemd, réseau).
	if _, err := os.Stat(e.configPath); os.IsNotExist(err) {
		return fmt.Errorf("configuration introuvable (%s) : lancez labosurf-pro.sh", e.configPath)
	}
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

// Start démarre le moteur UDP (bloquant jusqu'à l'arrêt).
func (e *UDPServerEngine) Start(ctx context.Context) error {
	config, err := loadConfig(e.configPath)
	if err != nil {
		return fmt.Errorf("erreur de configuration : %w", err)
	}

	// Charger le store central pour la persistance quota.
	st, err := store.LoadStore(store.StorePath())
	if err != nil {
		return fmt.Errorf("chargement store pour quota : %w", err)
	}
	e.store = st

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

// Status retourne l'état courant du moteur.
func (e *UDPServerEngine) Status() engine.EngineStatus {
	if e.server == nil {
		return engine.EngineStatus{Installed: true}
	}
	return engine.EngineStatus{
		Installed: true,
		Running:   true,
	}
}

// HealthCheck vérifie que le moteur est opérationnel.
func (e *UDPServerEngine) HealthCheck() error {
	if e.server == nil {
		return fmt.Errorf("moteur UDP non démarré")
	}
	return nil
}

// Logs retourne les dernières lignes de journal du moteur.
func (e *UDPServerEngine) Logs(lines int) ([]string, error) {
	return nil, fmt.Errorf("journal non implémenté")
}

// Update met à jour le moteur vers la dernière version.
func (e *UDPServerEngine) Update() error {
	return fmt.Errorf("mise à jour à faire via labosurf update")
}

// Uninstall désinstalle le moteur du système.
func (e *UDPServerEngine) Uninstall() error {
	_ = os.Remove(e.configPath)
	return nil
}

func init() {
	// Enregistre le moteur UDP dans le registre global de la plateforme.
	engine.Register("udp", UDPEngineFactory)
}
