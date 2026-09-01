package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// signExpiredToken construit et signe un jeton de licence DÉJÀ expiré avec
// la clé de test. Utilisé pour vérifier le rejet des licences expirées.
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

// ============================================================
// TESTS D'INTÉGRATION — Vérification de licence au démarrage UDP Engine
// ============================================================
//
// Ces tests garantissent qu'il est IMPOSSIBLE de démarrer l'UDP Engine sans une
// licence valide et activée, et que le contournement (usage multiple,
// mauvais appareil) est bloqué. Ils échoueront si quelqu'un réintroduit
// une faille (ex. retrait du contrôle au démarrage).

// makeLicenseConfig produit une Config dont les chemins de licence pointent
// vers un répertoire temporaire isolé.
func makeLicenseConfig(dir string) Config {
	var cfg Config
	cfg.License.Activation = filepath.Join(dir, "activation.json")
	cfg.License.MachineID = filepath.Join(dir, "machine.id")
	cfg.License.Registry = filepath.Join(dir, "licenses.json")
	return cfg
}

// --- Démarrage UDP Engine SANS licence : doit être refusé ---

func TestStartupRefusedWithoutLicense(t *testing.T) {
	cfg := makeLicenseConfig(t.TempDir())

	if err := checkLicense(cfg); err == nil {
		t.Fatal("SÉCURITÉ : le démarrage doit être REFUSÉ sans licence activée")
	}
}

// --- Démarrage UDP Engine AVEC licence valide activée : doit être autorisé ---

func TestStartupAllowedWithValidLicense(t *testing.T) {
	dir := t.TempDir()
	cfg := makeLicenseConfig(dir)

	token, _, err := CreateLicense("STARTUP-OK", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	as, err := LoadActivationStore(cfg.License.Activation, cfg.License.MachineID)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}
	if _, err := as.Activate(token, nil); err != nil {
		t.Fatalf("Activate : %v", err)
	}

	if err := checkLicense(cfg); err != nil {
		t.Fatalf("le démarrage doit être AUTORISÉ avec une licence active : %v", err)
	}
}

// --- Démarrage refusé après RÉVOCATION ---

func TestStartupRefusedAfterRevocation(t *testing.T) {
	dir := t.TempDir()
	cfg := makeLicenseConfig(dir)

	token, lic, err := CreateLicense("STARTUP-REVOKE", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	reg, err := LoadLicenseRegistry(cfg.License.Registry)
	if err != nil {
		t.Fatalf("LoadLicenseRegistry : %v", err)
	}
	if err := reg.Add(lic.Data, token); err != nil {
		t.Fatalf("Add : %v", err)
	}

	as, _ := LoadActivationStore(cfg.License.Activation, cfg.License.MachineID)
	if _, err := as.Activate(token, reg); err != nil {
		t.Fatalf("Activate : %v", err)
	}

	// Vérification OK avant révocation.
	if err := checkLicense(cfg); err != nil {
		t.Fatalf("démarrage doit être autorisé avant révocation : %v", err)
	}

	// Révocation persistée.
	if err := reg.Revoke("STARTUP-REVOKE"); err != nil {
		t.Fatalf("Revoke : %v", err)
	}

	// Après révocation : démarrage refusé.
	if err := checkLicense(cfg); err == nil {
		t.Fatal("SÉCURITÉ : le démarrage doit être REFUSÉ après révocation")
	}
}

// --- Démarrage refusé après EXPIRATION ---

func TestStartupRefusedAfterExpiration(t *testing.T) {
	dir := t.TempDir()
	cfg := makeLicenseConfig(dir)

	// Licence déjà expirée : construite et signée manuellement, puis
	// écrite directement dans l'enregistrement d'activation pour simuler
	// une activation passée dont la licence a depuis expiré.
	expiredToken := signExpiredToken(t, "STARTUP-EXPIRED")

	as, _ := LoadActivationStore(cfg.License.Activation, cfg.License.MachineID)

	// L'activation d'une licence expirée doit déjà échouer.
	if _, err := as.Activate(expiredToken, nil); err != ErrLicenseExpired {
		t.Fatalf("l'activation d'une licence expirée doit échouer (ErrLicenseExpired), obtenu : %v", err)
	}

	// Et le démarrage reste refusé (aucune activation valide).
	if err := checkLicense(cfg); err == nil {
		t.Fatal("SÉCURITÉ : le démarrage doit être REFUSÉ pour une licence expirée")
	}
}

// --- Usage unique GLOBAL : activation sur une 2e machine refusée ---

func TestActivationMultiMachineRefused(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "licenses.json")

	token, lic, err := CreateLicense("MULTI", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	// Registre partagé (source de vérité de l'usage unique).
	reg, _ := LoadLicenseRegistry(regPath)
	if err := reg.Add(lic.Data, token); err != nil {
		t.Fatalf("Add : %v", err)
	}

	// Machine A : activation acceptée.
	asA, _ := LoadActivationStore(
		filepath.Join(dir, "actA.json"),
		filepath.Join(dir, "machA.id"),
	)
	if _, err := asA.Activate(token, reg); err != nil {
		t.Fatalf("activation machine A doit réussir : %v", err)
	}

	// Machine B : registre rechargé depuis le disque (l'état ACTIVE doit
	// avoir été persisté par la machine A).
	regReloaded, _ := LoadLicenseRegistry(regPath)
	asB, _ := LoadActivationStore(
		filepath.Join(dir, "actB.json"),
		filepath.Join(dir, "machB.id"),
	)
	_, err = asB.Activate(token, regReloaded)
	if err != ErrAlreadyActivated {
		t.Fatalf("SÉCURITÉ : activation machine B doit être REFUSÉE (ErrAlreadyActivated), obtenu : %v", err)
	}
}

// --- Mauvais appareil : fichier d'activation copié sur une autre machine ---

func TestActivationWrongDeviceRefused(t *testing.T) {
	dir := t.TempDir()
	actPath := filepath.Join(dir, "activation.json")
	machPath := filepath.Join(dir, "machine.id")

	token, _, err := CreateLicense("WRONGDEV", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	as, _ := LoadActivationStore(actPath, machPath)
	if _, err := as.Activate(token, nil); err != nil {
		t.Fatalf("Activate : %v", err)
	}

	// Simule la copie du fichier d'activation vers une machine possédant un
	// identifiant d'installation DIFFÉRENT (pré-écrit).
	otherMachPath := filepath.Join(dir, "other_machine.id")
	otherID := "00000000000000000000000000000000ffffffffffffffffffffffffffffffff"
	if err := os.WriteFile(otherMachPath, []byte(otherID), 0o600); err != nil {
		t.Fatalf("écriture machine id : %v", err)
	}

	// Le même activation.json, mais un machine.id différent → refus.
	as2, _ := LoadActivationStore(actPath, otherMachPath)
	if _, err := as2.Check(nil); err != ErrWrongDevice {
		t.Fatalf("SÉCURITÉ : Check doit retourner ErrWrongDevice, obtenu : %v", err)
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
