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
	_ "labosurf/engines/slowdns"
	_ "labosurf/engines/ssh"
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