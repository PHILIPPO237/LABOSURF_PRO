// Test d'intégration CROSS-PROJET : la licence est générée avec la VRAIE
// clé privée du License Maker, puis vérifiée avec la VRAIE clé publique
// déployée dans LABOSURF PRO (release/license_pub.key).
//
// C'est le test de compatibilité décisif entre les deux projets.
//
// La clé privée n'est JAMAIS affichée ni copiée : elle est seulement lue
// par le test pour signer, comme le fait le Maker en production.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

const (
	makerPrivKeyPath = `C:\Users\atsan\OneDrive\Bureau\LABOSURF_LICENSE_MAKER\labosurf_admin.key`
	proPubKeyPath    = `C:\Users\atsan\OneDrive\Bureau\LABOSURF_PRO\release\license_pub.key`
)

// setupRealKeys charge les vraies clés des deux projets.
// Retourne false si les fichiers sont absents (test skippé proprement).
//
// IMPORTANT : TestMain (license_test.go) installe une paire de clés de TEST
// dans testSignKey/testVerifyKey pour tout le package. Ce test les remplace
// temporairement par les vraies clés de production, puis RESTAURE les clés
// de test à la fin pour ne pas perturber les autres tests du package.
func setupRealKeys(t *testing.T) bool {
	t.Helper()

	privRaw, err := os.ReadFile(makerPrivKeyPath)
	if err != nil {
		t.Logf("clé privée Maker absente (%v) — test skippé", err)
		return false
	}
	privBytes, err := hex.DecodeString(strings.TrimSpace(string(privRaw)))
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		t.Logf("clé privée Maker invalide — test skippé")
		return false
	}

	pubRaw, err := os.ReadFile(proPubKeyPath)
	if err != nil {
		t.Logf("clé publique PRO absente (%v) — test skippé", err)
		return false
	}
	pubBytes, err := hex.DecodeString(strings.TrimSpace(string(pubRaw)))
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		t.Logf("clé publique PRO invalide — test skippé")
		return false
	}

	// Sauvegarder les clés de test installées par TestMain.
	savedSign := testSignKey
	savedVerify := testVerifyKey

	testSignKey = ed25519.PrivateKey(privBytes)
	testVerifyKey = ed25519.PublicKey(pubBytes)

	// Restaurer les clés de test (PAS nil) pour les tests suivants.
	t.Cleanup(func() {
		testSignKey = savedSign
		testVerifyKey = savedVerify
	})
	return true
}

// TestCrossMakerToPRO_RealKeys est le scénario complet avec les vraies clés.
func TestCrossMakerToPRO_RealKeys(t *testing.T) {
	if !setupRealKeys(t) {
		return
	}

	// 1. La clé publique PRO doit correspondre à la clé privée Maker.
	derived := testSignKey.Public().(ed25519.PublicKey)
	if !derived.Equal(testVerifyKey) {
		t.Fatal("la clé publique PRO ne correspond PAS à la clé privée Maker")
	}
	t.Log("✓ paire de clés Maker/PRO cohérente")

	// 2. Créer une licence (comme le Maker le fait).
	tmp := t.TempDir()

	token, lic, err := CreateLicense("CLIENT-REEL-001", "serveur premium")
	if err != nil {
		t.Fatalf("CreateLicense : %v", err)
	}

	if len(lic.Data.Key) != 40 || !strings.HasPrefix(lic.Data.Key, "LABOSURF") {
		t.Fatalf("format de clé invalide : %q", lic.Data.Key)
	}
	t.Logf("✓ licence créée, clé=%s…", lic.Data.Key[:16])

	// 3. PRO vérifie la signature (TEST 1 : licence valide acceptée).
	data, status, err := VerifyLicenseToken(token)
	if err != nil {
		t.Fatalf("PRO a REFUSÉ une licence valide du Maker : %v", err)
	}
	if status != LicenseActive || data.ID != "CLIENT-REEL-001" {
		t.Fatalf("statut inattendu : %s", status)
	}
	t.Log("✓ PRO accepte la licence signée par le Maker")

	// 4. La clé ouvre UNE installation dans le délai de 3h.
	used, err := UseLicense(token, tmp, nil)
	if err != nil {
		t.Fatalf("installation refusée : %v", err)
	}
	if used.ID != "CLIENT-REEL-001" {
		t.Fatalf("ID inattendu : %q", used.ID)
	}
	t.Log("✓ installation autorisée dans la fenêtre de 3h")

	// 5. Persistance du reçu après relecture du dossier.
	recs, err := ListReceipts(tmp)
	if err != nil || len(recs) != 1 || recs[0].LicenseID != "CLIENT-REEL-001" {
		t.Fatalf("reçu non persistant : %+v, err=%v", recs, err)
	}
	t.Log("✓ reçu d'installation persistant")

	// 6. Réutilisation de la même clé (usage unique : refusée).
	if _, err := UseLicense(token, tmp, nil); err != ErrAlreadyUsed {
		t.Fatalf("seconde utilisation devrait être ErrAlreadyUsed, obtenu %v", err)
	}
	t.Log("✓ seconde utilisation refusée (1 clé = 1 installation)")

	// 7. Signature modifiée (refusée).
	badToken := token[:len(token)-4] + "AAAA"
	if _, _, err := VerifyLicenseToken(badToken); err == nil {
		t.Fatal("une signature modifiée a été acceptée")
	}
	t.Log("✓ signature modifiée refusée")

	// 8. Le serveur n'exige rien : vérification seule, sans reçu.
	if _, _, err := VerifyLicenseToken(token); err != nil {
		t.Fatalf("vérification seule refusée : %v", err)
	}
	t.Log("✓ le serveur tourne librement (licence = accès au script)")
}
