package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================
// TESTS D'INTÉGRATION CROSS-PROJECT
// ============================================================
//
// Ces tests simulent exactement le workflow :
//   License Maker → génération + signature Ed25519
//   LABOSURF PRO  → vérification + activation
//
// La clé privée utilisée ici est SIMULAIRE à celle du Maker.
// En production, le Maker signe et PRO vérifie avec la clé publique
// correspondante.
//
// Scénarios testés :
//   1. Licence valide créée par le Maker → acceptée par PRO
//   2. Signature modifiée → refusée
//   3. Payload modifié → refusée
//   4. Licence jamais activée mais fenêtre dépassée → refusée
//   5. Licence activée → reste valide après expiration fenêtre
//   6. Réutilisation d'une licence → refusée (usage unique)
//   7. Licence signée avec une mauvaise clé → refusée
//   8. Licence au format invalide → refusée

// ---------- helpers: simulation du License Maker ----------

// makerKeyPair simule la paire de clés du License Maker.
// En production, seul le Maker possède la clé privée.
type makerKeyPair struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newMakerKeyPair(t *testing.T) makerKeyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération de clés Maker : %v", err)
	}
	return makerKeyPair{priv: priv, pub: pub}
}

// makerCreateLicense reproduit exactement la logique de CreateLicense
// dans le License Maker (license.go).
func (m makerKeyPair) makerCreateLicense(id, comment string) (string, LicenseData, error) {
	const (
		makerProductName = "LABOSURF PRO"
		makerLicenseLen  = 40
		makerPrefix      = "LABOSURF"
		makerAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*_-+=?"
	)

	keyBytes := make([]byte, makerLicenseLen-len(makerPrefix))
	if _, err := rand.Read(keyBytes); err != nil {
		return "", LicenseData{}, err
	}

	var keyBuilder strings.Builder
	keyBuilder.Grow(makerLicenseLen)
	keyBuilder.WriteString(makerPrefix)
	for _, v := range keyBuilder.String() {
		_ = v
	}
	// Reset and write properly
	keyBuilder.Reset()
	keyBuilder.Grow(makerLicenseLen)
	keyBuilder.WriteString(makerPrefix)
	for _, v := range keyBytes {
		keyBuilder.WriteByte(makerAlphabet[int(v)%len(makerAlphabet)])
	}

	now := time.Now().UTC()

	data := LicenseData{
		ID:              id,
		Key:             keyBuilder.String(),
		IssuedAt:        now.Format(time.RFC3339),
		ActivationUntil: now.Add(3 * time.Hour).Format(time.RFC3339),
		Product:         makerProductName,
		Comment:         comment,
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return "", LicenseData{}, err
	}

	signature := ed25519.Sign(m.priv, payload)

	token := base64.RawURLEncoding.EncodeToString(payload) +
		"." +
		base64.RawURLEncoding.EncodeToString(signature)

	return token, data, nil
}

// makerCreateLicenseWithWindow reproduit CreateLicense avec une fenêtre
// d'activation personnalisée.
func (m makerKeyPair) makerCreateLicenseWithWindow(id string, window time.Duration) (string, LicenseData, error) {
	const (
		makerProductName = "LABOSURF PRO"
		makerLicenseLen  = 40
		makerPrefix      = "LABOSURF"
		makerAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*_-+=?"
	)

	keyBytes := make([]byte, makerLicenseLen-len(makerPrefix))
	if _, err := rand.Read(keyBytes); err != nil {
		return "", LicenseData{}, err
	}

	var keyBuilder strings.Builder
	keyBuilder.Grow(makerLicenseLen)
	keyBuilder.WriteString(makerPrefix)
	for _, v := range keyBytes {
		keyBuilder.WriteByte(makerAlphabet[int(v)%len(makerAlphabet)])
	}

	now := time.Now().UTC()

	data := LicenseData{
		ID:              id,
		Key:             keyBuilder.String(),
		IssuedAt:        now.Format(time.RFC3339),
		ActivationUntil: now.Add(window).Format(time.RFC3339),
		Product:         makerProductName,
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return "", LicenseData{}, err
	}

	signature := ed25519.Sign(m.priv, payload)

	token := base64.RawURLEncoding.EncodeToString(payload) +
		"." +
		base64.RawURLEncoding.EncodeToString(signature)

	return token, data, nil
}

// ---------- TEST 1: Licence valide créée par le Maker → acceptée par PRO ----------

func TestIntegration1_ValidLicenseAccepted(t *testing.T) {
	maker := newMakerKeyPair(t)

	// Simule le Maker qui crée une licence
	token, data, err := maker.makerCreateLicense("INT-001", "licence de test")
	if err != nil {
		t.Fatalf("Maker CreateLicense : %v", err)
	}

	if len(data.Key) != 40 {
		t.Fatalf("clé de longueur %d au lieu de 40", len(data.Key))
	}
	if !strings.HasPrefix(data.Key, "LABOSURF") {
		t.Fatalf("préfixe incorrect : %q", data.Key)
	}

	// Configure PRO pour vérifier avec la clé publique du Maker
	setTestVerifyKey(t, maker.pub)

	// PRO vérifie le jeton
	verifiedData, status, err := VerifyLicenseToken(token)
	if err != nil {
		t.Fatalf("VerifyLicenseToken : %v", err)
	}
	if status != LicenseActive {
		t.Fatalf("statut actif attendu, obtenu %q", status)
	}
	if verifiedData.ID != "INT-001" {
		t.Fatalf("ID attendu INT-001, obtenu %q", verifiedData.ID)
	}
	if verifiedData.Key != data.Key {
		t.Fatalf("clé vérifiée différente")
	}
}

// ---------- TEST 2: Signature modifiée → refusée ----------

func TestIntegration2_TamperedSignatureRejected(t *testing.T) {
	maker := newMakerKeyPair(t)
	setTestVerifyKey(t, maker.pub)

	token, _, err := maker.makerCreateLicense("INT-002", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatal("format de jeton invalide")
	}

	// Modifie la signature
	tamperedToken := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte("deadbeef0000000000000000000000000000000000000000000000000000deadbeef"))

	_, status, err := VerifyLicenseToken(tamperedToken)
	if err == nil {
		t.Fatal("signature modifiée doit provoquer une erreur")
	}
	if status != LicenseTampered {
		t.Fatalf("statut TAMPERED attendu, obtenu %q", status)
	}
}

// ---------- TEST 3: Payload modifié → refusée ----------

func TestIntegration3_TamperedPayloadRejected(t *testing.T) {
	maker := newMakerKeyPair(t)
	setTestVerifyKey(t, maker.pub)

	token, _, err := maker.makerCreateLicense("INT-003", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatal("format de jeton invalide")
	}

	// Décode le payload, le modifie, et ré-encode
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("décodage payload : %v", err)
	}

	var data LicenseData
	if err := json.Unmarshal(payloadBytes, &data); err != nil {
		t.Fatalf("unmarshal : %v", err)
	}

	// Modifie l'ID
	data.ID = "TAMPERED-ID"

	tamperedPayload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal : %v", err)
	}

	tamperedToken := base64.RawURLEncoding.EncodeToString(tamperedPayload) + "." + parts[1]

	_, status, err := VerifyLicenseToken(tamperedToken)
	if err == nil {
		t.Fatal("payload modifié doit provoquer une erreur")
	}
	if status != LicenseTampered {
		t.Fatalf("statut TAMPERED attendu, obtenu %q", status)
	}
}

// ---------- TEST 4: Licence jamais activée, fenêtre dépassée → refusée ----------

func TestIntegration4_ExpiredWindowNewActivationRejected(t *testing.T) {
	maker := newMakerKeyPair(t)
	setTestVerifyKey(t, maker.pub)

	// Crée une licence avec une fenêtre déjà passée (-1 heure)
	token, _, err := maker.makerCreateLicenseWithWindow("INT-004", -1*time.Hour)
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	as, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	// Activation d'une licence jamais activée avec fenêtre dépassée → REFUS
	_, err = as.Activate(token, nil)
	if err != ErrLicenseExpired {
		t.Fatalf("activation doit échouer avec ErrLicenseExpired, obtenu %v", err)
	}
}

// ---------- TEST 5: Licence activée → reste valide après expiration fenêtre ----------

func TestIntegration5_ActivatedLicenseRemainsValidAfterWindow(t *testing.T) {
	maker := newMakerKeyPair(t)
	setTestVerifyKey(t, maker.pub)

	// Crée une licence avec une fenêtre de 1 seconde
	token, data, err := maker.makerCreateLicenseWithWindow("INT-005", 1*time.Second)
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	as, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	// Active la licence pendant la fenêtre
	_, err = as.Activate(token, nil)
	if err != nil {
		t.Fatalf("activation initiale : %v", err)
	}

	// Attend l'expiration de la fenêtre
	time.Sleep(2 * time.Second)

	// Recharge l'activation (simule un redémarrage)
	as2, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("rechargement : %v", err)
	}

	// Vérifie que l'activation est toujours valide
	res, err := as2.Check(nil)
	if err != nil {
		t.Fatalf("Check après expiration fenêtre doit réussir : %v", err)
	}
	if !res.Activated {
		t.Fatal("l'activation doit rester active après expiration de la fenêtre")
	}
	if res.Data.ID != data.ID {
		t.Fatalf("ID attendu %q, obtenu %q", data.ID, res.Data.ID)
	}
}

// ---------- TEST 6: Réutilisation d'une licence → refusée ----------

func TestIntegration6_ReuseLicenseRejected(t *testing.T) {
	maker := newMakerKeyPair(t)
	setTestVerifyKey(t, maker.pub)

	token, _, err := maker.makerCreateLicense("INT-006", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	// Première activation → succès
	as1, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	_, err = as1.Activate(token, nil)
	if err != nil {
		t.Fatalf("1ère activation : %v", err)
	}

	// Deuxième activation avec la même licence → refus
	as2, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("rechargement : %v", err)
	}

	_, err = as2.Activate(token, nil)
	if err != ErrAlreadyActivated {
		t.Fatalf("2ème activation doit retourner ErrAlreadyActivated, obtenu %v", err)
	}
}

// ---------- TEST 7: Licence signée avec une mauvaise clé → refusée ----------

func TestIntegration7_WrongKeyRejected(t *testing.T) {
	// Le Maker signe avec une clé
	maker := newMakerKeyPair(t)
	token, _, err := maker.makerCreateLicense("INT-007", "")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	// PRO utilise une AUTRE clé publique (ne correspond pas au Maker)
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé autre : %v", err)
	}
	setTestVerifyKey(t, otherPub)

	_, status, err := VerifyLicenseToken(token)
	if err == nil {
		t.Fatal("clé publique incorrecte doit provoquer une erreur")
	}
	if status != LicenseTampered {
		t.Fatalf("statut TAMPERED attendu, obtenu %q", status)
	}
}

// ---------- TEST 8: Licence au format invalide → refusée ----------

func TestIntegration8_InvalidFormatRejected(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"vide", ""},
		{"sans point", "abc123"},
		{"payload invalide", "!!!." + base64.RawURLEncoding.EncodeToString([]byte("sig"))},
		{"signature invalide", base64.RawURLEncoding.EncodeToString([]byte(`{"id":"x"}`)) + ".!!!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Générer une clé pour le test
			pub, _, _ := ed25519.GenerateKey(rand.Reader)
			setTestVerifyKey(t, pub)

			_, _, err := VerifyLicenseToken(tt.token)
			if err == nil {
				t.Fatalf("token invalide %q doit provoquer une erreur", tt.token)
			}
		})
	}
}

// ---------- TEST BONUS: Vérification du format identique Maker/PRO ----------

func TestIntegration9_TokenFormatIdentical(t *testing.T) {
	maker := newMakerKeyPair(t)
	setTestVerifyKey(t, maker.pub)

	// Crée une licence avec le code du Maker
	token, data, err := maker.makerCreateLicense("FORMAT-TEST", "format check")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	// Le token doit avoir exactement 2 parties séparées par un point
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("le token doit contenir exactement 2 parties, obtenu %d", len(parts))
	}

	// La première partie (payload) doit être du base64url valide
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("payload pas du base64url valide : %v", err)
	}

	// Le payload décodé doit être du JSON valide
	var decodedData LicenseData
	if err := json.Unmarshal(payloadBytes, &decodedData); err != nil {
		t.Fatalf("payload n'est pas du JSON valide : %v", err)
	}

	if decodedData.ID != data.ID {
		t.Fatalf("ID dans le payload : attendu %q, obtenu %q", data.ID, decodedData.ID)
	}

	// La deuxième partie (signature) doit être du base64url valide
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("signature pas du base64url valide : %v", err)
	}

	// La signature doit faire 64 bytes (Ed25519)
	if len(sigBytes) != ed25519.SignatureSize {
		t.Fatalf("taille signature : attendue %d, obtenue %d", ed25519.SignatureSize, len(sigBytes))
	}

	// Vérifie que la signature est valide
	if !ed25519.Verify(maker.pub, payloadBytes, sigBytes) {
		t.Fatal("la signature Ed25519 n'est pas valide")
	}
}

// ---------- TEST BONUS: Vérification des statuts ----------

func TestIntegration10_LicenseStatuses(t *testing.T) {
	if string(LicenseNew) != "NEW" {
		t.Fatalf("LicenseNew doit être NEW, obtenu %q", LicenseNew)
	}
	if string(LicenseActive) != "ACTIVE" {
		t.Fatalf("LicenseActive doit être ACTIVE, obtenu %q", LicenseActive)
	}
	if string(LicenseExpired) != "EXPIRED" {
		t.Fatalf("LicenseExpired doit être EXPIRED, obtenu %q", LicenseExpired)
	}
	if string(LicenseRevoked) != "REVOKED" {
		t.Fatalf("LicenseRevoked doit être REVOKED, obtenu %q", LicenseRevoked)
	}
	if string(LicenseTampered) != "TAMPERED" {
		t.Fatalf("LicenseTampered doit être TAMPERED, obtenu %q", LicenseTampered)
	}
}

// ---------- TEST BONUS: LicenseData JSON compatible ----------

func TestIntegration11_LicenseDataJSONCompatible(t *testing.T) {
	// Simule un payload créé par le Maker
	original := LicenseData{
		ID:              "JSON-TEST",
		Key:             "LABOSURFABCDEFGHIJKLMNOPQRSTUVWX01234567",
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		ActivationUntil: time.Now().UTC().Add(3 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
		Comment:         "test JSON",
	}

	// Sérialise comme le Maker le ferait
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal : %v", err)
	}

	// Décode comme PRO le ferait
	var decoded LicenseData
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal : %v", err)
	}

	// Vérifie que tous les champs sont identiques
	if decoded.ID != original.ID {
		t.Fatalf("ID différent : %q != %q", decoded.ID, original.ID)
	}
	if decoded.Key != original.Key {
		t.Fatalf("Key différente : %q != %q", decoded.Key, original.Key)
	}
	if decoded.IssuedAt != original.IssuedAt {
		t.Fatalf("IssuedAt différent : %q != %q", decoded.IssuedAt, original.IssuedAt)
	}
	if decoded.ActivationUntil != original.ActivationUntil {
		t.Fatalf("ActivationUntil différent : %q != %q", decoded.ActivationUntil, original.ActivationUntil)
	}
	if decoded.Product != original.Product {
		t.Fatalf("Product différent : %q != %q", decoded.Product, original.Product)
	}
	if decoded.Comment != original.Comment {
		t.Fatalf("Comment différent : %q != %q", decoded.Comment, original.Comment)
	}
}

// ---------- TEST BONUS: Activation sans registre (déploiement autonome) ----------

func TestIntegration12_ActivationWithoutRegistry(t *testing.T) {
	maker := newMakerKeyPair(t)
	setTestVerifyKey(t, maker.pub)

	token, _, err := maker.makerCreateLicense("INT-012", "sans registre")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	tmpDir := t.TempDir()
	machinePath := filepath.Join(tmpDir, "machine.id")
	actPath := filepath.Join(tmpDir, "activation.json")

	as, err := LoadActivationStore(actPath, machinePath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}

	// Activation sans registre (nil) → doit fonctionner
	res, err := as.Activate(token, nil)
	if err != nil {
		t.Fatalf("Activate sans registre : %v", err)
	}
	if !res.Activated {
		t.Fatal("l'activation doit réussir sans registre")
	}
}

// ---------- helpers ----------

// setTestVerifyKey configure la clé publique pour les tests.
// La clé originale de TestMain est restaurée à la fin du test.
func setTestVerifyKey(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	orig := testVerifyKey
	testVerifyKey = pub
	t.Cleanup(func() { testVerifyKey = orig })
}

// Unused helper kept for clarity.
var _ = hex.EncodeToString
