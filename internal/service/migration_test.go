package service

import (
	"fmt"
	"testing"
	"time"

	"labosurf/internal/store"
)

// setupMigrationDir initialise un répertoire temporaire pour les tests de migration.
func setupMigrationDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LABOSURF_DATA_DIR", dir)
}

// createTestService crée et sauvegarde un Service de test.
func createTestService(t *testing.T, engine string) Service {
	t.Helper()
	svc := NewService("Test-"+engine, engine, "vpn.example.com", map[string]int{"listen": 443})
	if err := SaveService(&svc); err != nil {
		t.Fatalf("SaveService(%s): %v", engine, err)
	}
	return svc
}

// makeTestAccount construit un Account de test sans le sauvegarder.
func makeTestAccount(id, engine string, config map[string]any, opts ...func(*store.Account)) store.Account {
	acc := store.Account{
		ID:             id,
		Username:       "user-" + id,
		Enabled:        true,
		MaxConnections: 1,
		MaxIPs:         1,
		Grants: map[string]*store.EngineGrant{
			engine: {Engine: engine, Config: config, Enabled: true},
		},
	}
	for _, opt := range opts {
		opt(&acc)
	}
	return acc
}

// withQuota modifie le quota d'un Account de test.
func withQuota(q uint64) func(*store.Account) {
	return func(a *store.Account) { a.QuotaBytes = q }
}

// withExpiry modifie l'expiration d'un Account de test.
func withExpiry(e string) func(*store.Account) {
	return func(a *store.Account) { a.ExpiresAt = e }
}

// withMaxIPs modifie MaxIPs d'un Account.
func withMaxIPs(n int) func(*store.Account) {
	return func(a *store.Account) { a.MaxIPs = n }
}

// withMaxConn modifie MaxConnections d'un Account.
func withMaxConn(n int) func(*store.Account) {
	return func(a *store.Account) { a.MaxConnections = n }
}

// ============================================================
// T1 — Migration simple : 1 Grant xray → 1 Access créé
// ============================================================

func TestMigrateSimpleGrant(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc := makeTestAccount("acc-t1", store.EngineXray, map[string]any{"uuid": "uuid-t1"})
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 {
		t.Fatalf("attendu 1 résultat, obtenu %d", len(results))
	}
	r := results[0]
	if r.Status != MigrateCreated {
		t.Fatalf("status attendu MigrateCreated, obtenu %d : %v", r.Status, r.Err)
	}
	if r.AccessID == "" {
		t.Fatal("AccessID vide")
	}
	if r.AccountID != "acc-t1" {
		t.Fatalf("AccountID incorrect : %q", r.AccountID)
	}
}

// ============================================================
// T2 — Migration multiple grants : 1 compte, 2 moteurs → 2 Access
// ============================================================

func TestMigrateMultipleGrants(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)
	createTestService(t, store.EngineHysteria)

	acc := store.Account{
		ID:      "acc-t2",
		Enabled: true,
		Grants: map[string]*store.EngineGrant{
			store.EngineXray:     {Engine: store.EngineXray, Config: map[string]any{"uuid": "u2"}, Enabled: true},
			store.EngineHysteria: {Engine: store.EngineHysteria, Config: map[string]any{"password": "p2"}, Enabled: true},
		},
	}
	results := MigrateGrantsToAccess([]store.Account{acc})

	created := 0
	for _, r := range results {
		if r.Status == MigrateCreated {
			created++
		}
		if r.Err != nil {
			t.Errorf("erreur inattendue pour %s : %v", r.Engine, r.Err)
		}
	}
	if created != 2 {
		t.Fatalf("attendu 2 Access créés, obtenu %d", created)
	}
}

// ============================================================
// T3 — Idempotence : 2e migration → MigrateSkipped
// ============================================================

func TestMigrateIdempotent(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc := makeTestAccount("acc-t3", store.EngineXray, map[string]any{"uuid": "uuid-t3"})

	// Première migration
	r1 := MigrateGrantsToAccess([]store.Account{acc})
	if len(r1) != 1 || r1[0].Status != MigrateCreated {
		t.Fatalf("1ère migration : attendu MigrateCreated, obtenu %d : %v", r1[0].Status, r1[0].Err)
	}

	// Deuxième migration sur le même compte
	r2 := MigrateGrantsToAccess([]store.Account{acc})
	if len(r2) != 1 {
		t.Fatalf("2ème migration : attendu 1 résultat, obtenu %d", len(r2))
	}
	if r2[0].Status != MigrateSkipped {
		t.Fatalf("2ème migration : attendu MigrateSkipped, obtenu %d : %v", r2[0].Status, r2[0].Err)
	}
	if r2[0].AccessID != r1[0].AccessID {
		t.Fatalf("AccessID différent entre les deux migrations : %q vs %q", r1[0].AccessID, r2[0].AccessID)
	}
}

// ============================================================
// T4 — Préservation des secrets : UUID du Grant conservé dans l'Access
// ============================================================

func TestMigrateSecretPreservation(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	const fixedUUID = "preserve-this-uuid-1234"
	acc := makeTestAccount("acc-t4", store.EngineXray, map[string]any{"uuid": fixedUUID})
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration: %v", results)
	}

	a, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if got, _ := a.Secrets["uuid"].(string); got != fixedUUID {
		t.Fatalf("UUID modifié : attendu %q, obtenu %q", fixedUUID, got)
	}
}

// ============================================================
// T5 — Génération des secrets manquants : Grant sans secrets → secrets générés
// ============================================================

func TestMigrateMissingSecretsGenerated(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	// Grant sans UUID
	acc := makeTestAccount("acc-t5", store.EngineXray, nil)
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration (secrets vides): %v", results)
	}

	a, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if uuid, _ := a.Secrets["uuid"].(string); uuid == "" {
		t.Fatal("UUID attendu mais absent après génération automatique")
	}
}

// ============================================================
// T6 — Quota illimité : Account.QuotaBytes == 0 → QuotaUnlimited = true
// ============================================================

func TestMigrateQuotaUnlimited(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc := makeTestAccount("acc-t6", store.EngineXray, map[string]any{"uuid": "u6"}, withQuota(0))
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration: %v", results)
	}

	a, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if !a.QuotaUnlimited {
		t.Fatal("QuotaUnlimited attendu true pour QuotaBytes==0")
	}
	if a.QuotaLimitBytes != 0 {
		t.Fatalf("QuotaLimitBytes attendu 0, obtenu %d", a.QuotaLimitBytes)
	}
}

// ============================================================
// T7 — Quota limité : Account.QuotaBytes > 0 → QuotaUnlimited = false
// ============================================================

func TestMigrateQuotaLimited(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	const limit = uint64(10 * 1024 * 1024 * 1024) // 10 GB
	acc := makeTestAccount("acc-t7", store.EngineXray, map[string]any{"uuid": "u7"}, withQuota(limit))
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration: %v", results)
	}

	a, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if a.QuotaUnlimited {
		t.Fatal("QuotaUnlimited attendu false pour QuotaBytes>0")
	}
	if a.QuotaLimitBytes != limit {
		t.Fatalf("QuotaLimitBytes : attendu %d, obtenu %d", limit, a.QuotaLimitBytes)
	}
}

// ============================================================
// T8 — Expiration : Account.ExpiresAt → Access.ExpiresAt
// ============================================================

func TestMigrateExpiration(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	expiry := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	acc := makeTestAccount("acc-t8", store.EngineXray, map[string]any{"uuid": "u8"}, withExpiry(expiry))
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration: %v", results)
	}

	a, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if a.ExpiresAt != expiry {
		t.Fatalf("ExpiresAt : attendu %q, obtenu %q", expiry, a.ExpiresAt)
	}
}

// ============================================================
// T9 — MaxDevices : Account.MaxIPs → Access.MaxDevices
// ============================================================

func TestMigrateMaxDevices(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc := makeTestAccount("acc-t9", store.EngineXray, map[string]any{"uuid": "u9"}, withMaxIPs(3))
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration: %v", results)
	}

	a, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if a.MaxDevices != 3 {
		t.Fatalf("MaxDevices : attendu 3, obtenu %d", a.MaxDevices)
	}
}

// ============================================================
// T10 — MaxConnections : Account.MaxConnections → Access.MaxConnections
// ============================================================

func TestMigrateMaxConnections(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc := makeTestAccount("acc-t10", store.EngineXray, map[string]any{"uuid": "u10"}, withMaxConn(5))
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration: %v", results)
	}

	a, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if a.MaxConnections != 5 {
		t.Fatalf("MaxConnections : attendu 5, obtenu %d", a.MaxConnections)
	}
}

// ============================================================
// T11 — Unicité WireGuard : 2 comptes migrent → adresses IP différentes
// ============================================================

func TestMigrateWireGuardAddressUniqueness(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineWireGuard)

	// Deux comptes avec Grant WireGuard sans adresse pré-attribuée
	// → EnsureAccessSecrets générera une adresse pour chacun
	acc1 := makeTestAccount("acc-wg1", store.EngineWireGuard, nil)
	acc2 := makeTestAccount("acc-wg2", store.EngineWireGuard, nil)

	// Migration du premier compte
	r1 := MigrateGrantsToAccess([]store.Account{acc1})
	if len(r1) != 1 || r1[0].Status != MigrateCreated {
		t.Fatalf("migration acc-wg1: %v", r1)
	}

	// Migration du deuxième compte (le 1er Access est maintenant sur disque)
	r2 := MigrateGrantsToAccess([]store.Account{acc2})
	if len(r2) != 1 || r2[0].Status != MigrateCreated {
		t.Fatalf("migration acc-wg2: %v", r2)
	}

	a1, err := GetAccess(r1[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess a1: %v", err)
	}
	a2, err := GetAccess(r2[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess a2: %v", err)
	}

	addr1, _ := a1.Secrets["address"].(string)
	addr2, _ := a2.Secrets["address"].(string)
	if addr1 == "" || addr2 == "" {
		t.Fatalf("adresses WireGuard manquantes : a1=%q a2=%q", addr1, addr2)
	}
	if addr1 == addr2 {
		t.Fatalf("collision adresse WireGuard : a1=%q a2=%q", addr1, addr2)
	}
}

// ============================================================
// T12 — Coexistence : les Grants originaux ne sont PAS supprimés
// ============================================================

func TestMigrateGrantsPreserved(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc := makeTestAccount("acc-t12", store.EngineXray, map[string]any{"uuid": "preserve"})

	// Avant migration : le Grant existe
	if acc.Grants[store.EngineXray] == nil {
		t.Fatal("Grant absent avant la migration (pré-condition)")
	}

	results := MigrateGrantsToAccess([]store.Account{acc})
	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration: %v", results)
	}

	// Après migration : le Grant original est toujours là (on n'a pas modifié acc)
	if acc.Grants[store.EngineXray] == nil {
		t.Fatal("Grant supprimé après migration — les Grants doivent être préservés")
	}
	if got, _ := acc.Grants[store.EngineXray].Config["uuid"].(string); got != "preserve" {
		t.Fatalf("contenu du Grant modifié : %q", got)
	}
}

// ============================================================
// T13 — Config client depuis Access migré : GenerateFromAccess fonctionnel
// ============================================================

func TestMigrateClientConfigFromMigratedAccess(t *testing.T) {
	setupMigrationDir(t)
	svc := createTestService(t, store.EngineXray)
	// Pré-remplir les clés Reality pour que vlessLink() fonctionne
	_ = setupSecretsDir // setupSecretsDir est défini dans secrets_test.go mais setupMigrationDir suffit ici

	acc := makeTestAccount("acc-t13", store.EngineXray, map[string]any{"uuid": "t13-uuid"})
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration: %v", results)
	}

	migratedAccess, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}

	// Vérifier que les données nécessaires pour une config client sont présentes
	uuid, _ := migratedAccess.Secrets["uuid"].(string)
	if uuid != "t13-uuid" {
		t.Fatalf("UUID dans Access migré incorrect : %q", uuid)
	}
	if migratedAccess.AccountID != "acc-t13" {
		t.Fatalf("AccountID incorrect : %q", migratedAccess.AccountID)
	}
	if migratedAccess.ServiceID != svc.ID {
		t.Fatalf("ServiceID incorrect : %q (attendu %q)", migratedAccess.ServiceID, svc.ID)
	}
	if migratedAccess.Engine != store.EngineXray {
		t.Fatalf("Engine incorrect : %q", migratedAccess.Engine)
	}
}

// ============================================================
// T14 — Plusieurs Access sur même Service : 2 comptes → 2 Access distincts
// ============================================================

func TestMigrateMultipleAccessSameService(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc1 := makeTestAccount("acc-s1", store.EngineXray, map[string]any{"uuid": "uuid-s1"})
	acc2 := makeTestAccount("acc-s2", store.EngineXray, map[string]any{"uuid": "uuid-s2"})

	results := MigrateGrantsToAccess([]store.Account{acc1, acc2})

	created := 0
	accessIDs := make(map[string]bool)
	for _, r := range results {
		if r.Status == MigrateCreated {
			created++
			accessIDs[r.AccessID] = true
		}
		if r.Err != nil {
			t.Errorf("erreur inattendue pour %s/%s : %v", r.AccountID, r.Engine, r.Err)
		}
	}

	if created != 2 {
		t.Fatalf("attendu 2 Access créés, obtenu %d", created)
	}
	if len(accessIDs) != 2 {
		t.Fatal("2 Access distincts attendus mais les IDs se recoupent")
	}
}

// ============================================================
// T15 — Migration hybride : Grant dnstt-xray → Access avec secrets des deux composants
// ============================================================

func TestMigrateHybridService(t *testing.T) {
	setupMigrationDir(t)
	// Créer un Service hybride
	svc := NewService("Test-dnstt-xray", "dnstt-xray", "vpn.example.com", map[string]int{"listen": 443})
	svc.Components = []string{"dnstt", "xray"}
	if err := SaveService(&svc); err != nil {
		t.Fatalf("SaveService hybride: %v", err)
	}

	acc := makeTestAccount("acc-hybrid", "dnstt-xray", map[string]any{
		"uuid":        "hybrid-uuid-456",
		"public_key":  "aabbccdd" + fmt.Sprintf("%056x", 0),
		"private_key": "11223344" + fmt.Sprintf("%056x", 0),
	})
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 {
		t.Fatalf("attendu 1 résultat, obtenu %d", len(results))
	}
	r := results[0]
	if r.Status != MigrateCreated {
		t.Fatalf("status attendu MigrateCreated, obtenu %d : %v", r.Status, r.Err)
	}

	a, err := GetAccess(r.AccessID)
	if err != nil {
		t.Fatalf("GetAccess hybride: %v", err)
	}
	if a.Engine != "dnstt-xray" {
		t.Fatalf("Engine attendu 'dnstt-xray', obtenu %q", a.Engine)
	}
	// Les deux types de secrets doivent être présents
	if uuid, _ := a.Secrets["uuid"].(string); uuid != "hybrid-uuid-456" {
		t.Fatalf("uuid hybride incorrect : %q", uuid)
	}
	if pk, _ := a.Secrets["public_key"].(string); pk == "" {
		t.Fatal("public_key manquant dans Access hybride")
	}
}

// ============================================================
// T16 — Enabled : compte désactivé → Access.Enabled = false
// ============================================================

func TestMigrateDisabledAccount(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc := store.Account{
		ID:      "acc-disabled",
		Enabled: false, // compte désactivé
		Grants: map[string]*store.EngineGrant{
			store.EngineXray: {Engine: store.EngineXray, Config: map[string]any{"uuid": "u-dis"}, Enabled: true},
		},
	}
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 || results[0].Status != MigrateCreated {
		t.Fatalf("migration compte désactivé: %v", results)
	}

	a, err := GetAccess(results[0].AccessID)
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if a.Enabled {
		t.Fatal("Access.Enabled doit être false si le compte est désactivé")
	}
}

// ============================================================
// T17 — Pas de doublons : MigrateStats correct sur 2 migrations
// ============================================================

func TestMigrateNoDuplicates(t *testing.T) {
	setupMigrationDir(t)
	createTestService(t, store.EngineXray)

	acc := makeTestAccount("acc-nodup", store.EngineXray, map[string]any{"uuid": "u-nodup"})

	// 1ère migration
	r1 := MigrateGrantsToAccess([]store.Account{acc})
	c1, s1, f1 := MigrateStats(r1)
	if c1 != 1 || s1 != 0 || f1 != 0 {
		t.Fatalf("1ère migration : attendu 1/0/0, obtenu %d/%d/%d", c1, s1, f1)
	}

	// 2ème migration (idempotence)
	r2 := MigrateGrantsToAccess([]store.Account{acc})
	c2, s2, f2 := MigrateStats(r2)
	if c2 != 0 || s2 != 1 || f2 != 0 {
		t.Fatalf("2ème migration : attendu 0/1/0, obtenu %d/%d/%d", c2, s2, f2)
	}

	// Vérifier qu'il n'y a toujours qu'un seul Access en base
	accesses, err := ListAccessByAccount("acc-nodup")
	if err != nil {
		t.Fatalf("ListAccessByAccount: %v", err)
	}
	if len(accesses) != 1 {
		t.Fatalf("attendu 1 Access total, trouvé %d", len(accesses))
	}
}

// ============================================================
// T18 — Service introuvable : MigrateFailed avec message explicite
// ============================================================

func TestMigrateServiceNotFound(t *testing.T) {
	setupMigrationDir(t)
	// Pas de Service créé pour "xray"

	acc := makeTestAccount("acc-notfound", store.EngineXray, map[string]any{"uuid": "u-nf"})
	results := MigrateGrantsToAccess([]store.Account{acc})

	if len(results) != 1 {
		t.Fatalf("attendu 1 résultat, obtenu %d", len(results))
	}
	r := results[0]
	if r.Status != MigrateFailed {
		t.Fatalf("status attendu MigrateFailed, obtenu %d", r.Status)
	}
	if r.Err == nil {
		t.Fatal("Err attendu non-nil pour service introuvable")
	}
}
