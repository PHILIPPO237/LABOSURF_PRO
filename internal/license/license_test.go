package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Nouveau modèle : la licence ouvre l'ACCÈS AU SCRIPT D'INSTALLATION
// (1 clé = 1 installation), pas au serveur. Les tests utilisent
// LABOSURF_DATA_DIR isolé par test, jamais de fichiers dans le dépôt.

func useTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LABOSURF_DATA_DIR", dir)
	testVerifyKey = nil
	return dir
}

func makeKeyPair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clés : %v", err)
	}
	return priv, pub
}

func makeToken(t *testing.T, data LicenseData, signKey ed25519.PrivateKey) string {
	t.Helper()
	payload, err := canonicalPayload(data)
	if err != nil {
		t.Fatalf("canonicalPayload : %v", err)
	}
	sig := ed25519.Sign(signKey, payload)
	return encodeActivationKey(payload, sig)
}

// splitActivationKey sépare un jeton "LABOSURF-<payload>@<signature>" en
// ses deux blocs, pour les tests qui doivent altérer l'un des deux.
func splitActivationKey(t *testing.T, token string) (payloadBlock, sigBlock string) {
	t.Helper()
	body := strings.TrimPrefix(token, activationKeyPrefix)
	parts := strings.SplitN(body, "@", 2)
	if len(parts) != 2 {
		t.Fatalf("jeton mal formé pour le test : %q", token)
	}
	return parts[0], parts[1]
}

func validData(id string, window time.Duration) LicenseData {
	now := time.Now().UTC()
	return LicenseData{
		ID:              id,
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        now.Format(time.RFC3339),
		ActivationUntil: now.Add(window).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
		Comment:         "test",
	}
}

func TestVerifyToken_Valid(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	data, err := VerifyToken(makeToken(t, validData("T-VALID", 2*time.Hour), priv))
	if err != nil {
		t.Fatalf("licence valide refusée : %v", err)
	}
	if data.ID != "T-VALID" {
		t.Fatalf("ID attendu T-VALID, obtenu %q", data.ID)
	}
}

func TestVerifyToken_BadSignature(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	token := makeToken(t, validData("T-BAD-SIG", 2*time.Hour), priv)
	payloadBlock, _ := splitActivationKey(t, token)
	badSig := encodeKeyBlock(make([]byte, ed25519.SignatureSize)) // signature à zéro, forcément invalide
	badToken := activationKeyPrefix + payloadBlock + "@" + badSig

	if _, err := VerifyToken(badToken); err == nil {
		t.Fatal("signature corrompue acceptée")
	} else if !strings.Contains(err.Error(), "signature") {
		t.Fatalf("erreur signature attendue, obtenu : %v", err)
	}
}

func TestVerifyToken_TamperedPayload(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	token := makeToken(t, validData("T-TAMPER", 2*time.Hour), priv)
	_, sigBlock := splitActivationKey(t, token)
	payloadBytes, _ := decodeKeyBlock(strings.SplitN(strings.TrimPrefix(token, activationKeyPrefix), "@", 2)[0])
	var d LicenseData
	_ = json.Unmarshal(payloadBytes, &d)
	d.ID = "MODIFIED-ID"
	newPayload, _ := json.Marshal(d)
	tampered := activationKeyPrefix + encodeKeyBlock(newPayload) + "@" + sigBlock

	if _, err := VerifyToken(tampered); err == nil {
		t.Fatal("payload modifié accepté")
	}
}

func TestVerifyToken_WrongKey(t *testing.T) {
	useTempDataDir(t)
	privA, _ := makeKeyPair(t)
	_, pubB := makeKeyPair(t)
	testVerifyKey = pubB

	if _, err := VerifyToken(makeToken(t, validData("T-WRONG-KEY", 2*time.Hour), privA)); err == nil {
		t.Fatal("clé publique incorrecte acceptée")
	}
}

func TestVerifyToken_ExpiredWindow(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	_, err := VerifyToken(makeToken(t, validData("T-EXPIRED", -1*time.Hour), priv))
	if err != ErrLicenseExpired {
		t.Fatalf("fenêtre dépassée doit retourner ErrLicenseExpired, obtenu : %v", err)
	}
}

func TestVerifyToken_WrongProduct(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	d := validData("T-PRODUCT", 2*time.Hour)
	d.Product = "OTHER PRODUCT"
	if _, err := VerifyToken(makeToken(t, d, priv)); err == nil {
		t.Fatal("produit incompatible accepté")
	}
}

func TestVerifyToken_Malformed(t *testing.T) {
	useTempDataDir(t)
	for _, tok := range []string{"", "not-a-token", "onlyonepart", "LABOSURF-", "LABOSURF-ABCDEFGH", "LABOSURF-ABCDEFGH@", "LABOSURF-!!!@ABCDEFGH"} {
		if _, err := VerifyToken(tok); err == nil {
			t.Fatalf("jeton malformé accepté : %q", tok)
		}
	}
}

// TEST 8 (mission) : signature vide/manquante -> refusée. TestVerifyToken_Malformed
// couvre déjà ce cas indirectement ("payload.") ; ce test l'isole explicitement
// en signant un payload légitime puis en tronquant uniquement la signature.
func TestVerifyToken_EmptySignature(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	token := makeToken(t, validData("T-EMPTY-SIG", 2*time.Hour), priv)
	payloadBlock, _ := splitActivationKey(t, token)
	noSig := activationKeyPrefix + payloadBlock + "@"

	if _, err := VerifyToken(noSig); err == nil {
		t.Fatal("signature vide acceptée")
	} else if !strings.Contains(err.Error(), "signature") {
		t.Fatalf("erreur signature attendue, obtenu : %v", err)
	}
}

func TestActivate_Valid(t *testing.T) {
	dir := useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	if err := Activate(makeToken(t, validData("ACT-OK", 2*time.Hour), priv)); err != nil {
		t.Fatalf("Activate : %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".install_ACT-OK.receipt")); err != nil {
		t.Fatalf("reçu manquant : %v", err)
	}
}

func TestActivate_AlreadyUsed(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	token := makeToken(t, validData("ACT-DOUBLE", 2*time.Hour), priv)
	if err := Activate(token); err != nil {
		t.Fatalf("1ère utilisation : %v", err)
	}
	if err := Activate(token); !errors.Is(err, ErrAlreadyUsed) {
		t.Fatalf("2e utilisation doit être ErrAlreadyUsed, obtenu : %v", err)
	}
}

func TestActivate_ExpiredWindow(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	if err := Activate(makeToken(t, validData("ACT-EXP", -1*time.Hour), priv)); err != ErrLicenseExpired {
		t.Fatalf("fenêtre dépassée doit être ErrLicenseExpired, obtenu : %v", err)
	}
}

func TestStatus_Empty(t *testing.T) {
	useTempDataDir(t)
	if err := Status(); err != ErrNoReceipt {
		t.Fatalf("sans reçu, Status doit être ErrNoReceipt, obtenu : %v", err)
	}
}

func TestStatus_AfterActivate(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	if err := Activate(makeToken(t, validData("ACT-STATUS", 2*time.Hour), priv)); err != nil {
		t.Fatalf("Activate : %v", err)
	}
	if err := Status(); err != nil {
		t.Fatalf("Status après installation : %v", err)
	}
}

func TestVerify_Function(t *testing.T) {
	useTempDataDir(t)
	priv, pub := makeKeyPair(t)
	testVerifyKey = pub

	if err := Verify(makeToken(t, validData("VRF-OK", 2*time.Hour), priv)); err != nil {
		t.Fatalf("Verify : %v", err)
	}
	// Verify n'écrit aucun reçu.
	if err := Status(); err != ErrNoReceipt {
		t.Fatal("Verify ne doit pas écrire de reçu")
	}
}

func TestParseLicenseToken(t *testing.T) {
	priv, _ := makeKeyPair(t)
	d := validData("PARSE-1", time.Hour)
	token := makeToken(t, d, priv)

	parsed, sig, err := ParseLicenseToken(token)
	if err != nil {
		t.Fatalf("ParseLicenseToken : %v", err)
	}
	if parsed.ID != "PARSE-1" || len(sig) != ed25519.SignatureSize {
		t.Fatalf("décodage incorrect : %+v", parsed)
	}
}

func TestParseLicenseToken_BadFormat(t *testing.T) {
	for _, tok := range []string{"not-a-token", "onlyonepart", "LABOSURF-", "LABOSURF-ABCDEFGH", "LABOSURF-!!!@ABCDEFGH"} {
		if _, _, err := ParseLicenseToken(tok); err == nil {
			t.Fatalf("format invalide accepté : %q", tok)
		}
	}
}
