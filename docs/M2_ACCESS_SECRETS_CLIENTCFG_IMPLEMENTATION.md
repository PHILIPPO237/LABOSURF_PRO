# M2 — Access Secrets + Client Config : rapport d'implémentation

> Date : 2026-09-14
> Branche : main
> Commit de base : `7c9c824`
> Aucun fichier existant modifié.

---

## 1. Fichiers créés

| Fichier | Rôle |
|---------|------|
| `internal/service/secrets.go` | `EnsureAccessSecrets` + helpers (splitEngineComponents, nextAccessWireGuardAddress, secStr) |
| `internal/service/secrets_test.go` | 18 tests unitaires couvrant tous les moteurs, idempotence, unicité WireGuard, transition compat, hybrides |
| `internal/clientcfg/access.go` | `GenerateFromAccess` + helpers (accessStr, firstAccessDomain, wireguardClientConfigFromSecrets, accessPrimaryVPN, accessHybridClientConfig) |
| `internal/clientcfg/access_test.go` | 16 tests unitaires couvrant tous les moteurs, hybrides, cas d'erreur |

---

## 2. Fonction principale A — `service.EnsureAccessSecrets`

```go
func EnsureAccessSecrets(a *Access, extraUsedWGOctets map[int]bool) error
```

### Comportement

- **Idempotent** : ne remplace jamais un secret déjà présent et non-vide.
- **Moteurs simples** : génère les secrets manquants pour le moteur nommé dans `a.Engine`.
- **Moteurs hybrides** : décompose `a.Engine` (ex: `"dnstt-xray"`) via `splitEngineComponents`, génère les secrets de CHAQUE composant dans le même `a.Secrets` (map plate).
- **WireGuard** : alloue une adresse unique dans `10.66.0.2–254` en scannant tous les fichiers Access existants + les octets passés dans `extraUsedWGOctets` (transition M3).

### Secrets générés par moteur

| Moteur | Champ(s) | Algorithme |
|--------|----------|-----------|
| `xray` | `uuid` | UUID v4 |
| `hysteria` | `password` | RandToken(12 bytes) |
| `hysteria2` | `password` | RandToken(12 bytes) |
| `tuic` | `uuid` + `password` | UUID v4 + RandToken |
| `dnstt` | `public_key` + `private_key` | Ed25519 (hex) |
| `slowdns` | `public_key` + `private_key` | Ed25519 (hex) |
| `ssh` | `public_key` + `private_key` | Ed25519 (hex) |
| `wireguard` | `private_key` + `public_key` + `address` | X25519 (base64) + pool IP |
| `udp` | `password` | RandToken(12 bytes) |

### Hybrides supportés

- `dnstt-xray` → secrets dnstt + secrets xray
- `slowdns-xray` → secrets slowdns + secrets xray
- `dnstt-ssh` → une seule paire Ed25519 partagée (même convention que `aliasGrantForComponent`)
- Toute combinaison de moteurs connus séparés par `-`

### Paramètre `extraUsedWGOctets`

Passez `nil` dans le cas normal. Lors de la transition M3 (migration Grants→Access), passez une map des octets déjà attribués par les anciens `Account.Grants[wireguard].Config["address"]` pour garantir l'unicité inter-systèmes.

---

## 3. Fonction principale B — `clientcfg.GenerateFromAccess`

```go
func GenerateFromAccess(acc service.Access, svc service.Service, username string) (ClientResult, error)
```

Réutilise la structure `ClientResult{Engine, ClientLink, ServerConfig}` existante. Pas de nouveau type.

### Comportement

- **Moteur simple** : lit les secrets depuis `acc.Secrets`, génère le lien/config client du moteur nommé.
- **Moteur hybride** : utilise `svc.Components` pour trouver le composant VPN principal (`engineutil.Role() == RoleVPN`), génère son lien client.
- **Port** : lit `svc.ListenPort()` ; si nul (service sans port configuré), fallback sur `engineutil.EngineCapabilitiesMap[engine].Port`.
- **Hôte** : `svc.Domains[0]` si disponible, sinon `svc.Host`. Hôte vide → erreur.

### Config client par moteur

| Moteur | Format du `ClientLink` |
|--------|----------------------|
| `xray` | `vless://UUID@host:port?...&pbk=<reality-pub-key>...` |
| `hysteria` | `hysteria://password@host:port` |
| `hysteria2` | `hysteria2://user:pass@host:port?obfs=salamander&...` |
| `tuic` | `tuic://uuid:pass@host:port?...` |
| `wireguard` | Config wg-quick INI (format standard) |
| `ssh` | `ssh user@host -p port` (+ `ServerConfig` JSON avec public_key) |
| `dnstt` | `dnstt://user@host?key=<public_key>` |
| `slowdns` | `slowdns://user@host?key=<public_key>` |
| `udp` | `udp://user@host:port?pass=password` |

---

## 4. Préservation de l'API existante

| Symbole existant | Statut |
|-----------------|--------|
| `clientcfg.Generate(acc store.Account, engine string, prof srvcfg.Profile)` | **Inchangé** |
| `clientcfg.ApplyServerConfig(ctx, s, engine, prof)` | **Inchangé** |
| `clientcfg.ClientResult{Engine, ClientLink, ServerConfig}` | **Inchangé** |
| `clientcfg.GrantConfig`, `grantString`, `vlessLink`, … | **Inchangés** |
| `store.Account`, `store.EngineGrant`, `store.EnsureEngineSecrets` | **Inchangés** |
| `engineutil.CompositeEngine`, `buildComponentConfigs`, `aliasGrantForComponent` | **Inchangés** |

Les deux APIs coexistent pendant la transition : `Generate` pour les anciens Grants, `GenerateFromAccess` pour les nouveaux Access.

---

## 5. Gestion des secrets WireGuard et unicité des adresses

### Algorithme `nextAccessWireGuardAddress`

1. Crée une map `used` des octets déjà pris.
2. Intègre `extraUsedWGOctets` (anciens Grants, passés par l'appelant).
3. Scanne tous les fichiers `$LABOSURF_DATA_DIR/access/*.json` via `listAllAccess()`.
4. Pour chaque Access (sauf `currentAccessID`), extrait l'octet de `secrets["address"]` au format `10.66.0.N/32`.
5. Alloue le premier octet libre dans [2, 254].

### Compatibilité transition M3

L'appelant responsable de la migration passera les octets des anciens Grants :

```go
extra := map[int]bool{}
for _, acc := range store.ListAccounts() {
    addr := grantString(acc, "wireguard", "address")
    var n int
    if _, err := fmt.Sscanf(addr, "10.66.0.%d/32", &n); err == nil {
        extra[n] = true
    }
}
service.EnsureAccessSecrets(&newAccess, extra)
```

---

## 6. Support hybrides dans `GenerateFromAccess`

Pour un service hybride (ex: `dnstt-xray`) :

1. `svc.IsHybrid()` → `len(svc.Components) > 0`
2. `accessPrimaryVPN(svc.Components)` → premier composant avec `engineutil.Role() == RoleVPN` → `"xray"`
3. `accessSimpleClientConfig(acc, "xray", username, host, port)` → lien VLESS
4. Le nom du moteur dans `ClientResult.Engine` est restauré au nom hybride complet : `"dnstt-xray"`

Aucun appel à `engine.Get()` ni à `engine.Has()` — pas de dépendance au registre de moteurs.

---

## 7. Nouveaux helpers dans `internal/service/secrets.go`

| Fonction | Rôle |
|----------|------|
| `splitEngineComponents(name string) []string` | Décompose `"dnstt-xray"` → `["dnstt","xray"]` ; retourne `[name]` si non hybride ou composant inconnu |
| `isKnownSimpleEngine(name string) bool` | Valide qu'un composant est un moteur simple connu |
| `ensureComponentSecrets(eng string, secrets map[string]any, ...)` | Génère les secrets manquants pour UN composant |
| `nextAccessWireGuardAddress(accessID string, extra map[int]bool) (string, error)` | Alloue une adresse WireGuard unique |
| `secStr(v any) string` | Extrait une string depuis `map[string]any` |

---

## 8. Nouveaux helpers dans `internal/clientcfg/access.go`

| Fonction | Rôle |
|----------|------|
| `accessStr(secrets map[string]any, key string) string` | Équivalent de `grantString` pour les secrets Access |
| `firstAccessDomain(svc service.Service) string` | Premier domaine ou hôte IP du service |
| `accessPrimaryVPN(components []string) string` | Premier composant avec `RoleVPN` |
| `accessHybridClientConfig(...)` | Config client hybride (délègue au VPN principal) |
| `wireguardClientConfigFromSecrets(secrets, host, port)` | Config wg-quick depuis `map[string]any` (sans `store.Account`) |

---

## 9. Dépendances introduites

### `internal/service/secrets.go`

Nouvelle dépendance vers `labosurf/internal/secret` (pour UUID, RandToken, Ed25519Keypair, X25519Keypair). Pas de dépendance circulaire : `service` → `secret` uniquement.

### `internal/clientcfg/access.go`

Nouvelle dépendance vers `labosurf/internal/service`. Pas de dépendance circulaire : `clientcfg` → `service` est autorisé (`service` n'importe pas `clientcfg`).

---

## 10. Tests créés

### `internal/service/secrets_test.go` — 18 tests

| Test | Ce qu'il vérifie |
|------|-----------------|
| `TestEnsureAccessSecretsXray` | UUID généré pour xray |
| `TestEnsureAccessSecretsHysteria` | password généré pour hysteria |
| `TestEnsureAccessSecretsHysteria2` | password généré pour hysteria2 |
| `TestEnsureAccessSecretsTUIC` | uuid + password générés pour tuic |
| `TestEnsureAccessSecretsDNSTT` | clés Ed25519 générées pour dnstt |
| `TestEnsureAccessSecretsSlowDNS` | clés Ed25519 générées pour slowdns |
| `TestEnsureAccessSecretsSSH` | clés Ed25519 générées pour ssh |
| `TestEnsureAccessSecretsWireGuard` | clés X25519 + adresse valide 10.66.0.N/32 |
| `TestEnsureAccessSecretsUDP` | password généré pour udp |
| `TestEnsureAccessSecretsIdempotent` | uuid xray existant non remplacé |
| `TestEnsureAccessSecretsWireGuardIdempotent` | adresse WireGuard existante non remplacée |
| `TestWireGuardAddressUniqueness` | deux accès → deux adresses différentes |
| `TestWireGuardExtraUsedOctets` | octets 2/3/4 passés en extra → non réattribués |
| `TestEnsureAccessSecretsHybridDnsttXray` | secrets dnstt + xray dans le même Access |
| `TestEnsureAccessSecretsHybridSlowdnsXray` | secrets slowdns + xray dans le même Access |
| `TestSplitEngineComponentsSimple` | moteurs simples retournés tels quels |
| `TestSplitEngineComponentsHybrid` | `dnstt-xray` et `slowdns-xray` décomposés correctement |
| `TestSplitEngineComponentsUnknown` | `foo-bar` retourné intact (composants inconnus) |

### `internal/clientcfg/access_test.go` — 16 tests

| Test | Ce qu'il vérifie |
|------|-----------------|
| `TestGenerateFromAccessXray` | Lien VLESS avec UUID et clé Reality |
| `TestGenerateFromAccessHysteria` | Lien `hysteria://password@host:port` |
| `TestGenerateFromAccessHysteria2` | Lien `hysteria2://user:pass@...` avec obfs |
| `TestGenerateFromAccessTUIC` | Lien `tuic://uuid:pass@...` |
| `TestGenerateFromAccessWireGuard` | Config wg-quick avec PrivateKey/Address/Endpoint |
| `TestGenerateFromAccessSSH` | Commande `ssh user@host -p port` + ServerConfig JSON |
| `TestGenerateFromAccessDNSTT` | Lien `dnstt://user@host?key=...` |
| `TestGenerateFromAccessSlowDNS` | Lien `slowdns://user@host?key=...` |
| `TestGenerateFromAccessUDP` | Lien `udp://user@host:port?pass=...` |
| `TestGenerateFromAccessHybridDnsttXray` | Hybride → lien VLESS du VPN principal |
| `TestGenerateFromAccessHybridSlowdnsXray` | Hybride slowdns-xray → lien VLESS |
| `TestGenerateFromAccessRequiresHost` | Hôte vide → erreur |
| `TestGenerateFromAccessMissingSecrets` | UUID absent → placeholder (pas d'erreur fatale) |
| `TestGenerateFromAccessHysteriaMissingPassword` | Password absent → erreur explicite |
| `TestGenerateFromAccessUnknownEngine` | Moteur inconnu → erreur |
| `TestGenerateFromAccessDefaultPort` | Service sans port → fallback sur port par défaut du moteur |

---

## 11. Résultat de `go test ./...`

```
ok   labosurf/internal/service     3.680s    (52 tests, tous PASS)
                                              dont 18 nouveaux tests M2 secrets

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

**Détail failure clientcfg** : `engines/ssh/server.go:29:62: undefined: syscall.Credential` — API Linux-only, utilisée par `engines/ssh/server.go`. La failure est pré-existante (documentée en M1). Le package `clientcfg` lui-même **compile sans erreur** (`go build ./internal/clientcfg/...` → OK) ; c'est uniquement le binaire de test (qui importe `_ "labosurf/engines/ssh"`) qui échoue.

`go vet ./internal/service/...` → OK, aucun avertissement.

---

## 12. Fichiers existants volontairement laissés intacts

Aucun fichier existant n'a été modifié. Confirmés intacts :

- `engine.Engine` interface
- `internal/engineutil/` (CompositeEngine, ValidateHybrid, EvaluateChain, compat.go…)
- `internal/profile/` (17 tests toujours verts)
- `internal/clientcfg/clientcfg.go` — `Generate(...)` API préservée
- `internal/clientcfg/wireguard.go`, `hysteria.go`, `hysteria2.go`, `tuic.go`, `ssh.go`, `xray_options.go`
- `internal/srvcfg/`
- `internal/store/` (Account, EngineGrant, EnsureEngineSecrets)
- `cmd/labosurf/` (menus)
- `engines/` (xray, ssh, wireguard, slowdns, dnstt…)
- `internal/service/service.go`, `store.go`, `validate.go` (M1 — intacts)

---

## 13. Sécurité et contraintes respectées

- **Aucun secret réel dans Git** : les secrets sont générés à l'exécution, jamais écrits dans le code source.
- **Secrets traités séparément** : le champ `Access.Secrets map[string]any` est sérialisé dans `$LABOSURF_DATA_DIR/access/<id>.json` avec permissions 0o600.
- **Pas de contournement de facturation/contrôle opérateur** : les fonctions génèrent des configurations légitimes selon les protocoles officiels (WireGuard RFC, VLESS protocol spec, etc.).
- **Système réservé à l'administration d'infrastructures autorisées** : aucune fonctionnalité d'accès non autorisé.
- **Clés WireGuard X25519** : générées via `secret.X25519Keypair()` (courbe Curve25519, standard WireGuard officiel).
- **Clés Ed25519** : générées via `secret.Ed25519Keypair()` (standard SSH/DNS tunnel officiel).

---

## 14. Limites de M2

1. **Pas de migration automatique** : `EnsureAccessSecrets` ne migre pas les anciens `Account.Grants` vers des `Access`. La migration (M3) reste à implémenter.

2. **Pas de `ApplyServerConfigFromAccess`** : `GenerateFromAccess` génère uniquement la config CLIENT. La génération de config serveur groupée (multi-accès) à partir des nouveaux `Access` est prévue en M3/M4.

3. **`wireguardClientConfigFromSecrets` ne vérifie pas la validité des clés** : si des secrets corrompus sont passés (non-base64), la config wg-quick générée sera syntaxiquement valide mais refusée par WireGuard à l'import. Cette validation appartient à l'appelant.

4. **Pas de menus** : aucun menu n'a été créé ou modifié.

5. **Tests clientcfg non exécutables sur Windows** : failure de build pré-existante due à `engines/ssh/server.go` (syscall Linux-only). Vérification manuelle sur Linux/CI requise pour les 16 tests de `access_test.go`.

---

## 15. Ce qui sera traité en M3

- `MigrateGrantsToAccess` : création automatique des `Access` depuis les `Account.Grants` existants.
- `extraUsedWGOctets` : alimenté depuis les Grants lors de la migration.
- `ApplyServerConfigFromAccess` : config serveur groupée depuis les nouveaux `Access`.
- Suppression progressive des anciens Grants (avec période de coexistence).

---

## 16. Ce qui sera traité en M4

- Menus `[3] SERVICES` et `[5] ACCÈS` dans `cmd/labosurf/`.
- Intégration des nouvelles fonctions M2 dans les menus.

---

## 17. Vérification finale

```
$ ls -lh internal/service/secrets.go internal/service/secrets_test.go
$ ls -lh internal/clientcfg/access.go internal/clientcfg/access_test.go
$ go build ./internal/service/... ./internal/clientcfg/...
$ go vet ./internal/service/...
$ go test ./internal/service/... -count=1
ok   labosurf/internal/service   3.680s
```

Tous les nouveaux fichiers M2 compilent, vet passe, les 18 tests `service` (M2) + 34 tests (M1) = 52 tests PASS.

---

*Aucun commit automatique effectué. Bilan M2 uniquement.*
