package engcfg_test

import (
	"os"
	"path/filepath"
	"testing"

	"labosurf/internal/engcfg"
)

func TestNewProfile(t *testing.T) {
	p := engcfg.New("test")
	if p.Engine != "test" {
		t.Fatalf("Engine = %q, want %q", p.Engine, "test")
	}
	if p.Values == nil {
		t.Fatal("Values doit être initialisé")
	}
}

func TestGetFallback(t *testing.T) {
	p := engcfg.New("test")
	if got := p.Get("missing", "default"); got != "default" {
		t.Fatalf("Get = %q, want %q", got, "default")
	}
}

func TestGetInt(t *testing.T) {
	p := engcfg.New("test")
	p.SetInt("port", 8443)
	if got := p.GetInt("port", 443); got != 8443 {
		t.Fatalf("GetInt = %d, want %d", got, 8443)
	}
	if got := p.GetInt("missing", 53); got != 53 {
		t.Fatalf("GetInt fallback = %d, want %d", got, 53)
	}
}

func TestGetBool(t *testing.T) {
	p := engcfg.New("test")
	p.SetBool("enabled", true)
	if !p.GetBool("enabled", false) {
		t.Fatal("GetBool devrait retourner true")
	}
	p.SetBool("disabled", false)
	if p.GetBool("disabled", true) {
		t.Fatal("GetBool devrait retourner false")
	}
	if !p.GetBool("missing", true) {
		t.Fatal("GetBool fallback devrait retourner true")
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LABOSURF_DATA_DIR", dir)

	p := engcfg.New("slowdns")
	p.Set("backend", "10.0.0.1:22")
	p.SetInt("jitter_ms", 60)

	if err := engcfg.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Vérifie que le fichier existe au bon chemin.
	expected := filepath.Join(dir, "engines", "slowdns", "profile.json")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("fichier profil absent : %v", err)
	}

	p2, err := engcfg.Load("slowdns")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := p2.Get("backend", ""); got != "10.0.0.1:22" {
		t.Fatalf("backend = %q, want %q", got, "10.0.0.1:22")
	}
	if got := p2.GetInt("jitter_ms", 0); got != 60 {
		t.Fatalf("jitter_ms = %d, want %d", got, 60)
	}
}

func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LABOSURF_DATA_DIR", dir)

	p, err := engcfg.Load("nonexistent")
	if err != nil {
		t.Fatalf("Load d'un profil absent devrait réussir : %v", err)
	}
	if p.Engine != "nonexistent" {
		t.Fatalf("Engine = %q, want %q", p.Engine, "nonexistent")
	}
}

func TestSetOverwrite(t *testing.T) {
	p := engcfg.New("test")
	p.Set("key", "first")
	p.Set("key", "second")
	if got := p.Get("key", ""); got != "second" {
		t.Fatalf("Set overwrite = %q, want %q", got, "second")
	}
}
