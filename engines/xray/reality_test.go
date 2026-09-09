package xray

import (
	"testing"
)

// TestRealityKeyGenerateSaveLoadRoundTrip est un test de non-régression :
// SaveRealityKeys (via PrivateKeyPEM) échouait systématiquement avant
// correction avec "x509: unknown key type while marshaling PKCS#8:
// [32]uint8", car un scalaire X25519 brut n'est pas un type que
// x509.MarshalPKCS8PrivateKey sait sérialiser. En pratique, cela signifiait
// qu'EnsureRealityKeys (appelé par Install()) ne pouvait jamais terminer
// avec succès sur une machine sans clés REALITY préexistantes — c'est-à-dire
// jamais, puisque rien ne pouvait les écrire la première fois.
func TestRealityKeyGenerateSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	generated, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("GenerateRealityKeyPair: %v", err)
	}

	if err := SaveRealityKeys(generated, dir); err != nil {
		t.Fatalf("SaveRealityKeys: %v", err)
	}

	loaded, err := LoadRealityKeys(dir)
	if err != nil {
		t.Fatalf("LoadRealityKeys: %v", err)
	}

	if loaded.PrivateKey != generated.PrivateKey {
		t.Fatal("clé privée rechargée différente de la clé générée")
	}
	if loaded.PublicKey != generated.PublicKey {
		t.Fatal("clé publique rechargée différente de la clé générée")
	}
	if loaded.PublicKeyHex() != generated.PublicKeyHex() {
		t.Fatal("PublicKeyHex incohérent après round-trip")
	}
}

// TestEnsureRealityKeysIdempotent vérifie qu'un second appel réutilise les
// clés existantes au lieu d'en régénérer de nouvelles (comportement attendu
// pour ne pas invalider les clients déjà configurés à chaque redémarrage).
func TestEnsureRealityKeysIdempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := EnsureRealityKeys(dir)
	if err != nil {
		t.Fatalf("EnsureRealityKeys (1er appel): %v", err)
	}

	second, err := EnsureRealityKeys(dir)
	if err != nil {
		t.Fatalf("EnsureRealityKeys (2e appel): %v", err)
	}

	if first.PublicKey != second.PublicKey {
		t.Fatal("EnsureRealityKeys a régénéré une nouvelle paire de clés au lieu de réutiliser l'existante")
	}
}

// TestPrivateKeyBase64MatchesRawBytes vérifie que l'encodage utilisé dans la
// config Xray-core (realitySettings.privateKey) et dans le lien client
// (pbk=) décode bien vers les 32 octets bruts de la clé.
func TestPrivateKeyBase64MatchesRawBytes(t *testing.T) {
	keys, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("GenerateRealityKeyPair: %v", err)
	}

	if keys.PrivateKeyBase64() == "" {
		t.Fatal("PrivateKeyBase64 vide")
	}
	if keys.PublicKeyForVLESS() == "" {
		t.Fatal("PublicKeyForVLESS vide")
	}
	if keys.PrivateKeyBase64() == keys.PublicKeyForVLESS() {
		t.Fatal("clé privée et clé publique encodées identiquement — génération suspecte")
	}
}
