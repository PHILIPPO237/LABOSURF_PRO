package main

// Verrouille le correctif de la Phase Audit Licence : labosurf-pro.sh
// invoque exactement `"$BIN_PATH" license verify -token "$token" -print-id`
// (voir activate_license() dans labosurf-pro.sh). Avant correction, "license"
// n'était routé qu'en tant que sous-commande de "engine" (voir
// printRootUsage historique), donc cette invocation exacte échouait
// systématiquement — ces tests reproduisent le format d'appel réel de
// l'installateur, pas une forme simplifiée.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"labosurf/internal/license"
)

// signTestToken construit un jeton LABOSURF PRO valide (même format que
// LABOSURF_LICENSE_MAKER : base64url(payload).base64url(signature)) avec
// une paire de clés ed25519 JETABLE, jamais une clé de production.
func signTestToken(t *testing.T, id string, window time.Duration, priv ed25519.PrivateKey) string {
	t.Helper()
	data := license.LicenseData{
		ID:              id,
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(window).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal payload : %v", err)
	}
	sig := ed25519.Sign(priv, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// captureStdout redirige temporairement os.Stdout pendant l'exécution de fn
// et retourne tout ce qui y a été écrit — nécessaire car runLicenseVerify
// (mode -print-id) écrit directement sur os.Stdout via fmt.Println, exactement
// comme le fait le vrai binaire quand labosurf-pro.sh capture sa sortie via
// $(...).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe : %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("lecture stdout capturé : %v", err)
	}
	return string(out)
}

// TestRunLicenseVerify_TokenFlagAndPrintID reproduit EXACTEMENT l'appel de
// labosurf-pro.sh : license verify -token <jeton> -print-id. La sortie
// stdout doit être UNIQUEMENT l'ID de licence (rien d'autre), car le script
// la capture directement dans une variable shell (id="$(...)").
func TestRunLicenseVerify_TokenFlagAndPrintID(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé : %v", err)
	}
	t.Setenv("LABOSURF_LICENSE_PUBKEY", hex.EncodeToString(pub))

	token := signTestToken(t, "CLI-FLAG-TEST", 2*time.Hour, priv)

	var callErr error
	out := captureStdout(t, func() {
		callErr = runLicenseCmd([]string{"verify", "-token", token, "-print-id"})
	})
	if callErr != nil {
		t.Fatalf("runLicenseCmd(verify -token ... -print-id) : %v", callErr)
	}
	if got := strings.TrimSpace(out); got != "CLI-FLAG-TEST" {
		t.Fatalf("stdout attendu exactement %q (comme capturé par labosurf-pro.sh via $(...)), obtenu %q", "CLI-FLAG-TEST", got)
	}
}

// TestRunLicenseVerify_PositionalTokenStillWorks : la forme historique
// `license verify <token>` (sans flags) doit continuer à fonctionner —
// non-régression du correctif.
func TestRunLicenseVerify_PositionalTokenStillWorks(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé : %v", err)
	}
	t.Setenv("LABOSURF_LICENSE_PUBKEY", hex.EncodeToString(pub))

	token := signTestToken(t, "CLI-POSITIONAL-TEST", 2*time.Hour, priv)

	if err := runLicenseCmd([]string{"verify", token}); err != nil {
		t.Fatalf("runLicenseCmd(verify <token>) : %v", err)
	}
}

// TestRunLicenseVerify_MissingToken : ni -token ni argument positionnel ->
// erreur claire, jamais un jeton vide silencieusement traité.
func TestRunLicenseVerify_MissingToken(t *testing.T) {
	if err := runLicenseCmd([]string{"verify"}); err == nil {
		t.Fatal("verify sans jeton doit échouer")
	}
}

// TestRunLicenseVerify_BadSignatureRejected : le routage CLI ne doit pas
// contourner la vérification cryptographique — un jeton dont la signature
// ne correspond pas à la clé publique configurée doit être rejeté avec un
// code d'erreur (non-zero côté `main`, ici une erreur non-nil).
func TestRunLicenseVerify_BadSignatureRejected(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé signature : %v", err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération autre clé : %v", err)
	}
	t.Setenv("LABOSURF_LICENSE_PUBKEY", hex.EncodeToString(otherPub))

	token := signTestToken(t, "CLI-BADSIG-TEST", 2*time.Hour, priv)

	if err := runLicenseCmd([]string{"verify", "-token", token, "-print-id"}); err == nil {
		t.Fatal("jeton avec mauvaise signature accepté par le routage CLI")
	}
}

// TestRunLicenseActivate_TokenFlag : `license activate -token <jeton>` doit
// fonctionner avec la même syntaxe de flag que verify (cohérence).
func TestRunLicenseActivate_TokenFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LABOSURF_DATA_DIR", dir)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé : %v", err)
	}
	t.Setenv("LABOSURF_LICENSE_PUBKEY", hex.EncodeToString(pub))

	token := signTestToken(t, "CLI-ACTIVATE-TEST", 2*time.Hour, priv)

	if err := runLicenseCmd([]string{"activate", "-token", token}); err != nil {
		t.Fatalf("runLicenseCmd(activate -token ...) : %v", err)
	}
	// Réutiliser le même jeton doit échouer (1 clé = 1 installation).
	if err := runLicenseCmd([]string{"activate", "-token", token}); err == nil {
		t.Fatal("réactivation du même jeton acceptée (viole 1 clé = 1 installation)")
	}
}

// TestRunLicenseCmd_UnknownSubcommand : garde-fou basique.
func TestRunLicenseCmd_UnknownSubcommand(t *testing.T) {
	if err := runLicenseCmd([]string{"bogus"}); err == nil {
		t.Fatal("sous-commande inconnue acceptée")
	}
}
