# M4 — Interface terminal Services / Abonnés / Accès

**Date :** 2026-09-14
**Statut :** IMPLÉMENTÉ — build/vet/tests OK (Linux), non committé
**Branche :** main

---

## 1. Audit UI avant M4

Audit complet de `cmd/labosurf/` (16 fichiers) mené avant tout codage (Phase 0).

**Résultats clés :**
- Menu central : `runCentralMenu()` (`menu.go:42-87`), boucle `for` + `switch` textuel sur `promptLine(...)`. Aucune struct de menu déclarative.
- Sous-menus : chaque écran est une fonction `run<X>Menu()` avec sa propre boucle et son propre `switch`. Modèle le plus proche pour Service/Access : `menu_profiles.go` (liste numérotée + détail par lettre `[A]/[D]/[X]/...`) et la section "Gestion des utilisateurs" de `menu.go` (CRUD compte + génération config client).
- Helpers réutilisables identifiés : `promptLine`, `pauseMenu`, `clearScreen`, `printCentralHeader`, `orDim`, `wrapLine`, `green/red/cyan/yellow/dim`, `openStore()`. Pas de helper de confirmation dédié (pattern répété `"... (o/N)"` + `strings.EqualFold(...,"o")`), pas de helper de saisie numérique dédié (`fmt.Sscanf` inline partout).
- Symboles/couleurs : `●`/`○` (actif/inactif), `green`=action positive, `red`=danger, `cyan`=neutre, `yellow`=avertissement, `dim`=désactivé/séparateur.
- Moteurs : `engine.Names()`, `engine.Get(name).Status()` → `{Installed, Running, PID, Port}`. Hybrides déjà enregistrables via `runHybridCreateMenu()` (moteur composite `engineutil.CompositeEngine`).
- Persistance : `internal/service/store.go` a déjà toute l'API CRUD Service/Access nécessaire (M1), + `EnsureAccessSecrets` (M2), + `MigrateGrantsToAccess`/`ApplyServerConfigFromAccess` (M3). **Aucune nouvelle logique métier n'était nécessaire**, sauf un export mineur (voir §16).
- Dashboard : `printSystemPanel()` (`sysinfo.go:300`), déjà affiché sous le header à chaque rafraîchissement du menu central.

**Proposition validée avant codage :** ajouter deux entrées `6` (SERVICES) et `7` (ACCÈS) au menu central existant, sans renuméroter les entrées `1-5/9/0`, en répliquant fidèlement le style `menu_profiles.go` (liste/détail) et la section Utilisateurs (CRUD + génération client).

---

## 2. Menu avant / après

**Avant :**
```
[1] GESTION DES MOTEURS
[2] GESTION DES UTILISATEURS
[3] ÉTAT GLOBAL
[4] PROFIL SERVEUR
[5] PROFILS NOMMÉS
[9] À PROPOS
[0] QUITTER
```

**Après (ajout uniquement, rien renuméroté) :**
```
[1] GESTION DES MOTEURS
[2] GESTION DES UTILISATEURS
[3] ÉTAT GLOBAL
[4] PROFIL SERVEUR
[5] PROFILS NOMMÉS
[6] SERVICES            ← nouveau (M4)
[7] ACCÈS               ← nouveau (M4)
[9] À PROPOS
[0] QUITTER
```

Aucune entrée existante déplacée ou renommée.

---

## 3. Fichiers créés

| Fichier | Rôle |
|---|---|
| `cmd/labosurf/menu_services.go` | Menu SERVICES complet (liste, création simple/hybride, détail, édition, activation, suppression protégée, application config serveur). |
| `cmd/labosurf/menu_access.go` | Menu ACCÈS complet (liste globale, création, détail, quota, limites, expiration, révocation, suppression, génération config client, migration Grants→Access). |
| `cmd/labosurf/quota.go` | Conversion Go↔octets et transition de quota — **seule logique métier réellement nouvelle** introduite par M4. |
| `cmd/labosurf/quota_test.go` | 15 tests couvrant `parseQuotaGB`, `applyQuotaChoice`, `formatQuotaBytes`. |

## 4. Fichiers modifiés

| Fichier | Modification |
|---|---|
| `cmd/labosurf/menu.go` | 2 lignes ajoutées au menu central (`case "6"`, `case "7"`) + 2 entrées d'affichage. Aucune ligne existante supprimée. |
| `cmd/labosurf/sysinfo.go` | Ajout de `serviceAccessSummary()` + une ligne dans `printSystemPanel()` affichant Services/Accès actifs. Import `labosurf/internal/service` ajouté. Format des blocs existants inchangé. |
| `internal/service/store.go` | Ajout de `ListAllAccess() ([]Access, error)` — wrapper exporté de 3 lignes sur `listAllAccess()` déjà existant, nécessaire pour l'écran "lister tous les accès" (aucune fonction publique de ce type n'existait avant M4). |

---

## 5. Gestion des Services (`menu_services.go`)

**Écran liste** (`runServiceMenu`) : reprend le modèle `printProfileList` — bullet coloré, nom, type (simple/hybride), moteur(s), statut.

**Création** (`serviceCreateMenu(hybrid bool)`) :
1. Liste uniquement les moteurs **démarrés** (`Status().Running`) — "activé" au sens de la mission, distinct de "installé" (`Status().Installed`). Filtrés par présence/absence de `-` dans le nom selon simple/hybride.
2. Pour un hybride, les composants sont lus directement depuis `engineutil.CompositeEngine.Components` (type assertion), jamais recalculés par un split de chaîne — aucune combinaison inventée.
3. Champs demandés : nom, hôte (pré-rempli depuis `srvcfg.Load().Host`), port (pré-rempli depuis `prof.Port(engineName)`), domaine (uniquement si le moteur ou un composant est `dnstt`/`slowdns` — `needsDomain()`).
4. Lien optionnel vers un profil nommé existant compatible (`promptOptionalProfile`) — réutilise `internal/profile` sans dupliquer sa validation.
5. `service.NewService` + `service.SaveService` (validation déjà faite par `ValidateService`).

**Détail** (`serviceDetailMenu`) : ID, type, moteur/composants, profil lié, hôte, port, domaines, statut, date de création, **liste des Access associés** (abonné, statut, expiration, quota, devices, connexions — via `service.ListAccessByService`). Actions `[A]/[D]/[E]/[G]/[X]/[0]`.

**Suppression** (`serviceDeleteConfirm`) : vérifie `ListAccessByService` avant d'agir et affiche un message explicite si des Access dépendent encore du service ; l'appel à `service.DeleteService` porte de toute façon la garde définitive (`ErrServiceHasAccess`). Aucune suppression en cascade — jamais de suppression silencieuse d'Access liés.

**Application config serveur** (`serviceApplyServerConfig`) : appelle directement `clientcfg.ApplyServerConfigFromAccess(ctx, *s, accesses, prof)` — aucune logique de génération dupliquée dans l'écran.

---

## 6. Gestion des Accès (`menu_access.go`)

**Écran liste** (`runAccessMenu`) : liste globale (`service.ListAllAccess`, nouvelle fonction §16), triée par abonné puis service, avec résolution du nom de service (`service.GetService`, mise en cache locale).

**Création** (`accessCreateMenu`) — workflow conforme à la mission §8 :
1. Choisir l'abonné (`store.LoadStore` → `ListAccounts()`).
2. Choisir un **service actif** (`svc.Enabled`) parmi `service.ListServices()`.
3. Vérifier l'absence de doublon (`service.AccessExists`).
4. Politique : quota (illimité/limité, voir §7), MaxDevices, MaxConnections, MaxSourceIPs (voir §8), expiration en jours.
5. Activer immédiatement ou non.
6. `service.EnsureAccessSecrets(&a, nil)` — génère les secrets manquants **sans jamais demander à l'opérateur un UUID, une clé ou un mot de passe** (mission §9 respectée : ces valeurs ne sont jamais des champs de saisie).
7. `service.SaveAccess(&a)`.

**Détail** (`accessDetailMenu`) : ID, abonné, service+moteur, statut, quota (utilisé/limite), MaxDevices, MaxConnections, MaxSourceIPs, expiration, date de création. Actions `[A]/[D]/[R]/[Q]/[L]/[C]/[X]/[0]`.

**Renouvellement** (`accessRenew`) : modifie **uniquement** `ExpiresAt` — jamais les secrets, jamais l'UUID, jamais le mot de passe, jamais le ServiceID (mission §15 respectée).

**Génération config client** (`accessGenerateClientConfig`) : appelle directement `clientcfg.GenerateFromAccess(*a, svc, username)` — aucune logique de génération dupliquée. Le nom d'utilisateur affiché dans les liens provient de `store.Account.Username` (fallback sur `AccountID`).

**Migration** (`accessMigrateMenu`) : appelle `service.MigrateGrantsToAccess` **uniquement après confirmation explicite** (`"o/N"`) — jamais automatique au démarrage. Affiche le détail des échecs (`service.MigrateFailed`) sans les masquer.

---

## 7. Quota

`cmd/labosurf/quota.go` isole toute la logique de conversion :

- `parseQuotaGB(input string) (uint64, error)` : convertit une saisie en Go (décimal accepté) vers des octets. Refuse explicitement : chaîne vide, valeur non numérique, valeur ≤ 0 (avec message orientant vers le mode illimité), valeur dépassant la capacité d'un `uint64`.
- `applyQuotaChoice(a *service.Access, choice, gbInput string) error` : logique pure (aucun I/O) appliquant le choix `"1"` (illimité) ou `"2"` (limité) à un `*service.Access`. Utilisée à l'identique par `accessCreateMenu` et `accessEditQuota` — **une seule implémentation**, pas de duplication entre création et édition.
- `formatQuotaBytes(uint64) string` : affichage lisible en Go.

Voir §17 pour les tests couvrant explicitement les transitions limité↔illimité.

---

## 8. Appareils / Connexions / IP sources

Les trois notions ne sont **jamais confondues** dans l'UI (conformément à la correction architecturale M3) :

| Champ UI | Champ `Access` | Signification |
|---|---|---|
| MaxDevices | `MaxDevices` | Appareils physiques distincts autorisés. |
| MaxConnections | `MaxConnections` | Connexions simultanées, toutes confondues. |
| MaxSourceIPs | `MaxSourceIPs` | Adresses IP sources distinctes simultanées (contrainte réseau, héritée de l'ancien `Account.MaxIPs`). |

Chaque champ est saisi et affiché séparément (`accessEditLimits`), avec sa propre étiquette explicite. **Aucun mécanisme de tracking réseau réel n'est ajouté** — ce sont des limites déclaratives stockées sur l'`Access`, au même titre que dans le modèle M1/M3 ; leur application effective reste du ressort des moteurs (hors périmètre M4).

---

## 9. Expiration

`accessRenew` modifie `ExpiresAt` via `store.ExpiryFromDays(days)` (même fonction que pour les comptes, cohérence garantie). Une valeur vide remet l'accès à "aucune expiration". Aucun secret n'est touché par cette action.

---

## 10. Secrets

Aucun écran ne demande à l'opérateur un UUID, une clé publique/privée ou un mot de passe. `EnsureAccessSecrets` est appelé automatiquement à la création (`accessCreateMenu`) et gère l'idempotence en interne (M2) — jamais rappelé lors d'un renouvellement ou d'une modification de limites/quota.

---

## 11. Configuration client

`accessGenerateClientConfig` appelle `clientcfg.GenerateFromAccess` telle quelle. Affichage : abonné, service (+moteur), hôte, puis le lien/config (formaté avec `wrapLine`, comme pour l'ancien écran `menuUserClientConfig`). Aucune information sensible n'est écrite dans un log — uniquement affichée à l'écran, exactement comme le comportement existant pour `clientcfg.Generate`.

---

## 12. Hybrides

Un service hybride est affiché comme **un seul Service** avec `Type: hybride` et `Composants: A → B` — jamais comme deux services indépendants (`printServiceList`, `serviceDetailMenu`). Un Access vers cet hybride est un Access unique (`service.NewAccess(accountID, serviceID, svc.Engine)` où `svc.Engine` est le nom hybride complet, ex. `"dnstt-xray"`). La génération de configuration client passe par `GenerateFromAccess`, qui gère nativement les hybrides (M2) sans logique supplémentaire ici.

---

## 13. Migration

Action `[M]` dans le menu ACCÈS (`accessMigrateMenu`), jamais automatique, toujours confirmée. Réutilise `service.MigrateGrantsToAccess` (M3) et `service.MigrateStats` tels quels. Affiche créés/ignorés/échecs, et le détail de chaque échec sans les masquer.

---

## 14. Compatibilité — aucune régression

Rien n'a été supprimé ni modifié dans le comportement existant :

- `runUsersMenu`, `menuUserCreate/List/GrantEngine/Renew/Toggle/Delete/ClientConfig` : **inchangés**.
- `clientcfg.Generate`, `clientcfg.ApplyServerConfig` (anciennes API M0) : **inchangées**, toujours utilisées par le menu Utilisateurs existant.
- `store.Account`, `store.EngineGrant` : **inchangés**.
- `runEngineMenu`, `runHybridCreateMenu/RemoveMenu`, tous les `menu_<engine>_config.go` : **inchangés**.
- `runProfileMenu` et tout `menu_profiles.go` : **inchangés** (réutilisé en lecture pour `promptOptionalProfile`).
- Licence, maintenance, mise à jour, profil serveur : **inchangés**.
- Dashboard : une seule ligne ajoutée, format des blocs SYSTÈME/RÉSEAU/MOTEURS strictement identique.

---

## 15. Tests

### 15.1 `cmd/labosurf/quota_test.go` (15 tests, tous PASS)

| Test | Vérifie |
|---|---|
| `TestParseQuotaGBOneGB` | 1 Go → 1073741824 octets |
| `TestParseQuotaGBFractional` | 1.5 Go → conversion décimale correcte |
| `TestParseQuotaGBInvalidValue` | "abc" → erreur |
| `TestParseQuotaGBEmpty` | "" et espaces → erreur |
| `TestParseQuotaGBZero` | "0" → erreur explicite (utiliser illimité) |
| `TestParseQuotaGBNegative` | "-5" → erreur |
| `TestParseQuotaGBTooLarge` | valeur dépassant uint64 → erreur, pas d'overflow silencieux |
| `TestApplyQuotaChoiceUnlimited` | choix "1" → QuotaUnlimited=true, QuotaLimitBytes=0 |
| `TestApplyQuotaChoiceLimited` | choix "2"+"10" → QuotaUnlimited=false, 10 Go en octets |
| `TestApplyQuotaChoiceTransitionLimitedToUnlimited` | passage limité→illimité (mission §16) |
| `TestApplyQuotaChoiceTransitionUnlimitedToLimited` | passage illimité→limité (mission §16) |
| `TestApplyQuotaChoiceLimitedInvalidValue` | valeur Go invalide en mode limité → erreur |
| `TestApplyQuotaChoiceInvalidChoice` | choix ni "1" ni "2" → erreur |
| `TestFormatQuotaBytes` | affichage lisible |
| `TestFormatQuotaBytesZero` | affichage de 0 |

### 15.2 Couverture des autres points de la mission (§20)

La quasi-totalité de la logique métier listée (création Service, création Access, quota, MaxDevices, MaxConnections, MaxSourceIPs, expiration, activation, désactivation, suppression protégée, génération secrets, génération client, hybride) est **déjà couverte par les 92 tests de `internal/service` et les tests de `internal/clientcfg`** (M1-M3) : les écrans M4 ne font qu'appeler ces fonctions telles quelles, sans réimplémenter leur logique. Ajouter des tests UI dupliquant ces vérifications via de faux flux stdin aurait testé la plomberie d'E/S, pas la logique métier — la mission demande explicitement d'éviter les tests superficiels. Le renouvellement (`accessRenew`) et la modification de limites (`accessEditLimits`) sont des affectations de champs directes déjà couvertes indirectement par `ValidateAccess` (testé en M1).

**Écart assumé :** aucun test automatisé n'exerce les fonctions d'écran elles-mêmes (`accessCreateMenu`, `serviceDetailMenu`, etc.), car elles lisent `os.Stdin` directement (`menuReader` global) sans point d'injection — cohérent avec l'absence totale de tests de ce type dans le reste de `cmd/labosurf` (seuls `quota.go` et le pré-existant `menu_nilctx_test.go`/`license_cli_test.go` ont des tests, et ces derniers ne testent pas non plus les flux interactifs complets).

---

## 16. Ajout API mineur

`internal/service/store.go` — `ListAllAccess() ([]Access, error)` : wrapper exporté de la fonction interne `listAllAccess()` déjà existante depuis M1. Nécessaire car aucune fonction publique ne permettait de lister tous les Access indépendamment d'un compte ou d'un service (seules `ListAccessByAccount`/`ListAccessByService` existaient), et l'écran "ACCÈS → Lister" en a besoin pour une vue d'administration globale. Zéro changement de comportement, 3 lignes, purement additif.

---

## 17. Validation

Exécuté sous **WSL Ubuntu (Linux natif)**, car `go build`/`go vet`/`go test` natif Windows échouent sur ce dépôt pour une raison **pré-existante et sans rapport avec M4** (voir §18).

```
$ go build ./...     → OK (0 erreur)
$ go vet ./...       → OK (0 avertissement)
$ go test ./...      → OK, toutes les suites PASS :
    labosurf/cmd/labosurf              ok
    labosurf/engines/dnstt             ok
    labosurf/engines/freewaygate       ok
    labosurf/engines/hysteria          ok
    labosurf/engines/hysteria2         ok
    labosurf/engines/slowdns           ok
    labosurf/engines/ssh               ok
    labosurf/engines/tuic              ok
    labosurf/engines/wireguard         ok
    labosurf/engines/xray              ok
    labosurf/internal/clientcfg        ok
    labosurf/internal/engcfg           ok
    labosurf/internal/engine           ok
    labosurf/internal/engineutil       ok
    labosurf/internal/license          ok
    labosurf/internal/profile          ok
    labosurf/internal/secret           ok
    labosurf/internal/service          ok
    labosurf/internal/srvcfg           ok
    labosurf/internal/store            ok
```

`engines/udp` possède son propre module Go (hors de l'arborescence de modules principale) et n'est pas couvert par `go test ./...` à la racine — comportement pré-existant, sans rapport avec M4.

---

## 18. Problème pré-existant confirmé (Windows)

```
engines/ssh/server.go:29,42,70   : undefined: syscall.Credential
engines/freewaygate/engine.go:206: undefined: syscall.Kill
```

`syscall.Credential` et `syscall.Kill` sont des API **Linux uniquement**. Ce problème existait avant M1/M2/M3/M4 (déjà documenté dans le rapport M2) et bloque toute compilation native de `cmd/labosurf` et `internal/clientcfg` (tests) sous Windows. Confirmé via `git status` : ces fichiers ne sont touchés par aucune session M1-M4.

**Contournement utilisé pour valider M4 malgré cette limitation :**
1. `GOOS=linux GOARCH=amd64 go build ./cmd/labosurf/` (cross-compilation, valide la syntaxe et les types) → OK.
2. `GOOS=linux GOARCH=amd64 go vet ./cmd/labosurf/` → OK.
3. Exécution réelle de `go build/vet/test ./...` sous **WSL Ubuntu** (Go natif Linux déjà installé sur la machine) → tout PASS (voir §17).

---

## 19. Limites connues

- **Pas de renumérotation intelligente automatique** : les entrées `6`/`7` ont été ajoutées après `5` par choix éditorial (ordre logique MOTEUR→PROFIL→SERVICE→ACCÈS) ; l'opérateur peut préférer un autre emplacement, trivial à ajuster (2 lignes dans `menu.go`).
- **Édition de Service limitée** : `serviceEditMenu` ne permet de modifier que hôte/port/domaine — pas de changement de moteur ni de composants hybrides après création (cohérent avec le modèle M1 : `Engine`/`Components` ne sont pas conçus pour changer après coup sans casser les Access existants).
- **Pas de renommage de Service/Access** : le nom du service n'est pas modifiable depuis `serviceEditMenu` (uniquement à la création). Ajout trivial si souhaité.
- **`MaxDevices`/`MaxSourceIPs` restent déclaratifs** : comme en M3, aucun mécanisme d'application réseau réelle n'a été ajouté — ce sont des limites stockées, à appliquer par les moteurs eux-mêmes (hors périmètre UI).
- **Pas de test automatisé des écrans interactifs** eux-mêmes (voir §15.2) — limitation structurelle du style d'E/S de `cmd/labosurf`, pas spécifique à M4.
- **`ListAllAccess` charge tout en mémoire** : acceptable à l'échelle actuelle (fichiers JSON un par Access), mais ne scalera pas indéfiniment — non bloquant pour cette mission.

---

## 20. Prochaines étapes (hors périmètre M4)

Aucune — la mission M4 s'arrête ici. Pas de M5 entamée, pas de refactor supplémentaire, aucun changement de style visuel.

---

## 21. Contrôle final M4

Revue ciblée post-implémentation, avant publication. Deux points inspectés en profondeur.

### 21.1 Moteur activé ≠ moteur démarré (révisé — voir §22)

Inspection de `internal/engine`, `internal/engineutil` et `internal/engcfg` : **aucune notion `Enabled`/`Activated` distincte n'existe pour les moteurs**, nulle part dans le projet — recherche élargie ensuite (§22) à l'ensemble du dépôt, sans trouver davantage. Le seul état disponible est `engine.EngineStatus{Installed, Running, PID, Port}` (`internal/engine/engine.go:57`). Le concept `profile.Status` (Active/Inactive) est attaché à un *profil*, pas à un *moteur*. `store.Account.Enabled`, `store.EngineGrant.Enabled`, `profile.Activate()/Deactivate()` et `license.Activate` existent, mais aucun n'est un état de moteur.

Convention déjà en place dans l'UI existante (`runEngineMenu`, `printSystemPanel`) : `●` vert = `Running`, `○` gris = `Installed && !Running`, `·` = ni l'un ni l'autre.

**Conclusion initiale (revue sur ce point à la §22) :** ce contrôle avait d'abord retenu `Status().Running` comme filtre de disponibilité, jugé cohérent avec le bullet vert déjà utilisé ailleurs. Un contrôle final ultérieur a identifié que ce choix violait une exigence explicite de la mission (« un moteur activé mais arrêté doit rester disponible ») — voir §22 pour la correction retenue.

### 21.2 Profils spécifiques aux moteurs — lien Service→Profil décoratif (corrigé)

Inspection de `internal/profile/apply.go`, `internal/service`, `internal/clientcfg` :

- `profile.BuildConfig(p, s)` est le **seul** point du projet qui lit les `Params` techniques d'un profil (`network`, `security`, `domain`, `backend`, `congestion_control`, `obfs`, `run_as_user`, etc. — voir `apply.go:94,168-169,203,249,295`). Il n'est appelé que par `profileActivate()` dans le menu **PROFILS NOMMÉS** (`[5]`), qui pousse ensuite le résultat via `engine.Configure(...)`.
- `clientcfg.ApplyServerConfigFromAccess` / `buildGroupedConfig` (M3, utilisés par `menu_services.go` → `[G]`) construisent la config serveur **exclusivement** depuis `Access.Secrets` + `srvcfg.Profile` (host/port/domaines). Ils ne lisent **jamais** `Service.ProfileID` ni `profile.Profile.Params`. Pour xray en particulier, `network`/`security` y sont **fixés en dur** à `tcp`/`reality` (`clientcfg.go`, fonction de config xray), quel que soit le profil éventuellement lié.

**Conséquence concrète :** le champ `Service.ProfileID`, ajouté par `promptOptionalProfile` lors de la création d'un Service, est une **référence sans effet fonctionnel** sur la configuration réellement appliquée par `[G]`. Si l'opérateur veut que les paramètres techniques d'un profil (transport, sécurité, domaine spécifique, etc.) soient réellement appliqués au moteur, il doit **activer ce profil séparément** via `[5]` — et il doit alors savoir qu'un `[G]` ultérieur dans SERVICES écrasera cette configuration transport avec le format fixe de `ApplyServerConfigFromAccess`.

Cette divergence (deux chemins de configuration distincts, `profileActivate` vs `ApplyServerConfig(FromAccess)`, qui s'écrasent mutuellement) est **pré-existante à M4** — elle existait déjà entre `profileActivate` et l'ancien `clientcfg.ApplyServerConfig` (menu Utilisateurs) avant même l'introduction de Service/Access. La corriger nécessiterait de modifier `internal/clientcfg` pour lui faire consulter `profile.Profile.Params`, ce qui est explicitement hors périmètre de ce contrôle (« NE PAS TOUCHER : clientcfg »).

**Correction appliquée (M4, UI uniquement, aucune nouvelle fonctionnalité) :**
- `cmd/labosurf/menu_services.go` — `promptOptionalProfile` : commentaire de fonction et texte affiché à l'écran mis à jour pour indiquer explicitement que le lien est une **référence uniquement**, sans application automatique des paramètres techniques, et pour orienter l'opérateur vers `[5]`.
- `cmd/labosurf/menu_services.go` — `serviceDetailMenu` : la ligne d'affichage `Profil : <id>` porte désormais la mention `(référence — activez-le via [5] pour appliquer ses paramètres)`.

Aucun champ fictif n'a été ajouté, aucune logique de `internal/service`/`internal/clientcfg`/`internal/profile` n'a été modifiée — uniquement des chaînes de caractères côté écran, pour que l'interface ne prétende jamais à une capacité que le système ne possède pas.

### 21.3 Validation post-correction (WSL Ubuntu, Linux natif)

```
go build ./...   → OK
go vet ./...     → OK
go test ./...    → OK, toutes les suites PASS (mêmes 20 packages qu'en §17,
                   y compris labosurf/cmd/labosurf et labosurf/internal/service)
```

### 21.4 Verdict (contrôle initial)

M4 jugé validé à ce stade, avec une correction de texte UI apportée à `menu_services.go` pour ne pas induire l'opérateur en erreur sur la portée du lien Service→Profil. Le point « moteur activé vs démarré » avait été laissé tel quel (filtre sur `Running`) — **corrigé ensuite, voir §22.**

### 21.5 Limite restante (non résolue, hors périmètre)

Le lien `Service.ProfileID` reste fonctionnellement décoratif tant que `internal/clientcfg` ne consulte pas `profile.Profile.Params`. Une future mission pourrait soit (a) faire lire ces `Params` par `ApplyServerConfigFromAccess`, soit (b) supprimer purement et simplement le choix de profil de la création de Service si son absence d'effet est jugée trop trompeuse même avec l'avertissement actuel. Aucune de ces deux options n'a été implémentée ici : la mission demandait de signaler le manque, pas de le combler par une modification de M3/clientcfg.

---

## 22. Dernière correction M4 — distinction Installed / Activated / Running

Contrôle ciblé demandé après §21, portant spécifiquement sur le filtre de disponibilité des moteurs pour la création de Service.

### 22.1 La distinction Installed / Activated / Running existe-t-elle déjà ?

**Non, seulement partiellement.** Recherche exhaustive (`grep` sur tout le dépôt, motifs `Enable|Activat|IsEnabled|EngineEnabled|ActiveEngine`) :

- `store.Account.Enabled` / `Store.SetEnabled` — état d'un **compte**, pas d'un moteur.
- `store.EngineGrant.Enabled` / `Store.SetGrantEnabled` — état d'un **grant** (compte↔moteur), pas du moteur lui-même.
- `profile.Profile.Activate()/Deactivate()` — état d'un **profil nommé**, pas du moteur qu'il configure.
- `license.Activate` — activation de **licence**, sans rapport.
- `engines/udp` (module séparé) : `adminSetEnabled` — active/désactive un **utilisateur UDP**, pas le moteur UDP lui-même.

**Aucun de ces états ne représente "ce moteur est activé".** `engine.Engine` (interface, `internal/engine/engine.go:141`) n'expose ni `Enable()`, ni `Disable()`, ni champ persistant équivalent. Seul `EngineStatus{Installed, Running, PID, Port}` existe (`internal/engine/engine.go:57`) : `Installed` (persistant, vrai dès que le binaire/config est déployé) et `Running` (transitoire, vrai seulement pendant que le processus tourne).

### 22.2 Correction appliquée

Le contrôle précédent (§21.1) avait retenu `Status().Running` comme proxy de "moteur activé". C'est incorrect au regard de la règle de cette mission :

> « Un moteur activé mais actuellement arrêté doit pouvoir être préparé comme Service. »

`Running` exclurait à tort un moteur installé et destiné à être utilisé, mais simplement arrêté (maintenance, pas encore démarré après installation). **Correction : `availableEnginesForService()` filtre désormais sur `Status().Installed`**, pas `Running` — c'est la meilleure représentation existante de "moteur activé" au sens de la mission :
- `Installed == false` → jamais proposé (conforme : « un moteur non installé ne doit jamais être proposé »).
- `Installed == true` (qu'il soit `Running` ou non) → proposé comme disponible pour créer un Service (conforme : un moteur installé-mais-arrêté reste utilisable).

**Aucun nouveau système n'a été créé** (pas de champ `Enabled` ajouté à `engine.Engine`, pas de nouvelle persistance) — conformément à l'instruction « si elle n'existe pas, ne crée pas un nouveau système complexe ». Le choix retenu réutilise un champ déjà existant et déjà exposé par toutes les implémentations de `engine.Engine`.

**Fichier modifié :** `cmd/labosurf/menu_services.go` uniquement.
- `availableEnginesForService()` : `Status().Running` → `Status().Installed`, avec un commentaire de fonction détaillant explicitement pourquoi (limite documentée, pas une évidence).
- `serviceCreateMenu()` : le message d'avertissement "Aucun moteur … activé. Démarrez-en un…" devient "Aucun moteur … installé. Installez-en un…" (cohérent avec le nouveau filtre).
- La liste des moteurs candidats affiche désormais explicitement l'état `● en cours` / `○ arrêté` à côté de chaque nom (aucune fonctionnalité nouvelle : affichage informatif utilisant `Status().Running`, déjà lu par ailleurs, pour que l'opérateur sache si un démarrage sera nécessaire avant `[G]`).

`runEngineMenu` et les assistants `menu_<engine>_config.go` n'ont **pas été touchés** — seule la fonction de filtrage propre à M4 a changé.

### 22.3 Validation

```
go build ./...   → OK
go vet ./...     → OK
go test ./...    → OK, toutes les suites PASS (mêmes packages qu'en §17/§21.3,
                   y compris labosurf/cmd/labosurf et labosurf/internal/service)
```
Exécuté sous WSL Ubuntu (Linux natif), pour la même raison pré-existante que §18 (syscall Windows-only dans `engines/ssh`/`engines/freewaygate`, non modifiés).

### 22.4 Limite assumée

Sans champ `Enabled` dédié dans `engine.Engine`/`EngineStatus`, "installé" et "activé" restent **confondus** dans ce projet : un moteur installé mais que l'opérateur ne souhaiterait *pas* rendre disponible pour de nouveaux Services (ex. en cours de dépréciation) ne peut pas être exclu sélectivement — il faudrait le désinstaller. Introduire un véritable état `Enabled` par moteur (persistant, indépendant d'Installed/Running) réglerait ce cas mais constituerait une nouvelle fonctionnalité, explicitement hors périmètre de ce contrôle.

---

## 23. Contrôle final M4 (re-confirmation)

Nouvelle demande de vérification définitive de la distinction Installed / Enabled-Activated / Running, avec exigence explicite de re-vérifier le code réel (pas de confiance dans un résumé antérieur).

### 23.1 Re-vérification du code réel

```
grep "Status().Running|Status().Installed" cmd/labosurf/menu_services.go
  124:  if e.Status().Installed {              ← gating (disponibilité pour créer un Service)
  180:  if ... e.Status().Running {            ← affichage informatif uniquement (● en cours / ○ arrêté)
```

Confirmé : `availableEnginesForService()` (la fonction qui détermine quels moteurs sont proposés à `serviceCreateMenu`) filtre sur `Status().Installed` depuis la correction de la §22 — **pas** sur `Status().Running`. La seule occurrence de `.Running` dans `menu_services.go` sert à afficher l'état courant à côté de chaque moteur candidat, sans l'exclure de la liste. `git status` confirme qu'aucun fichier `menu_profiles.go` / `internal/profile` / `internal/clientcfg` n'a été touché par cette session — `profileActivate()`/`BuildConfig` restent le mécanisme intact d'application des paramètres techniques propres à chaque moteur (voir §21.2, toujours valide).

### 23.2 Recherche Enabled/Activated (re-confirmée, élargie)

Recherche répétée sur l'ensemble du dépôt (`Enable|Activat|IsEnabled|EngineEnabled|ActiveEngine`, tous fichiers `.go`) : les seuls résultats sont `store.Account.Enabled`, `store.EngineGrant.Enabled`, `profile.Profile.Activate/Deactivate`, `license.Activate`, et `engines/udp` `adminSetEnabled` (utilisateur UDP, module séparé). **Aucun ne représente un état de moteur.** `engine.Engine` (interface) n'a ni méthode `Enable()`/`Disable()`, ni champ persistant de ce type. Ce constat est identique à celui de la §22.1 — confirmé une seconde fois, aucun changement de conclusion.

### 23.3 Correction nécessaire ?

**Non.** Le code est déjà dans l'état demandé par cette mission : `Installed=false` → moteur jamais proposé ; `Installed=true` (que `Running` soit vrai ou faux) → moteur sélectionnable pour créer/préparer un Service. Comme aucun état `Enabled` distinct n'existe, le modèle à 3 états demandé (Installed/Enabled/Running) se réduit correctement à 2 états réels (Installed/Running), avec `Installed` utilisé comme meilleure approximation documentée d'« activé » — jamais présenté comme un véritable `Enabled` dans le code ou les commentaires (voir le commentaire de `availableEnginesForService`, §22.2, qui le dit explicitement).

### 23.4 Validation

```
go build ./...   → OK
go vet ./...     → OK
go test ./...    → OK, toutes les suites PASS
```
(Résultat détaillé identique à §22.3 — exécuté à nouveau sous WSL Ubuntu pour cette re-confirmation, sans régression.)

### 23.5 Réponse au format demandé

- **ACTIVATED existe déjà :** NON (aucun état `Enabled`/`Activated` propre aux moteurs, recherche exhaustive confirmée deux fois).
- **RUNNING est distinct :** OUI (`Installed` et `Running` sont deux champs séparés de `EngineStatus`, mais ni l'un ni l'autre n'est un véritable "Enabled" — `Installed` est utilisé ici comme la meilleure approximation disponible, documentée comme telle).
- **Correction nécessaire :** NON (le filtrage sur `Status().Installed` était déjà en place depuis la §22 ; cette re-vérification confirme qu'aucune régression ni oubli ne subsiste).
- **Tests :** OK — tous les packages PASS.
- **Build :** OK — `go build ./...` sans erreur.
- **Vet :** OK — `go vet ./...` sans avertissement.
