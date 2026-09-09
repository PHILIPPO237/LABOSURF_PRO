package engineutil_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"

	_ "labosurf/engines/hysteria"
	_ "labosurf/engines/slowdns"
)

// TestCompositeEngineRejectsIncompatibleChain démontre que Start() refuse
// clairement de chaîner hysteria derrière slowdns : hysteria n'écoute
// qu'en UDP (protocole "hysteria2" maison), mais slowdns ne sait relayer
// que vers un backend TCP (net.Dial("tcp", ...), audité en Phase 2). C'est
// exactement le type de composition que TestRemoveHybrid (compat_test.go)
// prouve enregistrable — la cardinalité (1 transport + 1 VPN max) est
// respectée — mais qui ne peut structurellement jamais transporter de
// trafic avec le mécanisme de chaînage actuel.
//
// Avant Phase 3, cette composition "démarrait" sans erreur (Configure()
// posait un faux endpoint "127.0.0.1:0" et Start() ne vérifiait jamais la
// compatibilité réseau) : elle semblait fonctionner tout en ne
// transportant jamais un octet. Ce test verrouille le comportement
// honnête inverse : Start() DOIT échouer clairement plutôt que de prétendre
// réussir.
func TestCompositeEngineRejectsIncompatibleChain(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("LABOSURF_HYSTERIA_CONFIG", filepath.Join(dataDir, "hysteria.json"))
	t.Setenv("LABOSURF_SLOWDNS_CONFIG", filepath.Join(dataDir, "slowdns.json"))

	ce := &engineutil.CompositeEngine{
		Components:   []string{"hysteria", "slowdns"},
		ConfigureAll: true,
	}
	ce.Spec.Name = "hysteria-slowdns"

	shared := map[string]any{
		"domain":  "t.example.com",
		"port":    0,
		"backend": "203.0.113.1:9",
	}
	raw, err := json.Marshal(shared)
	if err != nil {
		t.Fatalf("marshal config : %v", err)
	}

	ctx := context.Background()
	if err := ce.Configure(ctx, engine.EngineConfig{JSON: raw}); err != nil {
		t.Fatalf("Configure ne devrait pas échouer (c'est Start() qui doit détecter l'incompatibilité) : %v", err)
	}

	err = ce.Start(ctx)
	t.Cleanup(func() { ce.Stop() })
	if err == nil {
		t.Fatal("Start() a réussi pour un chaînage structurellement impossible (hysteria=UDP derrière slowdns=relais TCP uniquement) — c'est exactement le faux fonctionnement que la Phase 3 doit éviter")
	}
	if !strings.Contains(err.Error(), "udp") || !strings.Contains(err.Error(), "TCP") {
		t.Fatalf("erreur obtenue ne semble pas expliquer l'incompatibilité réseau réelle : %v", err)
	}
	t.Logf("✓ refus honnête du chaînage impossible : %v", err)
}

// TestCompositeEngineComponentReturnsStableInstance verrouille la
// correction du bug découvert en Phase 3 : component() (utilisé en interne
// par Start/Stop/Configure/Status/...) retournait auparavant une instance
// NEUVE du sous-moteur à CHAQUE appel (engine.Get(name) est documenté pour
// faire exactement ça), ce qui rendait Stop() totalement inopérant sur un
// CompositeEngine (il appelait Stop() sur une instance jamais démarrée).
func TestCompositeEngineComponentReturnsStableInstance(t *testing.T) {
	t.Setenv("LABOSURF_SLOWDNS_CONFIG", filepath.Join(t.TempDir(), "slowdns.json"))
	ce := &engineutil.CompositeEngine{Components: []string{"slowdns"}}

	a, err := ce.Component("slowdns")
	if err != nil {
		t.Fatalf("Component : %v", err)
	}
	b, err := ce.Component("slowdns")
	if err != nil {
		t.Fatalf("Component (2e appel) : %v", err)
	}
	if a != b {
		t.Fatal("Component() a retourné deux instances différentes pour le même composant sur le même CompositeEngine — Stop()/Status() opéreraient sur une instance jamais démarrée")
	}
}
