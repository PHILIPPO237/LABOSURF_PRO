package engine

import (
	"context"
	"errors"
	"testing"
)

// mockEngine est une implémentation minimale de Engine pour les tests.
type mockEngine struct{}

func (m *mockEngine) Name() string                    { return "mock" }
func (m *mockEngine) Version() string                 { return "0.0.1" }
func (m *mockEngine) Description() string             { return "moteur de test" }
func (m *mockEngine) Install(context.Context, InstallConfig) error { return nil }
func (m *mockEngine) Configure(context.Context, EngineConfig) error { return nil }
func (m *mockEngine) Start(context.Context) error     { return nil }
func (m *mockEngine) RunForeground(context.Context) error { return nil }
func (m *mockEngine) Stop() error                     { return nil }
func (m *mockEngine) Restart(context.Context) error   { return nil }
func (m *mockEngine) Status() EngineStatus            { return EngineStatus{Installed: true} }
func (m *mockEngine) HealthCheck() error              { return nil }
func (m *mockEngine) Logs(int) ([]string, error)      { return nil, nil }
func (m *mockEngine) Update() error                   { return nil }
func (m *mockEngine) Uninstall() error                { return nil }

func TestRegistryRegisterGet(t *testing.T) {
	// Utilise une instance locale pour isoler le test.
	local := make(map[string]Factory)
	factory := func() (Engine, error) { return &mockEngine{}, nil }
	local["mock"] = factory

	f, ok := local["mock"]
	if !ok {
		t.Fatal("moteur mock non enregistré")
	}

	e, err := f()
	if err != nil {
		t.Fatalf("factory a retourné une erreur : %v", err)
	}
	if e.Name() != "mock" {
		t.Fatalf("nom attendu mock, obtenu %s", e.Name())
	}
}

func TestRegistryFactoryError(t *testing.T) {
	local := make(map[string]Factory)

	wantErr := errors.New("erreur de construction")
	local["bad"] = func() (Engine, error) { return nil, wantErr }

	_, err := local["bad"]()
	if !errors.Is(err, wantErr) {
		t.Fatalf("erreur attendue %v, obtenue %v", wantErr, err)
	}
}
