package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ============================================================
// TESTS DU NOUVEAU MODÈLE — La licence ouvre l'INSTALLATION
// ============================================================
//
// 1 clé = 1 installation, dans les 3h. Le serveur tourne ensuite
// librement : AUCUN contrôle de licence au démarrage (pas de
// checkLicense, pas d'activation.json, pas de machine.id).
//
// Ces tests garantissent le flux installateur :
// verify (signature + fenêtre) -> reçu d'installation -> 2e usage refusé.

// signExpiredToken construit et signe un jeton DÉJÀ hors délai avec
// la clé de test. Utilisé pour vérifier le refus des clés périmées.
func signExpiredToken(t *testing.T, id string) string {
	t.Helper()

	data := LicenseData{
		ID:              id,
		Key:             "LABOSURFEXPIRED_TEST_KEY_123456789012345",
		IssuedAt:        time.Now().UTC().Add(-4 * time.Hour).Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	payload, err := canonicalPayload(data)
	if err != nil {
		t.Fatalf("canonicalPayload : %v", err)
	}

	sig := ed25519.Sign(testSignKey, payload)

	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

// --- Une licence valide ouvre UNE installation ---

func TestInstall_ValidTokenOpensInstall(t *testing.T) {
	dir := t.TempDir()

	token, lic, err := CreateLicense("INSTALL-OK", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	data, err := UseLicense(token, dir, nil)
	if err != nil {
		t.Fatalf("UseLicense : %v", err)
	}
	if data.ID != lic.Data.ID {
		t.Fatalf("ID attendu %q, obtenu %q", lic.Data.ID, data.ID)
	}

	recs, err := ListReceipts(dir)
	if err != nil || len(recs) != 1 || recs[0].LicenseID != "INSTALL-OK" {
		t.Fatalf("reçu attendu pour INSTALL-OK, obtenu %+v, err=%v", recs, err)
	}
}

// --- Le serveur n'exige rien : un jeton valide se vérifie sans installation ---

func TestInstall_ServerNeedsNoLicense(t *testing.T) {
	token, _, err := CreateLicense("FREE-SERVER", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	// La vérification seule suffit : le serveur démarre librement,
	// sans reçu, sans activation, sans machine.id.
	if _, _, err := VerifyLicenseToken(token); err != nil {
		t.Fatalf("VerifyLicenseToken : %v", err)
	}
}

// --- Fenêtre de 3h dépassée : installation refusée, aucun reçu ---

func TestInstall_ExpiredWindowRefused(t *testing.T) {
	dir := t.TempDir()

	expiredToken := signExpiredToken(t, "INSTALL-EXPIRED")

	if _, err := UseLicense(expiredToken, dir, nil); err != ErrLicenseExpired {
		t.Fatalf("clé périmée doit être ErrLicenseExpired, obtenu : %v", err)
	}

	recs, _ := ListReceipts(dir)
	if len(recs) != 0 {
		t.Fatalf("aucun reçu ne doit exister, obtenu %d", len(recs))
	}
}

// --- Usage unique : la même clé ne rouvre pas une 2e installation ---

func TestInstall_ReuseRefused(t *testing.T) {
	dir := t.TempDir()

	token, _, err := CreateLicense("INSTALL-ONCE", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	if _, err := UseLicense(token, dir, nil); err != nil {
		t.Fatalf("1ère utilisation : %v", err)
	}

	if _, err := UseLicense(token, dir, nil); err != ErrAlreadyUsed {
		t.Fatalf("2e utilisation doit être ErrAlreadyUsed, obtenu : %v", err)
	}
}

// --- Révocation locale : l'administrateur peut bloquer un ID ---

func TestInstall_RevokedRefused(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "licenses.json")

	token, lic, err := CreateLicense("INSTALL-REVOKE", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	reg, err := LoadLicenseRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadLicenseRegistry : %v", err)
	}
	if err := reg.Add(lic.Data, token); err != nil {
		t.Fatalf("Add : %v", err)
	}
	if err := reg.Revoke("INSTALL-REVOKE"); err != nil {
		t.Fatalf("Revoke : %v", err)
	}

	if _, err := UseLicense(token, dir, reg); err != ErrLicenseRevoked {
		t.Fatalf("clé révoquée doit être ErrLicenseRevoked, obtenu : %v", err)
	}
}

// --- Jeton altéré : refusé avant tout reçu ---

func TestInstall_TamperedRefused(t *testing.T) {
	dir := t.TempDir()

	token, _, err := CreateLicense("INSTALL-TAMPER", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	badToken := token[:len(token)-4] + "AAAA"
	if _, err := UseLicense(badToken, dir, nil); err == nil {
		t.Fatal("jeton altéré accepté")
	}
}

// --- Le client ne possède PAS de clé privée de signature ---

func TestClientHasNoEmbeddedSigningKey(t *testing.T) {
	savedSign := testSignKey
	testSignKey = nil
	defer func() { testSignKey = savedSign }()

	oldPriv := os.Getenv("LABOSURF_LICENSE_PRIVKEY")
	_ = os.Unsetenv("LABOSURF_LICENSE_PRIVKEY")
	defer func() { _ = os.Setenv("LABOSURF_LICENSE_PRIVKEY", oldPriv) }()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd : %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir temp : %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()

	if _, err := resolveSignKey(); err == nil {
		t.Fatal("SÉCURITÉ : aucune clé privée ne doit être disponible côté client par défaut")
	}
}

// --- Aucune clé privée embarquée en dur dans le binaire ---

func TestNoEmbeddedPrivateKeyConstant(t *testing.T) {
	// La constante de clé publique embarquée est autorisée (non secrète)
	// mais doit être vide par défaut ; aucune clé privée embarquée ne doit
	// exister. On vérifie qu'il n'existe pas de clé publique embarquée
	// résiduelle qui trahirait un secret laissé en dur.
	if embeddedVerifyKeyHex != "" {
		t.Errorf("clé publique embarquée non vide par défaut : %q", embeddedVerifyKeyHex)
	}
}
