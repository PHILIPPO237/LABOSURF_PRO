package service

import (
	"fmt"
	"strings"
	"time"

	"labosurf/internal/store"
)

// MigrateStatus décrit le résultat de la migration d'un Grant individuel.
type MigrateStatus int

const (
	// MigrateCreated indique qu'un nouvel Access a été créé.
	MigrateCreated MigrateStatus = iota
	// MigrateSkipped indique qu'un Access équivalent existait déjà (idempotence).
	MigrateSkipped
	// MigrateFailed indique une erreur lors de la migration de ce Grant.
	MigrateFailed
)

// MigrateResult décrit le résultat de la migration pour un Grant donné.
type MigrateResult struct {
	AccountID string
	Engine    string
	ServiceID string
	AccessID  string
	Status    MigrateStatus
	Err       error
}

// MigrateGrantsToAccess migre les Grants existants d'une liste de comptes
// (store.Account) vers des Access (service.Access).
//
// La migration est totalement idempotente : un Access déjà existant pour la
// même paire (AccountID, ServiceID) est ignoré sans erreur (MigrateSkipped).
//
// Les Grants originaux NE SONT PAS supprimés. Les deux systèmes coexistent.
//
// # Stratégie d'association Grant → Service
//
//   - Recherche des Services via ListServicesByEngine(grant.Engine).
//   - Exactement 1 service → association automatique.
//   - 0 service → MigrateFailed : créez le Service manuellement avant la migration.
//   - >1 services → MigrateFailed : ambiguïté impossible à résoudre automatiquement.
//
// # WireGuard
//
// Les adresses 10.66.0.N déjà attribuées par les anciens Grants WireGuard de
// TOUS les comptes sont collectées automatiquement comme `extraUsedWGOctets`
// passé à EnsureAccessSecrets — garantissant l'unicité entre les deux systèmes
// pendant toute la période de transition.
//
// # Quota
//
// Ancien système : Account.QuotaBytes == 0 → illimité (convention 0=unlimited).
// Nouveau système : QuotaUnlimited=true/false explicite.
// Mapping : QuotaBytes==0 → QuotaUnlimited=true ; QuotaBytes>0 → QuotaUnlimited=false.
func MigrateGrantsToAccess(accounts []store.Account) []MigrateResult {
	// Pré-calculer les octets WireGuard utilisés par les Grants existants.
	extraWG := collectGrantWGOctets(accounts)

	var results []MigrateResult
	for _, acc := range accounts {
		for engineName, grant := range acc.Grants {
			if grant == nil {
				continue
			}
			res := migrateSingleGrant(acc, engineName, grant, extraWG)
			results = append(results, res)
		}
	}
	return results
}

// migrateSingleGrant migre un Grant individuel vers un Access.
func migrateSingleGrant(
	acc store.Account,
	engineName string,
	grant *store.EngineGrant,
	extraWG map[int]bool,
) MigrateResult {
	res := MigrateResult{AccountID: acc.ID, Engine: engineName}

	// --- Découverte du Service ---
	services, err := ListServicesByEngine(engineName)
	if err != nil {
		res.Status = MigrateFailed
		res.Err = fmt.Errorf("recherche service pour moteur %q : %w", engineName, err)
		return res
	}

	var serviceID string
	switch len(services) {
	case 0:
		res.Status = MigrateFailed
		res.Err = fmt.Errorf("aucun Service avec moteur %q — créez le Service manuellement avant la migration",
			engineName)
		return res
	case 1:
		serviceID = services[0].ID
	default:
		ids := make([]string, len(services))
		for i, s := range services {
			ids[i] = s.ID
		}
		res.Status = MigrateFailed
		res.Err = fmt.Errorf("moteur %q ambigu : %d Services correspondants (%s) — association manuelle requise",
			engineName, len(services), strings.Join(ids, ", "))
		return res
	}
	res.ServiceID = serviceID

	// --- Idempotence ---
	exists, existingID, err := AccessExists(acc.ID, serviceID)
	if err != nil {
		res.Status = MigrateFailed
		res.Err = fmt.Errorf("vérification doublon Access (compte=%s, service=%s) : %w",
			acc.ID, serviceID, err)
		return res
	}
	if exists {
		res.Status = MigrateSkipped
		res.AccessID = existingID
		return res
	}

	// --- Création de l'Access ---
	a := convertGrantToAccess(acc, engineName, grant, serviceID)

	// --- Secrets manquants ---
	// Les secrets déjà présents dans grant.Config (copiés dans a.Secrets)
	// ne sont JAMAIS remplacés par EnsureAccessSecrets (idempotent).
	if err := EnsureAccessSecrets(&a, extraWG); err != nil {
		res.Status = MigrateFailed
		res.Err = fmt.Errorf("génération secrets Access : %w", err)
		return res
	}

	// --- Persistance ---
	if err := SaveAccess(&a); err != nil {
		res.Status = MigrateFailed
		res.Err = fmt.Errorf("sauvegarde Access : %w", err)
		return res
	}

	res.Status = MigrateCreated
	res.AccessID = a.ID
	return res
}

// convertGrantToAccess crée un Access à partir des données d'un Account + Grant.
// Les secrets du Grant sont copiés dans a.Secrets (pas régénérés).
func convertGrantToAccess(
	acc store.Account,
	engineName string,
	grant *store.EngineGrant,
	serviceID string,
) Access {
	a := NewAccess(acc.ID, serviceID, engineName)

	// Copie des secrets existants du Grant → Access.Secrets
	// (même format : map[string]any)
	if grant.Config != nil {
		a.Secrets = make(map[string]any, len(grant.Config))
		for k, v := range grant.Config {
			a.Secrets[k] = v
		}
	}

	// Quota : ancien système 0=illimité → nouveau système QuotaUnlimited=bool
	if acc.QuotaBytes == 0 {
		a.QuotaUnlimited = true
		a.QuotaLimitBytes = 0
	} else {
		a.QuotaUnlimited = false
		a.QuotaLimitBytes = acc.QuotaBytes
	}
	a.UsedBytes = acc.UsedBytes

	// MaxConnections : correspondance directe.
	a.MaxConnections = acc.MaxConnections

	// MaxSourceIPs : correspondance directe avec Account.MaxIPs.
	// MaxIPs dans l'ancien système est une contrainte réseau (nombre d'adresses IP
	// sources simultanées actives), contrôlée par le SessionManager UDP.
	// Cette notion est distincte de MaxDevices (appareils physiques) — une IP
	// peut regrouper plusieurs appareils (NAT) et un appareil peut changer d'IP.
	// On la préserve telle quelle dans MaxSourceIPs plutôt que de la convertir
	// en MaxDevices, ce qui serait architecturalement incorrect.
	a.MaxSourceIPs = acc.MaxIPs

	// MaxDevices : pas de correspondance directe dans l'ancien système.
	// L'ancien modèle ne distinguait pas les appareils physiques des adresses IP.
	// On laisse MaxDevices à 0 (illimité) après migration ; l'opérateur peut le
	// fixer explicitement via les menus M4.
	a.MaxDevices = 0

	// Expiration (string RFC3339 ou "" — même format)
	a.ExpiresAt = acc.ExpiresAt

	// Enabled : le compte ET le grant doivent être actifs
	a.Enabled = acc.Enabled && grant.Enabled

	// Timestamp de création : préserver celui du compte si disponible
	if acc.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, acc.CreatedAt); err == nil {
			a.CreatedAt = t
		}
	}

	return a
}

// collectGrantWGOctets collecte les octets WireGuard (10.66.0.N) déjà
// attribués dans les anciens Grants de tous les comptes passés en paramètre.
// Cette map est transmise à EnsureAccessSecrets pour éviter les collisions
// d'adresses entre l'ancien système et le nouveau pendant la transition.
func collectGrantWGOctets(accounts []store.Account) map[int]bool {
	used := make(map[int]bool)
	const addrFmt = "10.66.0.%d/32"
	for _, acc := range accounts {
		g := acc.Grants[store.EngineWireGuard]
		if g == nil || g.Config == nil {
			continue
		}
		if addr, ok := g.Config["address"].(string); ok {
			var n int
			if _, err := fmt.Sscanf(addr, addrFmt, &n); err == nil {
				used[n] = true
			}
		}
	}
	return used
}

// MigrateStats retourne un résumé statistique d'une liste de résultats.
func MigrateStats(results []MigrateResult) (created, skipped, failed int) {
	for _, r := range results {
		switch r.Status {
		case MigrateCreated:
			created++
		case MigrateSkipped:
			skipped++
		case MigrateFailed:
			failed++
		}
	}
	return
}
