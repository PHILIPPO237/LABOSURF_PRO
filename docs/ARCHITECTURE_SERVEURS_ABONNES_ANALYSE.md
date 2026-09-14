# LABOSURF_PRO — Analyse d'architecture : Moteurs → Profils → Serveurs → Abonnés → Accès → Config client

> Document d'analyse uniquement. Ne contient aucune modification de code.
> Basé sur l'observation réelle du dépôt au commit `3749132` (branche `main`).

---

## 1. Architecture actuelle réelle

Le système actuel suit ce flux :

```
MOTEUR (engine.Engine)
  ↓  configuration nommée
PROFIL MOTEUR (profile.Profile — V2)
  ↓  config globale du serveur
PROFIL SERVEUR (srvcfg.Profile — unique et global)
  ↓  compte avec grants par moteur
ABONNÉ (store.Account)
  ↓  grant par nom de moteur
GRANT MOTEUR (store.EngineGrant)
  ↓  secrets + profil serveur global
CONFIG CLIENT (internal/clientcfg/)
```

Il n'existe pas de niveau « Service » ou « Serveur nommé » entre le moteur et l'abonné.
L'accès abonné est directement rattaché au nom du moteur (ex: `"xray"`), pas à une instance nommée (ex: `"Xray-Production"`).

---

## 2. Modèles/types existants

### `engine.Engine` — interface (`internal/engine/`)

Interface à 13 méthodes : `Name()`, `Version()`, `Description()`, `Install()`, `Configure()`, `Start()`, `RunForeground()`, `Stop()`, `Restart()`, `Status()`, `HealthCheck()`, `Logs()`, `Update()`, `Uninstall()`.

Brique technique pure. Le moteur ne connaît ni les abonnés, ni les quotas, ni les ports d'écoute en dehors de ce que lui transmet `Configure()`.

### `engcfg.EngineProfile` — config moteur V1 (`internal/engcfg/`)

```go
type EngineProfile struct {
    Engine string            `json:"engine"`
    Values map[string]string `json:"values"`
}
```

Persisté dans `$LABOSURF_DATA_DIR/engines/<name>/profile.json`. Cache plat clé/valeur pour l'assistant de configuration (port, domaine…). Pas d'identité nommée, pas de statut actif/inactif, pas d'historique.

### `profile.Profile` — profil nommé V2 (`internal/profile/`)

```go
type Profile struct {
    ID          string
    Name        string
    Description string
    Kind        ProfileKind       // "simple" | "hybrid"
    Engine      string            // moteur pour Kind=simple
    Components  []string          // moteurs pour Kind=hybrid
    Params      map[string]string
    Status      ProfileStatus     // "draft" | "active" | "inactive"
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

Persisté dans `$LABOSURF_DATA_DIR/profiles/<id>.json`. Configuration nommée d'un moteur ou d'un hybride. Aucun champ port, domaine ou abonné.

### `srvcfg.Profile` — profil serveur global (`internal/srvcfg/`)

```go
type Profile struct {
    Host    string
    Ports   map[string]int    // ex: {"xray": 443, "ssh": 22}
    Domains []string
    Proxied []string
}
```

**Unique et global**. Un seul fichier pour tout le système. Pas de concept de plusieurs serveurs ou instances nommées. Le port xray est identique pour tous les abonnés.

### `store.Account` — abonné (`internal/store/account.go`)

```go
type Account struct {
    ID             string
    Username       string
    Password       string
    ExpiresAt      time.Time
    QuotaBytes     int64
    MaxConnections int
    MaxIPs         int
    Enabled        bool
    OfferID        string
    Token          string
    Grants         map[string]*EngineGrant
    UsedBytes      int64
    CurrentConns   int
    CurrentIPs     int
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

Le compte est le modèle abonné. Il contient directement les grants par moteur et les limites globales (quota, expiration, sessions).

### `store.EngineGrant` — accès par moteur (`internal/store/account.go`)

```go
type EngineGrant struct {
    Engine  string
    Config  map[string]any   // secrets spécifiques au moteur
    Enabled bool
}
```

Exemples de contenu de `Config` selon le moteur :
- `xray` → `{"uuid": "<uuid v4>"}`
- `ssh`, `dnstt`, `slowdns` → `{"public_key": "<hex>", "private_key": "<hex>"}`
- `tuic` → `{"uuid": "<uuid v4>", "password": "<token>"}`
- `wireguard` → `{"private_key": "<base64>", "public_key": "<base64>", "address": "10.66.0.N/32"}`
- `hysteria` → `{"password": "<token>"}`

### `store.Offer` — modèle d'abonnement (`internal/store/`)

```go
type Offer struct {
    ID           string
    Name         string
    DurationDays int
    QuotaBytes   int64
    MaxConnections int
    MaxIPs       int
}
```

Template réutilisable. Appliqué une fois à la création d'un compte, pas lié dynamiquement.

### `ClientResult` — résultat de génération (`internal/clientcfg/`)

```go
type ClientResult struct {
    Engine       string
    ClientLink   string    // URI ou commande client
    ServerConfig []byte    // JSON pour engine.Configure()
}
```

---

## 3. Relations entre les composants

```
engine.Engine  ←──── engines/xray, engines/ssh, engines/wireguard, …
     │
     │ Configure(EngineConfig{JSON})
     ▼
engcfg.EngineProfile  (V1 — cache plat)
     │
     │ pré-remplit
     ▼
profile.Profile  (V2 — profil nommé)
     │
     │ profil actif → BuildConfig() → Configure()
     ▼
srvcfg.Profile  (unique et global)
     │
     │ hostPort(engineName, prof)
     ▼
store.Account
     │
     │ acc.Grants["xray"]
     ▼
store.EngineGrant
     │
     │ Generate(acc, engineName, prof)
     ▼
clientcfg.ClientResult  (lien client + config serveur JSON)
```

Relations many-to-one actuelles :
- `engine.Engine` 1 → N `engcfg.EngineProfile` (un moteur peut avoir plusieurs configs en V1, mais une seule est active)
- `engine.Engine` 1 → N `profile.Profile` (V2 — plusieurs profils nommés par moteur, un seul actif)
- `srvcfg.Profile` : **1 seul** pour tout le système (pas de cardinalité multiple)
- `store.Account` 1 → N `store.EngineGrant` (un compte, plusieurs moteurs)
- `store.Offer` 1 → N `store.Account`

---

## 4. Gestion actuelle des moteurs

Chaque moteur est enregistré dans un registre en mémoire (`engine.Register`, `engine.Get`, `engine.Has`).

Les moteurs simples disponibles (constatés dans `internal/store/account.go` et `internal/engineutil/compat.go`) :
`xray`, `ssh`, `wireguard`, `hysteria`, `hysteria2`, `tuic`, `udp`, `slowdns`, `dnstt`

Les moteurs hybrides (composites) sont construits dynamiquement via `engineutil.CompositeEngine` et enregistrés via `RegisterHybridPersist`. Combinaisons valides actuelles :
- `slowdns→ssh`, `slowdns→xray`
- `dnstt→ssh`, `dnstt→xray`

Les moteurs UDP (tuic, hysteria2, wireguard) ne sont pas chaînables via le mécanisme actuel (RelaysTo ≠ Network pour UDP).

L'interface de gestion des moteurs est accessible via `[1] MOTEURS` dans le menu central.

---

## 5. Gestion actuelle des profils

Deux niveaux coexistent :

**V1 (`engcfg.EngineProfile`)** : un seul profil plat par moteur, géré via l'assistant de configuration dans le menu `[1] MOTEURS`. Persisté dans `engines/<name>/profile.json`.

**V2 (`profile.Profile`)** : profils nommés multiples, gérés via le menu `[2] PROFILS NOMMÉS` (option `[5]` dans le menu central). Persistés dans `profiles/<id>.json`. Un seul profil peut être `active` par moteur à la fois (garanti par `DeactivateAllForEngine`).

À l'activation d'un profil V2 :
1. `Validate()` — validation (y compris chaînage pour les hybrides)
2. `BuildConfig()` — construction du JSON depuis les `Params`
3. `engine.Configure()` — application au moteur
4. `DeactivateAllForEngine()` + `Activate()` + `Save()`

---

## 6. Gestion actuelle des serveurs

Il n'existe pas de modèle « Serveur » ou « Service » distinct dans le code.

Le rôle de configuration du serveur est tenu par `srvcfg.Profile` (unique, global). Il définit :
- L'hôte public du serveur
- Les ports par moteur (`Ports["xray"] = 443`)
- Les domaines et les domaines proxifiés

Ce profil est chargé via `srvcfg.Load()` et modifiable via le menu moteurs. Il n'est pas nommé, pas versionné, pas multi-instances.

La configuration serveur JSON réelle (inbounds Xray, authorized_keys SSH, etc.) est générée par `clientcfg.buildGroupedConfig()` ou `clientcfg.ApplyServerConfig()` au moment de l'activation ou de la régénération.

---

## 7. Gestion actuelle des utilisateurs/abonnés

Les abonnés sont gérés via le menu `[4] UTILISATEURS` (dans le menu central actuel — à vérifier le numéro exact).

Opérations disponibles : créer, modifier, activer/désactiver, attacher un moteur (`promptEngineAttach`), générer la config client, lister.

L'attachement d'un moteur à un abonné (`promptEngineAttach`) crée un `EngineGrant` dans `Account.Grants` sous le nom littéral du moteur (ex: `"xray"` ou `"dnstt-xray"` pour un hybride). Les secrets (UUID, clés) sont générés au moment de l'attachement via `EnsureEngineSecrets`.

---

## 8. Gestion actuelle des quotas

Les quotas sont définis au niveau du `store.Account` :
- `QuotaBytes int64` — limite de bande passante totale (0 = illimité)
- `UsedBytes int64` — utilisation courante
- `MaxConnections int` — sessions simultanées max
- `CurrentConns int` — sessions courantes
- `MaxIPs int` — IPs sources max
- `CurrentIPs int`

Ces valeurs sont **globales pour tous les moteurs de l'abonné**. Il est impossible de différencier un quota xray et un quota SSH pour le même abonné.

L'`Offer` définit des valeurs par défaut appliquées à la création du compte.

---

## 9. Gestion actuelle des expirations

L'expiration est définie sur `Account.ExpiresAt time.Time`.

Elle est **unique et globale** pour l'ensemble des accès de l'abonné. Il n'est pas possible de donner un accès Xray expirant le 31/12 et un accès SSH permanent au même abonné.

---

## 10. Gestion actuelle des identifiants spécifiques aux moteurs

Les identifiants spécifiques (UUID Xray, clés Ed25519 SSH/DNS, clés X25519 WireGuard) sont stockés dans `Account.Grants[engineName].Config map[string]any`.

Génération via `store.EnsureEngineSecrets(accountID, engine string)` — idempotente, génère les champs manquants uniquement :

| Moteur | Champs générés |
|--------|---------------|
| `xray`, `xray-*` | `uuid` (UUID v4) |
| `hysteria` | `password` (token 12 chars) |
| `tuic` | `uuid` + `password` |
| `dnstt`, `slowdns` | `public_key` + `private_key` (Ed25519 hex) |
| `ssh` | `public_key` + `private_key` (Ed25519 hex) |
| `wireguard` | `private_key` + `public_key` (X25519 base64) + `address` (10.66.0.N/32) |

L'adresse WireGuard est allouée de façon unique à l'échelle de tous les comptes via `nextWireGuardAddress()`.

Le champ `Token` de `Account` est un token d'API admin distinct des secrets moteur.

---

## 11. Gestion actuelle des configurations client

Générées par `internal/clientcfg/Generate(acc, engineName, prof)`.

Format produit par moteur :

| Moteur | Lien client |
|--------|------------|
| `xray` | `vless://UUID@host:port?encryption=none&flow=xtls-rprx-vision&security=reality&...` |
| `ssh` | `ssh username@host -p port` |
| `wireguard` | Fichier `.conf` WireGuard complet |
| `hysteria` | `hysteria://password@host:port` |
| `hysteria2` | URI hysteria2 |
| `tuic` | URI tuic |
| `slowdns`, `dnstt` | `<engine>://username@domain?key=public_key` |
| `udp` | `udp://username@host:port?pass=password` |

La config serveur JSON (inbounds, utilisateurs, clés) est également générée par `buildGroupedConfig()` en regroupant tous les comptes ayant un grant pour ce moteur.

Le lien VLESS utilise la vraie clé publique REALITY via `xray.LoadRealityKeys()` — une erreur explicite est retournée si le moteur n'a pas encore été installé (pas de placeholder silencieux).

---

## 12. Gestion actuelle des hybrides

Les moteurs hybrides combinent un transport DNS (slowdns/dnstt) et un VPN (ssh/xray) via `engineutil.CompositeEngine`.

Validation via :
- `engineutil.ValidateHybrid(components)` — rôles, unicité transport/VPN
- `engineutil.CompatibilityCheck(components)` — avertissements dépendances
- `engineutil.EvaluateChain(components, prof)` — chaînage réel (CanConnect + DetectPortConflicts)

Persistance du moteur hybride : `RegisterHybridPersist(components)` crée le `CompositeEngine` et l'enregistre dans le registre en mémoire.

Le grant d'un abonné pour un hybride est stocké sous le **nom littéral du moteur hybride** (ex: `"dnstt-xray"`), pas sous ses composants séparément. `aliasGrantForComponent()` dans `clientcfg` crée des alias pour que chaque composant puisse lire ses secrets depuis ce grant unique.

La configuration serveur d'un hybride est construite composant par composant via `buildComponentConfigs()`, chaque composant recevant sa propre config JSON (pas un blob partagé).

---

## 13. Fonctionnalités déjà présentes à conserver

Ces fonctionnalités sont **opérationnelles et correctement implémentées**. Toute migration doit les préserver à l'identique :

1. **Registre de moteurs** (`engine.Register`, `engine.Get`, `engine.Has`) — fonctionnel
2. **Interface `engine.Engine`** à 13 méthodes — ne pas modifier
3. **CompositeEngine et chaînage hybride** — ValidateHybrid, EvaluateChain, CanConnect, DetectPortConflicts — ne pas toucher
4. **Profils nommés V2** (17 tests passants) — `internal/profile/` — conservé tel quel
5. **EnsureEngineSecrets** — génération idempotente des secrets par moteur — à préserver (ne pas régénérer les secrets existants)
6. **Clés REALITY Xray** (`xray.LoadRealityKeys`, `xray.RealityDir()`) — générées à l'installation, pas dans le store abonné
7. **Adresses WireGuard uniques** (`nextWireGuardAddress`) — allocation séquentielle unique
8. **buildGroupedConfig** — génération de config serveur regroupant tous les abonnés d'un moteur
9. **ApplyServerConfig** — régénération et application de la config serveur complète
10. **Style visuel des menus** (bordures `═`, bullets `●/○`, numérotation) — à conserver exactement
11. **Génération lien VLESS avec vraie clé REALITY** (erreur explicite si moteur non installé)
12. **Migration idempotente** — aucune opération ne doit recréer des secrets déjà existants

---

## 14. Problèmes ou incohérences constatés

### P1 — Absence du niveau « Service/Serveur nommé »

Le flux actuel passe directement de `Engine → Account.Grants`. Il manque un niveau intermédiaire représentant une **instance nommée de service** (ex: `Xray-Production` sur le port 443, `Xray-Test` sur le port 8443). Sans ce niveau, il est impossible de :
- Distinguer deux instances du même moteur
- Assigner des groupes d'abonnés différents à chaque instance
- Avoir des ports/domaines différents par instance

### P2 — `srvcfg.Profile` global non rattaché à un service

Un seul `Ports["xray"]` pour tout le système. Pas possible de configurer deux instances Xray simultanément.

### P3 — Quota et expiration globaux

`Account.ExpiresAt` et `Account.QuotaBytes` s'appliquent à **tous** les accès de l'abonné. Impossible de différencier les limites par service pour un même abonné.

### P4 — Révoquer un service ≠ opération atomique

Pour retirer l'accès Xray d'un abonné, il faut manipuler directement `Account.Grants`. Il n'existe pas d'opération nommée « révoquer l'accès au service X pour l'abonné Y ». La suppression d'un grant peut supprimer des secrets sans confirmation.

### P5 — `Config map[string]any` opaque

Les champs requis par moteur dans `EngineGrant.Config` ne sont pas typés ni documentés dans la structure de données. Leur existence est documentée dans `EnsureEngineSecrets` uniquement. Un moteur qui lira un champ absent échouera silencieusement (retour de chaîne vide).

### P6 — Confusion vocabulaire `profile.Profile` vs `srvcfg.Profile`

Deux types différents s'appellent tous deux « Profile ». `profile.Profile` est une configuration moteur nommée ; `srvcfg.Profile` est la configuration du serveur physique. Cette ambiguïté complique la lecture du code.

### P7 — Un abonné est lié à un moteur, pas à un service

`acc.HasEngine("xray")` retourne vrai si l'abonné a un grant pour `"xray"`. Mais si on ajoute un second service Xray sur un autre port, il n'y a aucun moyen de savoir à quel service l'abonné est censé accéder.

### P8 — `clientcfg.Generate` suppose une correspondance 1:1 engine↔port

La fonction utilise `prof.Port(engineName)` sur le profil serveur global. Si plusieurs services utilisent le même moteur sur des ports différents, `Generate` ne peut pas distinguer lequel utiliser.

---

## 15. Architecture cible proposée

```
MOTEUR  (engine.Engine — inchangé)
  Brique technique pure.
  
  ↓ 1 moteur → N profils de configuration

PROFIL MOTEUR  (profile.Profile — V2 existant, conservé tel quel)
  Configuration nommée d'un moteur ou hybride.
  Ex: "Xray-Production", "Xray-Test", "SSH-Backup"
  
  ↓ 1 profil actif → 0..1 service

SERVICE  (nouveau : service.Service)
  Instance nommée en cours d'exécution.
  Ex: "Xray-Prod-443", "SSH-22", "WG-51820"
  Contient : host, ports, domaines, référence au profil moteur.
  Nœud central entre la configuration technique et les abonnés.
  
  ↓ 1 service → N accès abonnés

ACCÈS  (nouveau : service.Access — remplace store.EngineGrant)
  Droits d'un abonné sur un service précis.
  Contient : secrets spécifiques au protocole, quota, expiration, sessions.
  
  ↓ N accès → 1 abonné

ABONNÉ  (store.Account — évolue, ne casse pas)
  Identité administrative uniquement.
  Ne porte plus les secrets ni les limites par moteur.
```

---

## 16. Relations proposées entre Engine, Profile, Server, User, Subscription, Access et ClientConfig

### Types proposés

```go
// service.Service — instance nommée d'un moteur
type Service struct {
    ID        string
    Name      string            // ex: "Xray-Production"
    ProfileID string            // réf → profile.Profile.ID
    Engine    string            // dénormalisé pour accès rapide
    Host      string
    Ports     map[string]int    // {"listen": 443, "public": 443}
    Domains   []string
    Enabled   bool
    CreatedAt time.Time
    UpdatedAt time.Time
}

// service.Access — accès d'un abonné à un service
type Access struct {
    ID             string
    AccountID      string         // réf → store.Account.ID
    ServiceID      string         // réf → service.Service.ID
    Engine         string         // dénormalisé
    Secrets        map[string]any // uuid, clés, adresse — selon moteur
    QuotaBytes     int64          // 0 = illimité
    ExpiresAt      time.Time      // zero = jamais
    MaxConnections int
    MaxIPs         int
    Enabled        bool
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

### Cardinalités

| Relation | Cardinalité | Note |
|----------|-------------|------|
| Engine → EngineProfile (V2) | 1 → N | Plusieurs configs nommées par moteur |
| EngineProfile → Service | 1 → 0..1 | Un profil actif peut être lié à un service |
| Service → Access | 1 → N | Plusieurs abonnés sur un service |
| Account → Access | 1 → N | Un abonné peut avoir N accès (N services différents) |
| Offer → Account | 1 → N | Template appliqué à la création |

### Règles invariantes

1. Révoquer un `Access` ne supprime pas l'`Account`.
2. Les secrets (UUID, clés) vivent dans `Access.Secrets`, jamais dans `Account`.
3. Un abonné peut avoir des accès simultanés sur plusieurs services différents.
4. Quota et expiration sont définis par `Access`, différents par service pour le même abonné.
5. Chaque `Service` a ses propres ports — plus de conflit via un profil serveur global unique.
6. La config client est générée depuis un `Access` (qui référence son `Service` pour host/port).

### Génération config client avec le nouveau modèle

```
clientcfg.GenerateFromAccess(access service.Access, svc service.Service) → ClientResult
```

Remplace à terme : `clientcfg.Generate(acc store.Account, engineName string, prof srvcfg.Profile)`.

Les champs de `Access.Secrets` remplacent `Account.Grants[engineName].Config`.
Les champs de `Service` (Host, Ports, Domains) remplacent `srvcfg.Profile`.

---

## 17. Proposition d'organisation des menus

Style visuel conservé à l'identique (bordures `═`, bullets `●/○`, couleurs ANSI, numérotation).

### Menu central proposé

```
╔══════════════════════════════════════════════╗
║          LABOSURF_PRO — MENU PRINCIPAL       ║
╠══════════════════════════════════════════════╣
║  [1] MOTEURS           ← inchangé            ║
║  [2] PROFILS MOTEURS   ← V2 existant         ║
║  [3] SERVICES          ← NOUVEAU             ║
║  [4] ABONNÉS           ← évolution           ║
║  [5] ACCÈS             ← remplace Grants     ║
║  [6] OFFRES            ← inchangé            ║
║  [7] LICENCE           ← inchangé            ║
║  [0] QUITTER                                 ║
╚══════════════════════════════════════════════╝
```

### [3] SERVICES

```
╔══════════════════════════════════════════════╗
║              GESTION DES SERVICES            ║
╠══════════════════════════════════════════════╣
║  Services actifs :                           ║
║   ● Xray-Production  xray :443  [ACTIF]      ║
║   ○ SSH-Backup       ssh  :22   [inactif]    ║
║   ● WG-51820         wg   :51820[ACTIF]      ║
╠══════════════════════════════════════════════╣
║  [N] Nouveau service                         ║
║  [numéro] Gérer ce service                   ║
╚══════════════════════════════════════════════╝
```

Sous-menu service : `[A] Activer`, `[D] Désactiver`, `[E] Éditer`, `[U] Abonnés`, `[C] Config client par abonné`, `[X] Retour`.

### [5] ACCÈS

```
╔══════════════════════════════════════════════╗
║           GESTION DES ACCÈS ABONNÉS          ║
╠══════════════════════════════════════════════╣
║  Abonné : [chercher par nom/ID]              ║
╠══════════════════════════════════════════════╣
║  Accès de user_123 :                         ║
║   ● Xray-Production   expire: 2026-12-31     ║
║   ○ SSH-Backup        [désactivé]            ║
╠══════════════════════════════════════════════╣
║  [A] Ajouter un accès (choisir service)      ║
║  [R] Révoquer un accès                       ║
║  [Q] Quota / expiration par accès            ║
║  [C] Générer config client                   ║
╚══════════════════════════════════════════════╝
```

---

## 18. Proposition d'organisation des écrans

### Écran Service — détail

```
╔══════════════════════════════════════════════════════╗
║  SERVICE : Xray-Production                           ║
╠══════════════════════════════════════════════════════╣
║  Moteur     : xray                                   ║
║  Profil     : Xray-Prod (ID: abc123)                 ║
║  Hôte       : vpn.mondomaine.tld                     ║
║  Port       : 443                                    ║
║  Domaines   : vpn.mondomaine.tld                     ║
║  Statut     : ● ACTIF                                ║
║  Abonnés    : 12 accès actifs                        ║
╠══════════════════════════════════════════════════════╣
║  [A] Activer   [D] Désactiver   [E] Éditer           ║
║  [U] Voir les abonnés           [C] Config client    ║
║  [X] Retour                                          ║
╚══════════════════════════════════════════════════════╝
```

### Écran Accès — détail

```
╔══════════════════════════════════════════════════════╗
║  ACCÈS : user_alice → Xray-Production                ║
╠══════════════════════════════════════════════════════╣
║  Abonné    : alice (ID: acc_001)                     ║
║  Service   : Xray-Production (xray :443)             ║
║  UUID      : xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx    ║
║  Quota     : 10 GB (3.2 GB utilisés)                 ║
║  Expire    : 2026-12-31                              ║
║  Sessions  : 3 max / 1 active                        ║
║  Statut    : ● ACTIF                                 ║
╠══════════════════════════════════════════════════════╣
║  [C] Config client    [R] Révoquer    [Q] Quota      ║
║  [X] Retour                                          ║
╚══════════════════════════════════════════════════════╝
```

---

## 19. Plan de migration depuis l'architecture actuelle

### Principe directeur : aucune rupture sur les moteurs, profils V2 et comptes existants

**Fichiers non touchés :**
- `engine.Engine` interface
- `internal/profile/` (V2 — 17 tests passants)
- `internal/engineutil/` (ValidateHybrid, EvaluateChain, CanConnect…)
- `engines/` (xray, ssh, wireguard, slowdns, dnstt, hysteria, tuic, udp)

### Étape 1 — Créer `internal/service/` (nouveau package, aucune modification de l'existant)

Créer les types `Service` et `Access`, le store CRUD, les tests unitaires.
- Persistance : `$LABOSURF_DATA_DIR/services/<id>.json` et `$LABOSURF_DATA_DIR/access/<id>.json`
- Tests : CRUD Service, CRUD Access, unicité adresse WireGuard sur Access

### Étape 2 — Ajouter `service.EnsureAccessSecrets` (analogue de `store.EnsureEngineSecrets`)

Même logique, même idempotence, même tableau de secrets par moteur. Opère sur un `Access` au lieu d'un `Account.Grants`. Les secrets existants dans les Grants ne sont pas régénérés — la migration copie leur valeur.

### Étape 3 — Fonction de migration `MigrateGrantsToAccess`

```go
// MigrateGrantsToAccess lit les Grants d'un compte et crée
// des Access correspondants si un Service matching existe.
// Idempotent : ne crée pas d'Access si un équivalent existe déjà.
// Ne supprime pas les Grants existants (migration progressive).
func MigrateGrantsToAccess(s *store.Store, svcStore *service.Store) error
```

La migration se fait à chaud, compte par compte, sans downtime.

### Étape 4 — Ajouter `clientcfg.GenerateFromAccess`

Nouvelle surcharge acceptant `(access service.Access, svc service.Service)`. L'ancienne signature `Generate(acc, engineName, prof)` reste disponible pendant la transition.

### Étape 5 — Ajouter les menus [3] SERVICES et [5] ACCÈS

En parallèle des menus existants. Le menu [4] UTILISATEURS/ABONNÉS existant reste fonctionnel. La suppression de l'ancien menu Grants se fait quand 100% des comptes ont leurs Access migrés.

### Étape 6 — Dépréciation progressive de `Account.Grants`

Une fois tous les Access créés et les menus migrés, `Account.Grants` peut être vidé et marqué `Deprecated`. La suppression définitive du champ est une étape finale, après validation en production.

---

## 20. Risques de régression

| Risque | Niveau | Mitigation |
|--------|--------|-----------|
| Régénération des secrets existants (UUID, clés) | **CRITIQUE** | `EnsureAccessSecrets` vérifie l'existence avant de générer ; migration copie les valeurs |
| Clés REALITY Xray perdues ou recréées | **CRITIQUE** | Les clés REALITY sont dans `engines/xray/` (hors store) — migration ne les touche pas |
| Adresses WireGuard dupliquées à la migration | **ÉLEVÉ** | Copie des adresses depuis `Grants["wireguard"].Config["address"]`, pas de réallocation |
| Comptes existants sans Service correspondant | **MOYEN** | Migration idempotente — les Grants restent actifs si pas de Service créé |
| Tests V2 cassés | **FAIBLE** | `internal/profile/` n'est pas modifié ; les 17 tests existants doivent rester verts |
| Confusion de port `srvcfg.Profile` → `service.Service` | **MOYEN** | La migration crée un Service par moteur actif avec les ports issus de srvcfg |
| Style visuel des menus altéré | **FAIBLE** | Nouveaux menus construits avec les mêmes helpers d'affichage existants |

---

## 21. Fonctionnalités qui ne doivent surtout pas être supprimées

1. **Génération lien VLESS avec vraie clé REALITY** — erreur explicite si moteur non installé (pas de placeholder)
2. **`EnsureEngineSecrets` idempotent** — ne doit jamais recréer des secrets déjà existants
3. **Adresses WireGuard uniques** — unicité globale 10.66.0.2–254, allocation séquentielle
4. **`buildGroupedConfig` et `ApplyServerConfig`** — régénération de la config serveur complète pour tous les abonnés d'un moteur
5. **`aliasGrantForComponent`** — mécanisme permettant aux composants d'un hybride de lire les secrets depuis le grant nommé de l'hybride (ex: `"dnstt-xray"`)
6. **`buildComponentConfigs`** — config serveur composant par composant pour les hybrides (pas un blob partagé)
7. **`CompositeEngine` et `RegisterHybridPersist`** — pipeline hybride existant
8. **17 tests unitaires `internal/profile/`** — doivent rester verts sans modification
9. **Menu visuel LABOSURF_PRO** — style, couleurs, numérotation, alignement
10. **Jitter anti-DPI DNS** (`jitter_ms: 40` dans `dnsTunnelServerConfig`) — paramètre légitime, ne pas retirer

---

## 22. Questions ou points nécessitant une décision avant de coder

### Q1 — Rétrocompatibilité `Account.Grants`

Pendant la période de cohabitation (Grants + Access), quelle est la source de vérité pour la génération de config client ? Faut-il lire `Access` en priorité et tomber sur `Grants` en fallback, ou interdire la génération si `Access` n'existe pas encore ?

### Q2 — Création automatique d'un Service par défaut

À la migration, doit-on créer automatiquement un `Service` par moteur actif (depuis `srvcfg.Profile`), ou exiger que l'opérateur crée les Services manuellement avant de migrer les comptes ?

### Q3 — Nom du champ `Secrets` dans `Access`

Garder `map[string]any` (comme `EngineGrant.Config`) pour la flexibilité par moteur, ou définir des sous-types typés par moteur (struct XraySecrets, SSHSecrets…) ? Le typage fort évite les erreurs silencieuses mais complexifie la sérialisation.

### Q4 — Quota global vs quota par service

Le système actuel a un quota global. La proposition met le quota dans l'`Access` (par service). Faut-il aussi conserver un quota global de compte comme plafond supérieur, ou le supprimer complètement ?

### Q5 — Un Service peut-il être lié à plusieurs profils historiques ?

Actuellement, un profil V2 est « actif » ou non. Dans le nouveau modèle, un Service référence un `ProfileID`. Si on change le profil actif du moteur, le Service suit-il automatiquement, ou reste-t-il lié au profil au moment de sa création ?

### Q6 — Numérotation des menus

Le menu central actuel a les options `[1]` à `[5]` (ou selon la version installée). L'ajout de `[3] SERVICES` et `[5] ACCÈS` décale les numéros existants. Préférence pour renuméroter complètement ou intercaler les nouvelles options ?

### Q7 — Migration des hybrides

Les Grants hybrides utilisent le nom littéral du moteur hybride (ex: `"dnstt-xray"`) comme clé. Dans le nouveau modèle, un hybride est-il représenté comme **un seul Service** (avec Engine=`"dnstt-xray"`), ou comme **deux Services chaînés** ? La réponse conditionne la migration des Grants hybrides existants.

### Q8 — Suppression de `srvcfg.Profile`

`srvcfg.Profile` est actuellement chargé dans de nombreux endroits (`clientcfg`, `profile/validate.go`, menus). Sa suppression au profit de `service.Service` implique de modifier tous ces points d'usage. Faut-il maintenir `srvcfg.Profile` comme source de vérité pour la période de transition, ou le remplacer immédiatement ?

---

*Fin de l'analyse — aucune modification de code effectuée.*
*Basé sur l'observation du dépôt LABOSURF_PRO, commit `3749132`, branche `main`, date `2026-09-14`.*
