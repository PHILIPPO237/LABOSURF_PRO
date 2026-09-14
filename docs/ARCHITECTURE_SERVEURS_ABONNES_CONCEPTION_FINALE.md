# Conception finale — Service / Access / Abonné — LABOSURF_PRO

> Document de conception technique. Aucune modification de code.
> Basé sur l'inspection réelle du dépôt — commit `7c9c824`, branche `main`, date 2026-09-14.
> Référence : `docs/ARCHITECTURE_SERVEURS_ABONNES_ANALYSE.md`

---

## 1. Rappel de l'architecture retenue

```
MOTEUR  (engine.Engine — inchangé)
  ↓
PROFIL MOTEUR  (profile.Profile V2 — inchangé)
  ↓
SERVICE  (nouveau : internal/service/service.go)
  ↓
ACCÈS  (nouveau : internal/service/access.go)
  ↓
ABONNÉ  (store.Account — évolue progressivement)
  ↓
CONFIG CLIENT  (internal/clientcfg/ — nouvelle surcharge)
```

Un Service hybride (ex: DNSTT→Xray) est **un seul Service** pour l'abonné.
Un abonné possède **un seul Access** vers ce service hybride, même si ce service est composé.

---

## 2. Analyse des champs existants : MaxConnections, MaxIPs, CurrentConns, CurrentIPs, UsedBytes

### Observation réelle dans `internal/store/account.go`

```go
MaxConnections int      `json:"max_connections"`  // persisté
MaxIPs         int      `json:"max_ips"`           // persisté
Enabled        bool     `json:"enabled"`

UsedBytes      int64    `json:"used_bytes"`        // persisté — compteur trafic
CurrentConns   int      `json:"-"`                 // runtime UNIQUEMENT — non persisté
CurrentIPs     []string `json:"-"`                 // runtime UNIQUEMENT — non persisté (liste d'IPs)
```

### Ce que ces champs signifient réellement

| Champ | Signification réelle |
|-------|---------------------|
| `MaxConnections` | Nombre maximum de **connexions TCP/UDP simultanées** autorisées |
| `MaxIPs` | Nombre maximum d'**adresses IP sources distinctes** autorisées simultanément |
| `CurrentConns` | Compteur runtime (non persisté) — connexions ouvertes à l'instant |
| `CurrentIPs` | Liste runtime (non persistée) — IPs actuellement connectées |
| `UsedBytes` | Trafic total consommé (persisté, incrémenté via `UpdateUsedBytes`) |

### Distinction appareils ≠ connexions

La demande parle de **« nombre d'appareils »**. Analyse :
- `MaxConnections` = connexions simultanées. Un appareil peut ouvrir plusieurs connexions (ex: navigateur + client). Ce n'est pas un compteur d'appareils.
- `MaxIPs` = IPs sources distinctes. En pratique, **1 IP ≈ 1 appareil** (sauf NAT). C'est la meilleure approximation existante du "nombre d'appareils" dans le code actuel.

**Proposition adoptée** : dans l'Access, le champ `MaxDevices` correspond sémantiquement au
"nombre maximum d'appareils". Il sera implémenté en suivant la logique de `MaxIPs` (IPs sources distinctes). `MaxConnections` est conservé séparément pour les cas où l'opérateur veut limiter les connexions par appareil. Si l'opérateur ne saisit que `MaxDevices`, `MaxConnections` peut être calculé automatiquement (ex: MaxDevices × 2).

---

## 3. Analyse du quota actuel

```go
QuotaBytes uint64  // 0 = illimité dans le code actuel
```

La convention actuelle `0 = illimité` est implicite et peut être confondue avec "non configuré".

**Nouveau modèle** : représentation explicite via deux champs dans `Access` :

```go
QuotaUnlimited bool   // true = illimité (QuotaLimitBytes ignoré)
QuotaLimitBytes uint64 // octets, ignoré si QuotaUnlimited=true
UsedBytes       int64  // trafic consommé sur cet accès (persisté)
```

Règle de décision lors de la saisie :
```
[1] QUOTA ILLIMITÉ → QuotaUnlimited=true, QuotaLimitBytes=0
[2] QUOTA LIMITÉ   → QuotaUnlimited=false, QuotaLimitBytes=<valeur en Go>
```

### Quota global de l'abonné (Account)

`Account.QuotaBytes` est conservé comme **plafond global optionnel** :
- Si `Account.QuotaBytes == 0` → pas de plafond global (illimité au niveau compte)
- Si `Account.QuotaBytes > 0` → plafond global : la somme de `UsedBytes` de tous les accès
  de l'abonné ne doit pas dépasser cette valeur

Cette logique est cohérente avec le modèle actuel et permet la rétrocompatibilité.

---

## 4. Analyse de l'expiration actuelle

```go
ExpiresAt string  // RFC3339 ou "" (vide = jamais)
```

Le modèle actuel utilise une chaîne RFC3339. Vide = pas d'expiration. Correct et cohérent.

**Dans l'Access** : même convention.

```go
ExpiresAt string  // RFC3339 ou "" (jamais)
```

**Règle de priorité** : si l'abonné a un `Account.ExpiresAt` non vide, un accès ne peut pas
avoir une expiration **postérieure** à celle de l'abonné. L'accès expire au minimum à la date
de l'abonné.

---

## 5. Structures Go proposées

### 5.1 `service.Service`

```go
// Package service gère les instances nommées de moteurs (Services) et les
// accès des abonnés à ces services (Access).
package service

import "time"

// Service représente une instance nommée et configurée d'un moteur.
// Deux services peuvent utiliser le même moteur sur des ports différents.
// Ex: "Xray-Production" (:443) et "Xray-Test" (:8443).
type Service struct {
    ID        string `json:"id"`         // UUID v4 généré
    Name      string `json:"name"`       // ex: "Xray-Production"

    // Engine est le nom du moteur simple ou du moteur hybride composé.
    // Ex: "xray", "ssh", "wireguard", "dnstt-xray", "slowdns-ssh"
    Engine    string `json:"engine"`

    // ProfileID référence le profile.Profile V2 utilisé lors de la création.
    // Changer le profil actif du moteur ne modifie PAS silencieusement ce champ.
    // Pour mettre à jour le service, l'opérateur doit le faire explicitement.
    ProfileID string `json:"profile_id,omitempty"`

    // Components liste les moteurs composant un service hybride.
    // Vide pour un service simple.
    // Ex: ["dnstt", "xray"] pour un hybride DNSTT→Xray.
    Components []string `json:"components,omitempty"`

    // Configuration réseau
    Host    string         `json:"host"`            // IP ou domaine public
    Ports   map[string]int `json:"ports"`           // {"listen": 443, "public": 443}
    Domains []string       `json:"domains,omitempty"` // pour DNS tunnels

    // TLSMode : "reality" | "tls" | "none"
    TLSMode string `json:"tls_mode,omitempty"`

    Enabled   bool   `json:"enabled"`
    CreatedAt string `json:"created_at"` // RFC3339
    UpdatedAt string `json:"updated_at"` // RFC3339
}
```

### 5.2 `service.Access`

```go
// Access représente le droit d'un abonné à utiliser un Service.
// Un abonné peut avoir plusieurs Access (un par service auquel il est rattaché).
// Révoquer un Access ne supprime pas l'Account.
//
// Pour un service hybride, un seul Access contient TOUS les secrets
// nécessaires aux composants (ex: uuid pour xray + public_key pour dnstt).
type Access struct {
    ID        string `json:"id"`         // UUID v4 généré
    AccountID string `json:"account_id"` // réf → store.Account.ID
    ServiceID string `json:"service_id"` // réf → service.Service.ID

    // Engine dénormalisé depuis le Service (évite une lecture en cascade).
    Engine string `json:"engine"`

    // Secrets spécifiques au protocole (identiques à EngineGrant.Config actuel).
    // Pour un hybride, contient les secrets de TOUS les composants.
    // Exemples :
    //   xray        → {"uuid": "..."}
    //   ssh         → {"public_key": "...", "private_key": "..."}
    //   dnstt/slowdns → {"public_key": "...", "private_key": "..."}
    //   wireguard   → {"private_key": "...", "public_key": "...", "address": "10.66.0.N/32"}
    //   tuic        → {"uuid": "...", "password": "..."}
    //   hysteria    → {"password": "..."}
    //   dnstt-xray  → {"uuid": "...", "public_key": "...", "private_key": "..."} (les deux)
    Secrets map[string]any `json:"secrets,omitempty"`

    // --- Quota ---
    // Représentation explicite : illimité OU limité (pas de convention 0=illimité).
    QuotaUnlimited bool   `json:"quota_unlimited"`          // true = illimité
    QuotaLimitBytes uint64 `json:"quota_limit_bytes,omitempty"` // ignoré si QuotaUnlimited
    UsedBytes       int64  `json:"used_bytes"`               // trafic consommé, persisté

    // --- Appareils / Sessions ---
    // MaxDevices : nombre maximum d'appareils distincts (IPs sources).
    // Sémantiquement "appareils", implémenté via le comptage d'IPs sources.
    // 0 = illimité.
    MaxDevices int `json:"max_devices"`

    // MaxConnections : connexions simultanées max (toutes connexions confondues,
    // indépendamment du nombre d'appareils). 0 = illimité.
    // Si non défini explicitement, peut être calculé : MaxDevices * 2.
    MaxConnections int `json:"max_connections"`

    // --- Expiration ---
    // Chaîne RFC3339, vide = jamais.
    // Ne peut pas dépasser Account.ExpiresAt si ce dernier est défini.
    ExpiresAt string `json:"expires_at,omitempty"`

    Enabled   bool   `json:"enabled"`
    CreatedAt string `json:"created_at"` // RFC3339
    UpdatedAt string `json:"updated_at"` // RFC3339
}
```

### 5.3 `store.Account` — évolution minimale (rétrocompatible)

Aucun champ supprimé dans un premier temps. La migration est progressive.

```go
// Account reste inchangé structurellement.
// Les nouveaux champs Access sont dans internal/service/.
// Account.Grants est conservé pendant la migration.
// Account.QuotaBytes devient le plafond global optionnel.
// Account.MaxConnections et MaxIPs restent pour la rétrocompatibilité.
type Account struct {
    ID             string `json:"id"`
    Username       string `json:"username"`
    Password       string `json:"password"`
    ExpiresAt      string `json:"expires_at"`      // RFC3339 ou "" (jamais)
    QuotaBytes     uint64 `json:"quota_bytes"`      // plafond global (0=illimité)
    MaxConnections int    `json:"max_connections"`
    MaxIPs         int    `json:"max_ips"`
    Enabled        bool   `json:"enabled"`
    OfferID        string `json:"offer_id,omitempty"`
    Token          string `json:"token,omitempty"`
    Grants         map[string]*EngineGrant `json:"grants,omitempty"` // conservé pendant migration
    UsedBytes      int64    `json:"used_bytes"`
    CurrentConns   int      `json:"-"` // runtime
    CurrentIPs     []string `json:"-"` // runtime
    CreatedAt      string   `json:"created_at"`
    UpdatedAt      string   `json:"updated_at"`
}
```

---

## 6. Persistance

### Fichiers JSON

```
$LABOSURF_DATA_DIR/
├── users_db.json              ← store.Account (existant, inchangé)
├── server.json                ← srvcfg.Profile (existant, migration progressive)
├── profiles/<id>.json         ← profile.Profile V2 (existant, inchangé)
├── engines/<name>/profile.json ← engcfg.EngineProfile V1 (existant, inchangé)
├── hybrids/                   ← hybrid_store (existant, inchangé)
├── services/<id>.json         ← service.Service (NOUVEAU)
└── access/<id>.json           ← service.Access (NOUVEAU)
```

### Store Service (`internal/service/store.go`)

```go
// Dir retourne le répertoire des services.
func ServicesDir() string  // $LABOSURF_DATA_DIR/services/

// AccessDir retourne le répertoire des accès.
func AccessDir() string    // $LABOSURF_DATA_DIR/access/

// Opérations Services
func SaveService(s *Service) error
func GetService(id string) (Service, error)
func GetServiceByName(name string) (Service, bool, error)
func ListServices() ([]Service, error)
func ListServicesByEngine(engine string) ([]Service, error)
func DeleteService(id string) error       // vérifie qu'aucun Access actif ne pointe dessus

// Opérations Access
func SaveAccess(a *Access) error
func GetAccess(id string) (Access, error)
func ListAccessByAccount(accountID string) ([]Access, error)
func ListAccessByService(serviceID string) ([]Access, error)
func DeleteAccess(id string) error
func AccessExists(accountID, serviceID string) (bool, string, error) // bool + id si trouvé
```

### Format de persistance (exemple `services/abc123.json`)

```json
{
  "id": "abc123",
  "name": "Xray-Production",
  "engine": "xray",
  "profile_id": "prof-xyz",
  "host": "vpn.exemple.tld",
  "ports": {"listen": 443, "public": 443},
  "domains": [],
  "tls_mode": "reality",
  "enabled": true,
  "created_at": "2026-09-14T18:00:00Z",
  "updated_at": "2026-09-14T18:00:00Z"
}
```

### Format de persistance (exemple `access/def456.json`)

```json
{
  "id": "def456",
  "account_id": "alice",
  "service_id": "abc123",
  "engine": "xray",
  "secrets": {"uuid": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"},
  "quota_unlimited": false,
  "quota_limit_bytes": 53687091200,
  "used_bytes": 3435973836,
  "max_devices": 2,
  "max_connections": 4,
  "expires_at": "2026-12-31T23:59:59Z",
  "enabled": true,
  "created_at": "2026-09-14T18:00:00Z",
  "updated_at": "2026-09-14T19:00:00Z"
}
```

---

## 7. Relations et cardinalités

```
engine.Engine           1 → N   profile.Profile (V2)
profile.Profile (V2)    1 → 0..1 service.Service (via ProfileID)
service.Service         1 → N   service.Access
store.Account           1 → N   service.Access
store.Offer             1 → N   store.Account (template à la création)
```

### Règles invariantes

1. Supprimer un `Access` ne supprime **jamais** le `Account` correspondant.
2. Un `Account` peut avoir N `Access` sur N services différents simultanément.
3. Supprimer un `Service` requiert que **tous ses Access soient préalablement supprimés** (ou l'opérateur confirme la suppression en cascade).
4. Les `Secrets` de l'`Access` ne sont **jamais régénérés** si déjà présents (idempotence stricte).
5. Un service hybride = **1 seul** `service.Service` avec `Components` non vide.
   Un abonné sur ce service = **1 seul** `service.Access` contenant les secrets de tous les composants.
6. `ExpiresAt` d'un `Access` ne peut pas dépasser `Account.ExpiresAt` (si ce dernier est défini).
7. `QuotaUnlimited=true` → aucune limite de bande passante sur cet accès (indépendamment du plafond global `Account.QuotaBytes`).

---

## 8. Génération des secrets (`EnsureAccessSecrets`)

Analogue direct de `store.EnsureEngineSecrets`. Même logique, même idempotence.

```go
// EnsureAccessSecrets génère et persiste les secrets manquants d'un Access
// pour son moteur. Idempotent : ne modifie que les champs encore vides.
// Ne régénère JAMAIS un secret existant.
func EnsureAccessSecrets(a *Access) error {
    if a.Secrets == nil {
        a.Secrets = make(map[string]any)
    }

    switch a.Engine {

    case "xray":
        // Hybrides xray-* héritent aussi de ce cas.
        if strVal(a.Secrets["uuid"]) == "" {
            u, _ := secret.UUID()
            a.Secrets["uuid"] = u
        }

    case "ssh":
        if strVal(a.Secrets["public_key"]) == "" {
            pub, priv, _ := secret.Ed25519Keypair()
            a.Secrets["public_key"] = pub
            a.Secrets["private_key"] = priv
        }

    case "dnstt", "slowdns":
        if strVal(a.Secrets["public_key"]) == "" {
            pub, priv, _ := secret.Ed25519Keypair()
            a.Secrets["public_key"] = pub
            a.Secrets["private_key"] = priv
        }

    case "tuic":
        if strVal(a.Secrets["uuid"]) == "" {
            u, _ := secret.UUID()
            a.Secrets["uuid"] = u
        }
        if strVal(a.Secrets["password"]) == "" {
            tk, _ := secret.RandToken(12)
            a.Secrets["password"] = tk
        }

    case "hysteria", "hysteria2":
        if strVal(a.Secrets["password"]) == "" {
            tk, _ := secret.RandToken(12)
            a.Secrets["password"] = tk
        }

    case "wireguard":
        if strVal(a.Secrets["private_key"]) == "" {
            priv, pub, _ := secret.X25519Keypair()
            a.Secrets["private_key"] = priv
            a.Secrets["public_key"] = pub
        }
        if strVal(a.Secrets["address"]) == "" {
            addr, _ := nextWireGuardAddress() // unicité globale
            a.Secrets["address"] = addr
        }

    default:
        // Moteur hybride composé (ex: "dnstt-xray", "slowdns-ssh") :
        // générer les secrets pour CHAQUE composant du service.
        for _, comp := range a.ServiceComponents {
            compAccess := &Access{Engine: comp, Secrets: a.Secrets}
            _ = EnsureAccessSecrets(compAccess)
            // Les secrets générés sont écrits directement dans a.Secrets
            // avec les mêmes clés que pour le composant simple.
            // Ex: dnstt-xray → a.Secrets["uuid"] + a.Secrets["public_key"]
        }
    }

    return SaveAccess(a)
}
```

### Unicité des adresses WireGuard

`nextWireGuardAddress()` inspecte **tous les Access existants** (pas seulement les Grants)
pour garantir l'unicité du pool 10.66.0.2–10.66.0.254. Pendant la migration, elle inspecte
aussi les Grants existants pour éviter les collisions.

---

## 9. Génération des configurations client

### Nouvelle signature

```go
// GenerateFromAccess produit le lien client + la config serveur JSON
// à partir d'un Access et de son Service.
// Remplace à terme : clientcfg.Generate(acc, engineName, srvcfg.Profile)
func GenerateFromAccess(a Access, svc Service, accountUsername string) (ClientResult, error)
```

### Flux interne

```
GenerateFromAccess(a, svc, username) :
  host = svc.Host
  port = svc.Ports["listen"]

  switch a.Engine :

    "xray" :
      uuid = strVal(a.Secrets["uuid"])
      keys = xray.LoadRealityKeys()   ← sur le serveur, jamais dans Access
      → vless://uuid@host:port?security=reality&pbk=<clé_publique_serveur>&...
      Erreur explicite si clés REALITY absentes (moteur non installé)

    "ssh" :
      priv_key = strVal(a.Secrets["private_key"])
      → "ssh username@host -p port"
      → fichier ~/.ssh/<id_account> = priv_key (à écrire par le client)

    "wireguard" :
      address  = strVal(a.Secrets["address"])
      priv_key = strVal(a.Secrets["private_key"])
      → fichier .conf WireGuard complet

    "slowdns" / "dnstt" :
      domain  = svc.Domains[0]  ou svc.Host
      pub_key = strVal(a.Secrets["public_key"])
      → "<engine>://username@domain?key=pub_key"

    "tuic" :
      uuid = strVal(a.Secrets["uuid"])
      pw   = strVal(a.Secrets["password"])
      → URI tuic

    "hysteria" / "hysteria2" :
      pw = strVal(a.Secrets["password"])
      → URI hysteria/hysteria2

    "wireguard" :
      → fichier .conf WireGuard

    Hybride (ex: "dnstt-xray") :
      → lien du composant VPN principal (xray → VLESS)
      → inclure les infos DNS dans les metadata du lien si applicable
```

### Config serveur groupée (tous les abonnés d'un service)

```go
// ApplyServiceConfig régénère et applique la configuration serveur complète
// d'un service en regroupant TOUS ses accès actifs.
func ApplyServiceConfig(ctx context.Context, svc Service, accesses []Access, srvcfgProf srvcfg.Profile) error
```

Pour un hybride, `ApplyServiceConfig` délègue à `buildComponentConfigs` (existant),
en lisant les secrets depuis `Access.Secrets` au lieu de `Account.Grants[engine].Config`.

---

## 10. Gestion des hybrides

### Représentation dans Service

```json
{
  "id": "hyb-001",
  "name": "DNSTT-Xray-Production",
  "engine": "dnstt-xray",
  "components": ["dnstt", "xray"],
  "host": "vpn.exemple.tld",
  "ports": {"listen": 443, "public": 443},
  "domains": ["tunnel.exemple.tld"],
  "enabled": true
}
```

### Représentation dans Access

```json
{
  "id": "acc-hyb-001",
  "account_id": "alice",
  "service_id": "hyb-001",
  "engine": "dnstt-xray",
  "secrets": {
    "uuid":        "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "public_key":  "aabbcc...",
    "private_key": "ddeeff..."
  },
  "quota_unlimited": true,
  "max_devices": 3,
  "max_connections": 6,
  "expires_at": "2027-03-14T00:00:00Z"
}
```

Un seul Access, tous les secrets des composants dans le même `map[string]any`.

### EnsureAccessSecrets pour un hybride

La branche `default` de `EnsureAccessSecrets` itère sur `svc.Components` et appelle
récursivement `EnsureAccessSecrets` sur un access virtuel par composant, en écrivant les
résultats dans `a.Secrets`. Résultat : après l'appel, `a.Secrets` contient les secrets
de tous les composants (`uuid` pour xray, `public_key`/`private_key` pour dnstt).

---

## 11. Parcours complet — de l'activation du moteur à la config client

### Étape 1 — ACTIVER UN MOTEUR

```
[1] MOTEURS → Installer + Configurer + Démarrer
  e.Install(ctx)
  e.Configure(ctx, EngineConfig{JSON: cfg})
  e.Start(ctx) ou e.RunForeground(ctx)
```

Résultat : le moteur tourne. Il ne sait pas encore qui l'utilise ni sur quel port.

### Étape 2 — CRÉER UN PROFIL MOTEUR

```
[2] PROFILS MOTEURS → Nouveau profil
  profile.NewSimple(name, desc, engine, params)
  profile.Save(&p)
  [Optionnel] profileActivate(&p) → Configure le moteur avec les params du profil
```

Résultat : configuration nommée et versionnable du moteur.

### Étape 3 — CRÉER UN SERVICE

```
[3] SERVICES → Nouveau service
  Saisit : nom, moteur (ou hybride), profil lié, host, port, domaines
  service.Service{
    ID:        newID(),
    Name:      "Xray-Production",
    Engine:    "xray",
    ProfileID: p.ID,
    Host:      "vpn.exemple.tld",
    Ports:     {"listen": 443, "public": 443},
    Enabled:   true,
  }
  service.SaveService(&svc)
```

Résultat : une instance nommée du moteur, prête à recevoir des abonnés.

Pour un hybride :
```
  service.Service{
    Engine:     "dnstt-xray",
    Components: ["dnstt", "xray"],
    Domains:    ["tunnel.exemple.tld"],
    ...
  }
```

### Étape 4 — CRÉER UN ABONNÉ

```
[4] ABONNÉS → Créer abonné
  store.Account{
    ID:       "alice",
    Username: "alice",
    Password: "(généré ou saisi)",
    Enabled:  true,
  }
  s.CreateAccount(acc)
  [Optionnel] s.Subscribe(acc.ID, offerID)  ← applique les limites de l'offre
```

Résultat : un abonné sans accès. Aucun secret, aucun quota encore.

### Étape 5 — AJOUTER UN SERVICE À L'ABONNÉ

```
[5] ACCÈS → Ajouter un accès
  Sélectionne : abonné + service cible
  service.Access{
    ID:        newID(),
    AccountID: "alice",
    ServiceID: svc.ID,
    Engine:    svc.Engine,
    Enabled:   true,
  }
```

### Étape 6 — CONFIGURER LE QUOTA

```
  Choix :
  [1] QUOTA ILLIMITÉ
      access.QuotaUnlimited = true

  [2] QUOTA LIMITÉ
      Saisit : 5 / 10 / 20 / 50 / 100 Go (ou valeur libre)
      access.QuotaUnlimited = false
      access.QuotaLimitBytes = valeur × 1_073_741_824
```

### Étape 7 — CONFIGURER LES APPAREILS MAX

```
  Saisit : 1 / 2 / 3 / 5 (ou valeur libre)
  access.MaxDevices = N
  access.MaxConnections = N * 2  (si non défini explicitement)
```

### Étape 8 — CONFIGURER L'EXPIRATION

```
  Choix :
  [1] AUCUNE EXPIRATION
      access.ExpiresAt = ""

  [2] DURÉE EN JOURS
      Saisit : 30 / 60 / 90 / 180 / 365
      access.ExpiresAt = time.Now().Add(N jours).Format(RFC3339)

  [3] DATE PRÉCISE
      Saisit : 2026-12-31
      → Validation : ne dépasse pas Account.ExpiresAt si défini
```

### Étape 9 — GÉNÉRER LES SECRETS SPÉCIFIQUES

```
  service.EnsureAccessSecrets(&access)
  → Génère les secrets manquants selon access.Engine
  → Ne touche jamais les secrets déjà présents
  → Persiste dans $LABOSURF_DATA_DIR/access/<id>.json
```

### Étape 10 — GÉNÉRER LA CONFIG CLIENT

```
  [5] ACCÈS → Config client
  service.GenerateFromAccess(access, svc, account.Username)
  → ClientResult{Engine, ClientLink, ServerConfig}
  → Afficher le lien client à l'opérateur
  → [Optionnel] ApplyServiceConfig() pour mettre à jour la config serveur
```

---

## 12. Gestion quota illimité / quota limité — règles complètes

### Lors de la saisie (menu)

```
╔════════════════════════════════════════╗
║  QUOTA POUR CET ACCÈS                 ║
╠════════════════════════════════════════╣
║  [1] Illimité                          ║
║  [2] Limité                            ║
╚════════════════════════════════════════╝

Si [2] :
Entrez la quantité :
  [1]   5 Go
  [2]  10 Go
  [3]  20 Go
  [4]  50 Go
  [5] 100 Go
  [6] Valeur personnalisée (en Go)
```

### Affichage dans les menus

```
QuotaUnlimited=true  → "illimité"
QuotaUnlimited=false → "X.X Go / Y Go utilisés"
```

### Vérification à la connexion

```
Si access.QuotaUnlimited → autoriser (pas de vérification de bande passante)
Si !access.QuotaUnlimited && access.UsedBytes >= access.QuotaLimitBytes → refuser
Si Account.QuotaBytes > 0 && totalUsedAllAccess >= Account.QuotaBytes → refuser (plafond global)
```

---

## 13. Gestion du nombre maximum d'appareils — règles complètes

### Lors de la saisie (menu)

```
╔════════════════════════════════════════╗
║  APPAREILS MAXIMUM                     ║
╠════════════════════════════════════════╣
║  Nombre d'appareils autorisés :        ║
║  [1] 1 appareil                        ║
║  [2] 2 appareils                       ║
║  [3] 3 appareils                       ║
║  [4] 5 appareils                       ║
║  [5] Illimité (0)                      ║
║  [6] Valeur personnalisée              ║
╚════════════════════════════════════════╝
```

### Logique de vérification (runtime)

```
MaxDevices = 0 → illimité (nombre d'IPs sources non limité)
MaxDevices > 0 → nombre d'IPs sources distinctes actives ≤ MaxDevices
```

Correspondance avec le code actuel :
- `access.MaxDevices` ↔ logique de `Account.MaxIPs`
- `access.MaxConnections` ↔ logique de `Account.MaxConnections`

### Affichage

```
MaxDevices=0 → "appareils : illimité"
MaxDevices=N → "appareils : N max"
```

---

## 14. Plan de migration par étapes

### Principe directeur

**Aucune rupture** sur les moteurs, profils V2, comptes existants, tests existants.
Chaque étape est testable et réversible.

### Étape M1 — Créer `internal/service/` (aucune modification de l'existant)

Fichiers à créer :
- `internal/service/service.go` — types Service et Access
- `internal/service/store.go` — CRUD persisté (services/ et access/)
- `internal/service/secrets.go` — EnsureAccessSecrets
- `internal/service/migrate.go` — MigrateGrantsToAccess
- `internal/service/service_test.go` — tests unitaires

Tests à écrire :
```
TestSaveLoadService           — CRUD Service
TestSaveLoadAccess            — CRUD Access
TestEnsureAccessSecrets_Xray  — génère UUID, idempotent
TestEnsureAccessSecrets_SSH   — génère paire Ed25519, idempotent
TestEnsureAccessSecrets_WG    — génère clés X25519 + adresse unique
TestEnsureAccessSecrets_Hybrid — génère uuid + public_key pour dnstt-xray
TestWireGuardAddressUnique    — unicité du pool WireGuard sur Access
TestAccessExists              — détecte doublon account+service
TestListAccessByAccount       — tous les accès d'un abonné
TestDeleteServiceBlockedByAccess — empêche suppression si Access actifs
```

### Étape M2 — Ajouter `clientcfg.GenerateFromAccess` (ancienne signature conservée)

Fichier : `internal/clientcfg/clientcfg.go` — ajout d'une nouvelle fonction.
L'ancienne `Generate(acc, engineName, prof)` **n'est pas supprimée**.

Tests à ajouter :
```
TestGenerateFromAccess_Xray      — même lien que Generate(acc, "xray", prof)
TestGenerateFromAccess_SSH       — idem SSH
TestGenerateFromAccess_Hybrid    — lien du VPN principal pour un hybride
```

### Étape M3 — Ajouter `service.MigrateGrantsToAccess`

```go
// MigrateGrantsToAccess lit les Grants d'un compte et crée des Access
// correspondants si un Service matching existe.
// Idempotent : vérifie AccessExists() avant de créer.
// Ne supprime PAS les Grants. Ne régénère PAS les secrets existants.
func MigrateGrantsToAccess(acc store.Account, services []Service) ([]Access, error) {
    var created []Access
    for engineName, grant := range acc.Grants {
        svc, found := findServiceByEngine(services, engineName)
        if !found {
            continue // pas de Service créé pour ce moteur → on ignore
        }
        exists, _, _ := AccessExists(acc.ID, svc.ID)
        if exists {
            continue // déjà migré
        }
        a := Access{
            ID:          newID(),
            AccountID:   acc.ID,
            ServiceID:   svc.ID,
            Engine:      engineName,
            Secrets:     copySecrets(grant.Config), // copie exacte — zéro régénération
            QuotaUnlimited: acc.QuotaBytes == 0,
            QuotaLimitBytes: acc.QuotaBytes,
            MaxDevices:     acc.MaxIPs,
            MaxConnections: acc.MaxConnections,
            ExpiresAt:      acc.ExpiresAt,
            Enabled:        grant.Enabled,
        }
        _ = SaveAccess(&a)
        created = append(created, a)
    }
    return created, nil
}
```

Tests à ajouter :
```
TestMigrateGrantsToAccess_Simple     — migre un grant xray vers un Access
TestMigrateGrantsToAccess_Idempotent — n'écrase pas un Access existant
TestMigrateGrantsToAccess_NoService  — ignore un grant sans Service correspondant
TestMigrateGrantsToAccess_NoSecretsRegenerated — UUID existant conservé
```

### Étape M4 — Ajouter les menus [3] SERVICES et [5] ACCÈS

Fichiers à créer :
- `cmd/labosurf/menu_services.go` — gestion des Services
- `cmd/labosurf/menu_access.go` — gestion des Access

Modifier `cmd/labosurf/menu.go` :
- Ajouter `[3] SERVICES → runServiceMenu()`
- Ajouter `[5] ACCÈS → runAccessMenu()`
- Décaler ou renuméroter les options existantes (décision Q6 de l'analyse)

Les anciens menus utilisateurs (avec gestion des Grants) restent disponibles pendant la transition.

### Étape M5 — Dépréciation progressive de `Account.Grants`

Conditions pour passer à M5 :
1. Tous les Services ont été créés pour les moteurs actifs
2. `MigrateGrantsToAccess` a été exécuté pour tous les comptes (vérifiable : comptes avec Grants et sans Access correspondants = 0)
3. `GenerateFromAccess` est utilisé dans toutes les nouvelles générations de config

Actions M5 :
1. Marquer `Grants` comme `Deprecated` dans un commentaire
2. Les opérations de génération lisent `Access` en priorité, `Grants` en fallback
3. La suppression définitive du champ `Grants` est une étape M6, **après validation en production**

---

## 15. Risques de régression et mitigations

| Risque | Niveau | Détail | Mitigation |
|--------|--------|--------|-----------|
| Régénération UUID/clés existants | **CRITIQUE** | `EnsureAccessSecrets` écrase un secret déjà présent | Test `TestNoSecretsRegenerated` + vérification `strVal != ""` avant génération |
| Adresses WireGuard dupliquées | **CRITIQUE** | Migration copie l'adresse existante sans vérifier l'unicité entre Grants et Access | `nextWireGuardAddress()` inspecte Grants ET Access pendant la période de migration |
| Clés REALITY Xray perdues | **ÉLEVÉ** | Les clés REALITY sont hors store (dans `engines/xray/`) — migration les ignore | Non concerné par la migration — elles ne transitent jamais par le store |
| Config serveur incomplète post-migration | **ÉLEVÉ** | `ApplyServiceConfig` lit les Access — si certains Grants ne sont pas migrés, des abonnés manquent | Pendant M1–M4, `ApplyServerConfig` continue de lire les Grants (fallback) |
| Tests V2 `internal/profile/` cassés | **FAIBLE** | `internal/profile/` n'est pas modifié | Les 17 tests passants ne sont pas touchés |
| Style visuel altéré | **FAIBLE** | Nouveaux menus construits sans les helpers existants | Réutiliser exactement les fonctions d'affichage existantes |
| `ExpiresAt` Access > `Account.ExpiresAt` | **MOYEN** | Un accès survit à son abonné | Validation à la saisie + vérification à chaque connexion |
| Hybrid secrets incomplets | **MOYEN** | dnstt-xray nécessite uuid + public_key — si migration depuis `"dnstt-xray"` grant, les deux doivent être copiés | `copySecrets` copie tout le `map[string]any` du grant hybride |

---

## 16. Fonctionnalités qui ne doivent pas être supprimées

1. **Lien VLESS avec vraie clé REALITY** — erreur explicite si moteur non installé
2. **`EnsureEngineSecrets` idempotent** — conservé pendant toute la transition (Étapes M1–M4)
3. **`EnsureAccessSecrets` idempotent** — jamais d'écrasement de secret existant
4. **Adresses WireGuard uniques** — pool 10.66.0.2–254, unicité vérifiée globalement
5. **`buildGroupedConfig` / `buildGroupedConfigFromService`** — config serveur pour tous les abonnés
6. **`ApplyServerConfig` / `ApplyServiceConfig`** — régénération complète via `e.Configure()`
7. **`aliasGrantForComponent`** — accès aux secrets hybrides depuis le grant composite (conservé pendant migration)
8. **`buildComponentConfigs`** — config serveur composant par composant pour hybrides
9. **`CompositeEngine`, `RegisterHybridPersist`, `RemoveHybridPersist`** — pipeline hybride
10. **17 tests `internal/profile/`** — doivent rester verts sans modification
11. **Jitter anti-DPI DNS** (`jitter_ms: 40`) — paramètre technique légitime
12. **Style visuel LABOSURF_PRO** — bordures `═`, bullets `●/○`, couleurs ANSI, numérotation
13. **Exclusivité 1 profil actif par moteur** — `DeactivateAllForEngine` inchangé

---

## 17. Problèmes identifiés dans la conception

### P1 — `CurrentConns` et `CurrentIPs` sont runtime-only

Ces champs (`json:"-"`) ne sont pas persistés. Dans le modèle actuel, la vérification
des connexions actives est assurée par le moteur lui-même (ex: Xray compte les sessions).
Le store n'est pas notifié en temps réel.

**Impact sur MaxDevices** : la vérification `currentIPs <= MaxDevices` ne peut pas être
faite côté store. Elle doit être implémentée dans chaque moteur ou dans un middleware proxy.
Ce mécanisme d'enforcement n'est pas dans le scope de cette conception — il est à réserver
à une étape future.

### P2 — `UsedBytes` agrégé

La comptabilité de `UsedBytes` est globale dans `Account`. Dans le modèle cible, `Access.UsedBytes`
doit être mis à jour par service. Le mécanisme de reporting est moteur-dépendant (Xray a des stats,
SSH n'en a pas). Cette distinction est à implémenter moteur par moteur.

### P3 — `srvcfg.Profile` encore nécessaire pour `EvaluateChain`

`validateHybrid` dans `internal/profile/validate.go` appelle `srvcfg.Load()` pour détecter
les conflits de ports. Si `srvcfg.Profile` est remplacé par `service.Service`, cette
dépendance devra être mise à jour. Pendant la migration, `srvcfg.Load()` reste la source
de vérité pour `EvaluateChain`.

### P4 — Pas de contrainte de clé unique sur (AccountID, ServiceID)

La structure de fichiers `access/<id>.json` n'a pas d'index secondaire. `AccessExists(accountID, serviceID)`
doit scanner tous les accès pour détecter un doublon. Acceptable pour des petites installations
(< 1000 abonnés) ; à optimiser si nécessaire via un fichier index séparé.

---

## 18. Questions résolues par cette conception

| Question | Décision retenue |
|----------|-----------------|
| Q1 — Source de vérité Grants/Access | Lire Access en priorité, Grants en fallback pendant M1–M4 |
| Q2 — Services créés auto ou manuellement | Manuellement par l'opérateur (moins de magie, plus de contrôle) |
| Q3 — Typage de Secrets | `map[string]any` conservé — même convention que `EngineGrant.Config` actuel |
| Q4 — Quota global vs par service | Les deux : `Access.QuotaLimitBytes` + `Account.QuotaBytes` comme plafond global |
| Q5 — Service suit le profil actif | Non — `Service.ProfileID` est figé à la création, modification explicite requise |
| Q6 — Numérotation menus | À décider — renumérotation complète recommandée (menus [3] et [5] intercalés) |
| Q7 — Hybride = 1 ou 2 Services | 1 seul Service avec `Components` — un seul Access pour l'abonné |
| Q8 — Sort de srvcfg.Profile | Conservé pendant toute la migration, remplacé progressivement par Service |

---

*Fin de la conception finale — aucune modification de code effectuée.*
*Prêt pour implémentation après validation de ce document.*
*Date : 2026-09-14 — Commit de référence : `7c9c824`*
