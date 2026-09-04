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
	"path/filepath"
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
	actPath := filepath.Join(tmp, "activation.json")
	machPath := filepath.Join(tmp, "machine.id")

	token, lic, err := CreateLicense("CLIENT-REEL-001", "client premium")
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

	// 4. Activation complète dans le délai de 3h (TEST 1 suite).
	as, err := LoadActivationStore(actPath, machPath)
	if err != nil {
		t.Fatalf("LoadActivationStore : %v", err)
	}
	res, err := as.Activate(token, nil)
	if err != nil || !res.Activated {
		t.Fatalf("activation refusée : %v", err)
	}
	t.Log("✓ activation réussie dans la fenêtre de 3h")

	// 5. Persistance après rechargement (TEST 5).
	as2, _ := LoadActivationStore(actPath, machPath)
	check, err := as2.Check(nil)
	if err != nil || !check.Activated {
		t.Fatalf("activation non persistante : %v", err)
	}
	t.Log("✓ activation persistante après rechargement")

	// 6. Réutilisation de la même licence (TEST 6 : refusée).
	if _, err := as2.Activate(token, nil); err != ErrAlreadyActivated {
		t.Fatalf("seconde activation devrait être ErrAlreadyActivated, obtenu %v", err)
	}
	t.Log("✓ seconde activation refusée (usage unique local)")

	// 7. Signature modifiée (TEST 2 : refusée).
	badToken := token[:len(token)-4] + "AAAA"
	if _, _, err := VerifyLicenseToken(badToken); err == nil {
		t.Fatal("une signature modifiée a été acceptée")
	}
	t.Log("✓ signature modifiée refusée")

	// 8. Licence sur une autre machine (TEST machine ID).
	otherMach := filepath.Join(tmp, "other_machine.id")
	as3, _ := LoadActivationStore(actPath, otherMach)
	if _, err := as3.Check(nil); err != ErrWrongDevice {
		t.Fatalf("fichier copié sur autre machine devrait être ErrWrongDevice, obtenu %v", err)
	}
	t.Log("✓ activation liée à la machine (copie refusée)")
}
