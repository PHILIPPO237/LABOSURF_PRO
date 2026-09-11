package wireguard

import (
	"os"
	"testing"
)

// TestEnsureServerKeysPersists vérifie que la clé serveur est générée une
// fois, aléatoire (pas de valeur fixe), puis relue identique à l'appel
// suivant (idempotence) — même test que le secret d'obfuscation Hysteria2
// (EnsureObfsPassword), pour la même raison.
func TestEnsureServerKeysPersists(t *testing.T) {
	dir := t.TempDir()
	priv1, pub1, err := EnsureServerKeys(dir)
	if err != nil {
		t.Fatalf("EnsureServerKeys: %v", err)
	}
	if priv1 == "" || pub1 == "" {
		t.Fatal("clé serveur vide")
	}
	if priv1 == pub1 {
		t.Fatal("clé privée et clé publique identiques")
	}

	priv2, pub2, err := EnsureServerKeys(dir)
	if err != nil {
		t.Fatalf("EnsureServerKeys (second appel) : %v", err)
	}
	if priv1 != priv2 || pub1 != pub2 {
		t.Fatalf("clé serveur régénérée entre deux appels : (%q,%q) != (%q,%q)", priv1, pub1, priv2, pub2)
	}

	dir2 := t.TempDir()
	priv3, _, err := EnsureServerKeys(dir2)
	if err != nil {
		t.Fatalf("EnsureServerKeys (répertoire différent) : %v", err)
	}
	if priv3 == priv1 {
		t.Fatal("deux répertoires de données différents ne devraient pas partager la même clé serveur générée")
	}
}

// TestServerKeyFilePermissions vérifie que le fichier de clé privée n'est
// jamais lisible que par son propriétaire (mission P2, Étape 15).
func TestServerKeyFilePermissions(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := EnsureServerKeys(dir); err != nil {
		t.Fatalf("EnsureServerKeys: %v", err)
	}
	fi, err := os.Stat(serverKeyPath(dir))
	if err != nil {
		t.Fatalf("stat clé serveur : %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissions attendues 0600, obtenu %o", fi.Mode().Perm())
	}
}
