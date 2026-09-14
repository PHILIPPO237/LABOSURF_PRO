# M3 — Migration Grants → Access + Config Serveur depuis Access

**Date :** 2026-09-14  
**Statut :** IMPLÉMENTÉ — tests OK, non committé  
**Branche :** main  
**Fichiers créés :**
- `internal/service/migration.go`
- `internal/service/migration_test.go`
- `internal/clientcfg/server_access.go`

---

## 1. Objectif

M3 assure la transition du système **Grants** (store.Account → store.EngineGrant) vers le système **Access** (service.Access) introduit en M1/M2, en fournissant :

1. `MigrateGrantsToAccess` — migration idempotente de Grants existants vers des Access.
2. `ApplyServerConfigFromAccess` — nouvelle API de config serveur depuis des Access, sans modifier l'ancienne `ApplyServerConfig`.

---

## 2. Fichiers créés

### `internal/service/migration.go`

| Symbole | Rôle |
|---------|------|
| `MigrateStatus` (iota) | `MigrateCreated`, `MigrateSkipped`, `MigrateFailed` |
| `MigrateResult` | AccountID, Engine, ServiceID, AccessID, Status, Err |
| `MigrateGrantsToAccess(accounts []store.Account) []MigrateResult` | Point d'entrée principal — idempotent |
| `migrateSingleGrant(acc, engine, grant, extraWG)` | Migration d'un Grant individuel |
| `convertGrantToAccess(acc, engine, grant, serviceID)` | Conversion Grant → Access |
| `collectGrantWGOctets(accounts) map[int]bool` | Collecte octets WireGuard des Grants existants |
| `MigrateStats(results) (created, skipped, failed int)` | Résumé statistique |

### `internal/service/migration_test.go`

18 tests couvrant tous les aspects de la migration (voir §15).

### `internal/clientcfg/server_access.go`

| Symbole | Rôle |
|---------|------|
| `ApplyServerConfigFromAccess(ctx, svc, accesses, prof)` | Nouvelle API config serveur depuis Access |
| `accessesToSyntheticAccounts(accesses, engineName)` | Bridge Access → store.Account synthétiques |

---

## 3. Idempotence

`MigrateGrantsToAccess` est totalement idempotente :

- Avant de créer un Access, `AccessExists(accountID, serviceID)` est appelé.
- Si un Access existe déjà pour cette paire → `MigrateSkipped` (AccessID de l'existant retourné).
- Les secrets du Grant ne sont **jamais** régénérés si déjà présents (`EnsureAccessSecrets` est idempotent).
- La fonction peut être appelée plusieurs fois de suite sans effet de bord.

---

## 4. Stratégie d'association Grant → Service

Les Grants n'ont pas de ServiceID. La stratégie d'association est :

```
ListServicesByEngine(grant.Engine) →
  0 services  → MigrateFailed : créer le Service manuellement
  1 service   → association automatique
  >1 services → MigrateFailed : ambiguïté, association manuelle requise
```

**Limitation documentée** : si plusieurs Services utilisent le même moteur, la migration échoue volontairement sur les Grants concernés. L'opérateur doit choisir manuellement le Service cible et créer l'Access directement.

---

## 5. Copie des secrets

```go
// Dans convertGrantToAccess :
if grant.Config != nil {
    a.Secrets = make(map[string]any, len(grant.Config))
    for k, v := range grant.Config {
        a.Secrets[k] = v
    }
}
```

- Les secrets existants du Grant sont copiés **bit pour bit** dans `Access.Secrets`.
- `EnsureAccessSecrets(&a, extraWG)` est ensuite appelé — il ne remplace aucun secret déjà présent.
- Résultat : les UUIDs xray, mots de passe hysteria, clés WireGuard, etc. sont **préservés à l'identique**.

---

## 6. WireGuard — unicité des adresses (transition)

Pendant la coexistence des deux systèmes, les adresses `10.66.0.N` attribuées par les **anciens Grants** WireGuard doivent être exclues des nouveaux Access.

```go
extraWG := collectGrantWGOctets(accounts)
// → map[int]bool{5: true, 12: true, ...}
// transmis à EnsureAccessSecrets pour chaque Access migré
```

`collectGrantWGOctets` scanne `acc.Grants["wireguard"].Config["address"]` pour tous les comptes. `EnsureAccessSecrets` + `nextAccessWireGuardAddress` scannent ensuite aussi les fichiers Access déjà persistés. Les deux barrières combinées garantissent l'unicité totale.

---

## 7. Mapping Quota

| Ancien système | Nouveau système |
|----------------|-----------------|
| `Account.QuotaBytes == 0` | `Access.QuotaUnlimited = true` (illimité explicite) |
| `Account.QuotaBytes > 0` | `Access.QuotaUnlimited = false`, `QuotaLimitBytes = QuotaBytes` |

L'ambiguïté de l'ancien `0 = illimité` est éliminée dans le nouveau système.

---

## 8. Mapping Appareils / Connexions

| Ancien champ | Nouveau champ | Note |
|--------------|---------------|------|
| `Account.MaxIPs` | `Access.MaxDevices` | Appareils distincts (IPs sources) |
| `Account.MaxConnections` | `Access.MaxConnections` | Connexions simultanées |

---

## 9. Expiration

`Account.ExpiresAt` (string RFC3339) → `Access.ExpiresAt` (même format, copie directe). Pas de conversion.

---

## 10. Enabled

```go
a.Enabled = acc.Enabled && grant.Enabled
```

Un Access est actif uniquement si **le compte ET le Grant** sont tous deux actifs.

---

## 11. Grants préservés

Les Grants originaux ne sont **pas supprimés** par la migration. Les deux systèmes coexistent pendant la transition M3 → M4.

---

## 12. `ApplyServerConfigFromAccess` — bridge synthétique

Pour réutiliser `buildGroupedConfig` et `buildComponentConfigs` sans duplication :

```go
func accessesToSyntheticAccounts(accesses []service.Access, engineName string) []store.Account {
    out := make([]store.Account, 0, len(accesses))
    for _, a := range accesses {
        acc := store.Account{ID: a.AccountID, Enabled: a.Enabled}
        acc.Grants = map[string]*store.EngineGrant{
            engineName: {Engine: engineName, Config: a.Secrets, Enabled: a.Enabled},
        }
        out = append(out, acc)
    }
    return out
}
```

Les `[]store.Account` synthétiques sont passés directement à :
- `buildGroupedConfig(svc.Engine, synth, prof)` pour les moteurs simples
- `buildComponentConfigs(svc.Engine, ce.Components, synth, prof)` pour les hybrides

`aliasGrantForComponent` fonctionne correctement car les synthétiques ont leur Grant sous le nom hybride (ex: `"dnstt-xray"`), que la fonction copie ensuite sous chaque nom de composant.

---

## 13. Hybrides

Un Grant hybride (ex: engine `"dnstt-xray"`) contient les secrets de tous les composants dans une seule `map[string]any`. Ce format est identique à `Access.Secrets` pour un hybride. La migration fonctionne donc nativement :

```
Grant.Config["uuid"]       → Access.Secrets["uuid"]       (xray)
Grant.Config["public_key"] → Access.Secrets["public_key"] (dnstt)
Grant.Config["private_key"]→ Access.Secrets["private_key"](dnstt)
```

---

## 14. Régressions interdites

- `ApplyServerConfig` (ancienne API) : **inchangée**.
- `Generate` (clientcfg, ancienne API) : **inchangée**.
- `store.Account`, `store.EngineGrant` : **inchangés**.
- Aucun menu modifié.
- Aucune interface utilisateur touchée.

---

## 15. Tests — liste complète (18 tests)

| # | Nom | Ce qui est vérifié |
|---|-----|--------------------|
| T1 | `TestMigrateSimpleGrant` | 1 Grant xray → 1 Access MigrateCreated |
| T2 | `TestMigrateMultipleGrants` | 1 compte, 2 moteurs → 2 Access distincts |
| T3 | `TestMigrateIdempotent` | 2e migration → MigrateSkipped, même AccessID |
| T4 | `TestMigrateSecretPreservation` | UUID Grant conservé dans Access.Secrets |
| T5 | `TestMigrateMissingSecretsGenerated` | Grant vide → secrets générés automatiquement |
| T6 | `TestMigrateQuotaUnlimited` | QuotaBytes==0 → QuotaUnlimited=true |
| T7 | `TestMigrateQuotaLimited` | QuotaBytes>0 → QuotaUnlimited=false, QuotaLimitBytes correct |
| T8 | `TestMigrateExpiration` | ExpiresAt préservé tel quel |
| T9 | `TestMigrateMaxDevices` | Account.MaxIPs → Access.MaxDevices |
| T10 | `TestMigrateMaxConnections` | Account.MaxConnections → Access.MaxConnections |
| T11 | `TestMigrateWireGuardAddressUniqueness` | 2 comptes WG → adresses différentes |
| T12 | `TestMigrateGrantsPreserved` | Grants originaux non supprimés |
| T13 | `TestMigrateClientConfigFromMigratedAccess` | Access migré contient toutes les données nécessaires |
| T14 | `TestMigrateMultipleAccessSameService` | 2 comptes → 2 Access distincts sur 1 Service |
| T15 | `TestMigrateHybridService` | Grant dnstt-xray → Access hybride correct |
| T16 | `TestMigrateDisabledAccount` | acc.Enabled=false → Access.Enabled=false |
| T17 | `TestMigrateNoDuplicates` | MigrateStats : 1/0/0 puis 0/1/0, 1 seul Access en base |
| T18 | `TestMigrateServiceNotFound` | Aucun Service → MigrateFailed avec erreur explicite |

**Résultat :** 18/18 PASS (`go test ./internal/service/... -v`)

---

## 16. `go build` et `go vet`

```
go build ./internal/service/     → OK (0 erreur)
go build ./internal/clientcfg/   → OK (0 erreur)
go vet ./internal/service/...    → OK (0 avertissement propre à M3)
```

**Failure pré-existante (non causée par M3) :**
```
engines/ssh/server.go:29: undefined: syscall.Credential
```
`syscall.Credential` est une API Linux uniquement. Cette erreur existait depuis la V2, avant M1/M2/M3. Elle affecte uniquement le binaire de test de `clientcfg` sur Windows. Le build du package `clientcfg` seul (`go build ./internal/clientcfg/`) réussit.

---

## 17. Git

Aucun commit effectué. Aucun push effectué. Conformément aux instructions M3 §17.

Fichiers en attente de commit (M1 + M2 + M3) :
```
internal/service/         (6 fichiers M1 + migration.go + migration_test.go)
internal/clientcfg/access.go
internal/clientcfg/access_test.go
internal/clientcfg/server_access.go
docs/M1_SERVICE_ACCESS_IMPLEMENTATION.md
docs/M2_ACCESS_SECRETS_CLIENTCFG_IMPLEMENTATION.md
docs/M3_GRANTS_TO_ACCESS_SERVER_CONFIG_IMPLEMENTATION.md
docs/ARCHITECTURE_SERVEURS_ABONNES_CONCEPTION_FINALE.md
docs/ARCHITECTURE_SERVEURS_ABONNES_ANALYSE.md (modifié)
```

---

## 18. Dépendances entre packages

```
service  →  store               (Migration lit store.Account/EngineGrant)
service  →  (interne)           (EnsureAccessSecrets, SaveAccess, ListServicesByEngine)
clientcfg → service             (ApplyServerConfigFromAccess reçoit service.Service, []service.Access)
clientcfg → store               (accessesToSyntheticAccounts produit []store.Account)
clientcfg → engine, engineutil  (CompositeEngine, Configure)
clientcfg → srvcfg              (Profile)
```

Pas de dépendance circulaire introduite.

---

## 19. Contrat de la migration — résumé

| Propriété | Garantie |
|-----------|----------|
| Idempotence | Un Access existant → MigrateSkipped, jamais de doublon |
| Secrets | Copiés depuis Grant.Config, jamais régénérés si présents |
| Secrets manquants | Générés par EnsureAccessSecrets (idempotent) |
| Grants | Jamais supprimés, jamais modifiés |
| WireGuard | Unicité garantie entre anciens Grants et nouveaux Access |
| Quota | Mapping explicite (0=illimité dans ancien → QuotaUnlimited=true) |
| Service ambigu | MigrateFailed explicite (>1 services pour un moteur) |
| Service absent | MigrateFailed explicite (0 services pour un moteur) |

---

## 20. Prochaine étape (M4 — hors périmètre M3)

M4 : menus [3] SERVICES et [5] ACCÈS. Non démarré, non planifié dans cette session.

---

## 21. Contraintes de sécurité respectées

- Aucun mécanisme de contournement de contrôles ou de facturation opérateur.
- Aucun secret réel dans les fichiers versionnés (tests utilisent des valeurs factices).
- Secrets traités séparément, jamais loggués.
- Système destiné exclusivement à l'administration d'infrastructures autorisées.
- Headers, transports, proxys implémentés selon leur fonctionnement réseau légitime uniquement.

---

## 22. Fonctions publiques exposées par M3

```go
// internal/service/migration.go
func MigrateGrantsToAccess(accounts []store.Account) []MigrateResult
func MigrateStats(results []MigrateResult) (created, skipped, failed int)

// Types
type MigrateStatus int  // MigrateCreated, MigrateSkipped, MigrateFailed
type MigrateResult struct { AccountID, Engine, ServiceID, AccessID string; Status MigrateStatus; Err error }

// internal/clientcfg/server_access.go
func ApplyServerConfigFromAccess(ctx context.Context, svc service.Service, accesses []service.Access, prof srvcfg.Profile) error
```

---

## 23. Bilan M3

**Lignes de code :**
- `migration.go` : ~160 lignes
- `migration_test.go` : ~320 lignes
- `server_access.go` : ~75 lignes

**Tests :** 18 nouveaux tests migration (tous PASS) + 18 secrets (tous PASS) + 55 tests M1 existants = **91 tests PASS** dans `./internal/service/...`

**Objectifs M3 atteints :**
- [x] Migration idempotente Grants → Access
- [x] Préservation des secrets existants
- [x] Génération des secrets manquants
- [x] Unicité WireGuard pendant la transition
- [x] Mapping Quota, MaxDevices, MaxConnections, ExpiresAt, Enabled
- [x] Association Grant → Service (1 service = OK, ambiguïté = erreur explicite)
- [x] ApplyServerConfigFromAccess (nouvelle API, ancienne inchangée)
- [x] Support hybrides (bridge synthétique)
- [x] 18 tests obligatoires
- [x] Documentation (ce fichier)
- [x] Aucun commit / push
- [x] Aucune modification des menus
