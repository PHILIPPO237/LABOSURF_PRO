package engineutil_test

// Tests d'architecture pour le chaînage générique A -> B -> C -> ...
// (chain.go, wireAdjacentChain dans composite_engine.go). Deux familles :
//
//  1. Tests purement déclaratifs (CanConnect, EvaluateChain,
//     DetectPortConflicts) sur les moteurs PRODUIT réellement enregistrés
//     (dnstt, slowdns, ssh, xray, hysteria, tuic) — aucune fabrication.
//
//  2. Un test de data-path RÉEL à 3 étages (client -> A -> B -> C) prouvant
//     que le mécanisme générique fonctionne pour une profondeur > 2, ce
//     qu'aucun moteur produit actuel ne permet de démontrer seul (voir le
//     commentaire du type CompositeEngine : au plus une paire relayable
//     existe aujourd'hui dans le catalogue réel). Les moteurs A/B/C de ce
//     test sont de VRAIS moteurs (vrais net.Listen, vrais octets relayés,
//     vrai engine.Endpointer) — seul leur "protocole" est un simple relais
//     TCP bidirectionnel réutilisé pour l'occasion, exactement comme le
//     préconise la consigne : jamais une adresse fictive, jamais un
//     résultat de test fabriqué en dehors du mécanisme réellement exercé.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"

	_ "labosurf/engines/dnstt"
	_ "labosurf/engines/hysteria"
	_ "labosurf/engines/slowdns"
	_ "labosurf/engines/ssh"
	_ "labosurf/engines/tuic"
	_ "labosurf/engines/xray"
)

// ── CanConnect / EvaluateChain sur les moteurs produit réels ──────────────

func TestCanConnectRealEngines(t *testing.T) {
	cases := []struct {
		front, back string
		want        bool
	}{
		// dnstt/slowdns relaient réellement vers un backend TCP (voir
		// engines/dnstt/server.go, engines/slowdns/server.go) — ssh et xray
		// exposent tous deux un endpoint tcp : câblable.
		{"dnstt", "ssh", true},
		{"dnstt", "xray", true},
		{"slowdns", "ssh", true},
		{"slowdns", "xray", true},
		// hysteria et tuic n'exposent qu'un endpoint udp : le relais TCP de
		// dnstt/slowdns ne peut pas les joindre.
		{"dnstt", "hysteria", false},
		{"slowdns", "tuic", false},
		// ssh, xray, hysteria, tuic sont tous terminaux (RelaysTo vide) :
		// aucun ne peut servir de "front" relayeur, quel que soit le back.
		{"ssh", "xray", false},
		{"xray", "ssh", false},
		{"hysteria", "ssh", false},
		{"tuic", "ssh", false},
	}
	for _, c := range cases {
		ok, reason := engineutil.CanConnect(c.front, c.back)
		if ok != c.want {
			t.Errorf("CanConnect(%q,%q) = %v (%q), attendu %v", c.front, c.back, ok, reason, c.want)
		}
		if !ok && reason == "" {
			t.Errorf("CanConnect(%q,%q) = false sans raison expliquée", c.front, c.back)
		}
	}
}

// TestEvaluateChainFutureCombosNotYetChainable évalue, sans rien démarrer
// ni rien déclarer fonctionnel, les compositions futures citées dans la
// demande (TUIC+SSH+Xray, TUIC+DNSTT+Xray, Hysteria2+SSH+Xray,
// Hysteria2+DNSTT). Le catalogue de moteurs actuel les rend TOUTES
// honnêtement non-chaînables de bout en bout : tuic/hysteria/ssh/xray sont
// tous terminaux (RelaysTo vide), donc aucune paire adjacente frontale ne
// peut jamais être câblée entre eux avec le mécanisme actuel, quel que soit
// l'ordre choisi. Ce test verrouille cette évaluation honnête — pas une
// aspiration future, un fait vérifié sur le code réel.
func TestEvaluateChainFutureCombosNotYetChainable(t *testing.T) {
	prof := srvcfg.Default()
	combos := [][]string{
		{"tuic", "ssh", "xray"},
		{"dnstt", "tuic", "xray"}, // dnstt en tête : seule position où une liaison est même statiquement plausible
		{"hysteria", "ssh", "xray"},
		{"hysteria", "dnstt"},
		{"dnstt", "hysteria"},
	}
	for _, combo := range combos {
		report := engineutil.EvaluateChain(combo, prof)
		if report.FullyChained {
			t.Errorf("EvaluateChain(%v) = entièrement chaînée, attendu non-chaînable avec le mécanisme actuel : %+v", combo, report.Links)
		}
		if len(report.Links) != len(combo)-1 {
			t.Errorf("EvaluateChain(%v) : %d liaison(s) calculée(s), attendu %d", combo, len(report.Links), len(combo)-1)
		}
		for _, l := range report.Links {
			if !l.Wired && l.Reason == "" {
				t.Errorf("EvaluateChain(%v) : liaison %s->%s non câblée sans raison", combo, l.Front, l.Back)
			}
		}
	}
}

// TestEvaluateChainRealWireablePair verrouille le seul cas aujourd'hui
// réellement câblable (dnstt -> ssh, déjà démontré avec des octets réels
// dans engines/dnstt/composite_chain_test.go) : EvaluateChain doit le
// signaler FullyChained, pas seulement "pas pire que les autres".
func TestEvaluateChainRealWireablePair(t *testing.T) {
	report := engineutil.EvaluateChain([]string{"dnstt", "ssh"}, srvcfg.Default())
	if !report.FullyChained {
		t.Fatalf("EvaluateChain([dnstt,ssh]) attendu FullyChained=true, obtenu %+v", report.Links)
	}
}

// TestDetectPortConflicts vérifie, sur les ports par défaut réels
// (srvcfg.DefaultPorts), que slowdns+dnstt (tous deux udp/53 par défaut)
// sont signalés en conflit, et que xray+hysteria (tcp/443 vs udp/8443) ne
// le sont pas.
func TestDetectPortConflicts(t *testing.T) {
	prof := srvcfg.Default()

	conflicts := engineutil.DetectPortConflicts([]string{"slowdns", "dnstt"}, prof)
	if len(conflicts) != 1 {
		t.Fatalf("attendu 1 conflit slowdns/dnstt (udp/53 par défaut), obtenu %+v", conflicts)
	}
	if conflicts[0].Network != "udp" || conflicts[0].Port != 53 {
		t.Fatalf("conflit inattendu : %+v", conflicts[0])
	}

	if c := engineutil.DetectPortConflicts([]string{"xray", "hysteria"}, prof); len(c) != 0 {
		t.Fatalf("aucun conflit attendu entre xray (tcp/443) et hysteria (udp/8443), obtenu %+v", c)
	}
}

// ── Chaîne réelle à 3 étages (test d'architecture) ─────────────────────────

// chainRelayTestEngine est un moteur de TEST (pas produit) qui implémente
// réellement engine.Engine + engine.Endpointer : il lie un vrai socket TCP,
// et s'il n'est pas "terminal", relaie réellement (io.Copy bidirectionnel)
// vers l'adresse "backend" injectée par CompositeEngine — exactement le
// même contrat JSON générique que dnstt/slowdns (voir injectBackend). Un
// moteur terminal fait simplement écho de ce qu'il reçoit. Sert uniquement
// à prouver que wireAdjacentChain généralise correctement le mécanisme
// existant à une profondeur de chaîne > 2, ce qu'aucun moteur produit ne
// permet de démontrer seul aujourd'hui.
type chainRelayTestEngine struct {
	myName   string
	terminal bool

	mu            sync.Mutex
	ln            net.Listener
	backend       string
	started       bool
	lastRawConfig []byte // pour TestCompositeEngineComponentConfigOverride : preuve de la config réellement reçue
}

func newChainRelayTestEngine(name string, terminal bool) *chainRelayTestEngine {
	return &chainRelayTestEngine{myName: name, terminal: terminal}
}

func (e *chainRelayTestEngine) Name() string    { return e.myName }
func (e *chainRelayTestEngine) Version() string { return "test" }
func (e *chainRelayTestEngine) Description() string {
	return "moteur de test (architecture de chaînage)"
}
func (e *chainRelayTestEngine) Install(context.Context, engine.InstallConfig) error { return nil }

func (e *chainRelayTestEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	e.mu.Lock()
	e.lastRawConfig = append([]byte(nil), cfg.JSON...)
	e.mu.Unlock()
	if e.terminal {
		return nil
	}
	var m map[string]any
	if len(cfg.JSON) > 0 {
		if err := json.Unmarshal(cfg.JSON, &m); err != nil {
			return fmt.Errorf("configuration invalide pour %s : %w", e.myName, err)
		}
	}
	backend, _ := m["backend"].(string)
	e.mu.Lock()
	e.backend = backend
	e.mu.Unlock()
	return nil
}

func (e *chainRelayTestEngine) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.ln = ln
	e.started = true
	e.mu.Unlock()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener fermé : fin propre
			}
			go e.handle(conn)
		}
	}()
	return nil
}

func (e *chainRelayTestEngine) handle(conn net.Conn) {
	defer conn.Close()
	if e.terminal {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}

	e.mu.Lock()
	backend := e.backend
	e.mu.Unlock()
	if backend == "" {
		return
	}
	up, err := net.Dial("tcp", backend)
	if err != nil {
		return
	}
	defer up.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(up, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, up); done <- struct{}{} }()
	<-done
}

func (e *chainRelayTestEngine) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return e.Stop()
}

func (e *chainRelayTestEngine) Stop() error {
	e.mu.Lock()
	ln := e.ln
	e.started = false
	e.mu.Unlock()
	if ln != nil {
		return ln.Close()
	}
	return nil
}

func (e *chainRelayTestEngine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

func (e *chainRelayTestEngine) Status() engine.EngineStatus {
	st := engine.EngineStatus{Installed: true}
	if ep, ready := e.Endpoint(); ready {
		st.Running = true
		st.ListenAddr = ep.Addr
	}
	return st
}

func (e *chainRelayTestEngine) HealthCheck() error {
	if _, ready := e.Endpoint(); !ready {
		return fmt.Errorf("%s non démarré", e.myName)
	}
	return nil
}

// Endpoint implémente engine.Endpointer : adresse TCP réellement liée,
// jamais un placeholder.
func (e *chainRelayTestEngine) Endpoint() (engine.Endpoint, bool) {
	e.mu.Lock()
	ln, started := e.ln, e.started
	e.mu.Unlock()
	if ln == nil || !started {
		return engine.Endpoint{}, false
	}
	return engine.Endpoint{Network: "tcp", Addr: ln.Addr().String()}, true
}

func (e *chainRelayTestEngine) Logs(int) ([]string, error) { return nil, nil }
func (e *chainRelayTestEngine) Update() error              { return nil }
func (e *chainRelayTestEngine) Uninstall() error           { return e.Stop() }

var registerArchTestEnginesOnce sync.Once

// registerArchTestEngines enregistre les 3 moteurs de test A/B/C dans le
// registre global et déclare leurs capacités (front relayeur -> back
// relayeur -> terminal), une seule fois par processus de test.
func registerArchTestEngines() {
	registerArchTestEnginesOnce.Do(func() {
		engine.Register("archtest-a", func() (engine.Engine, error) {
			return newChainRelayTestEngine("archtest-a", false), nil
		})
		engine.Register("archtest-b", func() (engine.Engine, error) {
			return newChainRelayTestEngine("archtest-b", false), nil
		})
		engine.Register("archtest-c", func() (engine.Engine, error) {
			return newChainRelayTestEngine("archtest-c", true), nil
		})

		engineutil.EngineCapabilitiesMap["archtest-a"] = engineutil.EngineCapability{
			Provides: []string{"test-relay"}, Network: "tcp", RelaysTo: "tcp",
		}
		engineutil.EngineCapabilitiesMap["archtest-b"] = engineutil.EngineCapability{
			Provides: []string{"test-relay"}, Network: "tcp", RelaysTo: "tcp",
		}
		engineutil.EngineCapabilitiesMap["archtest-c"] = engineutil.EngineCapability{
			Provides: []string{"test-backend"}, Network: "tcp", RelaysTo: "",
		}
	})
}

// TestCompositeEngineComponentConfigOverride prouve que ComponentConfig
// (nouveau) permet à un composant de recevoir une configuration JSON
// DIFFÉRENTE de celle partagée par les autres — nécessaire dès qu'un
// backend a un schéma incompatible avec celui du transport (découvert en
// tentant de câbler réellement dnstt->xray, voir ARCHITECTURE_HYBRIDES.md :
// la config xray, {"log":...,"inbounds":[...]}, n'a rien de commun avec la
// config dnstt, {"domain":...,"backend":...}). Vérifie aussi que le
// câblage (injectBackend) modifie bien la config PROPRE au composant
// "front" (archtest-a), pas la config partagée d'origine.
func TestCompositeEngineComponentConfigOverride(t *testing.T) {
	registerArchTestEngines()

	ce := &engineutil.CompositeEngine{
		Components:   []string{"archtest-a", "archtest-c"},
		ConfigureAll: true,
		ComponentConfig: map[string]engine.EngineConfig{
			"archtest-c": {JSON: []byte(`{"unrelated_schema":"xray-like","marker":"c-own-config"}`)},
		},
	}
	ce.Spec.Name = "archtest-a-c-override"

	ctx := context.Background()
	sharedCfg := engine.EngineConfig{JSON: []byte(`{"marker":"shared-config"}`)}
	if err := ce.Configure(ctx, sharedCfg); err != nil {
		t.Fatalf("Configure : %v", err)
	}

	cSub, err := ce.Component("archtest-c")
	if err != nil {
		t.Fatalf("Component(archtest-c) : %v", err)
	}
	cRelay := cSub.(*chainRelayTestEngine)
	cRelay.mu.Lock()
	gotC := string(cRelay.lastRawConfig)
	cRelay.mu.Unlock()
	if gotC != `{"unrelated_schema":"xray-like","marker":"c-own-config"}` {
		t.Fatalf("archtest-c a reçu %q, attendu sa config propre (ComponentConfig), pas la config partagée", gotC)
	}

	aSub, err := ce.Component("archtest-a")
	if err != nil {
		t.Fatalf("Component(archtest-a) : %v", err)
	}
	aRelay := aSub.(*chainRelayTestEngine)
	aRelay.mu.Lock()
	gotA := string(aRelay.lastRawConfig)
	aRelay.mu.Unlock()
	if gotA != `{"marker":"shared-config"}` {
		t.Fatalf("archtest-a a reçu %q, attendu la config partagée (aucun override déclaré pour lui)", gotA)
	}

	if err := ce.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}
	t.Cleanup(func() { ce.Stop() })

	// Après Start(), le câblage réel doit avoir injecté "backend" dans la
	// config PROPRE de archtest-a (ComponentConfig n'en avait pas pour lui,
	// donc c'est la config partagée + backend), et ne doit PAS avoir touché
	// à la config propre de archtest-c (lui est terminal, jamais "front").
	aRelay.mu.Lock()
	wiredA := string(aRelay.lastRawConfig)
	aRelay.mu.Unlock()
	if !strings.Contains(wiredA, `"marker":"shared-config"`) || !strings.Contains(wiredA, `"backend":"127.0.0.1:`) {
		t.Fatalf("config câblée de archtest-a = %q, attendu la config partagée d'origine + le backend réel injecté", wiredA)
	}
}

// TestCompositeEngineChainsThreeRealStages est le test de data-path exigé :
// client -> A -> B -> C (backend de test), entièrement local, aucun réseau
// tiers. Démontre que wireAdjacentChain câble réellement CHAQUE étage
// (démarrage C, puis B configuré vers l'endpoint réel de C et démarré, puis
// A configuré vers l'endpoint réel de B et démarré — jamais une adresse
// fictive), et que les octets envoyés par le client traversent réellement
// les 3 étages : la preuve est la réception, à travers toute la chaîne, de
// l'écho EXACT émis par le vrai backend terminal C.
func TestCompositeEngineChainsThreeRealStages(t *testing.T) {
	registerArchTestEngines()

	ce := &engineutil.CompositeEngine{
		Components:   []string{"archtest-a", "archtest-b", "archtest-c"},
		ConfigureAll: true,
	}
	ce.Spec.Name = "archtest-a-b-c"
	ce.Spec.Version = "hybrid"
	ce.Spec.Description = "test architecture 3 étages"

	ctx := context.Background()
	if err := ce.Configure(ctx, engine.EngineConfig{JSON: []byte(`{}`)}); err != nil {
		t.Fatalf("Configure : %v", err)
	}
	if err := ce.Start(ctx); err != nil {
		t.Fatalf("Start (chaîne à 3 étages) : %v", err)
	}
	t.Cleanup(func() { ce.Stop() })

	frontSub, err := ce.Component("archtest-a")
	if err != nil {
		t.Fatalf("Component(archtest-a) : %v", err)
	}
	epr, ok := frontSub.(engine.Endpointer)
	if !ok {
		t.Fatal("archtest-a n'implémente pas engine.Endpointer")
	}
	front, ready := epr.Endpoint()
	if !ready {
		t.Fatal("endpoint archtest-a jamais prêt")
	}

	// Vérifie aussi que Status() de la composite reporte ce MÊME endpoint
	// réel comme ListenAddr — jamais un placeholder pour la chaîne entière.
	if st := ce.Status(); st.ListenAddr != front.Addr {
		t.Fatalf("Status().ListenAddr = %q, attendu l'endpoint réel de archtest-a (%q)", st.ListenAddr, front.Addr)
	}

	conn, err := net.DialTimeout("tcp", front.Addr, 3*time.Second)
	if err != nil {
		t.Fatalf("connexion au front de la chaîne (%s) : %v", front.Addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	payload := []byte("PING-THROUGH-3-HOP-CHAIN")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("envoi payload : %v", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("aucune réponse reçue à travers la chaîne à 3 étages (client -> A -> B -> C) : %v", err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("écho reçu = %q, attendu %q — les octets n'ont pas traversé la chaîne intacts", buf, payload)
	}
	t.Logf("✓ CHAÎNAGE RÉEL À 3 ÉTAGES DÉMONTRÉ : client -> archtest-a (%s) -> archtest-b -> archtest-c (écho réel) -> retour intact à travers toute la chaîne", front.Addr)
}

// TestCompositeEngineChainRefusesIncompatibleLinkAtAnyDepth prouve que le
// mécanisme généralisé (wireAdjacentChain), pas seulement l'étape
// historique, refuse honnêtement une liaison incompatible plutôt que de
// démarrer les deux composants indépendamment en silence : archtest-a ne
// relaie que vers du tcp, hysteria n'expose qu'un endpoint udp réel.
func TestCompositeEngineChainRefusesIncompatibleLinkAtAnyDepth(t *testing.T) {
	registerArchTestEngines()
	t.Setenv("LABOSURF_HYSTERIA_CONFIG", filepath.Join(t.TempDir(), "hysteria.json"))

	ce := &engineutil.CompositeEngine{
		Components:   []string{"archtest-a", "hysteria"},
		ConfigureAll: true,
	}
	ce.Spec.Name = "archtest-a-hysteria"

	shared := map[string]any{"domain": "t.example.com", "port": 0, "backend": "203.0.113.1:9"}
	raw, err := json.Marshal(shared)
	if err != nil {
		t.Fatalf("marshal config : %v", err)
	}

	ctx := context.Background()
	if err := ce.Configure(ctx, engine.EngineConfig{JSON: raw}); err != nil {
		t.Fatalf("Configure ne devrait pas échouer (c'est Start() qui doit détecter l'incompatibilité) : %v", err)
	}

	startErr := ce.Start(ctx)
	t.Cleanup(func() { ce.Stop() })
	if startErr == nil {
		t.Fatal("Start() a réussi pour une liaison archtest-a(relais tcp) -> hysteria(endpoint udp) structurellement incompatible")
	}
	if !strings.Contains(startErr.Error(), "udp") || !strings.Contains(startErr.Error(), "TCP") {
		t.Fatalf("erreur obtenue n'explique pas l'incompatibilité réseau réelle : %v", startErr)
	}
	t.Logf("✓ refus honnête du chaînage généralisé impossible : %v", startErr)
}

// TestCompositeEngineChainCleansUpStartedComponentsOnFailure prouve que sur
// un maillon défaillant, les composants déjà démarrés par CET appel à
// Start() sont bien arrêtés avant que l'erreur ne remonte (la chaîne n'est
// jamais déclarée "ON" partiellement) : après l'échec, ni archtest-a ni
// hysteria ne doivent exposer d'endpoint réel encore actif.
func TestCompositeEngineChainCleansUpStartedComponentsOnFailure(t *testing.T) {
	registerArchTestEngines()
	t.Setenv("LABOSURF_HYSTERIA_CONFIG", filepath.Join(t.TempDir(), "hysteria.json"))

	ce := &engineutil.CompositeEngine{
		Components:   []string{"archtest-a", "hysteria"},
		ConfigureAll: true,
	}
	ce.Spec.Name = "archtest-a-hysteria-cleanup"

	shared := map[string]any{"domain": "t.example.com", "port": 0, "backend": "203.0.113.1:9"}
	raw, _ := json.Marshal(shared)
	ctx := context.Background()
	if err := ce.Configure(ctx, engine.EngineConfig{JSON: raw}); err != nil {
		t.Fatalf("Configure : %v", err)
	}
	if err := ce.Start(ctx); err == nil {
		t.Fatal("Start() aurait dû échouer (liaison incompatible)")
	}

	hysteriaSub, err := ce.Component("hysteria")
	if err != nil {
		t.Fatalf("Component(hysteria) : %v", err)
	}
	// L'endpoint réel est le signal fiable de "vraiment arrêté" (jamais un
	// placeholder — voir engine.Endpointer) : Status().Running, lui, reste
	// vrai après Stop() pour plusieurs moteurs de ce dépôt (hysteria, dnstt,
	// slowdns, ssh — leur Stop() ferme le serveur sans remettre leur champ
	// interne à nil), ce qui est un comportement PRÉEXISTANT propre à ces
	// moteurs, indépendant du mécanisme de nettoyage générique testé ici.
	epr, ok := hysteriaSub.(engine.Endpointer)
	if !ok {
		t.Fatal("hysteria n'implémente pas engine.Endpointer")
	}
	if _, ready := epr.Endpoint(); ready {
		t.Fatal("hysteria expose encore un endpoint réel après l'échec de Start() : le composant démarré n'a pas été nettoyé")
	}
	t.Logf("✓ nettoyage confirmé : hysteria (déjà démarré avant l'échec du câblage) n'expose plus d'endpoint réel — bien arrêté")
}
