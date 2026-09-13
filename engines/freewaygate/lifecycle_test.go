package freewaygate

// Tests du cycle de vie REEL de freeway-gate avec un faux binaire compilé à
// la volée (aucune dépendance externe, cross-platform) : il lit -config,
// bind réellement l'adresse listen, sert /health en HTTP, et accepte d'être
// tué. Ces tests prouvent, comme pour hysteria2/tuic :
//   - Start est NON bloquant (rend la main, ne fige pas la CLI) ;
//   - Endpoint() ne ment pas : il exige une vraie connexion TCP réussie ;
//   - une sortie immédiate du binaire (port déjà pris par exemple) remonte
//     comme une erreur explicite de Start, jamais un silence.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"labosurf/internal/engine"
)

const fakeGateSrc = `package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
)

func main() {
	cfgPath := flag.String("config", "", "chemin de configuration JSON")
	flag.Parse()
	data, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var c struct {
		Listen string ` + "`json:\"listen\"`" + `
	}
	if err := json.Unmarshal(data, &c); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind %s: %v\n", c.Listen, err)
		os.Exit(4)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	_ = srv.Close()
}
`

// fakeGateOnce compile le faux binaire une seule fois pour tout le paquet
// (le `go build` est coûteux) et le réutilise entre les tests.
var fakeGateOnce struct {
	sync.Once
	path string
	err  error
}

// buildFakeGate retourne le chemin du faux binaire freeway-gate, compilé une
// seule fois (module Go autonome, stdlib uniquement — build hors réseau).
func buildFakeGate(t *testing.T) string {
	t.Helper()
	fakeGateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakegate-src-*")
		if err != nil {
			fakeGateOnce.err = err
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fakeGateSrc), 0o600); err != nil {
			fakeGateOnce.err = err
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fakegate\n\ngo 1.22\n"), 0o600); err != nil {
			fakeGateOnce.err = err
			return
		}
		out := filepath.Join(os.TempDir(), "fake-gate-under-test")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		_ = os.Remove(out)
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Dir = dir
		if o, err := cmd.CombinedOutput(); err != nil {
			fakeGateOnce.err = fmt.Errorf("compilation du faux binaire : %v\n%s", err, o)
			return
		}
		fakeGateOnce.path = out
	})
	if fakeGateOnce.err != nil {
		t.Fatalf("%v", fakeGateOnce.err)
	}
	return fakeGateOnce.path
}

// freePort retourne un port TCP libre sur 127.0.0.1.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("port libre : %v", err)
	}
	defer ln.Close()
	_, ps, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split port : %v", err)
	}
	n, err := strconv.Atoi(ps)
	if err != nil {
		t.Fatalf("atoi port : %v", err)
	}
	return n
}

// newTestEngine configure un moteur freeway-gate pointé sur un faux binaire
// et une config temporelle, et retourne l'instance typée.
func newTestEngine(t *testing.T, fake string) *FreewayGateEngine {
	t.Helper()
	e, err := New()
	if err != nil {
		t.Fatalf("New : %v", err)
	}
	f := e.(*FreewayGateEngine)
	f.binaryPath = fake
	f.configPath = filepath.Join(t.TempDir(), "config.json")
	return f
}

func TestFreewayGateConfigureRejectsInvalidJSON(t *testing.T) {
	f := newTestEngine(t, "unused")
	if err := f.Configure(context.Background(), engine.EngineConfig{JSON: []byte("not json")}); err == nil {
		t.Fatal("configure a accepté un JSON invalide")
	}
}

func TestFreewayGateLifecycleWithFakeBinary(t *testing.T) {
	fake := buildFakeGate(t)
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	f := newTestEngine(t, fake)
	cfgJSON := fmt.Sprintf(`{"listen": %q, "default_operator": "mtn", "rate_limit_max": 100}`, addr)
	if err := f.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfgJSON)}); err != nil {
		t.Fatalf("Configure : %v", err)
	}

	// Start doit rendre la main rapidement (NON bloquant) et sans erreur.
	startCh := make(chan error, 1)
	go func() { startCh <- f.Start(context.Background()) }()
	select {
	case err := <-startCh:
		if err != nil {
			t.Fatalf("Start a échoué : %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start n'a pas rendu la main (bloquant)")
	}

	// Attendre que le process et son bind soient réellement up.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ep, ok := f.Endpoint(); ok {
			if ep.Addr == addr {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	ep, ok := f.Endpoint()
	if !ok {
		t.Fatalf("Endpoint non disponible après démarrage (moteur: addr=%s)", addr)
	}
	if ep.Network != "tcp" || ep.Addr != addr {
		t.Fatalf("Endpoint inattendu : %+v", ep)
	}

	if err := f.HealthCheck(); err != nil {
		t.Fatalf("HealthCheck échoué : %v", err)
	}

	st := f.Status()
	if !st.Running || st.PID <= 0 {
		t.Fatalf("Status attendu running avec PID, obtenu %+v", st)
	}

	if err := f.Stop(); err != nil {
		t.Fatalf("Stop : %v", err)
	}
	if _, ok := f.Endpoint(); ok {
		t.Fatal("Endpoint toujours déclaré après Stop")
	}
	if st := f.Status(); st.Running {
		t.Fatal("Status toujours running après Stop")
	}
}

func TestFreewayGateStartReportsImmediateExit(t *testing.T) {
	fake := buildFakeGate(t)

	// Adresse invalide : le faux binaire ne peut PAS bind (indépendant de la
	// plateforme — sur Windows, re-binder un port occupé réussit parfois, donc
	// l'occupation de port n'est pas un test fiable) et sort immédiatement.
	f := newTestEngine(t, fake)
	cfgJSON := `{"listen": "pas-une-adresse"}`
	if err := f.Configure(context.Background(), engine.EngineConfig{JSON: []byte(cfgJSON)}); err != nil {
		t.Fatalf("Configure : %v", err)
	}

	err := f.Start(context.Background())
	if err == nil {
		t.Fatal("Start a retourné nil alors que le binaire s'est arrêté immédiatement (adresse de bind invalide)")
	}
	st := f.Status()
	if st.Running {
		t.Fatalf("Status running pour un process mort : %+v", st)
	}
}
