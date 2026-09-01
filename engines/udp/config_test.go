package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigDefaults : les valeurs par défaut sont appliquées
// lorsqu'elles sont absentes du fichier.
func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}

	config, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig : %v", err)
	}

	if config.Listen != defaultListen {
		t.Fatalf("Listen par défaut attendu %q, obtenu %q", defaultListen, config.Listen)
	}

	if config.TUN.Name != "labosurf0" {
		t.Fatalf("TUN.Name par défaut attendu labosurf0, obtenu %q", config.TUN.Name)
	}

	if config.TUN.Address != "10.77.0.1/24" {
		t.Fatalf("TUN.Address par défaut inattendu : %q", config.TUN.Address)
	}

	if config.Auth.Mode != "passwords" {
		t.Fatalf("Auth.Mode par défaut attendu passwords, obtenu %q", config.Auth.Mode)
	}

	if config.Auth.Users == nil {
		t.Fatal("Auth.Users ne doit jamais être nil après chargement")
	}
}

// TestLoadConfigUserLimitsDefaults : MaxConnections et MaxIPs à 0
// sont ramenés à 1 (une limite implicite raisonnable).
func TestLoadConfigUserLimitsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := `{
	  "auth": {
	    "users": {
	      "client1": {
	        "password": "CHANGE_ME",
	        "enabled": true
	      }
	    }
	  }
	}`

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}

	config, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig : %v", err)
	}

	user, ok := config.Auth.Users["client1"]
	if !ok {
		t.Fatal("client1 doit être présent")
	}

	if user.MaxConnections != 1 {
		t.Fatalf("MaxConnections par défaut attendu 1, obtenu %d", user.MaxConnections)
	}

	if user.MaxIPs != 0 {
		t.Fatalf("MaxIPs illimité attendu 0, obtenu %d", user.MaxIPs)
	}
}

// TestLoadConfigInvalidJSON : un JSON invalide remonte une erreur.
func TestLoadConfigInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte(`{ invalid`), 0o600); err != nil {
		t.Fatalf("écriture config : %v", err)
	}

	if _, err := loadConfig(path); err == nil {
		t.Fatal("un JSON invalide doit provoquer une erreur")
	}
}

// TestLoadConfigMissingFile : un fichier absent remonte une erreur.
func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := loadConfig(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("un fichier absent doit provoquer une erreur")
	}
}

// TestTunnelClientIDDeterministic : l'identifiant tunnel est stable et
// distingue les clients.
func TestTunnelClientIDDeterministic(t *testing.T) {
	a1 := tunnelClientID("127.0.0.1:5000")
	a2 := tunnelClientID("127.0.0.1:5000")
	b := tunnelClientID("127.0.0.1:6000")

	if a1 != a2 {
		t.Fatal("le même clientID doit produire le même identifiant tunnel")
	}

	if a1 == b {
		t.Fatal("deux clients différents doivent avoir des identifiants distincts")
	}
}
