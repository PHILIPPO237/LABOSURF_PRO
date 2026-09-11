package secret

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestUUIDFormat(t *testing.T) {
	for i := 0; i < 50; i++ {
		u, err := UUID()
		if err != nil {
			t.Fatalf("UUID : %v", err)
		}
		parts := strings.Split(u, "-")
		if len(parts) != 5 {
			t.Fatalf("UUID mal formé : %s", u)
		}
		if parts[2][0] != '4' {
			t.Fatalf("UUID non v4 : %s", u)
		}
	}
}

func TestUUIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		u, _ := UUID()
		if seen[u] {
			t.Fatalf("UUID dupliqué : %s", u)
		}
		seen[u] = true
	}
}

func TestRandHex_Length(t *testing.T) {
	h, err := RandHex(16)
	if err != nil {
		t.Fatalf("RandHex : %v", err)
	}
	if len(h) != 32 {
		t.Fatalf("RandHex(16) attend 32 hex, obtenu %d", len(h))
	}
	if _, err := hex.DecodeString(h); err != nil {
		t.Fatalf("RandHex n'est pas du hex valide : %v", err)
	}
}

func TestRandTokenReadable(t *testing.T) {
	tk, err := RandToken(12)
	if err != nil {
		t.Fatalf("RandToken : %v", err)
	}
	if len(tk) != 24 {
		t.Fatalf("RandToken(12) attend 24 chars, obtenu %d (%q)", len(tk), tk)
	}
	if strings.ContainsAny(tk, "0OIl15") {
		t.Fatalf("token contient des caractères ambigus : %s", tk)
	}
}

func TestEd25519Keypair(t *testing.T) {
	pubHex, privHex, err := Ed25519Keypair()
	if err != nil {
		t.Fatalf("Ed25519Keypair : %v", err)
	}
	pubB, _ := hex.DecodeString(pubHex)
	privB, _ := hex.DecodeString(privHex)
	if len(pubB) != 32 {
		t.Fatalf("clé publique attend 32 octets, obtenu %d", len(pubB))
	}
	if len(privB) != 64 {
		t.Fatalf("clé privée attend 64 octets, obtenu %d", len(privB))
	}
}

func TestPublicKeyHexMatches(t *testing.T) {
	_, privHex, _ := Ed25519Keypair()
	derived, err := PublicKeyHex(privHex)
	if err != nil {
		t.Fatalf("PublicKeyHex : %v", err)
	}
	// Dérive à nouveau et compare.
	derived2, err := PublicKeyHex(privHex)
	if err != nil {
		t.Fatalf("PublicKeyHex 2 : %v", err)
	}
	if derived != derived2 {
		t.Fatalf("dérivation non déterministe")
	}
	if derived == "" {
		t.Fatal("clé publique vide")
	}
}

func TestPublicKeyHexDerivation(t *testing.T) {
	pubHex, privHex, _ := Ed25519Keypair()
	derived, err := PublicKeyHex(privHex)
	if err != nil {
		t.Fatalf("PublicKeyHex : %v", err)
	}
	if derived != pubHex {
		t.Fatalf("clé publique dérivée ≠ clé publique : %s ≠ %s", derived, pubHex)
	}
}

func TestX25519Keypair(t *testing.T) {
	priv, pub, err := X25519Keypair()
	if err != nil {
		t.Fatalf("X25519Keypair : %v", err)
	}
	privB, err := base64.StdEncoding.DecodeString(priv)
	if err != nil {
		t.Fatalf("clé privée non base64 valide : %v", err)
	}
	pubB, err := base64.StdEncoding.DecodeString(pub)
	if err != nil {
		t.Fatalf("clé publique non base64 valide : %v", err)
	}
	if len(privB) != 32 {
		t.Fatalf("clé privée attend 32 octets, obtenu %d", len(privB))
	}
	if len(pubB) != 32 {
		t.Fatalf("clé publique attend 32 octets, obtenu %d", len(pubB))
	}
	if priv == pub {
		t.Fatal("clé privée et clé publique identiques")
	}
}

// TestX25519KeypairUnique vérifie que deux appels successifs produisent des
// clés DIFFÉRENTES (générées aléatoirement, jamais une valeur fixe).
func TestX25519KeypairUnique(t *testing.T) {
	priv1, _, _ := X25519Keypair()
	priv2, _, _ := X25519Keypair()
	if priv1 == priv2 {
		t.Fatal("deux appels ont produit la même clé privée — génération non aléatoire")
	}
}

// TestX25519PublicFromPrivateMatchesGenerated vérifie que la dérivation
// clé-publique-depuis-clé-privée (équivalent de `wg pubkey`) reproduit
// exactement la clé publique déjà retournée par X25519Keypair.
func TestX25519PublicFromPrivateMatchesGenerated(t *testing.T) {
	priv, pub, err := X25519Keypair()
	if err != nil {
		t.Fatalf("X25519Keypair : %v", err)
	}
	derived, err := X25519PublicFromPrivate(priv)
	if err != nil {
		t.Fatalf("X25519PublicFromPrivate : %v", err)
	}
	if derived != pub {
		t.Fatalf("clé publique dérivée ≠ clé publique générée : %s ≠ %s", derived, pub)
	}
}

func TestX25519PublicFromPrivateInvalid(t *testing.T) {
	if _, err := X25519PublicFromPrivate("pas-du-base64-valide!!"); err == nil {
		t.Fatal("attendu une erreur pour une clé privée invalide")
	}
	if _, err := X25519PublicFromPrivate(base64.StdEncoding.EncodeToString([]byte("trop-court"))); err == nil {
		t.Fatal("attendu une erreur pour une clé privée de mauvaise longueur")
	}
}