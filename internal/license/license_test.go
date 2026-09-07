package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Set up test keys before running tests
	pubBytes, privBytes, _ := ed25519.GenerateKey(rand.Reader)

	privHex := hex.EncodeToString(privBytes)
	pubHex := hex.EncodeToString(pubBytes)

	// Override embedded key for tests
	EmbeddedVerifyKeyHex = pubHex

	// Also write test key file
	os.WriteFile("labosurf_pub.key", []byte(pubHex), 0644)
	os.WriteFile("labosurf_admin.key", []byte(privHex), 0644)

	os.Exit(m.Run())
}

func createTestToken(t *testing.T, data LicenseData, signKey ed25519.PrivateKey) string {
	payload, err := canonicalPayload(data)
	if err != nil {
		t.Fatalf("canonicalPayload: %v", err)
	}
	sig := ed25519.Sign(signKey, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// TEST 1: Licence correctement signée -> ACCEPTÉE
func TestVerifyPlatformLicense_ValidSignature(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	// Set test key for this test
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "TEST-VALID-001",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
		Comment:         "test",
	}

	token := createTestToken(t, data, privBytes)

	// Write token to temp file
	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "license.token"), []byte(token), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	err := VerifyPlatformLicense()
	if err != nil {
		t.Fatalf("licence valide rejetée: %v", err)
	}
}

// TEST 2: Licence avec signature modifiée -> REFUSÉE
func TestVerifyPlatformLicense_InvalidSignature(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "TEST-BAD-SIG",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	// Create valid token first
	token := createTestToken(t, data, privBytes)

	// Corrupt the signature
	parts := strings.Split(token, ".")
	badSig := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	badToken := parts[0] + "." + badSig

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "license.token"), []byte(badToken), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	err := VerifyPlatformLicense()
	if err == nil {
		t.Fatal("licence avec signature corrompue devrait être refusée")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Fatalf("erreur attendue liée à la signature, obtenu: %v", err)
	}
}

// TEST 3: Licence avec une donnée modifiée après signature -> REFUSÉE
func TestVerifyPlatformLicense_TamperedPayload(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "TEST-TAMPER",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	// Create valid token
	token := createTestToken(t, data, privBytes)

	// Decode, modify payload, re-encode with SAME signature (should fail)
	parts := strings.Split(token, ".")
	payloadBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var origData LicenseData
	json.Unmarshal(payloadBytes, &origData)

	// Modify the payload
	origData.ID = "MODIFIED-ID"
	newPayload, _ := json.Marshal(origData)
	tamperedToken := base64.RawURLEncoding.EncodeToString(newPayload) + "." + parts[1]

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "license.token"), []byte(tamperedToken), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	err := VerifyPlatformLicense()
	if err == nil {
		t.Fatal("licence avec payload modifié devrait être refusée")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Fatalf("erreur attendue liée à la signature, obtenu: %v", err)
	}
}

// TEST 4: Licence signée avec une autre clé privée -> REFUSÉE
func TestVerifyPlatformLicense_WrongKey(t *testing.T) {
	// Key A signs the token
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	// Key B is the verification key (different from A)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)
	pubB := privB.Public().(ed25519.PublicKey)
	EmbeddedVerifyKeyHex = hex.EncodeToString(pubB)

	data := LicenseData{
		ID:              "TEST-WRONG-KEY",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	// Sign with key A
	token := createTestToken(t, data, privA)

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "license.token"), []byte(token), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	err := VerifyPlatformLicense()
	if err == nil {
		t.Fatal("licence signée avec une autre clé devrait être refusée")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Fatalf("erreur attendue liée à la signature, obtenu: %v", err)
	}
}

// TEST 5: Licence expirée mais correctement signée -> REFUSÉE
func TestVerifyPlatformLicense_Expired(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "TEST-EXPIRED",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Add(-4 * time.Hour).Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339), // expired 1h ago
		Product:         "LABOSURF PRO",
	}

	token := createTestToken(t, data, privBytes)

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "license.token"), []byte(token), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	err := VerifyPlatformLicense()
	if err == nil {
		t.Fatal("licence expirée devrait être refusée")
	}
	if !strings.Contains(err.Error(), "expir") {
		t.Fatalf("erreur attendue liée à l'expiration, obtenu: %v", err)
	}
}

// TEST 6: Licence destinée à une autre plateforme -> REFUSÉE
func TestVerifyPlatformLicense_WrongProduct(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "TEST-WRONG-PRODUCT",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "OTHER PRODUCT",
	}

	token := createTestToken(t, data, privBytes)

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "license.token"), []byte(token), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	err := VerifyPlatformLicense()
	if err == nil {
		t.Fatal("licence pour autre produit devrait être refusée")
	}
	if !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("erreur attendue liée au produit incompatible, obtenu: %v", err)
	}
}

// TEST 7: Licence malformée -> REFUSÉE proprement
func TestVerifyPlatformLicense_Malformed(t *testing.T) {
	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "license.token"), []byte("not-a-valid-token"), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	err := VerifyPlatformLicense()
	if err == nil {
		t.Fatal("licence malformée devrait être refusée")
	}
	if !strings.Contains(err.Error(), "format") && !strings.Contains(err.Error(), "invalide") {
		t.Fatalf("erreur attendue liée au format, obtenu: %v", err)
	}
}

// TEST 8: Signature vide/manquante -> REFUSÉE
func TestVerifyPlatformLicense_MissingSignature(t *testing.T) {
	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	// Token with empty signature part
	badToken := "eyJpZCI6IlRFU1QifQ."

	if err := os.WriteFile(filepath.Join(tmpDir, "license.token"), []byte(badToken), 0600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	err := VerifyPlatformLicense()
	if err == nil {
		t.Fatal("licence sans signature devrait être refusée")
	}
}

// Test Activate with valid token
func TestActivate_Valid(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "ACTIVATE-TEST",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	token := createTestToken(t, data, privBytes)

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	err := Activate(token)
	if err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	// Verify activation record was created with correct LicenseID
	actPath := filepath.Join(tmpDir, "activation.json")
	actBytes, err := os.ReadFile(actPath)
	if err != nil {
		t.Fatalf("read activation: %v", err)
	}
	var act ActivationRecord
	json.Unmarshal(actBytes, &act)
	if act.LicenseID != "ACTIVATE-TEST" {
		t.Fatalf("LicenseID in activation record should be ACTIVATE-TEST, got %q", act.LicenseID)
	}
}

// Test Activate with invalid signature
func TestActivate_InvalidSignature(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "ACT-BAD-SIG",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	token := createTestToken(t, data, privBytes)
	parts := strings.Split(token, ".")
	badToken := parts[0] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	err := Activate(badToken)
	if err == nil {
		t.Fatal("Activate with invalid signature should fail")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature error, got: %v", err)
	}
}

// Test Activate with expired token
func TestActivate_ExpiredToken(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "ACT-EXPIRED",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Add(-4 * time.Hour).Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	token := createTestToken(t, data, privBytes)

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	err := Activate(token)
	if err == nil {
		t.Fatal("Activate with expired token should fail")
	}
	if !strings.Contains(err.Error(), "expir") {
		t.Fatalf("expected expiration error, got: %v", err)
	}
}

// Test Activate with wrong product
func TestActivate_WrongProduct(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)
	pubBytes := privBytes.Public().(ed25519.PublicKey)
	testVerifyKey = pubBytes

	data := LicenseData{
		ID:              "ACT-WRONG-PROD",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "WRONG PRODUCT",
	}

	token := createTestToken(t, data, privBytes)

	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	err := Activate(token)
	if err == nil {
		t.Fatal("Activate with wrong product should fail")
	}
	if !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("expected incompatible product error, got: %v", err)
	}
}

// Test getOrCreateMachineID generates valid hex
func TestGetOrCreateMachineID(t *testing.T) {
	tmpDir := t.TempDir()
	oldDataDir := os.Getenv("LABOSURF_DATA_DIR")
	os.Setenv("LABOSURF_DATA_DIR", tmpDir)
	defer os.Setenv("LABOSURF_DATA_DIR", oldDataDir)

	id1, err := getOrCreateMachineID()
	if err != nil {
		t.Fatalf("getOrCreateMachineID: %v", err)
	}

	if len(id1) != 64 {
		t.Fatalf("machine ID should be 64 hex chars, got %d", len(id1))
	}

	// Verify it's valid hex
	if _, err := hex.DecodeString(id1); err != nil {
		t.Fatalf("machine ID should be valid hex: %v", err)
	}

	// Second call should return same ID
	id2, err := getOrCreateMachineID()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("machine ID should be consistent, got %q vs %q", id1, id2)
	}
}

// Test ParseLicenseToken
func TestParseLicenseToken(t *testing.T) {
	_, privBytes, _ := ed25519.GenerateKey(rand.Reader)

	data := LicenseData{
		ID:              "PARSE-TEST",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	token := createTestToken(t, data, privBytes)

	parsedData, signature, err := ParseLicenseToken(token)
	if err != nil {
		t.Fatalf("ParseLicenseToken: %v", err)
	}

	if parsedData.ID != data.ID {
		t.Fatalf("parsed ID mismatch: %q != %q", parsedData.ID, data.ID)
	}
	if len(signature) != ed25519.SignatureSize {
		t.Fatalf("signature length: expected %d, got %d", ed25519.SignatureSize, len(signature))
	}
}

// Test ParseLicenseToken with bad format
func TestParseLicenseToken_BadFormat(t *testing.T) {
	_, _, err := ParseLicenseToken("not-a-token")
	if err == nil {
		t.Fatal("malformed token should error")
	}

	_, _, err = ParseLicenseToken("onlyonepart")
	if err == nil {
		t.Fatal("single part token should error")
	}

	_, _, err = ParseLicenseToken("part1.part2.part3")
	if err == nil {
		t.Fatal("three parts should error")
	}
}

// Test canonicalPayload produces deterministic output
func TestCanonicalPayload_Deterministic(t *testing.T) {
	data := LicenseData{
		ID:              "DETERMINISTIC",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        "2024-01-01T00:00:00Z",
		ActivationUntil: "2024-01-01T03:00:00Z",
		Product:         "LABOSURF PRO",
		Comment:         "test comment",
	}

	p1, _ := canonicalPayload(data)
	p2, _ := canonicalPayload(data)

	if string(p1) != string(p2) {
		t.Fatal("canonicalPayload should be deterministic")
	}
}

// Test verifySignature with correct and incorrect keys
func TestVerifySignature(t *testing.T) {
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	pubA := privA.Public().(ed25519.PublicKey)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)
	pubB := privB.Public().(ed25519.PublicKey)

	data := LicenseData{
		ID:              "SIG-TEST",
		Key:             "LABOSURF12345678901234567890123456789012",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}

	payload, _ := canonicalPayload(data)
	sigA := ed25519.Sign(privA, payload)

	// Correct key should verify
	if !verifySignature(payload, sigA, pubA) {
		t.Fatal("correct key should verify")
	}

	// Wrong key should not verify
	if verifySignature(payload, sigA, pubB) {
		t.Fatal("wrong key should not verify")
	}

	// Tampered payload should not verify
	tamperedPayload := append(payload, byte(0xFF))
	if verifySignature(tamperedPayload, sigA, pubA) {
		t.Fatal("tampered payload should not verify")
	}
}