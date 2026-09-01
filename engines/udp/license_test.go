package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	privHex, pubHex, err := GenerateKeyPair()
	if err != nil {
		panic(err)
	}

	privBytes, _ := hex.DecodeString(privHex)
	pubBytes, _ := hex.DecodeString(pubHex)

	testSignKey = ed25519.PrivateKey(privBytes)
	testVerifyKey = ed25519.PublicKey(pubBytes)

	os.Exit(m.Run())
}

func TestLicenseCreateAndVerifyToken(t *testing.T) {
	token, lic, err := CreateLicense("TEST-001", "test licence")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	if lic.Data.ID != "TEST-001" {
		t.Fatalf("ID attendu TEST-001, obtenu %q", lic.Data.ID)
	}

	if lic.Data.Product != "LABOSURF PRO" {
		t.Fatalf("produit attendu LABOSURF PRO, obtenu %q", lic.Data.Product)
	}

	if len(lic.Data.Key) != 40 {
		t.Fatalf("clé de longueur %d au lieu de 40 : %q", len(lic.Data.Key), lic.Data.Key)
	}

	if !strings.HasPrefix(lic.Data.Key, "LABOSURF") {
		t.Fatalf("préfixe incorrect : %q", lic.Data.Key)
	}

	if !validateLicenseKey(lic.Data.Key) {
		t.Fatalf("clé refusée par validateLicenseKey : %q", lic.Data.Key)
	}

	if lic.Data.ActivationUntil == "" {
		t.Fatal("ActivationUntil doit être renseigné")
	}

	if token == "" {
		t.Fatal("le jeton ne doit pas être vide")
	}

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

	if data.Key != lic.Data.Key {
		t.Fatalf("clé vérifiée différente : %q != %q", data.Key, lic.Data.Key)
	}
}

func TestLicenseActivationWindow(t *testing.T) {
	now := time.Now().UTC()

	data := LicenseData{
		ID:              "WINDOW",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        now.Add(-4 * time.Hour).Format(time.RFC3339),
		ActivationUntil: now.Add(-1 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	payload, err := canonicalPayload(data)
	if err != nil {
		t.Fatalf("canonicalPayload : %v", err)
	}

	sig := ed25519.Sign(testSignKey, payload)

	token := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)

	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	as, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	_, err = as.Activate(token, nil)
	if err != ErrLicenseExpired {
		t.Fatalf("une licence dont la fenêtre d'activation est dépassée doit être refusée, obtenu %v", err)
	}
}

func TestLicenseTampered(t *testing.T) {
	token, _, err := CreateLicense("TAMPER", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

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

func TestLicenseKeyUniqueness(t *testing.T) {
	seen := make(map[string]bool)

	for i := 0; i < 100; i++ {
		_, lic, err := CreateLicense("UNIQUE-"+string(rune('A'+i%26)), "")
		if err != nil {
			t.Fatalf("CreateLicense : %v", err)
		}

		if seen[lic.Data.Key] {
			t.Fatalf("clé dupliquée : %q", lic.Data.Key)
		}

		seen[lic.Data.Key] = true
	}
}

func TestLicenseInvalidID(t *testing.T) {
	if _, _, err := CreateLicense("", ""); err == nil {
		t.Fatal("ID vide doit provoquer une erreur")
	}
}

func TestLicenseGenerateKeyPair(t *testing.T) {
	priv1, pub1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair : %v", err)
	}

	priv2, pub2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair 2 : %v", err)
	}

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
	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	token, lic, err := CreateLicense("ACTIV", "")
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

	if res.Data.Key != lic.Data.Key {
		t.Fatalf("clé activation différente")
	}

	checkAs, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("rechargement : %v", err)
	}

	checkRes, err := checkAs.Check(nil)
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

	token, _, err := CreateLicense("DOUBLE", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	as, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	if _, err := as.Activate(token, nil); err != nil {
		t.Fatalf("1ère activation : %v", err)
	}

	_, err = as.Activate(token, nil)
	if err != ErrAlreadyActivated {
		t.Fatalf("2ème activation doit retourner ErrAlreadyActivated, obtenu %v", err)
	}
}

func TestLicenseActivationPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	token, _, err := CreateLicense("PERSIST-ACT", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	as1, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	if _, err := as1.Activate(token, nil); err != nil {
		t.Fatalf("Activate : %v", err)
	}

	as2, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("rechargement : %v", err)
	}

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

	token, _, err := CreateLicense("DEACT", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	as, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	if _, err := as.Activate(token, nil); err != nil {
		t.Fatalf("Activate : %v", err)
	}

	if err := as.Deactivate(); err != nil {
		t.Fatalf("Deactivate : %v", err)
	}

	_, err = as.Check(nil)
	if err != ErrActivationMissing {
		t.Fatalf("Check après désactivation doit retourner ErrActivationMissing, obtenu %v", err)
	}
}

func TestLicenseRegistryRevoke(t *testing.T) {
	tmpDir := t.TempDir()
	regPath := filepath.Join(tmpDir, "licenses.json")
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	token, lic, err := CreateLicense("REG-REVOKE", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	reg, err := LoadLicenseRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadLicenseRegistry : %v", err)
	}

	if err := reg.Add(lic.Data, token); err != nil {
		t.Fatalf("Registry Add : %v", err)
	}

	if err := reg.Revoke("REG-REVOKE"); err != nil {
		t.Fatalf("Revoke : %v", err)
	}

	if !reg.IsRevoked("REG-REVOKE") {
		t.Fatal("la licence doit être marquée révoquée")
	}

	as, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	_, err = as.Activate(token, reg)
	if err != ErrLicenseRevoked {
		t.Fatalf("activation de licence révoquée doit échouer : %v", err)
	}
}

func TestLicenseRegistryList(t *testing.T) {
	tmpDir := t.TempDir()
	regPath := filepath.Join(tmpDir, "licenses.json")

	reg, err := LoadLicenseRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadLicenseRegistry : %v", err)
	}

	token1, lic1, err := CreateLicense("LIST-A", "")
	if err != nil {
		t.Fatalf("CreateLicense 1 : %v", err)
	}

	token2, lic2, err := CreateLicense("LIST-B", "")
	if err != nil {
		t.Fatalf("CreateLicense 2 : %v", err)
	}

	if err := reg.Add(lic1.Data, token1); err != nil {
		t.Fatalf("Add 1 : %v", err)
	}

	if err := reg.Add(lic2.Data, token2); err != nil {
		t.Fatalf("Add 2 : %v", err)
	}

	entries := reg.List()
	if len(entries) != 2 {
		t.Fatalf("2 licences attendues, obtenu %d", len(entries))
	}
}

func TestLicenseRegistryPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	regPath := filepath.Join(tmpDir, "licenses.json")

	token, lic, err := CreateLicense("REG-PERSIST", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	reg1, err := LoadLicenseRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadLicenseRegistry : %v", err)
	}

	if err := reg1.Add(lic.Data, token); err != nil {
		t.Fatalf("Registry Add : %v", err)
	}

	reg2, err := LoadLicenseRegistry(regPath)
	if err != nil {
		t.Fatalf("rechargement registre : %v", err)
	}

	entry, ok := reg2.Get("REG-PERSIST")
	if !ok {
		t.Fatal("la licence doit persister dans le registre")
	}

	if entry.Status != LicenseNew {
		t.Fatalf("état initial attendu NEW, obtenu %q", entry.Status)
	}

	if entry.ActivationUntil != lic.Data.ActivationUntil {
		t.Fatalf("ActivationUntil non conservé")
	}

	if entry.Product != "LABOSURF PRO" {
		t.Fatalf("produit attendu LABOSURF PRO, obtenu %q", entry.Product)
	}
}

func splitTokenParts(token string) []string {
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			return []string{token[:i], token[i+1:]}
		}
	}

	return nil
}
