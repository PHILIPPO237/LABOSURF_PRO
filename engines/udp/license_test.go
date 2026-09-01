package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// test*Key sont des variables package-level que license.go utilise pour
// l'injection en test (éviter tout accès disque aux clés réelles).
func TestMain(m *testing.M) {
	privHex, pubHex, err := GenerateKeyPair()
	if err != nil {
		panic(err)
	}
	privBytes, _ := hex.DecodeString(privHex)
	pubBytes, _ := hex.DecodeString(pubHex)
	testSignKey = privBytes
	testVerifyKey = pubBytes
	os.Exit(m.Run())
}

func TestLicenseCreateAndVerifyToken(t *testing.T) {
	token, lic, err := CreateLicense("TEST-001", 30, 10, "test licence")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	if lic.Data.ID != "TEST-001" {
		t.Fatalf("ID attendu TEST-001, obtenu %q", lic.Data.ID)
	}
	if lic.Data.Product != "LABOSURF PRO" {
		t.Fatalf("produit attendu LABOSURF PRO, obtenu %q", lic.Data.Product)
	}
	if lic.Data.MaxUsers != 10 {
		t.Fatalf("MaxUsers attendu 10, obtenu %d", lic.Data.MaxUsers)
	}
	if token == "" {
		t.Fatal("le jeton ne doit pas être vide")
	}

	// Vérification par jeton.
	data, status, err := VerifyLicenseToken(token)
	if err != nil {
		t.Fatalf("VerifyLicenseToken : %v", err)
	}
	if status != LicenseActive {
		t.Fatalf("statut actif attendu, obtenu %q", status)
	}
	if data.ID != "TEST-001" {
		t.Fatalf("ID vérifié attendu TEST-001, obtenu %q", data.ID)
	}
}

func TestLicenseExpired(t *testing.T) {
	// Construire manuellement une licence expirée pour le test.
	past := time.Now().UTC().Add(-60 * 24 * time.Hour)
	expired := time.Now().UTC().Add(-1 * time.Hour)

	data := LicenseData{
		ID:        "EXPIRED",
		IssuedAt:  past.Format(time.RFC3339),
		ExpiresAt: expired.Format(time.RFC3339),
		Product:   "LABOSURF PRO",
		MaxUsers:  5,
	}

	payload, _ := canonicalPayload(data)
	sig := ed25519.Sign(testSignKey, payload)

	token := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)

	_, status, _ := VerifyLicenseToken(token)
	if status != LicenseExpired {
		t.Fatalf("statut expiré attendu, obtenu %q", status)
	}
}

func TestLicenseTampered(t *testing.T) {
	token, _, _ := CreateLicense("TAMPER", 30, 5, "")

	// Corriger la signature (toujours la 2e partie du jeton).
	parts := splitTokenParts(token)
	if len(parts) < 2 {
		t.Fatal("format de jeton invalide")
	}
	parts[1] = "deadbeef0000000000000000000000000000000000000000000000000000dead"
	tampered := parts[0] + "." + parts[1]

	_, status, _ := VerifyLicenseToken(tampered)
	if status != LicenseTampered {
		t.Fatalf("statut altéré attendu, obtenu %q", status)
	}
}

func TestLicenseNoExpiry(t *testing.T) {
	token, _, err := CreateLicense("NOEXPIRY", 0, 5, "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	data, status, _ := VerifyLicenseToken(token)
	if status != LicenseActive {
		t.Fatalf("licence sans expiration doit être active, obtenu %q", status)
	}
	if data.ExpiresAt != "" {
		t.Fatalf("ExpiresAt doit être vide pour illimité, obtenu %q", data.ExpiresAt)
	}
}

func TestLicenseInvalidID(t *testing.T) {
	if _, _, err := CreateLicense("", 30, 5, ""); err == nil {
		t.Fatal("ID vide doit provoquer une erreur")
	}
}

func TestLicenseZeroDuration(t *testing.T) {
	// Zero = illimité, pas une erreur.
	token, _, err := CreateLicense("ZERO", 0, 5, "")
	if err != nil {
		t.Fatalf("duration=0 doit être valide (illimité) : %v", err)
	}

	_, status, _ := VerifyLicenseToken(token)
	if status != LicenseActive {
		t.Fatalf("licence illimitée doit être active, obtenu %q", status)
	}
}

func TestLicenseGenerateKeyPair(t *testing.T) {
	priv1, pub1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair : %v", err)
	}
	priv2, pub2, _ := GenerateKeyPair()

	if priv1 == priv2 || pub1 == pub2 {
		t.Fatal("deux paires doivent être distinctes")
	}
	if len(priv1) == 0 || len(pub1) == 0 {
		t.Fatal("les clés ne doivent pas être vides")
	}
}

func TestLicenseEmptyToken(t *testing.T) {
	_, _, err := VerifyLicenseToken("")
	if err == nil {
		t.Fatal("jeton vide doit provoquer une erreur")
	}
}

func TestLicenseBadFormat(t *testing.T) {
	_, _, err := VerifyLicenseToken("not-a-valid-token")
	if err == nil {
		t.Fatal("jeton mal formaté doit provoquer une erreur")
	}
}

func TestLicenseActivation(t *testing.T) {
	// Nettoyer les fichiers temporaires.
	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	token, lic, err := CreateLicense("ACTIV", 30, 5, "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	as, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	res, err := as.Activate(token, nil)
	if err != nil {
		t.Fatalf("Activate : %v", err)
	}

	if !res.Activated {
		t.Fatal("l'activation doit réussir")
	}
	if res.Data.ID != lic.Data.ID {
		t.Fatalf("ID activation attendu %q, obtenu %q", lic.Data.ID, res.Data.ID)
	}

	// Vérification persistance.
	CheckAs, _ := LoadActivationStore(actPath, machinePath)
	checkRes, err := CheckAs.Check(nil)
	if err != nil {
		t.Fatalf("Check après activation : %v", err)
	}
	if !checkRes.Activated {
		t.Fatal("Check doit confirmer l'activation")
	}
}

func TestLicenseAlreadyActivated(t *testing.T) {
	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	token, _, _ := CreateLicense("DOUBLE", 30, 5, "")

	as, _ := LoadActivationStore(actPath, machinePath)

	// 1ère activation : OK.
	_, err := as.Activate(token, nil)
	if err != nil {
		t.Fatalf("1ère activation : %v", err)
	}

	// 2ème activation de la même licence : ErrAlreadyActivated.
	_, err = as.Activate(token, nil)
	if err != ErrAlreadyActivated {
		t.Fatalf("2ème activation doit retourner ErrAlreadyActivated, obtenu %v", err)
	}
}

func TestLicenseActivationPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	token, _, _ := CreateLicense("PERSIST-ACT", 30, 5, "")

	// Activer.
	as1, _ := LoadActivationStore(actPath, machinePath)
	_, err := as1.Activate(token, nil)
	if err != nil {
		t.Fatalf("Activate : %v", err)
	}

	// Recharger depuis le disque.
	as2, _ := LoadActivationStore(actPath, machinePath)
	res, err := as2.Check(nil)
	if err != nil {
		t.Fatalf("Check après rechargement : %v", err)
	}
	if !res.Activated {
		t.Fatal("l'activation doit survivre au rechargement")
	}
	if res.Data.ID != "PERSIST-ACT" {
		t.Fatalf("ID attendu PERSIST-ACT, obtenu %q", res.Data.ID)
	}
}

func TestLicenseDeactivate(t *testing.T) {
	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	token, _, _ := CreateLicense("DEACT", 30, 5, "")

	as, _ := LoadActivationStore(actPath, machinePath)
	_, _ = as.Activate(token, nil)

	// Désactiver.
	if err := as.Deactivate(); err != nil {
		t.Fatalf("Deactivate : %v", err)
	}

	// Vérifier que l'activation a disparu.
	_, err := as.Check(nil)
	if err != ErrActivationMissing {
		t.Fatalf("Check après désactivation doit retourner ErrActivationMissing, obtenu %v", err)
	}
}

func TestLicenseRegistryRevoke(t *testing.T) {
	tmpDir := t.TempDir()
	regPath := filepath.Join(tmpDir, "licenses.json")
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	token, _, err := CreateLicense("REG-REVOKE", 30, 5, "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	// Enregistrer dans le registre.
	reg, _ := LoadLicenseRegistry(regPath)
	_ = reg.Add(LicenseData{ID: "REG-REVOKE"}, token)

	// Révoquer.
	if err := reg.Revoke("REG-REVOKE"); err != nil {
		t.Fatalf("Revoke : %v", err)
	}
	if !reg.IsRevoked("REG-REVOKE") {
		t.Fatal("la licence doit être marquée révoquée")
	}

	// Activer avec registre : doit échouer.
	as, _ := LoadActivationStore(actPath, machinePath)
	_, err = as.Activate(token, reg)
	if err != ErrLicenseRevoked {
		t.Fatalf("activation de licence révoquée doit échouer : %v", err)
	}
}

func TestLicenseRegistryList(t *testing.T) {
	tmpDir := t.TempDir()
	regPath := filepath.Join(tmpDir, "licenses.json")

	reg, _ := LoadLicenseRegistry(regPath)

	// Ajouter deux licences.
	token1, _, _ := CreateLicense("LIST-A", 30, 5, "")
	token2, _, _ := CreateLicense("LIST-B", 60, 10, "")
	_ = reg.Add(LicenseData{ID: "LIST-A"}, token1)
	_ = reg.Add(LicenseData{ID: "LIST-B"}, token2)

	entries := reg.List()
	if len(entries) != 2 {
		t.Fatalf("2 licences attendues, obtenu %d", len(entries))
	}
}

func TestLicenseRegistryPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	regPath := filepath.Join(tmpDir, "licenses.json")

	token, _, _ := CreateLicense("REG-PERSIST", 30, 5, "")

	// Créer et sauvegarder.
	reg1, _ := LoadLicenseRegistry(regPath)
	_ = reg1.Add(LicenseData{ID: "REG-PERSIST"}, token)

	// Recharger.
	reg2, _ := LoadLicenseRegistry(regPath)
	entry, ok := reg2.Get("REG-PERSIST")
	if !ok {
		t.Fatal("la licence doit persister dans le registre")
	}
	if entry.Status != LicenseNew {
		t.Fatalf("état initial attendu NEW, obtenu %q", entry.Status)
	}
}

// splitTokenParts est un helper de test pour corrompre un jeton.
func splitTokenParts(token string) []string {
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			return []string{token[:i], token[i+1:]}
		}
	}
	return nil
}
