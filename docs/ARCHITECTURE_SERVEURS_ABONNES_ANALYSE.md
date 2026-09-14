# Analyse de l'architecture actuelle — LABOSURF_PRO

> Document d'analyse uniquement. Aucune modification de code.
> Basé sur l'observation réelle du dépôt — commit `7c9c824`, branche `main`, date 2026-09-14.

---

## 1. Architecture actuelle

Le système actuel suit ce flux :

```
MOTEUR  (engine.Engine)
  ↓  configuration nommée via assistant
PROFIL MOTEUR V1  (engcfg.EngineProfile — cache plat clé/valeur)
  ↓  profil nommé avec statut
PROFIL MOTEUR V2  (profile.Profile — nommé, actif/inactif)
  ↓  config globale du serveur physique
PROFIL SERVEUR  (srvcfg.Profile — unique et global)
  ↓  compte avec grants par moteur
ABONNÉ  (store.Account)
  ↓  grant par nom de moteur
GRANT MOTEUR  (store.EngineGrant)
  ↓  secrets + profil serveur global
CONFIG CLIENT  (internal/clientcfg/)
```

Il n'existe pas de niveau « Service » ou « Serveur nommé » entre le moteur et l'abonné.
L'accès abonné est directement rattaché au nom littéral du moteur (ex: `"xray"`),
pas à une instance nommée (ex: `"Xray-Production"`).

---

## 2. Modèles/types existants

### `engine.Engine` — interface (`internal/engine/`)

Interface à 13 méthodes :
`Name()`, `Version()`, `Description()`, `Install()`, `Configure()`,
`Start()`, `RunForeground()`, `Stop()`, `Restart()`, `Status()`,
`HealthCheck()`, `Logs()`, `Update()`, `Uninstall()`.

Brique technique pure. Le moteur ne connaît ni les abonnés, ni les quotas,
ni les ports d'écoute en dehors de ce que lui transmet `Configure(EngineConfig{JSON})`.

### `engcfg.EngineProfile` — config moteur V1 (`internal/engcfg/`)

```go
type EngineProfile struct {
    Engine string            `json:"engine"`
    Values map[string]string `json:"values"`
}
```

Persisté dans `$LABOSURF_DATA_DIR/engines/<name>/profile.json`.
Cache plat clé/valeur pour l'assistant de configuration (port, domaine, etc.).
Pas d'identité nommée, pas de statut actif/inactif, pas d'historique.

### `profile.Profile` — profil nommé V2 (`internal/profile/`)

```go
type Profile struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Description string            `json:"description,omitempty"`
    Kind        ProfileKind       `json:"kind"`       // "simple" | "hybrid"
    Engine      string            `json:"engine,omitempty"`
    Components  []string          `json:"components,omitempty"`
    Params      map[string]string `json:"params,omitempty"`
    Status      ProfileStatus     `json:"status"`     // "draft" | "active" | "inactive"
    CreatedAt   time.Time         `json:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at"`
}
```

Persisté dans `$LABOSURF_DATA_DIR/profiles/<id>.json`.
Configuration nommée d'un moteur (Kind=simple) ou d'un hybride (Kind=hybrid).
Un seul profil peut être `active` par moteur à la fois.
Aucun champ port, domaine ou abonné.

### `srvcfg.Profile` — profil serveur global (`internal/srvcfg/`)

```go
type Profile struct {
    Host    string
    Ports   map[string]int    // ex: {"xray": 443, "ssh": 22, "wireguard": 51820}
    Domains []string
    Proxied []string
}
```

**Unique et global** — un seul fichier pour tout le système.
Pas de concept de plusieurs serveurs ou instances nommées.
Le port xray est identique pour tous les abonnés.

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

Le compte est le modèle abonné central. Il contient directement les grants par moteur
et les limites globales (quota, expiration, sessions max).

### `store.EngineGrant` — accès par moteur

```go
type EngineGrant struct {
    Engine  string
    Config  map[string]any   // secrets spécifiques au moteur
    Enabled bool
}
```

Exemples de contenu de `Config` selon le moteur :

| Moteur | Champs dans Config |
|--------|-------------------|
| `xray`, `xray-*` | `{"uuid": "<uuid v4>"}` |
| `ssh` | `{"public_key": "<hex>", "private_key": "<hex>"}` |
| `dnstt`, `slowdns` | `{"public_key": "<hex>", "private_key": "<hex>"}` |
| `tuic` | `{"uuid": "<uuid v4>", "password": "<token>"}` |
| `wireguard` | `{"private_key": "<base64>", "public_key": "<base64>", "address": "10.66.0.N/32"}` |
| `hysteria` | `{"password": "<token>"}` |
| `hysteria2` | `{"password": "<token>"}` |

### `store.Offer` — modèle d'abonnement

```go
type Offer struct {
    ID             string
    Name           string
    DurationDays   int
    QuotaBytes     int64
    MaxConnections int
    MaxIPs         int
}
```

Template réutilisable appliqué une fois à la création d'un compte, non lié dynamiquement.

### `engineutil.EngineCapability` — matrice de compatibilité (`internal/engineutil/compat.go`)

```go
type EngineCapability struct {
    Provides  []string   // rôles fournis (ex: "vpn", "transport")
    Requires  []string   // rôles requis
    Protocol  string
    Port      int
    Network   string
    RelaysTo  string
}
```

Utilisée par `ValidateHybrid`, `CanConnect`, `EvaluateChain` pour valider les combinaisons hybrides.

### `ClientResult` — résultat de génération (`internal/clientcfg/`)

```go
type ClientResult struct {
    Engine       string
    ClientLink   string    // URI ou commande client
    ServerConfig []byte    // JSON pour engine.Configure()
}
```

---

## 3. Relations entre les modèles

```
engine.Engine  ←──── engines/xray, engines/ssh, engines/wireguard,
                     engines/slowdns, engines/dnstt, engines/hysteria,
                     engines/hysteria2, engines/tuic, engines/udp
     │
     │ Configure(EngineConfig{JSON})
     ▼
engcfg.EngineProfile  (V1 — cache plat, 1 par moteur)
     │
     │ pré-remplit les Params lors de la création
     ▼
profile.Profile  (V2 — profil nommé, N par moteur, 1 actif max)
     │
     │ profil actif → BuildConfig() → Configure()
     ▼
srvcfg.Profile  (unique et global — host, ports, domaines)
     │
     │ hostPort(engineName, prof) dans clientcfg
     ▼
store.Account
     │
     │ acc.Grants["xray"] / acc.Grants["dnstt-xray"] / etc.
     ▼
store.EngineGrant  (secrets spécifiques au protocole)
     │
     │ Generate(acc, engineName, prof)
     ▼
clientcfg.ClientResult  (lien client URI + config serveur JSON)
```

Cardinalités actuelles :

| Relation | Cardinalité |
|----------|-------------|
| Engine → EngineProfile V1 | 1 → 1 (un seul par moteur) |
| Engine → Profile V2 | 1 → N (plusieurs nommés, 1 actif max) |
| srvcfg.Profile (global) | 1 seul pour tout le système |
| Account → EngineGrant | 1 → N (un grant par moteur accédé) |
| Offer → Account | 1 → N |
| EngineGrant.Config | map[string]any opaque (non typé) |

---

## 4. Gestion des moteurs

Chaque moteur est enregistré dans un registre en mémoire via `engine.Register(e Engine)`.
Accessible via `engine.Get(name string)` et `engine.Has(name string)`.

Moteurs simples disponibles (constatés dans `internal/store/account.go` et `engines/`) :
`xray`, `ssh`, `wireguard`, `hysteria`, `hysteria2`, `tuic`, `udp`, `slowdns`, `dnstt`

Les moteurs hybrides (composites) sont construits dynamiquement via `engineutil.CompositeEngine`
et enregistrés via `RegisterHybridPersist(components []string)`.

Combinaisons hybrides valides actuellement (via RelaysTo=`"tcp"` → Network=`"tcp"`) :
- `slowdns → ssh` ✓
- `slowdns → xray` ✓
- `dnstt → ssh` ✓
- `dnstt → xray` ✓

Les moteurs UDP (`tuic`, `hysteria2`, `wireguard`) ne sont pas chaînables avec le mécanisme
actuel (RelaysTo ≠ Network pour UDP).

L'interface de gestion des moteurs est accessible via `[1] MOTEURS` dans le menu central.

---

## 5. Activation / désactivation des moteurs

L'activation d'un moteur se fait en deux temps :

### 5.1 Activation directe du moteur

Via `e.Start(ctx)` ou `e.RunForeground(ctx)` — démarre le processus du moteur.
Le moteur doit avoir été installé (`e.Install`) et configuré (`e.Configure`) au préalable.

### 5.2 Activation via un profil nommé V2

`profileActivate(p *profile.Profile)` dans `cmd/labosurf/menu_profiles.go` :

1. `profile.Validate(p)` — vérifie la validité (chaînage pour les hybrides)
2. `profile.BuildConfig(p, store)` — construit le JSON depuis `p.Params`
3. `e.Configure(ctx, engine.EngineConfig{JSON: cfg})` — injecte la config dans le moteur
4. `profile.DeactivateAllForEngine(engineName, exceptID)` — désactive les autres profils du même moteur
5. `p.Activate()` + `profile.Save(&p)` — persiste le statut `active`

Pour un profil hybride :
- Étape 3 remplacée par `engineutil.RegisterHybridPersist(p.Components)` — crée le `CompositeEngine`
- L'injection d'endpoint backend se fait via `waitForEndpoint` dans `CompositeEngine.Start`

### 5.3 Désactivation

`profileDeactivate(p *profile.Profile)` :
- Pour un hybride : appelle `engineutil.RemoveHybridPersist` avant de désactiver
- Pour un simple : `p.Deactivate()` + `profile.Save(&p)` uniquement

### 5.4 Règle d'exclusivité

`DeactivateAllForEngine(engineName, exceptID string)` garantit qu'un seul profil
est `active` par moteur à tout moment. Tous les autres profils du même moteur
passent à `inactive`.

---

## 6. Gestion des Engine Profiles

Deux générations coexistent :

### V1 — `engcfg.EngineProfile`

- Un seul profil par moteur, géré via l'assistant de configuration (`[1] MOTEURS → Configurer`)
- Persisté dans `$LABOSURF_DATA_DIR/engines/<name>/profile.json`
- Format : `{Engine string, Values map[string]string}`
- Sert de source de pré-remplissage pour la création de profils V2

### V2 — `profile.Profile`

- Plusieurs profils nommés par moteur, accessible via `[5] PROFILS NOMMÉS` dans le menu central
- Persistés dans `$LABOSURF_DATA_DIR/profiles/<id>.json` (un fichier par profil)
- Format complet : ID, nom, description, kind, engine/components, params, status, timestamps
- Un seul profil peut être `active` par moteur (garanti par `DeactivateAllForEngine`)
- Un profil `draft` peut être testé sans activation via `profileDryRun` (validation uniquement)
- Un profil peut être dupliqué via `p.Duplicate(newName)` — produit un `draft` avec un nouvel ID

Opérations disponibles sur un profil V2 :
- Activer (`[A]`), Désactiver (`[D]`), Test/dry-run (`[T]`), Dupliquer (`[C]`), Éditer les params (`[E]`), Supprimer (`[X]`)
- La suppression vérifie `IsActive()` et `Dependents()` avant d'autoriser `Delete()`

---

## 7. Gestion des serveurs/services

**Il n'existe pas de modèle « Serveur » ou « Service » distinct dans le code.**

Le rôle de configuration du serveur physique est tenu par `srvcfg.Profile` (unique, global).
Il définit :
- L'hôte public du serveur (`Host string`)
- Les ports par moteur (`Ports map[string]int` — ex: `{"xray": 443, "ssh": 22}`)
- Les domaines publics (`Domains []string`)
- Les domaines proxifiés (`Proxied []string`)

Ce profil est chargé via `srvcfg.Load()` et modifiable via le menu moteurs.
Il n'est pas nommé, pas versionné, pas multi-instances.

La configuration serveur JSON réelle (inbounds Xray, authorized_keys SSH, etc.) est générée par :
- `clientcfg.buildGroupedConfig(engineName, accounts, prof)` — config regroupant tous les abonnés
- `clientcfg.ApplyServerConfig(ctx, store, engineName, prof)` — régénère et applique via `e.Configure()`

---

## 8. Gestion des utilisateurs / abonnés

Les abonnés sont gérés via le menu `[4] UTILISATEURS` (ou numéro équivalent dans le menu central).

Opérations disponibles :
- Lister, créer, modifier, activer/désactiver
- Attacher un moteur (`promptEngineAttach`) — crée un `EngineGrant` dans `Account.Grants`
- Générer la config client — via `clientcfg.Generate(acc, engineName, srvcfg.Load())`

L'attachement d'un moteur à un abonné :
1. Crée un `EngineGrant` dans `Account.Grants[engineName]`
2. Appelle `store.EnsureEngineSecrets(accountID, engineName)` pour générer les secrets manquants

Le grant est stocké sous le **nom littéral du moteur** :
- Moteur simple : clé = `"xray"`, `"ssh"`, `"wireguard"`, etc.
- Moteur hybride : clé = `"dnstt-xray"`, `"slowdns-ssh"`, etc. (nom complet du moteur composite)

---

## 9. Quotas

Les quotas sont définis au niveau de `store.Account` — **globaux pour tous les moteurs de l'abonné** :

```go
QuotaBytes     int64   // limite bande passante totale (0 = illimité)
UsedBytes      int64   // utilisation courante
MaxConnections int     // sessions simultanées max
CurrentConns   int     // sessions courantes
MaxIPs         int     // IPs sources max
CurrentIPs     int
```

Il est **impossible** de différencier un quota xray et un quota SSH pour le même abonné.
L'`Offer` définit des valeurs par défaut appliquées à la création du compte.

---

## 10. Expiration

L'expiration est définie sur `Account.ExpiresAt time.Time`.

Elle est **unique et globale** pour l'ensemble des accès de l'abonné.
Il n'est pas possible de donner un accès Xray expirant le 31/12 et un accès SSH permanent
au même abonné.

---

## 11. Identifiants spécifiques aux protocoles

Les identifiants spécifiques (UUID Xray, clés Ed25519 SSH/DNS, clés X25519 WireGuard)
sont stockés dans `Account.Grants[engineName].Config map[string]any`.

Génération via `store.EnsureEngineSecrets(accountID, engine string)` :
- **Idempotente** : génère les champs manquants uniquement, ne touche jamais les secrets existants
- Persistée immédiatement dans `users_db.json`

| Moteur | Champs générés | Mécanisme |
|--------|---------------|-----------|
| `xray`, `xray-*` | `uuid` | `secret.UUID()` — UUID v4 |
| `hysteria` | `password` | `secret.RandToken(12)` |
| `tuic` | `uuid` + `password` | UUID v4 + token 12 chars |
| `dnstt`, `slowdns` | `public_key` + `private_key` | `secret.Ed25519Keypair()` (hex) |
| `ssh` | `public_key` + `private_key` | `secret.Ed25519Keypair()` (hex) |
| `wireguard` | `private_key` + `public_key` + `address` | X25519 base64 + allocation unique |

L'adresse WireGuard est allouée de façon unique à l'échelle de **tous** les comptes via
`nextWireGuardAddress()` — pool 10.66.0.2–10.66.0.254, séquentiel.

Le `Token` de `Account` est un token d'API admin distinct des secrets moteur (ne pas confondre).

Les clés REALITY Xray (paire `private_key`/`public_key` pour le protocole REALITY) sont
stockées séparément dans `engines/xray/<RealityDir>/` et ne transitent **jamais** par
le store abonné.

---

## 12. Génération des configurations client

Générées par `internal/clientcfg/Generate(acc store.Account, engineName string, prof srvcfg.Profile)`.

Format produit par moteur :

| Moteur | Lien client généré |
|--------|-------------------|
| `xray` | `vless://UUID@host:port?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=<réelle clé REALITY>&...` |
| `ssh` | `ssh username@host -p port` |
| `wireguard` | Fichier `.conf` WireGuard complet (texte multiligne) |
| `hysteria` | `hysteria://password@host:port` |
| `hysteria2` | URI hysteria2 |
| `tuic` | URI tuic avec UUID + password |
| `slowdns`, `dnstt` | `<engine>://username@domain?key=public_key` |
| `udp` | `udp://username@host:port?pass=password` |

Points critiques :
- Le lien VLESS utilise la **vraie clé publique REALITY** via `xray.LoadRealityKeys()` —
  une erreur explicite est retournée si le moteur xray n'a pas encore été installé
  (pas de placeholder silencieux : un lien avec une fausse clé échouerait silencieusement
  au handshake REALITY)
- La config serveur JSON (inbounds, utilisateurs, clés) est générée par `buildGroupedConfig()`
  en regroupant **tous** les abonnés ayant un grant pour ce moteur

---

## 13. Gestion des serveurs simples

Un moteur simple (non hybride) est géré directement dans le registre en mémoire via
`engine.Register(e)` et `engine.Get(name)`.

Flux de configuration d'un moteur simple :
1. `e.Install(ctx)` — installe le binaire/service
2. `e.Configure(ctx, EngineConfig{JSON})` — injecte la configuration JSON
3. `e.Start(ctx)` — démarre le processus

La configuration JSON est construite par :
- `clientcfg.buildGroupedConfig(engineName, accounts, prof)` — tous les abonnés du moteur
- `profile.BuildConfig(p, store)` — depuis les `Params` d'un profil V2

Pour appliquer la config serveur complète après modification d'un compte :
```go
clientcfg.ApplyServerConfig(ctx, s *store.Store, engineName string, prof srvcfg.Profile)
```

Garantit d'abord `EnsureEngineSecrets` pour tous les comptes, puis régénère et applique
la config groupée via `e.Configure()`.

---

## 14. Gestion des serveurs hybrides

Un moteur hybride combine un **transport DNS** (slowdns ou dnstt) et un **backend VPN** (ssh ou xray).

### 14.1 Validation

`engineutil.ValidateHybrid(components []string)` — vérifie :
- Au moins 2 composants
- Exactement 1 rôle transport et 1 rôle VPN/account
- Pas de doublons de rôle

`engineutil.CompatibilityCheck(components)` — avertissements sur les dépendances optionnelles.

`engineutil.EvaluateChain(components, prof)` → `ChainReport` :
- `CanConnect(front, back)` — vérifie `RelaysTo == Network` entre composants adjacents
- `DetectPortConflicts(components, prof)` — conflits de port réels depuis `srvcfg.Profile`

### 14.2 Création

`engineutil.RegisterHybridPersist(components []string)` :
1. Crée un `CompositeEngine{Components: components}`
2. Enregistre dans le registre mémoire sous le nom littéral (ex: `"dnstt-xray"`)
3. Persiste dans `$LABOSURF_DATA_DIR/hybrids/` pour survie aux redémarrages

### 14.3 Exécution

`CompositeEngine.Start(ctx)` :
1. Démarre le composant VPN backend (ex: xray)
2. Attend l'endpoint backend via `waitForEndpoint`
3. Injecte l'endpoint dans la config du composant transport (ex: dnstt)
4. Démarre le composant transport

### 14.4 Grants abonnés pour les hybrides

Le grant d'un abonné pour un hybride est stocké sous le **nom littéral du moteur hybride**
(ex: `"dnstt-xray"`), pas sous ses composants séparément.

`aliasGrantForComponent(accounts, hybridName, component)` dans `clientcfg` crée des alias
pour que chaque composant puisse lire ses secrets depuis ce grant unique.

### 14.5 Config serveur hybride

`buildComponentConfigs(hybridName, components, accounts, prof)` construit la configuration
**propre à chaque composant** (pas un blob partagé) :
- Composant transport (dnstt) → reçoit son domaine/port/utilisateurs
- Composant VPN (xray) → reçoit ses inbounds/clients

Chaque composant reçoit sa propre configuration via `CompositeEngine.ComponentConfig`.

---

## 15. Fonctions existantes à préserver

Ces fonctionnalités sont **opérationnelles et correctement implémentées**. Toute migration V3
doit les préserver sans modification :

1. **Interface `engine.Engine`** à 13 méthodes — ne jamais modifier
2. **Registre de moteurs** (`engine.Register`, `engine.Get`, `engine.Has`) — fonctionnel
3. **CompositeEngine et chaînage hybride** — `ValidateHybrid`, `EvaluateChain`, `CanConnect`,
   `DetectPortConflicts` — ne pas toucher
4. **Profils nommés V2** — `internal/profile/` avec 17 tests passants — conserver tel quel
5. **`EnsureEngineSecrets` idempotent** — ne jamais régénérer des secrets déjà existants
6. **Clés REALITY Xray** (`xray.LoadRealityKeys`, `xray.RealityDir()`) — hors store abonné
7. **Adresses WireGuard uniques** (`nextWireGuardAddress`) — unicité globale, pool 10.66.0.2–254
8. **`buildGroupedConfig`** — génération config serveur regroupant tous les abonnés d'un moteur
9. **`ApplyServerConfig`** — régénération et application complète de la config serveur
10. **`aliasGrantForComponent`** — alias de grants pour composants hybrides
11. **`buildComponentConfigs`** — config serveur composant par composant pour hybrides
12. **`RegisterHybridPersist` / `RemoveHybridPersist`** — cycle de vie hybride
13. **Lien VLESS avec vraie clé REALITY** — erreur explicite si moteur non installé
14. **Jitter anti-DPI DNS** (`jitter_ms: 40` dans `dnsTunnelServerConfig`) — paramètre légitime
15. **Style visuel des menus** (bordures `═`, bullets `●/○`, couleurs ANSI, numérotation) — à
    conserver exactement

---

## 16. Problèmes et incohérences actuels

### P1 — Absence du niveau « Service/Serveur nommé »

Le flux passe directement de `Engine → Account.Grants`. Il manque le niveau intermédiaire
représentant une **instance nommée de service** (ex: `Xray-Production` sur port 443,
`Xray-Test` sur port 8443). Sans ce niveau, il est impossible de :
- Distinguer deux instances du même moteur
- Assigner des groupes d'abonnés différents à chaque instance
- Avoir des ports/domaines différents par instance

### P2 — `srvcfg.Profile` global non rattaché à un service

Un seul `Ports["xray"]` pour tout le système. Impossible de configurer deux instances Xray
simultanément avec des ports différents.

### P3 — Quota et expiration globaux au niveau du compte

`Account.ExpiresAt` et `Account.QuotaBytes` s'appliquent à **tous** les accès de l'abonné.
Impossible de différencier les limites par service pour un même abonné.

### P4 — Révoquer un service n'est pas une opération atomique

Pour retirer l'accès Xray d'un abonné, il faut manipuler directement `Account.Grants`.
Pas d'opération nommée « révoquer le service Xray-Production pour l'abonné Y ».
La suppression d'un grant peut supprimer des secrets sans confirmation claire.

### P5 — `Config map[string]any` opaque dans EngineGrant

Les champs requis par moteur dans `EngineGrant.Config` ne sont pas typés ni documentés
dans la structure de données. Leur existence est documentée dans `EnsureEngineSecrets`
uniquement. Un moteur qui lira un champ absent échouera silencieusement (chaîne vide).

### P6 — Confusion vocabulaire `profile.Profile` vs `srvcfg.Profile`

Deux types différents portent tous deux le nom « Profile » :
- `profile.Profile` : configuration moteur nommée (V2)
- `srvcfg.Profile` : configuration du serveur physique global

Cette ambiguïté complique la lecture et la navigation dans le code.

### P7 — Abonné lié à un moteur, pas à un service

`acc.HasEngine("xray")` retourne vrai si l'abonné a un grant pour `"xray"`. Si on ajoute
un second service Xray sur un autre port, il n'y a aucun moyen de savoir à quelle instance
l'abonné est censé accéder.

### P8 — `clientcfg.Generate` suppose une correspondance 1:1 engine↔port

La fonction utilise `prof.Port(engineName)` sur le profil serveur global. Si plusieurs
services utilisent le même moteur sur des ports différents, `Generate` ne peut pas distinguer
lequel utiliser.

---

## 17. Architecture cible proposée

```
MOTEUR  (engine.Engine — inchangé)
  Brique technique pure. Sait installer, démarrer, configurer, arrêter.
  Ne sait pas : qui l'utilise, à quel port, quota abonné.

  ↓ 1 moteur → N profils de configuration

PROFIL MOTEUR  (profile.Profile — V2 existant, conservé tel quel)
  Configuration nommée d'un moteur ou hybride.
  Ex: "Xray-Production", "Xray-Test", "SSH-Backup"
  Contient : params techniques (transport, flow, options…)
  Ne contient PAS : ports d'écoute, identité abonné, quota

  ↓ 1 profil actif → 0..1 service

SERVICE  (nouveau : service.Service)
  Instance nommée en cours d'exécution.
  Ex: "Xray-Prod-443", "SSH-22", "WG-51820"
  Contient : host, ports d'écoute, domaines, référence au profil moteur.
  Nœud central de l'accès abonné.

  ↓ 1 service → N accès

ACCÈS  (nouveau : service.Access — remplace store.EngineGrant)
  Droits d'un abonné sur un service précis.
  Contient : secrets spécifiques au protocole, quota, expiration, sessions.
  Révocable sans supprimer l'abonné.

  ↓ N accès → 1 abonné

ABONNÉ  (store.Account — évolue progressivement)
  Identité administrative uniquement.
  Ne porte plus les secrets ni les limites par moteur.
  Les secrets sont dans les Access.
```

---

## 18. Relations exactes entre Engine / Engine Profile / Server / User / Subscription / Access / ClientConfig

### Types proposés

```go
// service.Service — instance nommée d'un moteur en cours d'exécution
type Service struct {
    ID        string
    Name      string            // "Xray-Production"
    ProfileID string            // réf → profile.Profile.ID
    Engine    string            // "xray" (dénormalisé)
    Host      string            // "vpn.mondomaine.tld"
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
    Engine         string         // dénormalisé pour accès rapide
    Secrets        map[string]any // secrets spécifiques au protocole
    QuotaBytes     int64          // 0 = illimité
    ExpiresAt      time.Time      // zero = jamais
    MaxConnections int
    MaxIPs         int
    Enabled        bool
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

Contenu de `Access.Secrets` par moteur (identique à `EngineGrant.Config` actuel) :
- `xray` → `{"uuid": "..."}`
- `ssh` → `{"public_key": "...", "private_key": "..."}`
- `dnstt`, `slowdns` → `{"public_key": "...", "private_key": "..."}`
- `wireguard` → `{"private_key": "...", "public_key": "...", "address": "10.66.0.N/32"}`
- `tuic` → `{"uuid": "...", "password": "..."}`

### Cardinalités proposées

| Relation | Cardinalité | Note |
|----------|-------------|------|
| Engine → EngineProfile V2 | 1 → N | Plusieurs configs nommées par moteur |
| EngineProfile → Service | 1 → 0..1 | Un profil actif peut avoir un service lié |
| Service → Access | 1 → N | Plusieurs abonnés sur un service |
| Account → Access | 1 → N | Un abonné sur N services simultanément |
| Offer → Account | 1 → N | Template appliqué à la création |

### Règles invariantes du modèle cible

1. **Révoquer un Access ≠ supprimer l'Account** — l'abonné reste, seul l'accès au service est retiré
2. **UUID / clés = champs de l'Access**, jamais du Account
3. **Un abonné peut avoir des accès simultanés sur plusieurs services** (ex: Xray-Prod + SSH + WireGuard)
4. **Quota et expiration vivent dans l'Access** — différents par service pour le même abonné
5. **Chaque Service a ses propres ports** — plus de conflit via un profil serveur global unique
6. **Config client générée depuis un Access** (référence son Service pour host/port)

### `store.Account` simplifié (cible)

```go
type Account struct {
    ID        string
    Username  string
    Password  string    // pour l'API admin ou l'auth générique
    OfferID   string    // template utilisé à la création
    Token     string    // token API admin
    Enabled   bool
    CreatedAt time.Time
    UpdatedAt time.Time
    // Grants supprimés progressivement après migration
    // ExpiresAt / QuotaBytes / MaxConnections / MaxIPs → migrés vers Access
}
```

---

## 19. Proposition de nouvelle organisation des menus

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

### Menu [3] SERVICES

```
╔══════════════════════════════════════════════╗
║              GESTION DES SERVICES            ║
╠══════════════════════════════════════════════╣
║  Services enregistrés :                      ║
║   ● Xray-Production  xray  :443  [ACTIF]     ║
║   ○ SSH-Backup       ssh   :22   [inactif]   ║
║   ● WG-51820         wg    :51820[ACTIF]     ║
╠══════════════════════════════════════════════╣
║  [N] Nouveau service                         ║
║  [numéro] Gérer ce service                   ║
║  [0] Retour                                  ║
╚══════════════════════════════════════════════╝
```

Sous-menu service :
```
  [A] Activer / Démarrer
  [D] Désactiver / Arrêter
  [E] Éditer (port, domaine, profil lié)
  [U] Voir les abonnés de ce service
  [C] Générer config client (par abonné)
  [X] Retour
```

### Menu [5] ACCÈS

```
╔══════════════════════════════════════════════╗
║           GESTION DES ACCÈS ABONNÉS          ║
╠══════════════════════════════════════════════╣
║  Abonné : [chercher par nom/ID]              ║
╠══════════════════════════════════════════════╣
║  Accès de user_alice :                       ║
║   ● Xray-Production   expire: 2026-12-31     ║
║   ● WG-51820          illimité               ║
║   ○ SSH-Backup        [désactivé]            ║
╠══════════════════════════════════════════════╣
║  [A] Ajouter un accès (choisir service)      ║
║  [R] Révoquer un accès                       ║
║  [Q] Modifier quota / expiration par accès   ║
║  [C] Générer config client                   ║
║  [0] Retour                                  ║
╚══════════════════════════════════════════════╝
```

### Menu [4] ABONNÉS (simplifié)

```
╔══════════════════════════════════════════════╗
║             GESTION DES ABONNÉS              ║
╠══════════════════════════════════════════════╣
║  [1] Lister les abonnés                      ║
║  [2] Créer un abonné                         ║
║  [3] Modifier un abonné                      ║
║  [4] Voir les accès d'un abonné  →  [5]      ║
║  [5] Supprimer un abonné                     ║
║  [0] Retour                                  ║
╚══════════════════════════════════════════════╝
```

---

## 20. Proposition des écrans

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
║  ACCÈS : alice → Xray-Production                     ║
╠══════════════════════════════════════════════════════╣
║  Abonné    : alice (ID: acc_001)                     ║
║  Service   : Xray-Production (xray :443)             ║
║  UUID      : xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx    ║
║  Quota     : 10 GB (3.2 GB utilisés)                 ║
║  Expire    : 2026-12-31                              ║
║  Sessions  : 3 max / 1 active                        ║
║  IPs max   : 2                                       ║
║  Statut    : ● ACTIF                                 ║
╠══════════════════════════════════════════════════════╣
║  [C] Config client    [R] Révoquer    [Q] Quota      ║
║  [E] Activer/désactiver               [X] Retour     ║
╚══════════════════════════════════════════════════════╝
```

### Écran Abonné — vue d'ensemble

```
╔══════════════════════════════════════════════════════╗
║  ABONNÉ : alice (acc_001)                            ║
╠══════════════════════════════════════════════════════╣
║  Username  : alice                                   ║
║  Offre     : Standard-30j                            ║
║  Statut    : ● ACTIF                                 ║
╠══════════════════════════════════════════════════════╣
║  Accès actifs :                                      ║
║   ● Xray-Production   expire: 2026-12-31  10GB       ║
║   ● WG-51820          illimité                       ║
║   ○ SSH-Backup        [désactivé]                    ║
╠══════════════════════════════════════════════════════╣
║  [A] Gérer les accès  [E] Éditer  [X] Supprimer      ║
╚══════════════════════════════════════════════════════╝
```

---

## 21. Flux complet de création d'un abonné

Dans le modèle cible, la création d'un abonné est distincte de l'attribution d'un accès.

```
Opérateur saisit : username, mot de passe (ou généré), offre
         ↓
store.Account créé avec : ID, Username, Password, OfferID, Enabled=true
         ↓
Aucun secret, aucun accès, aucun quota attribué à ce stade
         ↓
L'abonné est visible dans [4] ABONNÉS avec 0 accès
         ↓
→ Aller dans [5] ACCÈS pour attribuer un service
```

Comparaison avec le modèle actuel :
- Actuellement : création + attachement moteur peuvent se faire ensemble
- Cible : séparation claire des deux opérations (abonné d'abord, accès ensuite)

---

## 22. Flux d'attribution d'un service à un abonné

```
Opérateur sélectionne : abonné + service cible
         ↓
Saisit les limites spécifiques à cet accès :
  - quota_bytes (0 = illimité ou hérité de l'offre)
  - expires_at (ou hérité de l'offre)
  - max_connections, max_ips
         ↓
service.Access créé avec :
  AccountID, ServiceID, Engine (dénormalisé)
  Secrets : vide à ce stade
  Les limites saisies
  Enabled = true
         ↓
service.EnsureAccessSecrets(access) génère les secrets manquants
  (UUID pour xray, clés Ed25519 pour SSH/DNS, adresse WireGuard…)
  → Idempotent : ne touche pas les secrets déjà présents
         ↓
access.Save() → persiste dans $LABOSURF_DATA_DIR/access/<id>.json
         ↓
L'accès est visible dans [5] ACCÈS sous l'abonné
```

---

## 23. Flux de génération des identifiants spécifiques au service

Les identifiants (UUID, clés, adresses) sont générés **une seule fois** à la création de l'accès,
puis réutilisés à l'identique à chaque regénération de config.

```
service.EnsureAccessSecrets(access *Access) :
         ↓
switch access.Engine :
  "xray"       → si Secrets["uuid"] absent  : générer UUID v4
  "ssh"        → si Secrets["public_key"] absent : générer paire Ed25519 (hex)
  "dnstt"      → si Secrets["public_key"] absent : générer paire Ed25519 (hex)
  "slowdns"    → si Secrets["public_key"] absent : générer paire Ed25519 (hex)
  "wireguard"  → si Secrets["private_key"] absent : générer paire X25519 (base64)
                 si Secrets["address"] absent     : allouer adresse unique 10.66.0.N/32
  "tuic"       → si Secrets["uuid"] absent       : générer UUID v4
                 si Secrets["password"] absent    : générer token 12 chars
  "hysteria"   → si Secrets["password"] absent   : générer token 12 chars
         ↓
access.Save() → persiste les nouveaux secrets
```

Points critiques :
- L'adresse WireGuard doit être unique parmi **tous** les accès WireGuard existants
- Les clés REALITY Xray ne sont **jamais** dans `Access.Secrets` — elles sont sur le serveur
- Un secret déjà présent ne doit jamais être écrasé (idempotence stricte)

---

## 24. Flux de génération des configurations client

Avec le modèle cible, `Generate` reçoit un `Access` et son `Service` :

```
clientcfg.GenerateFromAccess(access service.Access, svc service.Service) :
         ↓
host, port = svc.Host, svc.Ports["listen"]
secrets    = access.Secrets
         ↓
switch access.Engine :
  "xray" :
    uuid = secrets["uuid"]
    keys = xray.LoadRealityKeys()  ← sur le serveur, hors Access
    → vless://uuid@host:port?security=reality&pbk=<clé_publique_serveur>&...
         ↓
  "ssh" :
    → "ssh username@host -p port"
    (la clé privée est dans Access.Secrets["private_key"] → fichier client)
         ↓
  "wireguard" :
    address  = secrets["address"]
    priv_key = secrets["private_key"]
    → fichier .conf WireGuard complet
         ↓
  "slowdns" / "dnstt" :
    domain  = svc.Domains[0]
    pub_key = secrets["public_key"]
    → "<engine>://username@domain?key=pub_key"
         ↓
  (autres moteurs : même logique)
         ↓
ClientResult{Engine, ClientLink, ServerConfig}
```

Config serveur groupée (tous les abonnés d'un service) :
```
buildGroupedConfigFromService(svc service.Service, accesses []Access) :
  Regroupe tous les Access du service
  Construit le JSON serveur avec tous les clients/utilisateurs
  → Passé à svc.Engine.Configure() via ApplyServiceConfig()
```

---

## 25. Plan de migration

### Principe : aucune rupture sur les moteurs, profils V2 et comptes existants

**Fichiers non touchés :**
- `engine.Engine` interface et implémentations dans `engines/`
- `internal/profile/` (V2 — 17 tests passants, conservé tel quel)
- `internal/engineutil/` (ValidateHybrid, EvaluateChain, CanConnect, CompositeEngine…)

### Étape 1 — Créer `internal/service/` (aucune modification de l'existant)

Créer les types `Service` et `Access`, le store CRUD, les tests unitaires.
- `$LABOSURF_DATA_DIR/services/<id>.json`
- `$LABOSURF_DATA_DIR/access/<id>.json`
- Tests : CRUD Service, CRUD Access, EnsureAccessSecrets, unicité adresse WireGuard

### Étape 2 — Ajouter `service.EnsureAccessSecrets`

Analogue de `store.EnsureEngineSecrets`. Même logique, même idempotence.
Opère sur `Access.Secrets` au lieu de `Account.Grants[engine].Config`.

### Étape 3 — Fonction de migration `MigrateGrantsToAccess`

```go
// Lit les Grants d'un compte et crée des Access correspondants
// si un Service matching existe. Idempotent.
// Ne supprime PAS les Grants existants.
func MigrateGrantsToAccess(s *store.Store, svcStore *service.Store) error
```

Migration à chaud, compte par compte, sans downtime.
Les Grants restent actifs et opérationnels pendant toute la période de transition.

### Étape 4 — Ajouter `clientcfg.GenerateFromAccess`

Nouvelle surcharge `GenerateFromAccess(access service.Access, svc service.Service)`.
L'ancienne signature `Generate(acc, engineName, prof)` reste disponible pendant la transition.

### Étape 5 — Ajouter les menus [3] SERVICES et [5] ACCÈS

En parallèle des menus existants (pas de remplacement immédiat).
Le menu [4] ABONNÉS existant reste fonctionnel en lecture pendant la transition.

### Étape 6 — Dépréciation progressive de `Account.Grants`

Une fois tous les Access créés et les menus migrés :
1. Marquer `Grants` comme deprecated dans le code
2. Les opérations de génération de config lisent l'Access en priorité, Grants en fallback
3. Suppression définitive du champ `Grants` uniquement après validation complète en production

---

## 26. Risques de régression

| Risque | Niveau | Mitigation |
|--------|--------|-----------|
| Régénération des secrets existants (UUID, clés) | **CRITIQUE** | `EnsureAccessSecrets` vérifie avant de générer ; migration copie les valeurs existantes |
| Clés REALITY Xray perdues ou recréées | **CRITIQUE** | Clés dans `engines/xray/` hors store — migration ne les touche pas |
| Adresses WireGuard dupliquées à la migration | **ÉLEVÉ** | Copier `Grants["wireguard"].Config["address"]` vers `Access.Secrets`, pas de réallocation |
| Comptes existants sans Service correspondant | **MOYEN** | Migration idempotente — les Grants restent actifs si pas de Service créé |
| Tests V2 `internal/profile/` cassés | **FAIBLE** | `internal/profile/` non modifié ; 17 tests doivent rester verts |
| Confusion port `srvcfg.Profile` → `service.Service` | **MOYEN** | La migration crée un Service par moteur actif avec les ports issus de srvcfg |
| Style visuel des menus altéré | **FAIBLE** | Nouveaux menus construits avec les mêmes helpers d'affichage existants |
| Config serveur incomplète (abonnés manquants) | **ÉLEVÉ** | `buildGroupedConfigFromService` doit lire tous les Access du service, pas seulement les actifs |
| Hybrid grants non migrés correctement | **MOYEN** | Le grant hybride (`"dnstt-xray"`) doit mapper vers un Access du service hybride composite |

---

## 27. Fonctionnalités qui ne doivent absolument pas être supprimées

1. **Génération lien VLESS avec vraie clé REALITY** — erreur explicite si moteur non installé (pas de placeholder)
2. **`EnsureEngineSecrets` / `EnsureAccessSecrets` idempotents** — ne jamais écraser les secrets existants
3. **Adresses WireGuard uniques** — unicité globale 10.66.0.2–254, allocation séquentielle
4. **`buildGroupedConfig` / `buildGroupedConfigFromService`** — config serveur complète pour tous les abonnés d'un moteur
5. **`ApplyServerConfig` / `ApplyServiceConfig`** — régénération et application complète via `e.Configure()`
6. **`aliasGrantForComponent`** — accès aux secrets hybrides depuis le grant du moteur composite
7. **`buildComponentConfigs`** — config serveur composant par composant pour les hybrides
8. **`CompositeEngine`**, **`RegisterHybridPersist`**, **`RemoveHybridPersist`** — pipeline hybride complet
9. **17 tests `internal/profile/`** — doivent rester verts sans modification
10. **Jitter anti-DPI DNS** (`jitter_ms: 40`) — paramètre de sécurité réseau légitime
11. **Style visuel LABOSURF_PRO** — bordures `═`, bullets `●/○`, couleurs ANSI, numérotation
12. **Exclusivité 1 profil actif par moteur** — `DeactivateAllForEngine` et sa garantie d'unicité
13. **Vérification des dépendants avant suppression** — `Dependents()` avant `Delete()` pour les profils/moteurs

---

## 28. Questions ouvertes / décisions à prendre

### Q1 — Source de vérité pendant la cohabitation Grants / Access

Pendant la période de transition, quelle est la source de vérité pour la génération de config client ?
- Option A : lire `Access` en priorité, tomber sur `Grants` en fallback
- Option B : interdire la génération si `Access` n'existe pas encore (force la migration)

### Q2 — Création automatique ou manuelle des Services à la migration

À la migration, doit-on :
- Option A : créer automatiquement un `Service` par moteur actif (depuis `srvcfg.Profile`)
- Option B : exiger que l'opérateur crée les Services manuellement avant de migrer les comptes

### Q3 — Typage de `Access.Secrets`

Garder `map[string]any` (comme `EngineGrant.Config`) pour la flexibilité par moteur,
ou définir des sous-types typés par moteur (`XraySecrets`, `SSHSecrets`…) ?
- `map[string]any` : flexible, sérialisation simple, mais erreurs silencieuses possibles
- Types nommés : sécurité à la compilation, mais complexifie la sérialisation JSON

### Q4 — Quota global vs quota par service

Le système actuel a un quota global par compte.
La proposition met le quota dans l'`Access` (par service).
Faut-il aussi conserver un quota global de compte comme plafond supérieur ?
- Option A : quota uniquement dans l'Access (par service)
- Option B : quota dans l'Access + plafond global dans le Account

### Q5 — Liaison Service ↔ EngineProfile

Si on change le profil V2 actif d'un moteur, le Service suit-il automatiquement ?
- Option A : le Service référence le profil au moment de sa création et ne change pas
- Option B : le Service suit toujours le profil actif du moteur (référence dynamique)

### Q6 — Numérotation des menus

Le menu central actuel a les options [1] à [5] (selon la version installée).
L'ajout de [3] SERVICES et [5] ACCÈS décale les numéros existants.
- Option A : renuméroter complètement (rupture des habitudes)
- Option B : intercaler les nouvelles options aux numéros [3] et [5], décaler les suivants

### Q7 — Représentation des hybrides dans le modèle cible

Les Grants hybrides utilisent le nom littéral (`"dnstt-xray"`). Dans le nouveau modèle :
- Option A : un seul `Service` hybride (Engine=`"dnstt-xray"`)
- Option B : deux Services chaînés (Service transport + Service VPN liés)

La réponse conditionne la migration des Grants hybrides existants.

### Q8 — Sort de `srvcfg.Profile`

`srvcfg.Profile` est chargé dans de nombreux endroits (clientcfg, profile/validate.go, menus).
- Option A : maintenir `srvcfg.Profile` pendant toute la période de transition (fallback)
- Option B : remplacer immédiatement par `service.Service` dès la création du package

---

*Fin de l'analyse — aucune modification de code effectuée.*
*Basé sur l'observation du dépôt LABOSURF_PRO, commit `7c9c824`, branche `main`, date 2026-09-14.*
