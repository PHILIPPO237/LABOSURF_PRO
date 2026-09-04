package srvcfg

import (
	"os"
	"path/filepath"
	"testing"
)

// setTempDir bascule le répertoire de données LABOSURF vers un dossier temporaire,
// garantissant que Path/Load/Save n'écrivent pas dans /etc/labosurf.
func setTempDir(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "labosurf-srvcfg-test")
	os.RemoveAll(dir)
	t.Setenv("LABOSURF_DATA_DIR", dir)
}

func TestDefaults(t *testing.T) {
	p := Default()
	if p.Port("udp") != 5667 || p.Port("xray") != 443 || p.Port("ssh") != 22 {
		t.Fatalf("ports par défaut incorrects : %+v", p.Ports)
	}
	if p.Port("inconnu") != 0 {
		t.Fatalf("un moteur inconnu devrait donner un port 0")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	setTempDir(t)
	p := Default()
	p.Host = "185.16.166.120"
	p.Domains = []string{"tun.example.com"}
	p.SetPort("xray", 8443)

	if err := p.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Host != p.Host {
		t.Fatalf("Host : attendu %q, obtenu %q", p.Host, got.Host)
	}
	if len(got.Domains) != 1 || got.Domains[0] != "tun.example.com" {
		t.Fatalf("Domaines : %v", got.Domains)
	}
	if got.Port("xray") != 8443 {
		t.Fatalf("Port xray non persisté : %d", got.Port("xray"))
	}
}

func TestLoadMissingGivesDefaults(t *testing.T) {
	setTempDir(t)
	p, err := Load()
	if err != nil {
		t.Fatalf("Load (fichier absent) : %v", err)
	}
	if p.Host != "" || p.Port("udp") != 5667 {
		t.Fatalf("attendu profil par défaut, obtenu %+v", p)
	}
}