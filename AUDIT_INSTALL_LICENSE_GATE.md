# AUDIT_INSTALL_LICENSE_GATE — Verrouillage de l'installation par licence

**Mission** : garantir qu'une installation complète de LABOSURF_PRO est impossible sans une clé de licence valide réellement générée par LABOSURF_LICENSE_MAKER, sans toucher au menu existant du License Maker, sans créer d'API serveur, sans modifier les moteurs réseau. Base de départ : `AUDIT_LICENSE_COMPATIBILITY.md` (compatibilité cryptographique déjà vérifiée) et le code réel des deux dépôts.

**Méthode** : chaque affirmation ci-dessous est adossée à une commande réellement exécutée et observée dans cette session, ou à une lecture directe du code source actuel (fichier:ligne cité). Rien n'est supposé.

---

## 1. ARCHITECTURE ACTUELLE

```
LABOSURF_LICENSE_MAKER (dépôt Git frère, privé, outil de l'opérateur)
        │  menu interactif (menu.go) → option 1 "générer une licence"
        │  CreateLicense(id, comment)  (license.go)
        │  signature Ed25519 avec labosurf_admin.key (jamais distribué)
        ▼
   jeton  base64url(payload JSON).base64url(signature)
        │  remis manuellement à l'exploitant du VPS (copier/coller)
        ▼
LABOSURF_PRO — labosurf-pro.sh (installateur, distribué publiquement)
        │  [5/9] télécharge le binaire "labosurf" (asset GitHub release)
        │  [6/9] fetch_public_key() + activate_license() ← LE VERROU
        │        appelle : labosurf license verify -token <jeton> -print-id
        ▼
   accepté (exit 0) ──────────────┐        refusé (exit≠0)
        │                          │              │
   reçu écrit                      │        die() → exit 1
        ▼                          │              │
   [7/9] moteurs, [8/9] service,   │        AUCUN moteur, AUCUN service,
   [9/9] validation finale         │        installation INCOMPLÈTE
        ▼
   LABOSURF PRO actif (systemd)
```

**Découverte architecturale importante** (jamais documentée dans les audits précédents, vérifiée par lecture directe de `.github/workflows/release.yml`) : le binaire `/usr/local/bin/labosurf` que `labosurf-pro.sh` télécharge et utilise réellement pour le verrou de licence **n'est pas** `cmd/labosurf` (le "gestionnaire multi-moteurs" avec `internal/license`, les hybrides, le menu interactif `menu.go`), mais **`engines/udp`** — le module Go séparé historique, compilé avec la clé publique de production embarquée à la compilation (`-ldflags "-X main.embeddedVerifyKeyHex=${PUBKEY}"`, `.github/workflows/release.yml:64-66`). `cmd/labosurf` est bien publié (asset `labosurf-mgr-<arch>`), mais **`labosurf-pro.sh` ne le télécharge ni ne l'invoque jamais** (`grep -n "labosurf-mgr\|cmd/labosurf" labosurf-pro.sh` → vide). C'est donc **`engines/udp/license.go` + `license_cli.go`** qui gate réellement chaque installation en production aujourd'hui, pas `internal/license`. Les deux implémentations sont compatibles avec LABOSURF_LICENSE_MAKER (vérifié dans `AUDIT_LICENSE_COMPATIBILITY.md` pour les deux), donc ceci ne change rien à la sécurité du verrou, mais c'était nécessaire à établir avant de tester le bon binaire.

---

## 2. MENU EXISTANT DE LABOSURF_LICENSE_MAKER

Inspecté en lecture seule (`main.go`, `menu.go`). **Non modifié, ni supprimé, ni réorganisé.**

Premier démarrage (si `labosurf_admin.key` absent) : assistant à 2 choix — clé aléatoire, ou phrase secrète déterministe (pour synchroniser la même clé entre plusieurs appareils).

Menu principal (`printMenu()`, `menu.go:69-87`) — 9 options + quitter, toutes présentes et fonctionnelles, aucune retirée :

| # | Option | Fonction |
|---|---|---|
| 1 | 🔑 générer une licence | `generateFromMenu` → `CreateLicense(id, comment)` |
| 2 | 📋 consulter les licences | `showLicenses` → `Registry.List()` |
| 3 | 📊 statistiques | `showStats` → `Registry.Stats()` |
| 4 | 🚫 révoquer une licence | `revokeLicense` → `Registry.Revoke(id)` |
| 5 | 🛡️ sécurité / état des clés | `securityStatus` |
| 6 | ℹ️ à propos / aide | `about` |
| 7 | 🔐 définir clé perso | `defineCustomKey` → `GenerateKeyPairFromPassphrase` |
| 8 | 🔄 synchroniser (pc ↔ android/tablettes) | `syncRegistry` (git pull/push sur `licenses.json`) |
| 9 | ⬆️ mettre à jour depuis GitHub | `updateApp` |
| 0 | ❌ quitter | — |

Aucune de ces options n'a été modifiée. Le format des licences produites par l'option 1 (`CreateLicense`) n'a pas changé.

---

## 3. FONCTIONNEMENT ACTUEL DES CLÉS

- **Format du jeton** (identique dans les 3 implémentations Go — `LICENSE_MAKER/license.go`, `PRO/internal/license/license.go`, `PRO/engines/udp/license.go`) : `base64url(json.Marshal(LicenseData)).base64url(ed25519.Sign(payload))`, avec `LicenseData{ID, Key, IssuedAt, ActivationUntil, Product="LABOSURF PRO", Comment}`. Clé métier `Key` : 40 caractères, préfixe `LABOSURF`.
- **Clé privée** : jamais dans LABOSURF_PRO. Vit uniquement dans `LABOSURF_LICENSE_MAKER/labosurf_admin.key`, gitignorée (`*.key`, règle explicite), jamais trackée (`git ls-files` vide sur ce motif), jamais committée dans l'historique (`git log --all --diff-filter=A` vide).
- **Clé publique** : `7b27e59816d60f38a7299e226c714a3cb31a011f91f424099368506ded209595`, identique dans `LICENSE_MAKER/labosurf_pub.key`, `PRO/internal/license/license.go` (`EmbeddedVerifyKeyHex`), `PRO/engines/udp/labosurf_pub.key`, `PRO/release/license_pub.key`, et embarquée à la compilation dans le binaire `engines/udp` publié (ldflags CI). Cohérence revérifiée cette session.
- **Fenêtre d'activation** : 3 heures après émission, contrôlée à la vérification (`ActivationUntil`), pas une expiration du "produit" — une fois installé, le serveur tourne sans contrôle ultérieur (modèle documenté et assumé, pas un oubli).
- **1 clé = 1 installation** : un reçu local (`.install_<ID>.receipt`) empêche de réutiliser la même clé sur la même machine.

Rien de tout cela n'a été changé cette session (cryptographie, format, algorithme identiques à avant).

---

## 4. FONCTIONNEMENT ACTUEL DE L'INSTALLATION (avant correction)

Lecture complète de `labosurf-pro.sh` (610 lignes), point d'entrée `main()` (ligne 542), 9 étapes :

1. Vérification système · 2. Dépendances (`apt-get`) · 3. Réseau (TUN/NAT) · 4. Répertoires · **5. Téléchargement du binaire `labosurf`** · **6. Licence** (`fetch_public_key` + `activate_license`) · 7. Moteurs sélectionnés · 8. Service systemd · 9. Validation finale.

**`activate_license()` (ligne 375, avant correction)** :
```bash
[[ "${LABOSURF_DEV:-0}" == "1" ]] && { info "Mode DÉVELOPPEMENT..."; return 0; }
...
IFS= read -r token < /dev/tty
[[ -n "${token//[[:space:]]/}" ]] || die "Aucune licence fournie."
id="$(LABOSURF_LICENSE_PUBKEY="$(cat "$PUBKEY_PATH")" "$BIN_PATH" license verify -token "$token" -print-id)" \
  || die "Licence refusée..."
```

**Ordre déjà correct avant cette session** : la licence est vérifiée à l'étape 6/9, **avant** l'installation des moteurs (7/9) et **avant** la création/démarrage du service systemd (8/9). Un `die` à l'étape 6 empêche donc déjà, structurellement, toute installation complète (aucun moteur, aucun service). Ceci confirme et affine les correctifs déjà documentés dans les commits `530a955`/`ec9e26d` (historique git).

---

## 5. PROBLÈMES TROUVÉS

| # | Problème | Gravité | Preuve |
|---|---|---|---|
| 1 | **Contournement total par variable d'environnement** : `LABOSURF_DEV=1` sautait entièrement `activate_license()` (`return 0` avant toute lecture/vérification du jeton) | **Critique** | `labosurf-pro.sh:380` (avant correction) ; script public (fetché depuis GitHub releases), donc lisible par n'importe quel utilisateur final — un `grep LABOSURF_DEV` sur le script suffisait à trouver le contournement exact. Confirmé non utilisé par aucun CI/test (`grep -rn LABOSURF_DEV` sur tout le dépôt ne renvoyait que cette ligne) |
| 2 | Aucun test n'exerçait réellement `activate_license()` avec un vrai jeton `LICENSE_MAKER` (les tests Go existants testent les bibliothèques `internal/license`/`engines/udp` directement, jamais l'intégration shell) | Moyen (absence de couverture, pas une faille en soi) | Recherche de tests référençant `activate_license`/`labosurf-pro.sh` : aucun avant cette session |
| 3 | (Confirmé, pas une régression) Le binaire réellement déployé par l'installateur (`engines/udp`) diffère du binaire documenté par un commentaire de `cmd/labosurf/main.go` qui laisse penser que c'est ce dernier que `labosurf-pro.sh` invoque | Faible (confusion documentaire, aucun impact de sécurité — les deux implémentations sont compatibles et sûres) | §1 ci-dessus |

**Vérifié et écarté (pas un problème)** : CAS 1 à 7 du cahier des charges (absence, vide, malformée, modifiée, signature invalide, expirée, valide) étaient **déjà** correctement gérés par la logique cryptographique de `activate_license()` — le seul point réellement cassé était le contournement `LABOSURF_DEV=1` (problème #1), qui rendait ces bonnes vérifications **optionnelles** pour quiconque le découvrait.

---

## 6. MODIFICATIONS EFFECTUÉES

### 6.1 `LABOSURF_PRO/labosurf-pro.sh`

1. **Suppression complète** de la ligne de contournement `LABOSURF_DEV=1` dans `activate_license()`. Aucun remplacement par un autre mécanisme de contournement — la fonction exige désormais systématiquement un jeton qui passe la vérification Ed25519 réelle.
2. **Extraction de `read_license_token()`** (nouvelle petite fonction) : isole la lecture `/dev/tty` pour permettre à un test de la redéfinir sans dépendre d'un vrai terminal. **Ne change aucun comportement pour un utilisateur réel** (toujours `/dev/tty` par défaut).
3. **Garde de sourcing** en bas du script (`if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then main "$@"; fi`) : permet à un test de sourcer le script sans déclencher une installation. Comportement inchangé quand le script est exécuté normalement (`bash labosurf-pro.sh`, `curl | bash`).

Aucune autre ligne de `labosurf-pro.sh` n'a été modifiée : dépendances, réseau, téléchargement de binaires, moteurs, service systemd, validation finale — **inchangés**.

### 6.2 `LABOSURF_PRO/test_install_license_gate.sh` (nouveau)

Test d'intégration bout-en-bout du verrou (détail §9-§10).

### 6.3 `LABOSURF_LICENSE_MAKER`

**Aucune modification.** Le menu, ses 9 options, `CreateLicense`, la signature, le stockage (`licenses.json`), la gestion des clés (`labosurf_admin.key`/`labosurf_pub.key`) sont strictement inchangés. Aucune nécessité de modification n'a été identifiée : le format et la cryptographie sont déjà compatibles avec LABOSURF_PRO (`AUDIT_LICENSE_COMPATIBILITY.md`).

### 6.4 Autres fichiers

Aucun moteur (udp, ssh, xray, hysteria, slowdns, dnstt) n'a été touché. Aucune API n'a été créée.

---

## 7. PROTECTION DU LICENSE MAKER

- LABOSURF_LICENSE_MAKER reste un outil 100% local/CLI, aucune transformation en serveur, aucune API d'activation créée.
- La clé privée (`labosurf_admin.key`) réside **uniquement** dans le dépôt `LABOSURF_LICENSE_MAKER`, gitignorée, jamais committée. Vérifié à nouveau cette session : `find` sur tout `LABOSURF_PRO` ne trouve aucun fichier `*admin*key*`, `git log --all` (les deux dépôts) ne montre aucun ajout historique d'un tel fichier.
- LABOSURF_PRO (les deux implémentations, `internal/license` et `engines/udp`) ne contient **que la clé publique** : soit embarquée en dur dans le source (`EmbeddedVerifyKeyHex` / `-ldflags` à la compilation), soit lisible depuis un fichier `labosurf_pub.key` ou une variable d'environnement — jamais de clé privée dans un test, un script d'installation, un binaire ou un fichier destiné à l'utilisateur final.
- Les tests de cette session (Go et shell) utilisent exclusivement des paires de clés ed25519 **jetables**, générées à la volée, jamais la clé de production.

---

## 8. NOUVEAU FLUX D'INSTALLATION

Identique au flux décrit en §1, avec un seul changement de comportement réel : **il n'existe plus aucune manière de passer l'étape 6/9 sans un jeton qui vérifie cryptographiquement**. Concrètement :

- **Clé absente / vide** → `die "Aucune licence fournie. Installation annulée."`, exit 1, aucun reçu écrit, script terminé.
- **Clé malformée, modifiée, signature invalide, ou expirée** → `"$BIN_PATH" license verify` retourne un code non nul → `die "Licence refusée..."`, exit 1, aucun reçu écrit.
- **Clé valide** → reçu d'installation écrit, l'étape 6/9 se termine avec succès, l'installation continue automatiquement vers les étapes 7 (moteurs), 8 (service systemd) et 9 (validation finale).
- **Même clé valide réutilisée sur la même machine** → bloquée par le reçu déjà présent (`die "... 1 clé = 1 installation ..."`), comportement préexistant, revérifié.

---

## 9. TESTS DE VALIDATION

Tests Go déjà existants (revérifiés, non modifiés) : `internal/license/license_test.go`, `engines/udp/license_test.go`, ainsi que `internal/license/crossproject_test.go` et `engines/udp/crossproject_test.go` (ajoutés dans la session précédente, couvrant déjà les items 1-10 du cahier des charges au niveau bibliothèque : clé valide/invalide/absente/vide/malformée, licence modifiée, signature invalide, licence expirée, jeton réellement généré par LICENSE_MAKER, vérifié par PRO — pour les **deux** implémentations).

**Nouveau cette session** : `test_install_license_gate.sh`, qui reproduit l'exact appel shell de `activate_license()` contre le **vrai binaire de production** (`engines/udp`, §1) et un **vrai jeton généré par le vrai LABOSURF_LICENSE_MAKER** (menu piloté par séquence stdin, binaire compilé à la volée depuis ses sources) :

| Scénario | Résultat attendu | Résultat observé |
|---|---|---|
| CAS 7 — clé valide (vrai jeton LICENSE_MAKER) | installation continue | ✅ PASS |
| CAS 1 — aucune clé | bloqué | ✅ PASS |
| CAS 2 — clé vide (espaces) | bloqué | ✅ PASS |
| CAS 3 — clé malformée | bloqué | ✅ PASS |
| CAS 4/5 — clé modifiée / signature invalide | bloqué | ✅ PASS |
| CAS 6 — clé expirée (même clé de test, fenêtre dans le passé) | bloqué | ✅ PASS |
| Bonus — réutilisation de la même licence valide | bloqué dès la 2e tentative | ✅ PASS |
| Vérification structurelle — aucune référence active à `LABOSURF_DEV` | aucun contournement | ✅ PASS |

**13/13 PASS.**

---

## 10. TESTS D'INSTALLATION

**Testé localement dans cette session** (sans root, sans VPS, sans systemd — via sourcing isolé de `labosurf-pro.sh`, décrit ci-dessus) :
- Le comportement exact de l'étape 6/9 (`activate_license`) pour les 7 cas + réutilisation, contre le vrai binaire de production et un vrai jeton du vrai License Maker.
- La confirmation empirique que `LABOSURF_DEV=1` n'a plus aucun effet (essai manuel : `LABOSURF_DEV=1` + jeton vide → toujours bloqué, exit 1, aucun reçu).
- La compilation réussie du binaire `engines/udp` et du binaire `cmd/labosurf` (les deux modules Go du dépôt).

**Non testé dans cet environnement (nécessite un vrai VPS Linux avec root/systemd/apt)** :
- Le flux complet des étapes 1 à 5 et 7 à 9 (`apt-get`, TUN/NAT, téléchargement réel depuis une release GitHub publiée, installation des moteurs tiers, création et démarrage réel des services systemd, `final_check()` avec `systemctl is-active`).
- Le téléchargement réel de `license_pub.key` et du binaire `labosurf` depuis une release GitHub réelle (`fetch_public_key`, `download_asset` — testés par leur logique dans `test_release_local.sh`, pas par un vrai téléchargement réseau ici).
- Un parcours humain réel : lancer l'installateur sur un VPS neuf, coller un jeton obtenu de LABOSURF_LICENSE_MAKER, observer l'installation aboutir, puis retenter avec un jeton invalide/absent sur un second VPS et observer le blocage.

C'est la seule façon de transformer la preuve de cette session (intégration shell locale, binaire réel, jeton réel) en preuve de fonctionnement réseau réel de bout en bout.

---

## 11. TESTS DE NON-RÉGRESSION

```
# LABOSURF_PRO — module racine
go build ./...        exit 0
go vet ./...           propre
go test ./...           tous les packages testables PASS (aucune régression)

# LABOSURF_PRO — module engines/udp
go build ./...          exit 0
go vet ./...            propre
go test -race ./...     PASS, aucune race

# LABOSURF_LICENSE_MAKER
go build ./...          exit 0
go vet ./...            propre
go test ./... -v         PASS (TestCrossGenerate, inchangé)

# Script d'installation
bash -n labosurf-pro.sh                exit 0 (syntaxe valide après modification)
./test_install_license_gate.sh          13/13 PASS
```

Aucun test préexistant n'a été supprimé, désactivé ou affaibli.

---

## 12. RÉSULTATS

- Le seul contournement réel identifié (`LABOSURF_DEV=1`, un booléen d'environnement trivialement découvrable dans un script public) est **supprimé**, sans remplacement par un autre mécanisme de contournement.
- Le verrou cryptographique lui-même (Ed25519, format de jeton, fenêtre de 3h, 1 clé = 1 install) était **déjà correct** et n'a pas été touché.
- L'ordre des étapes de l'installateur (licence vérifiée avant moteurs et service) était **déjà correct**.
- Un test d'intégration bout-en-bout nouveau (`test_install_license_gate.sh`) prouve, contre le **vrai** binaire de production et un **vrai** jeton du **vrai** LABOSURF_LICENSE_MAKER, que les 7 cas du cahier des charges + la réutilisation de licence se comportent exactement comme demandé.
- Le menu de LABOSURF_LICENSE_MAKER est **intégralement conservé** (9 options, aucune modification).
- Aucune clé privée n'existe, n'a jamais existé, ni ne peut se retrouver dans LABOSURF_PRO.

---

## 13. LIMITES ÉVENTUELLES

- **Limite fondamentale de toute vérification côté client** (pas spécifique à ce projet) : `labosurf-pro.sh` est un script shell **public**. Un utilisateur suffisamment déterminé et compétent pourrait toujours écrire son propre script réutilisant les binaires publiés (SHA-256 vérifiés) sans jamais appeler `activate_license()`. Aucune protection logicielle distribuée publiquement ne peut empêcher cela sans un serveur d'activation réseau — **explicitement exclu du périmètre de cette mission**. Ce rapport ne prétend donc PAS qu'une installation est *cryptographiquement impossible* sans clé pour un attaquant qui réécrit l'installateur ; il garantit que **l'installateur officiel, tel que distribué**, ne peut plus être trivialement contourné par une variable d'environnement, un fichier supprimé, ou un jeton vide/invalide.
- **Clé publique téléchargée en plus de la clé embarquée** : `fetch_public_key()` télécharge `license_pub.key` depuis la release GitHub et **cette valeur prend le pas** (via `LABOSURF_LICENSE_PUBKEY`) sur la clé publique déjà embarquée dans le binaire à la compilation (`resolveVerifyKey()` priorise la variable d'environnement). En pratique les deux valeurs proviennent de la même release et sont donc identiques dans le cas légitime ; ce n'est pas une régression de cette session (comportement préexistant, non modifié), mais c'est une redondance qui n'ajoute pas de garantie au-delà de la confiance déjà accordée à la release GitHub (les binaires eux-mêmes ne sont protégés que par le même mécanisme HTTPS + SHA256SUMS). Documenté ici pour être honnête sur son niveau de protection réel ; non corrigé cette session (changement plus large que le périmètre "verrouillage contre bypass triviaux", risque de casser un usage non entièrement cartographié de `$PUBKEY_PATH`).
- **`cmd/labosurf` (le "gestionnaire" avec hybrides et `internal/license`) n'est actuellement invoqué par aucun mécanisme d'installation** (§1) — ce n'est pas un défaut de licence, mais une incohérence architecturale plus large entre deux implémentations parallèles du CLI LABOSURF PRO, hors périmètre de cette mission (qui concerne uniquement la clé/la licence, pas les moteurs ni l'architecture CLI).
- **Pas de test sur VPS réel** (§10) — tout ce qui précède est vérifié par compilation, exécution locale du binaire réel et sourcing isolé du script, pas par un déploiement réseau complet.
- **`-print-id` de `engines/udp/license_cli.go`** imprime un dump multi-lignes sur stdout en cas d'échec au lieu de respecter strictement son contrat ("n'imprimer que l'ID") — sans conséquence de sécurité (le code de sortie non nul déclenche `die` immédiatement, quel que soit le contenu capturé), mais une incohérence de code mineure, non corrigée (hors périmètre du verrouillage lui-même).

---

## 14. ÉTAT FINAL

| Question | Réponse |
|---|---|
| Menu original de LICENSE_MAKER conservé ? | **Oui**, intégralement (9 options, aucune suppression, aucune modification) |
| Format des clés conservé ? | **Oui**, strictement identique |
| Cryptographie existante conservée ? | **Oui**, Ed25519 inchangé, aucun nouvel algorithme |
| Où se trouve la clé privée ? | Uniquement dans `LABOSURF_LICENSE_MAKER/labosurf_admin.key`, gitignorée, jamais dans LABOSURF_PRO |
| Ce que LABOSURF_PRO possède pour vérifier | Uniquement la clé **publique** (embarquée + fichier + variable d'environnement, jamais la privée), dans les deux implémentations (`internal/license`, `engines/udp`) |
| Moment exact où l'installation demande la clé | Étape 6/9 de `labosurf-pro.sh`, **avant** l'installation des moteurs (7/9) et du service systemd (8/9) |
| Avec une clé valide | Reçu écrit, l'installation continue automatiquement vers les moteurs et le service |
| Avec une clé invalide (absente/vide/malformée/modifiée/expirée) | `die`, exit 1, script terminé immédiatement, aucun reçu, aucun moteur, aucun service |
| Ce qui empêche une installation complète sans clé valide | La suppression du contournement `LABOSURF_DEV=1` (seul point réellement cassé) + l'ordre déjà correct des étapes (licence avant moteurs/service) + la vérification cryptographique Ed25519 déjà correcte |

---

*Rapport généré à la fin de la session. Chaque affirmation est adossée à une commande exécutée et observée dans cette session, ou à une lecture directe du code source actuel des deux dépôts. Aucun commit git n'a été effectué.*
