package engine

import (
	"context"
	"fmt"
)

// Manager pilote les moteurs enregistrés dans le registre global.
type Manager struct{}

// NewManager retourne un Manager prêt à l'emploi.
func NewManager() *Manager {
	return &Manager{}
}

// Install installe le moteur nommé.
func (m *Manager) Install(ctx context.Context, name string, cfg InstallConfig) error {
	e, err := Get(name)
	if err != nil {
		return err
	}
	return e.Install(ctx, cfg)
}

// Configure applique la configuration au moteur nommé.
func (m *Manager) Configure(ctx context.Context, name string, cfg EngineConfig) error {
	e, err := Get(name)
	if err != nil {
		return err
	}
	return e.Configure(ctx, cfg)
}

// Start démarre le moteur nommé (bloquant).
func (m *Manager) Start(ctx context.Context, name string) error {
	e, err := Get(name)
	if err != nil {
		return err
	}
	return e.Start(ctx)
}

// Stop arrête le moteur nommé.
func (m *Manager) Stop(name string) error {
	e, err := Get(name)
	if err != nil {
		return err
	}
	return e.Stop()
}

// Restart redémarre le moteur nommé.
func (m *Manager) Restart(ctx context.Context, name string) error {
	e, err := Get(name)
	if err != nil {
		return err
	}
	return e.Restart(ctx)
}

// Status retourne l'état du moteur nommé.
func (m *Manager) Status(name string) (EngineStatus, error) {
	e, err := Get(name)
	if err != nil {
		return EngineStatus{}, err
	}
	return e.Status(), nil
}

// HealthCheck vérifie l'état de santé du moteur nommé.
func (m *Manager) HealthCheck(name string) error {
	e, err := Get(name)
	if err != nil {
		return err
	}
	return e.HealthCheck()
}

// Logs retourne les dernières lignes de journal du moteur nommé.
func (m *Manager) Logs(name string, lines int) ([]string, error) {
	e, err := Get(name)
	if err != nil {
		return nil, err
	}
	return e.Logs(lines)
}

// List retourne la liste des moteurs enregistrés.
func (m *Manager) List() []string {
	return Names()
}

// Summary produit une description humaine de l'état global des moteurs.
func (m *Manager) Summary() string {
	names := Names()
	if len(names) == 0 {
		return "aucun moteur enregistré"
	}
	return fmt.Sprintf("%d moteur(s) : %v", len(names), names)
}
