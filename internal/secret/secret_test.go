package secret

import (
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