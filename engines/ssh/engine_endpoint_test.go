package ssh

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSSHEngineWrapperEndpointIsReal démontre, pour le moteur SSH,
// l'ensemble du cycle attendu de la Phase 3 (interface engine.Endpointer) :
// Start réel, obtention d'un endpoint réel (jamais un placeholder), preuve
// qu'il est réellement lié, Restart propre (depuis l'état démarré), puis
// Stop propre qui libère le port.
//
// Contrairement aux moteurs UDP natifs, le listener TCP de SSH est ouvert
// de façon asynchrone à l'intérieur de Run() (goroutine) : Endpoint() peut
// donc ne pas être prêt immédiatement après le retour de Start() — ce test
// interroge Endpoint() en boucle bornée, exactement comme le fait
// engineutil.waitForEndpoint dans le chaînage réel.
func TestSSHEngineWrapperEndpointIsReal(t *testing.T) {
	// loadSSHConfig substitue le port par défaut (22) quand Port<=0 :
	// passer par le wrapper (comme le fait réellement CompositeEngine)
	// exige donc un port explicite et réellement libre.
	cfgPath := filepath.Join(t.TempDir(), "ssh.json")
	cfg := SSHConfig{Port: freeTCPPort(t), Dir: t.TempDir()}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config : %v", err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}

	w := &SSHEngine{configPath: cfgPath}
	ctx := context.Background()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}

	ep := waitReadyEndpoint(t, w)
	if ep.Network != "tcp" {
		t.Fatalf("réseau attendu tcp, obtenu %q", ep.Network)
	}
	if ep.Addr == "" || ep.Addr == ":0" {
		t.Fatalf("endpoint fictif/non résolu détecté : %q", ep.Addr)
	}
	assertTCPPortBusy(t, ep.Addr)

	if err := w.Restart(ctx); err != nil {
		t.Fatalf("Restart : %v", err)
	}
	ep2 := waitReadyEndpoint(t, w)
	assertTCPPortBusy(t, ep2.Addr)

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop : %v", err)
	}
	if _, ready := w.Endpoint(); ready {
		t.Fatal("Endpoint() doit redevenir indisponible après Stop()")
	}
	assertTCPPortFree(t, ep2.Addr)
}

func waitReadyEndpoint(t *testing.T, w *SSHEngine) (ep struct {
	Network string
	Addr    string
}) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if e, ready := w.Endpoint(); ready {
			ep.Network, ep.Addr = e.Network, e.Addr
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Endpoint() jamais devenu prêt après Start()/Restart()")
	return
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sondage port TCP libre : %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func assertTCPPortBusy(t *testing.T, addr string) {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err == nil {
		l.Close()
		t.Fatalf("endpoint annoncé comme réel mais rebindable : %s n'était pas réellement occupé", addr)
	}
}

func assertTCPPortFree(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		l, err := net.Listen("tcp", addr)
		if err == nil {
			l.Close()
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("le port %s n'a pas été libéré après Stop() : %v", addr, lastErr)
}
