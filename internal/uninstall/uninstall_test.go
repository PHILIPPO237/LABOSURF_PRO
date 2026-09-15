package uninstall

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// tempPaths construit un jeu de Paths entièrement sous t.TempDir() — aucun
// test de ce fichier ne touche à un vrai répertoire système.
func tempPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	p := Paths{
		BinDir:     filepath.Join(root, "bin"),
		OptDir:     filepath.Join(root, "opt", "labosurf"),
		DataDir:    filepath.Join(root, "etc", "labosurf"),
		SystemdDir: filepath.Join(root, "systemd"),
	}
	for _, d := range []string{p.BinDir, p.SystemdDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	return p
}

func touch(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ============================================================
// Confirmation refusée / incorrecte / annulation
// ============================================================

func TestShouldProceed(t *testing.T) {
	cases := map[string]bool{
		"o": true, "O": true, " o ": true,
		"n": false, "N": false, "": false, "oui": false, "yes": false,
	}
	for in, want := range cases {
		if got := ShouldProceed(in); got != want {
			t.Errorf("ShouldProceed(%q) = %v, attendu %v", in, got, want)
		}
	}
}

func TestValidatePhraseExact(t *testing.T) {
	if !ValidatePhrase("DESINSTALLER") {
		t.Fatal("la phrase exacte doit être acceptée")
	}
}

func TestValidatePhraseIncorrect(t *testing.T) {
	for _, in := range []string{"desinstaller", "DESINSTALLER ", " DESINSTALLER", "Desinstaller", "", "oui", "DESINSTALL"} {
		if ValidatePhrase(in) {
			t.Errorf("ValidatePhrase(%q) : ne doit PAS être acceptée", in)
		}
	}
}

func TestModeRequiresTypedConfirmation(t *testing.T) {
	if ModeProgramOnly.RequiresTypedConfirmation() {
		t.Fatal("le mode programme seul ne doit pas exiger la phrase de confirmation")
	}
	if !ModeProgramKeepData.RequiresTypedConfirmation() {
		t.Fatal("le mode programme+suppression config doit exiger la phrase de confirmation")
	}
	if !ModeFull.RequiresTypedConfirmation() {
		t.Fatal("le mode complet doit exiger la phrase de confirmation")
	}
}

// ============================================================
// ValidatePathComponent — chemin sûr (absolu, non système, non vide)
// ============================================================

func TestValidatePathComponentAcceptsLegitimateAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "labosurf")
	clean, err := ValidatePathComponent(target)
	if err != nil {
		t.Fatalf("chemin légitime refusé : %v", err)
	}
	if clean != filepath.Clean(target) {
		t.Fatalf("attendu %q, obtenu %q", filepath.Clean(target), clean)
	}
}

func TestValidatePathComponentRejectsEmpty(t *testing.T) {
	if _, err := ValidatePathComponent(""); err == nil {
		t.Fatal("attendu une erreur pour un chemin vide")
	}
	if _, err := ValidatePathComponent("   "); err == nil {
		t.Fatal("attendu une erreur pour un chemin composé uniquement d'espaces")
	}
}

func TestValidatePathComponentRejectsRelative(t *testing.T) {
	for _, p := range []string{"labosurf", "./labosurf", "../labosurf", "etc/labosurf", "."} {
		_, err := ValidatePathComponent(p)
		if err == nil {
			t.Errorf("ValidatePathComponent(%q) : attendu une erreur (chemin relatif)", p)
			continue
		}
		if !errors.Is(err, ErrRelativePath) {
			t.Errorf("ValidatePathComponent(%q) : attendu ErrRelativePath, obtenu %v", p, err)
		}
	}
}

func TestValidatePathComponentRejectsTraversalResolvingToSystemPath(t *testing.T) {
	// "/etc/labosurf/../../etc" se nettoie en "/etc" — un répertoire système.
	_, err := ValidatePathComponent("/etc/labosurf/../../etc")
	if !errors.Is(err, ErrDangerousRoot) {
		t.Fatalf("attendu ErrDangerousRoot après résolution de '..', obtenu : %v", err)
	}
}

// TestValidatePathComponentRejectsAllProtectedSystemPaths couvre
// exactement la liste exigée par l'audit — chacun de ces chemins DOIT être
// refusé comme racine, quelle que soit sa provenance (variable
// d'environnement mal configurée ou non).
func TestValidatePathComponentRejectsAllProtectedSystemPaths(t *testing.T) {
	dangerous := []string{
		"/", "/etc", "/var", "/usr", "/opt", "/home", "/root",
		"/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys",
		"/run", "/tmp",
	}
	for _, p := range dangerous {
		_, err := ValidatePathComponent(p)
		if !errors.Is(err, ErrDangerousRoot) {
			t.Errorf("ValidatePathComponent(%q) : attendu ErrDangerousRoot, obtenu %v", p, err)
		}
		// Variante avec slash final — doit être nettoyée et refusée pareil.
		_, err2 := ValidatePathComponent(p + "/")
		if p != "/" {
			if !errors.Is(err2, ErrDangerousRoot) {
				t.Errorf("ValidatePathComponent(%q) : attendu ErrDangerousRoot, obtenu %v", p+"/", err2)
			}
		}
	}
}

func TestValidatePathComponentAcceptsChildOfProtectedPath(t *testing.T) {
	// /opt/labosurf et /etc/labosurf sont des ENFANTS de répertoires
	// protégés, pas les répertoires protégés eux-mêmes — ils doivent rester
	// potentiellement autorisables (l'identité LABOSURF PRO est vérifiée
	// séparément par Detect, pas par ValidatePathComponent).
	for _, p := range []string{"/opt/labosurf", "/etc/labosurf"} {
		if _, err := ValidatePathComponent(p); err != nil {
			t.Errorf("ValidatePathComponent(%q) : ne doit PAS être refusé (enfant, pas la racine système elle-même), obtenu %v", p, err)
		}
	}
}

// ============================================================
// Détection des fichiers + identité LABOSURF PRO
// ============================================================

func TestDetectFindsPresentFiles(t *testing.T) {
	p := tempPaths(t)
	touch(t, filepath.Join(p.BinDir, "labosurf"), "x")
	touch(t, filepath.Join(p.BinDir, "labosurf-xray"), "x")
	touch(t, filepath.Join(p.BinDir, "menu"), "x")
	touch(t, filepath.Join(p.SystemdDir, "labosurf.service"), "x")
	touch(t, filepath.Join(p.SystemdDir, "labosurf-xray.service"), "x")
	touch(t, filepath.Join(p.DataDir, "config.json"), "{}")
	touch(t, filepath.Join(p.DataDir, "users_db.json"), "{}")
	if err := os.MkdirAll(p.OptDir, 0o755); err != nil {
		t.Fatal(err)
	}

	inv := Detect(p)

	if len(inv.Warnings) != 0 {
		t.Fatalf("aucun avertissement attendu pour une configuration légitime, obtenu %v", inv.Warnings)
	}
	if len(inv.Binaries) != 3 {
		t.Fatalf("attendu 3 binaires détectés, obtenu %d : %v", len(inv.Binaries), inv.Binaries)
	}
	if len(inv.SystemdUnits) != 2 {
		t.Fatalf("attendu 2 unités systemd, obtenu %d : %v", len(inv.SystemdUnits), inv.SystemdUnits)
	}
	if inv.ThirdPartyDir == "" {
		t.Fatal("répertoire tiers attendu détecté")
	}
	if len(inv.ConfigFiles) != 1 {
		t.Fatalf("attendu 1 fichier de config (config.json), obtenu %v", inv.ConfigFiles)
	}
	if inv.DataDir == "" {
		t.Fatal("DataDir attendu détecté")
	}
	if len(inv.DataFiles) != 1 {
		t.Fatalf("attendu 1 fichier de données (users_db.json), obtenu %v", inv.DataFiles)
	}
}

func TestDetectAbsentInstallationReturnsEmpty(t *testing.T) {
	p := tempPaths(t) // rien créé à part BinDir/SystemdDir vides
	inv := Detect(p)
	if len(inv.Binaries) != 0 || len(inv.SystemdUnits) != 0 || inv.ThirdPartyDir != "" || inv.DataDir != "" {
		t.Fatalf("aucune installation ne doit être détectée sur des répertoires vides : %+v", inv)
	}
}

func TestDetectNeverInventsArbitraryUnits(t *testing.T) {
	p := tempPaths(t)
	// Un service qui n'appartient pas à LABOSURF PRO — ne doit jamais être détecté.
	touch(t, filepath.Join(p.SystemdDir, "nginx.service"), "x")
	inv := Detect(p)
	if len(inv.SystemdUnits) != 0 {
		t.Fatalf("un service tiers (nginx) ne doit jamais apparaître dans l'inventaire : %v", inv.SystemdUnits)
	}
}

// TestDetectRejectsDangerousDataDir couvre exactement le scénario critique
// de l'audit : LABOSURF_DATA_DIR mal configuré sur un répertoire système.
// Peu importe que le répertoire existe (il existe presque toujours pour
// /etc, /var, /usr, /home, /root) : il ne doit JAMAIS être retenu comme
// DataDir, et donc jamais devenir une cible de suppression.
func TestDetectRejectsDangerousDataDir(t *testing.T) {
	dangerous := []string{
		"/", "/etc", "/var", "/usr", "/opt", "/home", "/root",
		"/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys",
	}
	for _, d := range dangerous {
		p := tempPaths(t)
		p.DataDir = d
		inv := Detect(p)
		if inv.DataDir != "" {
			t.Errorf("DataDir=%q : ne doit JAMAIS être retenu comme répertoire de données, obtenu inv.DataDir=%q", d, inv.DataDir)
		}
		if len(inv.Warnings) == 0 {
			t.Errorf("DataDir=%q : un avertissement était attendu", d)
		}
	}
}

// TestDetectRejectsDangerousOptDir couvre le même scénario pour OptDir
// (LABOSURF_BIN_DIR) — ex. LABOSURF_BIN_DIR=/opt (sans le suffixe
// "/labosurf") ne doit jamais faire de /opt entier une cible de suppression.
func TestDetectRejectsDangerousOptDir(t *testing.T) {
	dangerous := []string{"/", "/opt", "/etc", "/var", "/usr", "/home", "/root"}
	for _, d := range dangerous {
		p := tempPaths(t)
		p.OptDir = d
		inv := Detect(p)
		if inv.ThirdPartyDir != "" {
			t.Errorf("OptDir=%q : ne doit JAMAIS être retenu comme répertoire tiers, obtenu inv.ThirdPartyDir=%q", d, inv.ThirdPartyDir)
		}
	}
}

func TestDetectRejectsRelativeDataDir(t *testing.T) {
	p := tempPaths(t)
	p.DataDir = "labosurf-data-relative"
	inv := Detect(p)
	if inv.DataDir != "" {
		t.Fatalf("un DataDir relatif ne doit jamais être retenu, obtenu %q", inv.DataDir)
	}
	if len(inv.Warnings) == 0 {
		t.Fatal("un avertissement était attendu pour un DataDir relatif")
	}
}

func TestDetectRejectsEmptyDataDir(t *testing.T) {
	p := tempPaths(t)
	p.DataDir = ""
	inv := Detect(p)
	if inv.DataDir != "" {
		t.Fatal("un DataDir vide ne doit jamais être retenu")
	}
	// Un DataDir vide n'est pas une erreur de configuration explicite (cas
	// normal si l'administrateur n'a jamais défini LABOSURF_DATA_DIR et que
	// engineutil retombe sur son propre défaut) : pas d'avertissement exigé
	// ici, seulement l'absence de DataDir.
}

func TestDetectRejectsDotAndDotDotDataDir(t *testing.T) {
	for _, d := range []string{".", ".."} {
		p := tempPaths(t)
		p.DataDir = d
		inv := Detect(p)
		if inv.DataDir != "" {
			t.Errorf("DataDir=%q : ne doit jamais être retenu (chemin relatif)", d)
		}
	}
}

// TestDetectRejectsDataDirWithoutLabosurfIdentity : un répertoire qui
// existe réellement, au bon endroit (sous un chemin absolu non protégé),
// mais dont ni le nom ni le contenu ne permettent de l'identifier comme
// appartenant à LABOSURF PRO — doit être ignoré, jamais supposé "safe par
// défaut".
func TestDetectRejectsDataDirWithoutLabosurfIdentity(t *testing.T) {
	root := t.TempDir()
	unrelated := filepath.Join(root, "some-other-app-data")
	touch(t, filepath.Join(unrelated, "config.yaml"), "unrelated: true")

	p := tempPaths(t)
	p.DataDir = unrelated
	inv := Detect(p)

	if inv.DataDir != "" {
		t.Fatalf("un répertoire sans identité LABOSURF PRO confirmée ne doit jamais être retenu, obtenu %q", inv.DataDir)
	}
	if len(inv.Warnings) == 0 {
		t.Fatal("un avertissement d'identité non confirmée était attendu")
	}
}

// TestDetectRejectsDataDirWithLabosurfNameButNoMarkers : le nom seul
// ("labosurf" dans le chemin) ne doit JAMAIS suffire — un dossier vide
// nommé "labosurf" ne doit pas être traité comme une installation réelle.
func TestDetectRejectsDataDirWithLabosurfNameButNoMarkers(t *testing.T) {
	root := t.TempDir()
	fakeDir := filepath.Join(root, "labosurf")
	if err := os.MkdirAll(fakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Un fichier quelconque, mais AUCUN marqueur LABOSURF PRO connu.
	touch(t, filepath.Join(fakeDir, "readme.txt"), "just a folder named labosurf")

	p := tempPaths(t)
	p.DataDir = fakeDir
	inv := Detect(p)

	if inv.DataDir != "" {
		t.Fatal("un nom de répertoire imitant 'labosurf' sans aucun marqueur de contenu ne doit jamais suffire")
	}
}

// TestDetectAcceptsGenuineTempInstallation : cas positif de contrôle — un
// répertoire temporaire nommé et structuré comme une vraie installation
// doit, lui, être accepté (sinon toute la fonctionnalité serait inutile).
func TestDetectAcceptsGenuineTempInstallation(t *testing.T) {
	p := tempPaths(t)
	touch(t, filepath.Join(p.DataDir, "users_db.json"), "{}")
	inv := Detect(p)
	if inv.DataDir == "" {
		t.Fatal("une installation légitime dans un répertoire temporaire doit être détectée")
	}
}

// ============================================================
// Plans par option
// ============================================================

func baseInventory(p Paths) Inventory {
	return Inventory{
		Binaries:      []string{filepath.Join(p.BinDir, "labosurf"), filepath.Join(p.BinDir, "labosurf-xray")},
		SystemdUnits:  []string{filepath.Join(p.SystemdDir, "labosurf.service")},
		ThirdPartyDir: p.OptDir,
		ConfigFiles:   []string{filepath.Join(p.DataDir, "config.json")},
		DataDir:       p.DataDir,
		DataFiles:     []string{filepath.Join(p.DataDir, "users_db.json")},
	}
}

func TestBuildPlanProgramOnlyKeepsData(t *testing.T) {
	p := tempPaths(t)
	inv := baseInventory(p)
	plan := BuildPlan(inv, ModeProgramOnly)

	if !containsAll(plan.RemovePaths, inv.Binaries...) {
		t.Fatal("les binaires doivent être supprimés en mode programme seul")
	}
	if !containsAll(plan.RemovePaths, inv.SystemdUnits...) {
		t.Fatal("les unités systemd doivent être supprimées en mode programme seul")
	}
	if !contains(plan.RemovePaths, inv.ThirdPartyDir) {
		t.Fatal("le répertoire tiers doit être supprimé en mode programme seul")
	}
	if !contains(plan.KeepPaths, inv.DataDir) {
		t.Fatal("DataDir doit être conservé en mode programme seul")
	}
	if contains(plan.RemovePaths, inv.DataDir) {
		t.Fatal("DataDir ne doit JAMAIS être dans RemovePaths en mode programme seul")
	}
}

func TestBuildPlanKeepDataRemovesConfigOnly(t *testing.T) {
	p := tempPaths(t)
	inv := baseInventory(p)
	plan := BuildPlan(inv, ModeProgramKeepData)

	if !containsAll(plan.RemovePaths, inv.ConfigFiles...) {
		t.Fatal("les fichiers de configuration doivent être supprimés en mode conservation des données")
	}
	if !containsAll(plan.KeepPaths, inv.DataFiles...) {
		t.Fatal("les fichiers de données doivent être conservés en mode conservation des données")
	}
	if contains(plan.RemovePaths, inv.DataDir) {
		t.Fatal("DataDir entier ne doit pas être supprimé en mode conservation des données")
	}
}

func TestBuildPlanFullRemovesEverything(t *testing.T) {
	p := tempPaths(t)
	inv := baseInventory(p)
	plan := BuildPlan(inv, ModeFull)

	if !contains(plan.RemovePaths, inv.DataDir) {
		t.Fatal("DataDir doit être supprimé en mode complet")
	}
	if len(plan.KeepPaths) != 0 {
		t.Fatalf("rien ne doit être conservé en mode complet, obtenu %v", plan.KeepPaths)
	}
}

// TestBuildPlanNeverExposesUnconfirmedDirectory : si Detect() n'a pas
// confirmé l'identité d'un DataDir (inv.DataDir == ""), BuildPlan ne peut
// matériellement pas le faire apparaître dans RemovePaths — ce test
// documente et verrouille cette garantie structurelle.
func TestBuildPlanNeverExposesUnconfirmedDirectory(t *testing.T) {
	inv := Inventory{} // DataDir et ThirdPartyDir vides, comme après un Detect() qui a tout refusé
	plan := BuildPlan(inv, ModeFull)
	if len(plan.RemovePaths) != 0 {
		t.Fatalf("aucun chemin ne doit être supprimé si Detect n'a rien confirmé, obtenu %v", plan.RemovePaths)
	}
}

func containsAll(hay []string, needles ...string) bool {
	for _, n := range needles {
		if !contains(hay, n) {
			return false
		}
	}
	return true
}
func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// ============================================================
// Chemin inattendu / non autorisé / dangereux (RemovePath)
// ============================================================

func TestRemovePathRejectsUnsafePath(t *testing.T) {
	p := tempPaths(t)
	outside := filepath.Join(t.TempDir(), "innocent-file.txt")
	touch(t, outside, "ne pas supprimer")

	err := RemovePath(outside, p)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("attendu ErrUnsafePath, obtenu : %v", err)
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatal("le fichier hors des répertoires autorisés ne doit jamais être supprimé")
	}
}

func TestRemovePathRejectsRootAndEmpty(t *testing.T) {
	p := Paths{BinDir: "", OptDir: "/", DataDir: "", SystemdDir: ""}
	err := RemovePath("/etc/passwd", p)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("un Paths vide/racine ne doit jamais autoriser une suppression, obtenu : %v", err)
	}
}

func TestRemovePathAllowsPathUnderKnownRoot(t *testing.T) {
	p := tempPaths(t)
	target := filepath.Join(p.BinDir, "labosurf-xray")
	touch(t, target, "x")

	if err := RemovePath(target, p); err != nil {
		t.Fatalf("suppression d'un chemin légitime : %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("le fichier aurait dû être supprimé")
	}
}

// TestRemovePathRejectsDangerousRootsEvenIfConfiguredAsPaths : même si un
// Paths malveillant/mal configuré désigne un répertoire système comme l'un
// de ses champs, RemovePath doit refuser de l'utiliser comme racine — c'est
// la dernière ligne de défense, indépendante de Detect().
func TestRemovePathRejectsDangerousRootsEvenIfConfiguredAsPaths(t *testing.T) {
	dangerousConfigs := []Paths{
		{DataDir: "/etc"},
		{DataDir: "/var"},
		{DataDir: "/usr"},
		{DataDir: "/home"},
		{DataDir: "/root"},
		{OptDir: "/opt"},
		{OptDir: "/"},
		{BinDir: "/bin"},
		{SystemdDir: "/sbin"},
	}
	for _, p := range dangerousConfigs {
		// On tente de supprimer EXACTEMENT le répertoire système configuré.
		var target string
		switch {
		case p.DataDir != "":
			target = p.DataDir
		case p.OptDir != "":
			target = p.OptDir
		case p.BinDir != "":
			target = p.BinDir
		case p.SystemdDir != "":
			target = p.SystemdDir
		}
		if err := RemovePath(target, p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("RemovePath(%q, %+v) : attendu ErrUnsafePath, obtenu %v", target, p, err)
		}
	}
}

func TestRemovePathRejectsRelativeCandidate(t *testing.T) {
	p := tempPaths(t)
	if err := RemovePath("labosurf-xray", p); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("un chemin candidat relatif doit être refusé, obtenu : %v", err)
	}
}

func TestRemovePathRejectsPathTraversalOutOfRoot(t *testing.T) {
	p := tempPaths(t)
	// Construit un chemin qui, une fois nettoyé, sort de BinDir.
	escape := filepath.Join(p.BinDir, "..", "..", "etc", "passwd")
	if err := RemovePath(escape, p); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("une tentative de traversée hors de la racine doit être refusée, obtenu : %v", err)
	}
}

// ============================================================
// Arrêt propre des services (CommandRunner factice)
// ============================================================

func TestStopUnitsCallsSystemctlForEachUnitOnly(t *testing.T) {
	var calls [][]string
	fake := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}
	units := []string{"/etc/systemd/system/labosurf.service", "/etc/systemd/system/labosurf-xray.service"}
	errs := StopUnits(units, fake)
	if len(errs) != 0 {
		t.Fatalf("aucune erreur attendue, obtenu %v", errs)
	}
	if len(calls) != 2 {
		t.Fatalf("attendu 2 appels systemctl stop, obtenu %d : %v", len(calls), calls)
	}
	for _, c := range calls {
		if c[0] != "systemctl" || c[1] != "stop" {
			t.Errorf("appel inattendu : %v", c)
		}
	}
}

func TestStopUnitsSurfacesErrors(t *testing.T) {
	fake := func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("service introuvable")
	}
	errs := StopUnits([]string{"/etc/systemd/system/labosurf.service"}, fake)
	if len(errs) != 1 {
		t.Fatalf("une erreur attendue, obtenu %v", errs)
	}
}

func TestIsUnitActive(t *testing.T) {
	activeRunner := func(name string, args ...string) ([]byte, error) { return []byte("active\n"), nil }
	inactiveRunner := func(name string, args ...string) ([]byte, error) { return []byte("inactive\n"), nil }
	if !IsUnitActive("labosurf.service", activeRunner) {
		t.Fatal("attendu actif")
	}
	if IsUnitActive("labosurf.service", inactiveRunner) {
		t.Fatal("attendu inactif")
	}
}

// ============================================================
// Sauvegarde réussie / échouée / tronquée
// ============================================================

func TestBackupRoundTrip(t *testing.T) {
	p := tempPaths(t)
	touch(t, filepath.Join(p.DataDir, "users_db.json"), `{"accounts":[]}`)
	touch(t, filepath.Join(p.DataDir, "engines", "xray", "reality.json"), `{}`)

	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := CreateBackup(p.DataDir, archive); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if err := VerifyBackup(archive); err != nil {
		t.Fatalf("VerifyBackup: %v", err)
	}
	if info, err := os.Stat(archive); err != nil || info.Size() == 0 {
		t.Fatal("l'archive doit exister et être non vide")
	}
}

func TestBackupFailsOnMissingSourceDir(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	err := CreateBackup(filepath.Join(t.TempDir(), "n-existe-pas"), archive)
	if err == nil {
		t.Fatal("attendu une erreur : répertoire source absent")
	}
	if _, statErr := os.Stat(archive); statErr == nil {
		t.Fatal("aucune archive ne doit être créée si la source est absente")
	}
}

func TestVerifyBackupRejectsCorruptArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	touch(t, archive, "ceci n'est pas une archive gzip valide")
	if err := VerifyBackup(archive); err == nil {
		t.Fatal("attendu une erreur pour une archive corrompue")
	}
}

// TestVerifyBackupRejectsTruncatedArchive : construit une archive valide
// PUIS la tronque — VerifyBackup doit détecter la troncature même si le
// tout premier en-tête tar était parfaitement lisible (c'était le trou de
// l'ancienne implémentation, qui ne lisait que la première entrée).
func TestVerifyBackupRejectsTruncatedArchive(t *testing.T) {
	p := tempPaths(t)
	touch(t, filepath.Join(p.DataDir, "users_db.json"), `{"accounts":[1,2,3,4,5,6,7,8,9,10]}`)
	touch(t, filepath.Join(p.DataDir, "services", "svc1.json"), `{"id":"svc1","name":"un service avec un peu de contenu"}`)

	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := CreateBackup(p.DataDir, archive); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if err := VerifyBackup(archive); err != nil {
		t.Fatalf("l'archive complète doit être valide avant troncature : %v", err)
	}

	full, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) < 20 {
		t.Fatal("archive de test trop petite pour être tronquée de façon significative")
	}
	truncated := full[:len(full)-20] // coupe les 20 derniers octets (fin de flux gzip)
	if err := os.WriteFile(archive, truncated, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := VerifyBackup(archive); err == nil {
		t.Fatal("attendu une erreur : archive tronquée en fin de fichier")
	}
}

func TestBackupFailureBlocksRemoval(t *testing.T) {
	var removedCalls []string
	failingBackup := func() error { return errors.New("disque plein") }
	remove := func(path string) error {
		removedCalls = append(removedCalls, path)
		return nil
	}

	backedUp, _, err := ExecuteWithOptionalBackup([]string{"/a", "/b"}, true, failingBackup, remove)
	if err == nil {
		t.Fatal("attendu une erreur : sauvegarde échouée")
	}
	if backedUp {
		t.Fatal("backedUp doit rester false si la sauvegarde échoue")
	}
	if len(removedCalls) != 0 {
		t.Fatalf("aucune suppression ne doit être tentée si la sauvegarde échoue, obtenu %v", removedCalls)
	}
}

func TestBackupSuccessAllowsRemoval(t *testing.T) {
	var removedCalls []string
	okBackup := func() error { return nil }
	remove := func(path string) error {
		removedCalls = append(removedCalls, path)
		return nil
	}

	backedUp, outcome, err := ExecuteWithOptionalBackup([]string{"/a", "/b"}, true, okBackup, remove)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !backedUp {
		t.Fatal("backedUp doit être true après une sauvegarde réussie")
	}
	if len(outcome.Removed) != 2 {
		t.Fatalf("attendu 2 chemins supprimés, obtenu %v", outcome.Removed)
	}
}

// ============================================================
// Rollback / arrêt sur erreur (pas de succès partiel silencieux)
// ============================================================

func TestExecuteRemovalsStopsOnFirstError(t *testing.T) {
	paths := []string{"/a", "/b", "/c"}
	remove := func(path string) error {
		if path == "/b" {
			return errors.New("permission refusée")
		}
		return nil
	}
	outcome := ExecuteRemovals(paths, remove)

	if len(outcome.Removed) != 1 || outcome.Removed[0] != "/a" {
		t.Fatalf("attendu ['/a'] supprimé avant l'échec, obtenu %v", outcome.Removed)
	}
	if outcome.FailedAt != "/b" {
		t.Fatalf("attendu échec sur /b, obtenu %q", outcome.FailedAt)
	}
	if len(outcome.Remaining) != 1 || outcome.Remaining[0] != "/c" {
		t.Fatalf("attendu ['/c'] jamais tenté, obtenu %v", outcome.Remaining)
	}
	if outcome.Err == nil {
		t.Fatal("Err doit être renseigné")
	}
}

func TestExecuteRemovalsAllSucceed(t *testing.T) {
	paths := []string{"/a", "/b"}
	remove := func(path string) error { return nil }
	outcome := ExecuteRemovals(paths, remove)
	if len(outcome.Removed) != 2 || outcome.Err != nil || len(outcome.Remaining) != 0 {
		t.Fatalf("attendu succès complet, obtenu %+v", outcome)
	}
}

// ============================================================
// Auto-désinstallation
// ============================================================

func TestIsPathCurrentExecutableMatchesSelf(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("os.Executable indisponible sur cette plateforme de test")
	}
	if !IsPathCurrentExecutable(exe) {
		t.Fatal("le binaire de test courant doit être reconnu comme exécutable courant")
	}
}

func TestIsPathCurrentExecutableRejectsOtherPath(t *testing.T) {
	other := filepath.Join(t.TempDir(), "autre-binaire")
	touch(t, other, "x")
	if IsPathCurrentExecutable(other) {
		t.Fatal("un chemin différent ne doit jamais être reconnu comme le binaire courant")
	}
}

// TestSpawnSelfDeleteHelperDeletesAfterWatchedProcessExits exerce le VRAI
// mécanisme (script shell réellement lancé), Linux uniquement, sans jamais
// toucher à une vraie installation : la cible est un fichier factice dans
// un répertoire temporaire, et le "processus courant" simulé est un
// processus jetable (`sleep`) dont le test contrôle entièrement le cycle de
// vie.
func TestSpawnSelfDeleteHelperDeletesAfterWatchedProcessExits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("mécanisme d'auto-suppression : Linux uniquement")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "fake-binary")
	touch(t, target, "contenu factice")

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("lancement du processus factice : %v", err)
	}
	watchedPID := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	if err := spawnSelfDeleteHelperForPID(watchedPID, target); err != nil {
		t.Fatalf("spawnSelfDeleteHelperForPID: %v", err)
	}

	// Tant que le processus surveillé tourne, le fichier doit rester intact.
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(target); err != nil {
		t.Fatal("le fichier ne doit pas être supprimé avant la fin du processus surveillé")
	}

	// On termine le processus surveillé volontairement, avant son sleep naturel.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("arrêt du processus factice : %v", err)
	}
	_ = cmd.Wait()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return // succès : le helper a bien supprimé la cible
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("le fichier cible n'a pas été supprimé après la fin du processus surveillé (helper inopérant ?)")
}

func TestSpawnSelfDeleteHelperRefusesOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("ce test vérifie le refus explicite sur les plateformes non-Linux")
	}
	err := SpawnSelfDeleteHelper(filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Fatal("attendu une erreur explicite hors Linux, jamais un mécanisme silencieusement inopérant")
	}
}
