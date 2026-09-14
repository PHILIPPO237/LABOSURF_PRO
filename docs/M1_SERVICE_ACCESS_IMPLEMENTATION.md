# M1 — Service / Access : rapport d'implémentation

> Date : 2026-09-14
> Branche : main
> Commit de base : `7c9c824`
> Aucun code fonctionnel existant modifié.

---

## 1. Fichiers créés

| Fichier | Rôle |
|---------|------|
| `internal/service/service.go` | Types `Service` et `Access`, constructeurs, helpers |
| `internal/service/store.go` | CRUD persisté (services/ et access/), écriture atomique |
| `internal/service/validate.go` | Validations `ValidateService` et `ValidateAccess` |
| `internal/service/service_test.go` | 34 tests unitaires couvrant CRUD, validations, quota, expiration, hybrides |

---

## 2. Structures créées

### `service.Service`

```go
type Service struct {
    ID         string
    Name       string
    Engine     string         // ex: "xray", "dnstt-xray"
    ProfileID  string
    Components []string       // pour les hybrides, dans l'ordre du chaînage
    Host       string
    Ports      map[string]int // {"listen": port, "public": port}
    Domains    []string
    TLSMode    string         // "reality" | "tls" | "none"
    Enabled    bool
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

Méthodes : `IsHybrid()`, `StatusBullet()`, `StatusLabel()`, `ListenPort()`

### `service.Access`

```go
type Access struct {
    ID              string
    AccountID       string
    ServiceID       string
    Engine          string
    Secrets         map[string]any
    QuotaUnlimited  bool
    QuotaLimitBytes uint64
    UsedBytes       int64
    MaxDevices      int
    MaxConnections  int
    ExpiresAt       string   // RFC3339 ou ""
    Enabled         bool
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

Méthodes : `IsExpired()`, `QuotaExceeded()`, `StatusBullet()`

---

## 3. Persistance

- Fichiers : `$LABOSURF_DATA_DIR/services/<id>.json` et `$LABOSURF_DATA_DIR/access/<id>.json`
- Un fichier par entité (convention identique au package `internal/profile/`)
- Écriture atomique : fichier temporaire `.tmp` + `os.Rename` (convention identique à `srvcfg.Save()`)
- Permissions 0o600 sur les fichiers, 0o700 sur les répertoires
- `LABOSURF_DATA_DIR` lu depuis l'environnement (fallback `/etc/labosurf` identique au reste du projet)
- Jamais de chemin codé en dur

---

## 4. CRUD

### Services

| Fonction | Comportement |
|----------|-------------|
| `SaveService(s *Service)` | Valide + met à jour `UpdatedAt` + écriture atomique |
| `GetService(id)` | Charge par ID, retourne `ErrServiceNotFound` si absent |
| `GetServiceByName(name)` | Recherche insensible à la casse |
| `ListServices()` | Tous les services, répertoire absent = liste vide sans erreur |
| `ListServicesByEngine(engine)` | Filtre par nom de moteur exact |
| `DeleteService(id)` | Refuse si des Access référencent ce service (`ErrServiceHasAccess`) |

### Access

| Fonction | Comportement |
|----------|-------------|
| `SaveAccess(a *Access)` | Valide + met à jour `UpdatedAt` + écriture atomique |
| `GetAccess(id)` | Charge par ID, retourne `ErrAccessNotFound` si absent |
| `ListAccessByAccount(accountID)` | Tous les accès d'un abonné |
| `ListAccessByService(serviceID)` | Tous les accès d'un service |
| `DeleteAccess(id)` | Supprime sans vérification (l'appelant gère les confirmations) |
| `AccessExists(accountID, serviceID)` | Détecte les doublons avant insertion |

---

## 5. Validations

### `ValidateService`

- ID, Name, Engine obligatoires
- Chaque port : entre 0 et 65535 (port négatif → erreur, port > 65535 → erreur)
- Si `IsHybrid()` : au moins 2 composants, aucun composant vide

### `ValidateAccess`

- ID, AccountID, ServiceID, Engine obligatoires
- MaxDevices ≥ 0, MaxConnections ≥ 0
- `QuotaUnlimited=false` + `QuotaLimitBytes=0` → erreur explicite (0 ≠ illimité)
- `ExpiresAt` doit être parseable en RFC3339 si non vide

---

## 6. Gestion quota

Représentation explicite — deux champs distincts, pas de convention `0=illimité` :

```
QuotaUnlimited=true  → accès illimité, QuotaLimitBytes ignoré
QuotaUnlimited=false → limite réelle = QuotaLimitBytes octets
                       QuotaLimitBytes=0 + QuotaUnlimited=false → refusé à la validation
```

`NewAccess()` crée un accès illimité par défaut (`QuotaUnlimited=true`).

`QuotaExceeded()` : retourne toujours `false` si `QuotaUnlimited=true`.

---

## 7. Gestion MaxDevices / MaxConnections

```
MaxDevices     : nombre maximum d'appareils (IPs sources distinctes)
                 0 = illimité
MaxConnections : connexions simultanées max (toutes connexions confondues)
                 0 = illimité
```

**Important** : M1 ne contient aucun système de tracking réseau. Ces champs sont des
données de configuration. L'enforcement (comptage des IPs actives) est hors du scope de M1.

---

## 8. Gestion expiration

- `ExpiresAt string` : RFC3339 ou chaîne vide (= aucune expiration)
- `IsExpired()` : retourne `false` si `ExpiresAt=""`, `true` si la date est passée
- La contrainte "un accès ne peut pas dépasser Account.ExpiresAt" est une règle de validation
  de haut niveau — à appliquer dans le menu lors de la saisie (hors du scope de M1)

---

## 9. Support des services hybrides au niveau modèle

Un service hybride est **un seul** `Service` :
- `Engine = "dnstt-xray"` (ou tout autre nom de moteur composite)
- `Components = ["dnstt", "xray"]` (ordre du chaînage)

Un abonné sur ce service a **un seul** `Access` :
- `Engine = "dnstt-xray"` (dénormalisé)
- `Secrets` contient tous les secrets des composants dans le même `map[string]any`

Exemple : `{"uuid": "...", "public_key": "...", "private_key": "..."}` pour dnstt-xray.

---

## 10. Tests créés

34 tests dans `internal/service/service_test.go` :

| Test | Ce qu'il vérifie |
|------|-----------------|
| `TestSaveLoadService` | Persistance et relecture d'un Service |
| `TestGetServiceByName` | Recherche insensible à la casse, absent = false |
| `TestListServices` | Liste de 3 services |
| `TestListServicesEmptyDir` | Répertoire absent = liste vide sans erreur |
| `TestListServicesByEngine` | Filtre par moteur (xray=2, ssh=1, wireguard=0) |
| `TestDeleteServiceBlockedByAccess` | Suppression refusée si Access existent |
| `TestDeleteServiceOK` | Suppression réussie sans Access |
| `TestDeleteServiceNotFound` | ID inconnu → erreur |
| `TestSaveLoadAccess` | Persistance et relecture d'un Access |
| `TestListAccessByAccount` | alice=2, bob=1, charlie=0 |
| `TestListAccessByService` | 2 abonnés sur 1 service |
| `TestAccessExists` | Détecte un doublon (account+service) |
| `TestDuplicateAccess` | Workflow de détection avant insertion |
| `TestDeleteAccess` | Suppression réussie |
| `TestDeleteAccessNotFound` | ID inconnu → erreur |
| `TestServiceValidation` (9 sous-tests) | ID vide, Name vide, Engine vide, port négatif, port > 65535, hybride 1 composant, hybride 2 composants, composant vide |
| `TestAccessValidation` (9 sous-tests) | ID, AccountID, ServiceID, Engine vides, MaxDevices négatif, MaxConnections négatif, ExpiresAt invalide, ExpiresAt valide |
| `TestQuotaUnlimited` | QuotaExceeded=false, validation OK |
| `TestQuotaLimited` | Sous/à/au-delà de la limite, validation OK |
| `TestQuotaLimitedZeroRejected` | QuotaUnlimited=false + QuotaLimitBytes=0 → erreur |
| `TestMaxDevices` | MaxDevices=2 et MaxDevices=0 (illimité) |
| `TestMaxConnections` | MaxConnections=5 et MaxConnections=0 (illimité) |
| `TestExpirationValidation` | ExpiresAt vide, passé, futur |
| `TestHybridService` | 1 seul Service hybride + 1 seul Access avec secrets composites |
| `TestSaveServiceUpdateTime` | UpdatedAt mis à jour après SaveService |

---

## 11. Résultat de `go test ./...`

```
ok   labosurf/internal/service     1.754s    (34 tests, tous PASS)
ok   labosurf/internal/profile     (cached)  (17 tests existants, inchangés)
ok   labosurf/internal/store       (cached)
ok   labosurf/internal/secret      (cached)
ok   labosurf/internal/srvcfg      (cached)
ok   labosurf/internal/engcfg      (cached)
ok   labosurf/internal/engine      (cached)
ok   labosurf/internal/license     (cached)

FAIL labosurf/internal/clientcfg   [build failed — pré-existant Windows]
FAIL labosurf/internal/engineutil  [build failed — pré-existant Windows]
```

Les deux failures sont **pré-existantes** (documentées depuis la V2) : `engines/ssh/server.go`
utilise `syscall.Credential` qui n'existe que sur Linux. Aucun lien avec M1.

Vérification : `git status --short -- "*.go"` ne montre que les 4 nouveaux fichiers.

---

## 12. Fichiers existants volontairement laissés intacts

Aucun fichier existant n'a été modifié. Liste des fichiers protégés confirmés intacts :

- `engine.Engine` interface
- `internal/engineutil/` (CompositeEngine, ValidateHybrid, EvaluateChain…)
- `internal/profile/` (17 tests toujours verts)
- `internal/clientcfg/`
- `internal/srvcfg/`
- `internal/store/` (Account, EngineGrant, EnsureEngineSecrets)
- `cmd/labosurf/` (menus)
- `engines/` (xray, ssh, wireguard, slowdns, dnstt…)

---

## 13. Limites de M1

1. **Pas de génération de secrets** : `EnsureAccessSecrets` n'existe pas encore. Le champ
   `Secrets map[string]any` est persisté mais reste vide jusqu'à M2.

2. **Pas de tracking réseau** : `MaxDevices` et `MaxConnections` sont des données de
   configuration. L'enforcement côté moteur est hors scope.

3. **Pas d'index secondaire** : `AccessExists` scanne tous les fichiers. Acceptable pour
   < 1000 abonnés ; à optimiser via un fichier index si nécessaire.

4. **Pas de migration** : les `Account.Grants` existants ne sont pas migrés vers des `Access`.
   La migration (MigrateGrantsToAccess) est prévue en M3.

5. **Pas de menus** : aucun menu n'a été créé ou modifié. Les menus [3] SERVICES et [5] ACCÈS
   sont prévus en M4.

6. **Pas de génération client** : `GenerateFromAccess` n'existe pas encore. La nouvelle
   surcharge de `clientcfg` est prévue en M2.

---

## 14. Ce qui sera traité dans M2

- `service.EnsureAccessSecrets(a *Access)` : génération idempotente des secrets par moteur
  (UUID Xray, clés Ed25519 SSH/DNS, clés X25519 WireGuard + adresse unique)
- `clientcfg.GenerateFromAccess(a Access, svc Service, username string)` : nouvelle surcharge
  de génération de config client depuis un Access (sans modifier la signature existante)
- Tests de non-régénération des secrets déjà présents
- Tests de génération pour chaque moteur

---

*Aucun commit automatique effectué. Bilan M1 uniquement.*
