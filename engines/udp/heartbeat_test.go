package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLicenseRevokedFlag_InitiallyAbsent(t *testing.T) {
	dir := t.TempDir()
	if isLicenseRevoked(dir) {
		t.Fatal("le drapeau ne doit pas exister avant toute révocation détectée")
	}
}

func TestMarkLicenseRevoked_SetsFlag(t *testing.T) {
	dir := t.TempDir()
	markLicenseRevoked(dir, "TEST-ID")
	if !isLicenseRevoked(dir) {
		t.Fatal("le drapeau doit exister après markLicenseRevoked")
	}
	raw, err := os.ReadFile(licenseRevokedFlagPath(dir))
	if err != nil {
		t.Fatalf("lecture drapeau : %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("le drapeau ne doit pas être vide (message explicatif attendu)")
	}
}

func TestMarkLicenseRevoked_Idempotent(t *testing.T) {
	dir := t.TempDir()
	markLicenseRevoked(dir, "TEST-ID")
	markLicenseRevoked(dir, "TEST-ID") // ne doit pas paniquer / échouer
	if !isLicenseRevoked(dir) {
		t.Fatal("le drapeau doit toujours exister")
	}
}

func TestLicenseKeysWithReceipts_SkipsEmptyKey(t *testing.T) {
	dir := t.TempDir()
	// Reçu ANCIEN (avant le serveur central) : pas de champ Key.
	old := InstallReceipt{LicenseID: "OLD", InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	writeReceiptForTest(t, dir, "OLD", old)
	// Reçu récent avec Key.
	withKey := InstallReceipt{LicenseID: "NEW", InstalledAt: time.Now().UTC().Format(time.RFC3339), Key: "LABOSURFTESTKEY"}
	writeReceiptForTest(t, dir, "NEW", withKey)

	keys := licenseKeysWithReceipts(dir)
	if len(keys) != 1 || keys[0] != "LABOSURFTESTKEY" {
		t.Fatalf("attendu 1 clé (LABOSURFTESTKEY), obtenu %v", keys)
	}
}

func TestLicenseKeysWithReceipts_NoReceipts_ReturnsNilNoError(t *testing.T) {
	dir := t.TempDir()
	if keys := licenseKeysWithReceipts(dir); len(keys) != 0 {
		t.Fatalf("aucun reçu -> aucune clé, obtenu %v", keys)
	}
}

// writeReceiptForTest écrit un reçu directement (sans passer par
// UseLicense/Activate) pour tester licenseKeysWithReceipts en isolation.
func writeReceiptForTest(t *testing.T, dir, id string, rec InstallReceipt) {
	t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal reçu : %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".install_"+id+".receipt"), raw, 0o600); err != nil {
		t.Fatalf("écriture reçu : %v", err)
	}
}

// TestSendHeartbeat_Revoked_SetsFlag est le test le plus important de ce
// fichier : un serveur central de TEST répond "REVOKED", et vérifie que
// (a) le drapeau local est posé, (b) AUCUNE panique, AUCUNE interruption
// du processus appelant — cohérent avec la décision "avertissement
// seulement, service non coupé".
func TestSendHeartbeat_Revoked_SetsFlag(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": "REVOKED"})
	}))
	defer srv.Close()

	sendHeartbeat(srv.URL, dir, "LABOSURFTESTKEY")

	if !isLicenseRevoked(dir) {
		t.Fatal("le drapeau de révocation doit être posé après un heartbeat signalant REVOKED")
	}
}

// TestSendHeartbeat_Active_NoFlag : un serveur répondant ACTIVE ne pose
// aucun drapeau.
func TestSendHeartbeat_Active_NoFlag(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": "ACTIVE"})
	}))
	defer srv.Close()

	sendHeartbeat(srv.URL, dir, "LABOSURFTESTKEY")

	if isLicenseRevoked(dir) {
		t.Fatal("aucun drapeau ne doit être posé quand le statut est ACTIVE")
	}
}

// TestSendHeartbeat_ServerUnreachable_NoFlag : décision explicite (§11) —
// une panne réseau/serveur ne doit JAMAIS poser le drapeau de révocation
// ni faire paniquer l'appelant.
func TestSendHeartbeat_ServerUnreachable_NoFlag(t *testing.T) {
	dir := t.TempDir()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("sendHeartbeat ne doit jamais paniquer sur une panne réseau : %v", r)
		}
	}()
	sendHeartbeat("http://127.0.0.1:1", dir, "LABOSURFTESTKEY") // port fermé
	if isLicenseRevoked(dir) {
		t.Fatal("un serveur injoignable ne doit jamais poser le drapeau de révocation")
	}
}
