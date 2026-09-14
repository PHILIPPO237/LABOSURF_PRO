package profile_test

import (
	"os"
	"testing"

	"labosurf/internal/profile"
)

// setDataDir redirige le stockage vers un répertoire temporaire.
func setDataDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LABOSURF_DATA_DIR", dir)
}

// TestNewSimple vérifie les champs initiaux d'un profil simple.
func TestNewSimple(t *testing.T) {
	p := profile.NewSimple("Xray-Prod", "production", "xray", map[string]string{"port": "443"})
	if p.Kind != profile.KindSimple {
		t.Fatalf("Kind = %q, want %q", p.Kind, profile.KindSimple)
	}
	if p.Status != profile.StatusDraft {
		t.Fatalf("Status = %q, want %q", p.Status, profile.StatusDraft)
	}
	if p.Engine != "xray" {
		t.Fatalf("Engine = %q, want %q", p.Engine, "xray")
	}
	if p.Param("port", "") != "443" {
		t.Fatal("Param port attendu 443")
	}
	if p.ID == "" {
		t.Fatal("ID vide")
	}
}

// TestNewHybrid vérifie les champs initiaux d'un profil hybride.
func TestNewHybrid(t *testing.T) {
	p := profile.NewHybrid("SlowDNS→SSH", "", []string{"slowdns", "ssh"})
	if p.Kind != profile.KindHybrid {
		t.Fatalf("Kind = %q, want %q", p.Kind, profile.KindHybrid)
	}
	if len(p.Components) != 2 {
		t.Fatalf("Components len = %d, want 2", len(p.Components))
	}
}

// TestActivateDeactivate vérifie la transition de statut.
func TestActivateDeactivate(t *testing.T) {
	p := profile.NewSimple("test", "", "xray", nil)
	if p.IsActive() {
		t.Fatal("un nouveau profil ne doit pas être actif")
	}
	p.Activate()
	if !p.IsActive() {
		t.Fatal("profil devrait être actif après Activate()")
	}
	if p.StatusLabel() != "ACTIF" {
		t.Fatalf("StatusLabel = %q, want ACTIF", p.StatusLabel())
	}
	p.Deactivate()
	if p.IsActive() {
		t.Fatal("profil ne devrait pas être actif après Deactivate()")
	}
	if p.StatusLabel() != "inactif" {
		t.Fatalf("StatusLabel = %q, want inactif", p.StatusLabel())
	}
}

// TestDuplicate vérifie que le duplicate est un brouillon avec un ID distinct.
func TestDuplicate(t *testing.T) {
	orig := profile.NewSimple("Xray-Prod", "original", "xray", map[string]string{"port": "443"})
	orig.Activate()

	dup := orig.Duplicate("Xray-Test")
	if dup.ID == orig.ID {
		t.Fatal("le duplicate doit avoir un ID distinct")
	}
	if dup.Status != profile.StatusDraft {
		t.Fatalf("Status = %q, want draft", dup.Status)
	}
	if dup.Name != "Xray-Test" {
		t.Fatalf("Name = %q, want Xray-Test", dup.Name)
	}
	if dup.Param("port", "") != "443" {
		t.Fatal("les paramètres doivent être copiés dans le duplicate")
	}
}

// TestSaveLoad vérifie la persistance d'un profil sur disque.
func TestSaveLoad(t *testing.T) {
	setDataDir(t)

	p := profile.NewSimple("Xray-Prod", "test", "xray", map[string]string{"port": "443"})
	if err := profile.Save(&p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := profile.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != p.Name {
		t.Fatalf("Name = %q, want %q", got.Name, p.Name)
	}
	if got.Param("port", "") != "443" {
		t.Fatal("paramètre port non persisté")
	}
}

// TestListAll vérifie que tous les profils sauvegardés sont listés.
func TestListAll(t *testing.T) {
	setDataDir(t)

	p1 := profile.NewSimple("A", "", "xray", nil)
	p2 := profile.NewSimple("B", "", "ssh", nil)
	p3 := profile.NewHybrid("C", "", []string{"slowdns", "ssh"})

	for _, p := range []profile.Profile{p1, p2, p3} {
		pp := p
		if err := profile.Save(&pp); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	all, err := profile.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAll len = %d, want 3", len(all))
	}
}

// TestDeleteProtectsActive vérifie que Delete ne bloque pas (pas de contrôle dans Delete) —
// c'est l'appelant qui doit vérifier via Dependents avant d'appeler Delete.
func TestDeleteProfile(t *testing.T) {
	setDataDir(t)

	p := profile.NewSimple("ToDelete", "", "xray", nil)
	if err := profile.Save(&p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := profile.Delete(p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	all, _ := profile.ListAll()
	for _, got := range all {
		if got.ID == p.ID {
			t.Fatal("profil supprimé encore présent dans ListAll")
		}
	}
}

// TestDependents vérifie que les profils hybrides citant un moteur sont détectés.
func TestDependents(t *testing.T) {
	setDataDir(t)

	hyb := profile.NewHybrid("SlowDNS→SSH", "", []string{"slowdns", "ssh"})
	if err := profile.Save(&hyb); err != nil {
		t.Fatalf("Save hybrid: %v", err)
	}

	deps, err := profile.Dependents("slowdns")
	if err != nil {
		t.Fatalf("Dependents: %v", err)
	}
	if len(deps) != 1 || deps[0].ID != hyb.ID {
		t.Fatalf("Dependents = %v, want [%s]", deps, hyb.ID)
	}

	// Un moteur non utilisé ne génère pas de dépendance.
	deps2, _ := profile.Dependents("xray")
	if len(deps2) != 0 {
		t.Fatalf("Dependents(xray) = %d, want 0", len(deps2))
	}
}

// TestActiveProfile vérifie qu'un seul profil actif par moteur est retourné.
func TestActiveProfile(t *testing.T) {
	setDataDir(t)

	p1 := profile.NewSimple("Xray-Prod", "", "xray", nil)
	p1.Activate()
	p2 := profile.NewSimple("Xray-Test", "", "xray", nil)

	for _, p := range []profile.Profile{p1, p2} {
		pp := p
		if err := profile.Save(&pp); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, found := profile.ActiveProfile("xray")
	if !found {
		t.Fatal("ActiveProfile devrait trouver un profil actif")
	}
	if got.Name != "Xray-Prod" {
		t.Fatalf("ActiveProfile name = %q, want Xray-Prod", got.Name)
	}

	_, found2 := profile.ActiveProfile("ssh")
	if found2 {
		t.Fatal("aucun profil ssh actif attendu")
	}
}

// TestValidateSimple vérifie la validation d'un profil simple bien formé.
func TestValidateSimpleOK(t *testing.T) {
	p := profile.NewSimple("Xray-Prod", "", "xray", nil)
	res := profile.Validate(p)
	if !res.OK {
		t.Fatalf("Validate(simple xray) = %v", res.Errors)
	}
}

// TestValidateSimpleBadEngine vérifie qu'un moteur inconnu génère une erreur.
func TestValidateSimpleBadEngine(t *testing.T) {
	p := profile.NewSimple("Test", "", "moteur-imaginaire", nil)
	res := profile.Validate(p)
	if res.OK {
		t.Fatal("Validate(moteur inconnu) devrait échouer")
	}
}

// TestValidateHybridOK vérifie un hybride compatible (slowdns→ssh).
func TestValidateHybridOK(t *testing.T) {
	p := profile.NewHybrid("SlowDNS→SSH", "", []string{"slowdns", "ssh"})
	res := profile.Validate(p)
	// slowdns→ssh est un hybride valide (ValidateHybrid passe, CanConnect passe)
	// Des warnings sont possibles (port conflict absent si srvcfg absent), pas d'erreurs.
	if len(res.Errors) > 0 {
		t.Fatalf("Validate(slowdns→ssh) errors = %v", res.Errors)
	}
}

// TestValidateHybridTooFewComponents vérifie le rejet d'un hybride avec 1 seul composant.
func TestValidateHybridTooFewComponents(t *testing.T) {
	p := profile.NewHybrid("Solo", "", []string{"xray"})
	res := profile.Validate(p)
	if res.OK {
		t.Fatal("Validate(hybride 1 composant) devrait échouer")
	}
}

// TestDeactivateAllForEngine vérifie qu'activer un profil désactive les autres.
func TestDeactivateAllForEngine(t *testing.T) {
	setDataDir(t)

	p1 := profile.NewSimple("Xray-A", "", "xray", nil)
	p1.Activate()
	p2 := profile.NewSimple("Xray-B", "", "xray", nil)
	p2.Activate()

	for _, p := range []profile.Profile{p1, p2} {
		pp := p
		if err := profile.Save(&pp); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	if err := profile.DeactivateAllForEngine("xray", p2.ID); err != nil {
		t.Fatalf("DeactivateAllForEngine: %v", err)
	}

	// p1 doit être inactif
	got1, _ := profile.Get(p1.ID)
	if got1.IsActive() {
		t.Fatal("p1 devrait être inactif après DeactivateAllForEngine")
	}
	// p2 reste actif (excluded)
	got2, _ := profile.Get(p2.ID)
	if !got2.IsActive() {
		t.Fatal("p2 doit rester actif (il est l'exception)")
	}
}

// TestGetByName vérifie la recherche par nom insensible à la casse.
func TestGetByName(t *testing.T) {
	setDataDir(t)

	p := profile.NewSimple("Xray-Production", "", "xray", nil)
	if err := profile.Save(&p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, found, err := profile.GetByName("xray-production")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if !found {
		t.Fatal("GetByName devrait trouver le profil")
	}
	if got.ID != p.ID {
		t.Fatalf("GetByName ID = %q, want %q", got.ID, p.ID)
	}
}

// TestListAllEmptyDir vérifie que ListAll ne retourne pas d'erreur si le répertoire est absent.
func TestListAllEmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LABOSURF_DATA_DIR", dir)
	// Le sous-répertoire profiles/ n'existe pas encore.
	_ = os.RemoveAll(profile.Dir())

	all, err := profile.ListAll()
	if err != nil {
		t.Fatalf("ListAll (répertoire absent): %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("ListAll (répertoire absent) = %d, want 0", len(all))
	}
}
