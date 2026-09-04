package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempLicensePaths(t *testing.T) (registry, activation, machine string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "licenses.json"),
		filepath.Join(dir, "activation.json"),
		filepath.Join(dir, "machine.id")
}

func TestLicenseKeygen(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "admin.key")
	pub := filepath.Join(dir, "pub.key")

	if err := licenseKeygen([]string{"-priv", priv, "-pub", pub, "-force"}); err != nil {
		t.Fatalf("licenseKeygen : %v", err)
	}

	privRaw, err := os.ReadFile(priv)
	if err != nil {
		t.Fatalf("lecture clé privée : %v", err)
	}
	pubRaw, err := os.ReadFile(pub)
	if err != nil {
		t.Fatalf("lecture clé publique : %v", err)
	}

	privHex := strings.TrimSpace(string(privRaw))
	pubHex := strings.TrimSpace(string(pubRaw))
	if len(privHex) != 128 { // 32 octets privé + 32 octets public, hex => 128
		t.Fatalf("clé privée hex de taille %d attendue 128", len(privHex))
	}
	if len(pubHex) != 64 {
		t.Fatalf("clé publique hex de taille %d attendue 64", len(pubHex))
	}
}

func TestLicenseKeygenRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "admin.key")
	pub := filepath.Join(dir, "pub.key")

	f, err := os.Create(priv)
	if err != nil {
		t.Fatal(err)
	}
	f.Close() // indispensable sous Windows : le fichier doit être libéré pour le rename

	err = licenseKeygen([]string{"-priv", priv, "-pub", pub})
	if err == nil {
		t.Fatal("licenseKeygen doit refuser d'écraser sans -force")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Fatalf("message -force attendu, obtenu %v", err)
	}

	if err := licenseKeygen([]string{"-priv", priv, "-pub", pub, "-force"}); err != nil {
		t.Fatalf("licenseKeygen -force : %v", err)
	}
}

func TestLicenseCreateAndRegistry(t *testing.T) {
	registry, _, _ := tempLicensePaths(t)

	if err := licenseCreate([]string{"-registry", registry, "-id", "LIC-001", "-comment", "test"}); err != nil {
		t.Fatalf("licenseCreate : %v", err)
	}

	reg, err := LoadLicenseRegistry(registry)
	if err != nil {
		t.Fatalf("LoadLicenseRegistry : %v", err)
	}
	entries := reg.List()
	if len(entries) != 1 {
		t.Fatalf("1 entrée attendue, obtenu %d", len(entries))
	}
	if entries[0].ID != "LIC-001" {
		t.Fatalf("ID attendu LIC-001, obtenu %q", entries[0].ID)
	}
	if entries[0].Status != LicenseNew {
		t.Fatalf("statut NEW attendu, obtenu %q", entries[0].Status)
	}
	if entries[0].Token == "" {
		t.Fatal("le jeton doit être enregistré")
	}
}

func TestLicenseCreateMissingID(t *testing.T) {
	registry, _, _ := tempLicensePaths(t)
	if err := licenseCreate([]string{"-registry", registry}); err == nil {
		t.Fatal("licenseCreate doit refuser un -id absent")
	}
}

func TestLicenseListEmpty(t *testing.T) {
	registry, _, _ := tempLicensePaths(t)
	if err := licenseList([]string{"-registry", registry}); err != nil {
		t.Fatalf("licenseList (vide) : %v", err)
	}
}

func TestLicenseListNonEmpty(t *testing.T) {
	registry, _, _ := tempLicensePaths(t)
	licenseCreate([]string{"-registry", registry, "-id", "LIC-001"})

	if err := licenseList([]string{"-registry", registry}); err != nil {
		t.Fatalf("licenseList : %v", err)
	}
}

func TestLicenseRevoke(t *testing.T) {
	registry, _, _ := tempLicensePaths(t)
	licenseCreate([]string{"-registry", registry, "-id", "LIC-001"})

	if err := licenseRevoke([]string{"-registry", registry, "-id", "LIC-001"}); err != nil {
		t.Fatalf("licenseRevoke : %v", err)
	}

	reg, _ := LoadLicenseRegistry(registry)
	entries := reg.List()
	if entries[0].Status != LicenseRevoked {
		t.Fatalf("statut REVOKED attendu, obtenu %q", entries[0].Status)
	}

	if err := licenseRevoke([]string{"-registry", registry, "-id", "LIC-ABSENT"}); err == nil {
		t.Fatal("licenseRevoke doit échouer pour une licence absente")
	}
}

func TestLicenseRevokeMissingID(t *testing.T) {
	registry, _, _ := tempLicensePaths(t)
	if err := licenseRevoke([]string{"-registry", registry}); err == nil {
		t.Fatal("licenseRevoke doit refuser un -id absent")
	}
}

func TestLicenseFullLifecycleCLI(t *testing.T) {
	registry, activation, machine := tempLicensePaths(t)

	// 1. Émission côté administrateur.
	if err := licenseCreate([]string{"-registry", registry, "-id", "LIC-CLI-1"}); err != nil {
		t.Fatalf("licenseCreate : %v", err)
	}

	// Le jeton doit être récupérable depuis le registre.
	reg, _ := LoadLicenseRegistry(registry)
	entries := reg.List()
	token := entries[0].Token

	// 2. Activation côté utilisateur (VPS).
	if err := licenseActivate([]string{
		"-registry", registry,
		"-activation", activation,
		"-machine", machine,
		"-token", token,
	}); err != nil {
		t.Fatalf("licenseActivate : %v", err)
	}

	// 3. Statut : licence active.
	if err := licenseStatus([]string{
		"-registry", registry,
		"-activation", activation,
		"-machine", machine,
	}); err != nil {
		t.Fatalf("licenseStatus : %v", err)
	}

	// 4. La réactivation de la même licence doit être refusée.
	if err := licenseActivate([]string{
		"-registry", registry,
		"-activation", activation,
		"-machine", machine,
		"-token", token,
	}); err == nil {
		t.Fatal("la réactivation de la même licence doit être refusée")
	}

	// 5. Vérification du jeton.
	if err := licenseVerify([]string{"-token", token}); err != nil {
		t.Fatalf("licenseVerify : %v", err)
	}

	// 6. Désactivation locale.
	if err := licenseDeactivate([]string{"-activation", activation, "-machine", machine}); err != nil {
		t.Fatalf("licenseDeactivate : %v", err)
	}

	// 7. Statut après désactivation : erreur attendue.
	if err := licenseStatus([]string{
		"-registry", registry,
		"-activation", activation,
		"-machine", machine,
	}); err == nil {
		t.Fatal("licenseStatus doit échouer après désactivation")
	}
}

func TestLicenseActivateErrorsNoToken(t *testing.T) {
	_, activation, machine := tempLicensePaths(t)
	err := licenseActivate([]string{"-activation", activation, "-machine", machine})
	if err == nil {
		t.Fatal("licenseActivate doit exiger un jeton")
	}
	if !strings.Contains(err.Error(), "-token") {
		t.Fatalf("message -token attendu, obtenu %v", err)
	}
}

func TestLicenseActivateViaFile(t *testing.T) {
	registry, activation, machine := tempLicensePaths(t)
	tokenFile := filepath.Join(t.TempDir(), "token.txt")

	licenseCreate([]string{"-registry", registry, "-id", "LIC-FILE-1", "-out", tokenFile})

	if err := licenseActivate([]string{
		"-registry", registry,
		"-activation", activation,
		"-machine", machine,
		"-file", tokenFile,
	}); err != nil {
		t.Fatalf("licenseActivate (-file) : %v", err)
	}
}

func TestLicenseDeactivateWithoutActivation(t *testing.T) {
	_, activation, machine := tempLicensePaths(t)
	if err := licenseDeactivate([]string{"-activation", activation, "-machine", machine}); err != nil {
		t.Fatalf("licenseDeactivate sans activation doit être sans erreur : %v", err)
	}
}

func TestLicenseVerifyTampered(t *testing.T) {
	token, _, err := CreateLicense("LIC-TAMPER", "t")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	// Altérer un caractère au MILIEU de la partie signature du jeton.
	//
	// Pourquoi pas le dernier caractère ? Une signature Ed25519 fait 64 octets
	// soit 86 caractères base64url. Le dernier caractère n'encode que 2 bits
	// utiles : le modifier peut laisser les octets décodés identiques, ce qui
	// rendait ce test intermittent (flaky). Un caractère au milieu de la
	// signature modifie toujours les octets réellement vérifiés.
	sep := strings.Index(token, ".")
	if sep < 0 || len(token)-sep < 8 {
		t.Fatalf("format de jeton inattendu : %q", token)
	}
	mid := sep + 1 + (len(token)-sep-1)/2 // milieu de la partie signature
	c := token[mid]
	repl := byte('A')
	if c == 'A' {
		repl = 'B'
	}
	tampered := token[:mid] + string(repl) + token[mid+1:]
	if tampered == token {
		t.Fatal("l'altération n'a pas modifié le jeton")
	}

	// Un caractère altéré dans la signature doit produire un statut TAMPERED.
	err = licenseVerify([]string{"-token", tampered})
	if err == nil {
		t.Fatal("licenseVerify doit échouer sur un jeton altéré")
	}
}

func TestLicenseStatusWithoutActivation(t *testing.T) {
	_, activation, machine := tempLicensePaths(t)
	if err := licenseStatus([]string{"-activation", activation, "-machine", machine}); err == nil {
		t.Fatal("licenseStatus sans activation doit retourner une erreur")
	}
}

func TestReadTokenSources(t *testing.T) {
	if _, err := readToken("   ", ""); err == nil {
		t.Fatal("readToken doit exiger une source")
	}

	if got, err := readToken("  TOKEN  ", ""); err != nil || got != "TOKEN" {
		t.Fatalf("readToken (-token) : %q, %v", got, err)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "token.txt")
	if err := os.WriteFile(file, []byte("  TOKEN-FILE\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, err := readToken("", file); err != nil || got != "TOKEN-FILE" {
		t.Fatalf("readToken (-file) : %q, %v", got, err)
	}

	if _, err := readToken("", filepath.Join(dir, "absent.txt")); err == nil {
		t.Fatal("readToken doit échouer sur un fichier absent")
	}
}

func TestRunLicenseDispatch(t *testing.T) {
	if err := runLicense([]string{"help"}); err != nil {
		t.Fatalf("runLicense help : %v", err)
	}
	if err := runLicense(nil); err != nil {
		t.Fatalf("runLicense sans argument : %v", err)
	}

	err := runLicense([]string{"bogus"})
	if err == nil {
		t.Fatal("une commande licence inconnue doit retourner une erreur")
	}
}