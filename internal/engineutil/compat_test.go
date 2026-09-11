package engineutil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"

	_ "labosurf/engines/dnstt"
	_ "labosurf/engines/hysteria"
	_ "labosurf/engines/hysteria2"
	_ "labosurf/engines/slowdns"
	_ "labosurf/engines/ssh"
	_ "labosurf/engines/tuic"
	_ "labosurf/engines/wireguard"
	_ "labosurf/engines/xray"
)

func setHybridTempDir(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "labosurf-hybrid-test")
	os.RemoveAll(dir)
	t.Setenv("LABOSURF_DATA_DIR", dir)
}

func TestCompatibilityNoAlertForVpnPlusTransport(t *testing.T) {
	// xray (VPN) + slowdns (transport) : combinaison recommandée, aucun avertissement.
	if w := engineutil.CompatibilityCheck([]string{"xray", "slowdns"}); len(w) != 0 {
		t.Fatalf("attendu 0 avertissement, obtenu %v", w)
	}
}

func TestCompatibilitySSHNotTransport(t *testing.T) {
	// ssh n'est pas un transport : avertissement attendu.
	w := engineutil.CompatibilityCheck([]string{"xray", "ssh"})
	found := false
	for _, msg := range w {
		if strings.Contains(msg, "ssh") && strings.Contains(msg, "ne sert pas de transport") {
			found = true
		}
	}
	if !found {
		t.Fatalf("avertissement SSH attendu, obtenu %v", w)
	}
}

func TestCompatibilityTwoTransports(t *testing.T) {
	w := engineutil.CompatibilityCheck([]string{"xray", "slowdns", "dnstt"})
	found := false
	for _, msg := range w {
		if strings.Contains(msg, "plusieurs transports") {
			found = true
		}
	}
	if !found {
		t.Fatalf("avertissement multi-transports attendu, obtenu %v", w)
	}
}

func TestCompatibilityVpnWithoutTransport(t *testing.T) {
	w := engineutil.CompatibilityCheck([]string{"xray", "hysteria"})
	found := false
	for _, msg := range w {
		if strings.Contains(msg, "aucun transport") {
			found = true
		}
	}
	if !found {
		t.Fatalf("avertissement sans transport attendu, obtenu %v", w)
	}
}

// TestTUICCapabilityDeclared vérifie que le moteur tuic est bien déclaré
// dans la carte de capacités (comme xray/hysteria), avec le rôle VPN et un
// protocole/port explicites — sans quoi menu.go (runHybridCreateMenu) et
// CompatibilityCheck le traiteraient comme un moteur "inconnu".
func TestTUICCapabilityDeclared(t *testing.T) {
	cap, ok := engineutil.GetEngineCapability("tuic")
	if !ok {
		t.Fatal("capacité 'tuic' absente de EngineCapabilitiesMap")
	}
	if cap.Protocol != "tuic" {
		t.Fatalf("protocole attendu 'tuic', obtenu %q", cap.Protocol)
	}
	if cap.Port != 443 {
		t.Fatalf("port par défaut attendu 443, obtenu %d", cap.Port)
	}
	if engineutil.Role("tuic") != engineutil.RoleVPN {
		t.Fatalf("rôle attendu RoleVPN, obtenu %v", engineutil.Role("tuic"))
	}
}

// TestCompatibilityTUICWithoutTransport reproduit, pour tuic, le même
// avertissement honnête que hysteria : un VPN purement UDP/QUIC sans
// transport ne relaie rien (voir AUDIT_TUIC_INTEGRATION.md §1.4/§4).
func TestCompatibilityTUICWithoutTransport(t *testing.T) {
	w := engineutil.CompatibilityCheck([]string{"xray", "tuic"})
	found := false
	for _, msg := range w {
		if strings.Contains(msg, "aucun transport") {
			found = true
		}
	}
	if !found {
		t.Fatalf("avertissement sans transport attendu, obtenu %v", w)
	}
}

// TestHysteria2CapabilityDeclared vérifie que le moteur hysteria2 (officiel)
// est bien déclaré dans la carte de capacités, avec le rôle VPN et un
// protocole/port explicites — même exigence que pour tuic.
func TestHysteria2CapabilityDeclared(t *testing.T) {
	cap, ok := engineutil.GetEngineCapability("hysteria2")
	if !ok {
		t.Fatal("capacité 'hysteria2' absente de EngineCapabilitiesMap")
	}
	if cap.Protocol != "hysteria2" {
		t.Fatalf("protocole attendu 'hysteria2', obtenu %q", cap.Protocol)
	}
	if cap.Port != 443 {
		t.Fatalf("port par défaut attendu 443, obtenu %d", cap.Port)
	}
	if cap.Network != "udp" {
		t.Fatalf("réseau attendu 'udp', obtenu %q", cap.Network)
	}
	if cap.RelaysTo != "" {
		t.Fatalf("hysteria2 doit être terminal (RelaysTo vide), obtenu %q", cap.RelaysTo)
	}
	if engineutil.Role("hysteria2") != engineutil.RoleVPN {
		t.Fatalf("rôle attendu RoleVPN, obtenu %v", engineutil.Role("hysteria2"))
	}
}

// TestHysteria2CapabilityDistinctFromHysteria verrouille l'absence de
// collision entre les deux entrées "hysteria" (maison) et "hysteria2"
// (officiel) dans EngineCapabilitiesMap — exigence explicite de la mission
// P1 : ne jamais fusionner ni faire passer l'un pour l'autre.
func TestHysteria2CapabilityDistinctFromHysteria(t *testing.T) {
	h1, ok1 := engineutil.GetEngineCapability("hysteria")
	h2, ok2 := engineutil.GetEngineCapability("hysteria2")
	if !ok1 || !ok2 {
		t.Fatalf("les deux capacités doivent exister : hysteria=%v hysteria2=%v", ok1, ok2)
	}
	if h1.Port == h2.Port {
		t.Fatalf("les deux moteurs hysteria partagent le même port par défaut (%d) — collision non voulue", h1.Port)
	}
}

// TestCompatibilityHysteria2WithoutTransport reproduit, pour hysteria2, le
// même avertissement honnête que tuic/hysteria : un VPN purement UDP/QUIC
// sans transport ne relaie rien.
func TestCompatibilityHysteria2WithoutTransport(t *testing.T) {
	w := engineutil.CompatibilityCheck([]string{"xray", "hysteria2"})
	found := false
	for _, msg := range w {
		if strings.Contains(msg, "aucun transport") {
			found = true
		}
	}
	if !found {
		t.Fatalf("avertissement sans transport attendu, obtenu %v", w)
	}
}

// TestCanConnectHysteria2Terminal vérifie que hysteria2 ne peut structurellement
// jamais être un backend relayé par dnstt/slowdns (RelaysTo TCP-only) ni un
// relais lui-même — cohérent avec ETUDE_PROTOCOLes_COMPATIBLES.md.
func TestCanConnectHysteria2Terminal(t *testing.T) {
	if ok, reason := engineutil.CanConnect("dnstt", "hysteria2"); ok {
		t.Fatalf("dnstt ne peut pas relayer vers hysteria2 (udp à trame QUIC) : %s", reason)
	}
	if ok, _ := engineutil.CanConnect("hysteria2", "ssh"); ok {
		t.Fatal("hysteria2 est terminal : il ne doit jamais pouvoir relayer vers un composant suivant")
	}
}

// TestWireGuardCapabilityDeclared vérifie que le moteur wireguard est bien
// déclaré dans la carte de capacités, avec le rôle VPN et un
// protocole/port/réseau explicites, et RelaysTo vide (terminal) — mission
// P2, Étape 7 : ne jamais lui attribuer une capacité de relais qu'il ne
// possède pas.
func TestWireGuardCapabilityDeclared(t *testing.T) {
	cap, ok := engineutil.GetEngineCapability("wireguard")
	if !ok {
		t.Fatal("capacité 'wireguard' absente de EngineCapabilitiesMap")
	}
	if cap.Protocol != "wireguard" {
		t.Fatalf("protocole attendu 'wireguard', obtenu %q", cap.Protocol)
	}
	if cap.Network != "udp" {
		t.Fatalf("réseau attendu 'udp', obtenu %q", cap.Network)
	}
	if cap.RelaysTo != "" {
		t.Fatalf("wireguard doit être terminal (RelaysTo vide), obtenu %q", cap.RelaysTo)
	}
	if engineutil.Role("wireguard") != engineutil.RoleVPN {
		t.Fatalf("rôle attendu RoleVPN, obtenu %v", engineutil.Role("wireguard"))
	}
}

// TestWireGuardPortNoCollision verrouille l'absence de collision de port
// avec TOUS les autres moteurs déjà UDP de ce dépôt (mission P2, Étape 6).
func TestWireGuardPortNoCollision(t *testing.T) {
	wg, ok := engineutil.GetEngineCapability("wireguard")
	if !ok {
		t.Fatal("capacité 'wireguard' absente")
	}
	for _, other := range []string{"udp", "hysteria", "tuic", "hysteria2"} {
		cap, ok := engineutil.GetEngineCapability(other)
		if !ok {
			continue
		}
		if cap.Network == wg.Network && cap.Port == wg.Port {
			t.Fatalf("collision de port : wireguard et %s partagent %s/%d", other, wg.Network, wg.Port)
		}
	}
}

// TestCompatibilityWireGuardWithoutTransport reproduit, pour wireguard, le
// même avertissement honnête que tuic/hysteria/hysteria2 : un VPN purement
// UDP sans transport ne relaie rien.
func TestCompatibilityWireGuardWithoutTransport(t *testing.T) {
	w := engineutil.CompatibilityCheck([]string{"xray", "wireguard"})
	found := false
	for _, msg := range w {
		if strings.Contains(msg, "aucun transport") {
			found = true
		}
	}
	if !found {
		t.Fatalf("avertissement sans transport attendu, obtenu %v", w)
	}
}

// TestCanConnectWireGuardTerminal vérifie que wireguard ne peut
// structurellement jamais être chaîné derrière dnstt/slowdns/ssh ni relayer
// lui-même — mission P2, Étape 12 : DNSTT/SlowDNS/SSH -> WireGuard =
// INCOMPATIBLE avec l'architecture actuelle, aucun adaptateur développé.
func TestCanConnectWireGuardTerminal(t *testing.T) {
	for _, front := range []string{"dnstt", "slowdns"} {
		if ok, reason := engineutil.CanConnect(front, "wireguard"); ok {
			t.Fatalf("%s ne peut pas relayer vers wireguard (udp à trame Noise/WireGuard) : %s", front, reason)
		}
	}
	if ok, _ := engineutil.CanConnect("wireguard", "ssh"); ok {
		t.Fatal("wireguard est terminal : il ne doit jamais pouvoir relayer vers un composant suivant")
	}
	if ok, _ := engineutil.CanConnect("ssh", "wireguard"); ok {
		t.Fatal("ssh (RoleAccount) ne relaie jamais vers wireguard : ErrAccountAsTransport / RelaysTo vide")
	}
}

func TestRegisterHybridPersist(t *testing.T) {
	setHybridTempDir(t)
	components := []string{"xray", "slowdns"}
	name, err := engineutil.RegisterHybridPersist(components)
	if err != nil {
		t.Fatalf("RegisterHybridPersist: %v", err)
	}
	if name != "xray-slowdns" {
		t.Fatalf("nom attendu xray-slowdns, obtenu %s", name)
	}

	// Doit persister : un LoadHybrids retrouve la composition.
	loaded, err := engineutil.LoadHybrids()
	if err != nil {
		t.Fatalf("LoadHybrids: %v", err)
	}
	if len(loaded) != 1 || engineutil.HybridName(loaded[0]) != name {
		t.Fatalf("loaded inattendu : %v", loaded)
	}

	// Idempotent : ré-enregistrer ne duplique pas.
	if _, err := engineutil.RegisterHybridPersist(components); err != nil {
		t.Fatalf("ré-enregistrement: %v", err)
	}
	loaded, _ = engineutil.LoadHybrids()
	if len(loaded) != 1 {
		t.Fatalf("composition dupliquée : %v", loaded)
	}
}

func TestComposeSSHXraySlowdns(t *testing.T) {
	// Cas évoqué : ssh + xray + slowdns. Doit être composable (nom à 3 parties).
	components := []string{"ssh", "xray", "slowdns"}
	setHybridTempDir(t)
	name, err := engineutil.RegisterHybridPersist(components)
	if err != nil {
		t.Fatalf("RegisterHybridPersist: %v", err)
	}
	if name != "ssh-xray-slowdns" {
		t.Fatalf("nom attendu ssh-xray-slowdns, obtenu %s", name)
	}
	// Le guide signale le rôle ssh mais autorise quand même.
	_ = engineutil.CompatibilityCheck(components)
}

func TestRemoveHybrid(t *testing.T) {
	setHybridTempDir(t)
	components := []string{"hysteria", "dnstt"}
	if _, err := engineutil.RegisterHybridPersist(components); err != nil {
		t.Fatalf("RegisterHybridPersist: %v", err)
	}
	name := "hysteria-dnstt"

	// Retrait => fichier vide ET registre nettoyé.
	if err := engineutil.RemoveHybrid(name); err != nil {
		t.Fatalf("RemoveHybrid: %v", err)
	}
	loaded, _ := engineutil.LoadHybrids()
	if len(loaded) != 0 {
		t.Fatalf("hybrids.json devrait être vide, obtenu %v", loaded)
	}
	if engine.Has(name) {
		t.Fatalf("le registre devrait avoir retiré %s", name)
	}
}