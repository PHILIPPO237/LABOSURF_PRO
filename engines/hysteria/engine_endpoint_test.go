package hysteria

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHysteriaEngineWrapperEndpointIsReal démontre, pour le moteur
// Hysteria, l'ensemble du cycle attendu de la Phase 3 (interface
// engine.Endpointer) : Start réel, obtention d'un endpoint réel (jamais un
// placeholder), preuve qu'il est réellement lié (pas juste une chaîne
// plausible), Stop propre qui libère le port, puis Restart propre qui
// relie un nouvel endpoint réel.
//
// La preuve que l'endpoint est réel — plutôt que de simplement faire
// confiance à la valeur retournée — consiste à tenter de re-écouter sur
// EXACTEMENT la même adresse : ça doit échouer (port réellement occupé)
// pendant que le moteur tourne, et réussir une fois qu'il est arrêté (le
// port a été réellement libéré par Close()).
func TestHysteriaEngineWrapperEndpointIsReal(t *testing.T) {
	// loadHysteriaConfig substitue le port par défaut (8443) quand
	// Port<=0 (comportement voulu en production, pour ne pas démarrer
	// silencieusement sur le port 0) : contrairement à un appel direct à
	// NewHysteriaServer, passer par le wrapper (comme le fait réellement
	// CompositeEngine) exige donc un port explicite. On en choisit un
	// réellement libre au moment du test plutôt qu'un port fixe, pour
	// éviter toute collision avec un autre test/processus.
	cfgPath := filepath.Join(t.TempDir(), "hysteria.json")
	cfg := HysteriaConfig{Port: freeUDPPort(t), Obfs: "test-obfs", Backend: "127.0.0.1:0"}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config : %v", err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}

	w := &HysteriaEngineWrapper{configPath: cfgPath}
	ctx := context.Background()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}

	ep, ready := w.Endpoint()
	if !ready {
		t.Fatal("Endpoint() pas prêt juste après Start() alors que le socket UDP est ouvert de façon synchrone dans NewHysteriaServer")
	}
	if ep.Network != "udp" {
		t.Fatalf("réseau attendu udp, obtenu %q", ep.Network)
	}
	if ep.Addr == "" || ep.Addr == "127.0.0.1:0" || ep.Addr == ":0" {
		t.Fatalf("endpoint fictif/non résolu détecté : %q", ep.Addr)
	}
	assertUDPPortBusy(t, ep.Addr)

	// Restart propre depuis l'état démarré (le cas réel : CompositeEngine
	// et le menu appellent Restart() sur un moteur en cours d'exécution,
	// jamais après un Stop() manuel préalable) : le port doit rester
	// réellement occupé après coup, avec un endpoint toujours réel.
	if err := w.Restart(ctx); err != nil {
		t.Fatalf("Restart : %v", err)
	}
	ep2, ready := w.Endpoint()
	if !ready {
		t.Fatal("Endpoint() pas prêt après Restart()")
	}
	assertUDPPortBusy(t, ep2.Addr)

	// Stop propre pour finir : le port doit être réellement libéré.
	if err := w.Stop(); err != nil {
		t.Fatalf("Stop : %v", err)
	}
	if _, ready := w.Endpoint(); ready {
		t.Fatal("Endpoint() doit redevenir indisponible après Stop()")
	}
	assertUDPPortFree(t, ep2.Addr)
}

// freeUDPPort trouve un port UDP réellement libre au moment de l'appel, en
// laissant l'OS en choisir un (port 0) puis en le refermant aussitôt.
// Fenêtre de compétition théorique acceptée pour un test isolé.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatalf("sondage port UDP libre : %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()
	return port
}

func assertUDPPortBusy(t *testing.T, addr string) {
	t.Helper()
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("résolution %s : %v", addr, err)
	}
	l, err := net.ListenUDP("udp", udpAddr)
	if err == nil {
		l.Close()
		t.Fatalf("endpoint annoncé comme réel mais rebindable : %s n'était pas réellement occupé", addr)
	}
}

func assertUDPPortFree(t *testing.T, addr string) {
	t.Helper()
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("résolution %s : %v", addr, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		l, err := net.ListenUDP("udp", udpAddr)
		if err == nil {
			l.Close()
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("le port %s n'a pas été libéré après Stop() : %v", addr, lastErr)
}
