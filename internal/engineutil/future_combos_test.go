package engineutil_test

// Caractérisation empirique (exécutée, pas seulement lue) des 7
// combinaisons candidates à devenir de véritables hybrides fonctionnels :
// TUIC+SSH+Xray, TUIC+DNSTT+Xray, Hysteria2+SSH+Xray, Hysteria2+DNSTT,
// UDP+Hysteria2, UDP+TUIC, UDP+Xray. Verrouille les constats utilisés par
// l'audit technique correspondant (voir le rapport de session) : pourquoi
// chacune échoue aujourd'hui, avec la cause exacte plutôt qu'une
// supposition de lecture de code.

import (
	"testing"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"

	_ "labosurf/engines/dnstt"
	_ "labosurf/engines/hysteria"
	_ "labosurf/engines/slowdns"
	_ "labosurf/engines/ssh"
	_ "labosurf/engines/tuic"
	_ "labosurf/engines/xray"
	_ "labosurf/internal/engineudp"
)

// TestFutureCombosCardinality verrouille que ValidateHybrid rejette
// aujourd'hui, pour la raison EXACTE indiquée (ErrMultipleVPNs), 6 des 7
// combinaisons demandées — toutes celles qui associent deux moteurs de rôle
// RoleVPN. Seule Hysteria2+DNSTT (1 VPN + 1 transport) passe ce garde-fou de
// cardinalité ; voir TestFutureCombosLinkCompatibility pour la raison
// distincte (réseau) qui la bloque quand même.
func TestFutureCombosCardinality(t *testing.T) {
	cases := []struct {
		name       string
		components []string
		wantErr    error // nil = aucune erreur de cardinalité attendue
	}{
		{"TUIC+SSH+Xray", []string{"tuic", "ssh", "xray"}, engineutil.ErrMultipleVPNs},
		{"TUIC+DNSTT+Xray", []string{"tuic", "dnstt", "xray"}, engineutil.ErrMultipleVPNs},
		{"Hysteria2+SSH+Xray", []string{"hysteria", "ssh", "xray"}, engineutil.ErrMultipleVPNs},
		{"Hysteria2+DNSTT", []string{"hysteria", "dnstt"}, nil},
		{"UDP+Hysteria2", []string{"udp", "hysteria"}, engineutil.ErrMultipleVPNs},
		{"UDP+TUIC", []string{"udp", "tuic"}, engineutil.ErrMultipleVPNs},
		{"UDP+Xray", []string{"udp", "xray"}, engineutil.ErrMultipleVPNs},
	}
	for _, c := range cases {
		err := engineutil.ValidateHybrid(c.components)
		if c.wantErr == nil {
			if err != nil {
				t.Errorf("%s : ValidateHybrid(%v) a échoué de façon inattendue : %v", c.name, c.components, err)
			}
			continue
		}
		if err != c.wantErr {
			t.Errorf("%s : ValidateHybrid(%v) = %v, attendu %v", c.name, c.components, err, c.wantErr)
		}
	}
}

// TestFutureCombosLinkCompatibility verrouille le verdict réel de
// CanConnect pour les paires pertinentes : la seule combinaison à
// cardinalité valide (dnstt/hysteria, dans les deux ordres) reste
// incompatible au niveau réseau (dnstt/slowdns ne relaient que vers du tcp,
// hysteria n'expose que de l'udp) ; "udp" ne peut être câblé ni comme front
// ni comme back avec aucun des trois autres moteurs des combinaisons 5/6/7
// (il ne relaie jamais — RelaysTo vide).
func TestFutureCombosLinkCompatibility(t *testing.T) {
	cases := []struct {
		front, back string
		wantWired   bool
	}{
		{"dnstt", "hysteria", false},
		{"hysteria", "dnstt", false},
		{"slowdns", "hysteria", false},
		{"hysteria", "slowdns", false},
		{"udp", "hysteria", false},
		{"udp", "tuic", false},
		{"udp", "xray", false},
		{"hysteria", "udp", false},
		{"tuic", "udp", false},
		{"xray", "udp", false},
	}
	for _, c := range cases {
		ok, reason := engineutil.CanConnect(c.front, c.back)
		if ok != c.wantWired {
			t.Errorf("CanConnect(%q,%q) = %v (%q), attendu %v", c.front, c.back, ok, reason, c.wantWired)
		}
	}
}

// TestUDPEngineHasNoRealEndpoint verrouille que le moteur "udp" enregistré
// (internal/engineudp, wrapper engineutil.SystemEngine) n'implémente pas
// engine.Endpointer aujourd'hui. Conséquence directe pour les combinaisons
// 5/6/7 (UDP+Hysteria2, UDP+TUIC, UDP+Xray) : même si la cardinalité était
// un jour assouplie, "udp" ne pourrait structurellement servir ni de
// backend câblé (endpoint réel jamais lisible) ni de relais (il ne lit
// aucun champ "backend" générique).
func TestUDPEngineHasNoRealEndpoint(t *testing.T) {
	e, err := engine.Get("udp")
	if err != nil {
		t.Fatalf("engine.Get(udp) : %v", err)
	}
	if _, ok := e.(engine.Endpointer); ok {
		t.Fatal("le moteur 'udp' implémente désormais engine.Endpointer : cette caractérisation (et l'analyse associée) doit être révisée")
	}
}
