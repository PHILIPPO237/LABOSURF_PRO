package main

import (
	"testing"

	"labosurf/internal/engine"
)

// TestUDPEngineImplementsInterface vérifie que l'adaptateur UDP Engine
// respecte bien le contrat de la plateforme (interface engine.Engine).
func TestUDPEngineImplementsInterface(t *testing.T) {
	var _ engine.Engine = (*UDPServerEngine)(nil)
}

// TestUDPEngineIdentity vérifie l'identité du moteur.
func TestUDPEngineIdentity(t *testing.T) {
	e, err := UDPEngineFactory()
	if err != nil {
		t.Fatalf("factory a retourné une erreur : %v", err)
	}

	if e.Name() != "udp" {
		t.Fatalf("nom attendu udp, obtenu %s", e.Name())
	}

	if e.Version() == "" {
		t.Fatal("version vide")
	}

	if e.Description() == "" {
		t.Fatal("description vide")
	}
}

// TestUDPEngineStatusNotStarted vérifie l'état avant démarrage.
func TestUDPEngineStatusNotStarted(t *testing.T) {
	e, err := UDPEngineFactory()
	if err != nil {
		t.Fatalf("factory error : %v", err)
	}

	st := e.Status()
	if !st.Installed {
		t.Fatal("moteur devrait être considéré comme installé")
	}
	if st.Running {
		t.Fatal("moteur ne devrait pas être running avant Start")
	}
}

// TestUDPEngineRegister vérifie que nom "udp" est enregistré dans le registre
// global une fois que l'init() du paquet a tourné.
func TestUDPEngineRegister(t *testing.T) {
	if !engine.Has("udp") {
		t.Fatal("le moteur udp n'est pas enregistré dans le registre")
	}
}
