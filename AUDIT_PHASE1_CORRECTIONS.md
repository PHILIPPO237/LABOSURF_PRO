# AUDIT PHASE 1 — CORRECTIONS CRITIQUES LABOSURF_PRO (2026-09-09)

**Portée** : suite directe de `AUDIT_TECHNIQUE_COMPLET_2026-09-09.md`. Ce rapport ne réaudite pas ce qui était déjà vérifié — il documente uniquement le travail de correction réellement effectué dans cette session, avec preuve d'exécution pour chaque changement. Priorités traitées : **UDP Engine**, **Menu/Administration**, **Xray/VLESS**. Les moteurs hybrides n'ont **pas** été modifiés (voir §5 — raison documentée, pas un oubli).

---

## 1. CORRECTIONS EFFECTUÉES

### 1.1 UDP Engine — deadlock `Server.Close()` (P0 de l'audit précédent)

**Cause confirmée** : `Close()` attendait `tunWG.Wait()` **avant** de fermer le TUN, alors que `tunLoop()` ne peut sortir de son `Read()` bloquant (sans deadline) que si le TUN est fermé. Attente circulaire → deadlock inconditionnel dès qu'aucun paquet n'arrive au moment de l'arrêt (situation normale sur un serveur idle).

**Correction** (`engines/udp/server.go`) : inversion de l'ordre — le TUN est maintenant fermé (sous `tunMu.Lock()`) **avant** `tunWG.Wait()`. La fermeture du descripteur débloque le `Read()` en cours ; `tunLoop` revérifie `s.tun == nil` à l'itération suivante et sort proprement.

**Effet de bord découvert et corrigé** : une fois le deadlock levé, la suite complète de tests a pu tourner jusqu'au bout pour la première fois avec `-race`, révélant une **seconde data race réelle**, jusque-là masquée par le deadlock lui-même (le test suivant n'était jamais atteint) :
- `handleTunnelPacket()` (server.go) lisait `s.tun` directement, sans passer par `tunMu`, en concurrence avec `Close()` qui le remet à `nil`. Corrigé en snapshotant `s.tun` sous `tunMu.RLock()` avant utilisation (même motif que `tunLoop`).

**Bug non lié à la concurrence, découvert en exécutant la suite complète** : `validateBackendAddress()` rejetait **toute** adresse loopback (127.0.0.1), y compris le backend par défaut du projet (`backendAddress()` renvoie `127.0.0.1:22` par défaut — documenté dans README/DEPLOY.md). Cette adresse est fixée par l'opérateur serveur (variable d'environnement `LABOSURF_TCP_BACKEND`), jamais par le client distant — il n'y a donc aucun risque SSRF à valider ici. Corrigé : seules les adresses link-local/multicast/unspecified restent rejetées (loopback autorisé). Ce bug faisait échouer 2 tests (`TestServerAccountsTrafficBothDirections`, `TestServerCleanupClosesStreamOnExpiredSession`) qui n'avaient, semble-t-il, jamais été vérifiés en suite complète depuis son introduction.

**Test de non-régression ajouté** : `TestServerCloseThenRestart` (`engines/udp/tunnel_integration_test.go`) — démarre un serveur, l'arrête **sans aucun trafic TUN en attente** (le cas exact qui provoquait le deadlock), avec timeout de 5s pour échouer vite plutôt que de bloquer, puis répète le cycle (Start → Close → Start → Close) pour vérifier qu'un redémarrage complet fonctionne.

**`mockTUN` du fichier de test corrigé** : `Close()` était un no-op complet (`return nil`) — il ne débloquait jamais `Read()`. Corrigé pour fermer un channel `closed` que `Read()` sélectionne désormais en parallèle du channel de données. Le champ `written` (accédé depuis plusieurs goroutines) est maintenant protégé par un mutex, exposé via une méthode `Written()` thread-safe ; tous les accès directs `.written` dans les tests existants ont été mis à jour en conséquence.

### 1.2 Menu / Administration — `context.Context` nil (P0 de l'audit précédent)

**Cause confirmée** : `cmd/labosurf/menu.go` appelait `e.Start(nil)`, `e.Restart(nil)`, `e.Install(nil, ...)`, `e.Configure(nil, ...)` — un `context.Context` nil, alors que **tous** les moteurs font `context.WithCancel(ctx)` dans leur `Start()`, ce qui panique sur `nil`.

**Correction** : les 4 appels remplacés par `context.Background()` (lignes 353, 367, 406, 426).

**Test de non-régression ajouté** : `TestMenuNeverPassesNilContext` (`cmd/labosurf/menu_nilctx_test.go`) — analyse statique (AST Go, `go/parser`) de `menu.go` qui échoue si un appel à `Start`/`Restart`/`Install`/`Configure` reçoit à nouveau un littéral `nil` en premier argument. **Vérifié réellement efficace** : le test a été temporairement re-cassé (réintroduction de `e.Start(nil)`) pour confirmer qu'il détecte bien la régression (il a échoué comme attendu), puis la correction a été restaurée.

### 1.3 Xray/VLESS — placeholders et clés REALITY (P0/P1 de l'audit précédent, + 3 bugs supplémentaires découverts en corrigeant)

En creusant le point audité ("lien client cassé"), la correction propre a révélé que **la génération de clé REALITY elle-même était totalement non fonctionnelle** — un problème bien plus grave que le simple placeholder identifié initialement :

1. **`RealityKeyPair.PrivateKeyPEM()` (`engines/xray/reality.go`) ne fonctionnait jamais.** Elle appelait `x509.MarshalPKCS8PrivateKey(k.PrivateKey)` sur un `[32]byte` brut — un type que cette fonction ne sait pas sérialiser. Conséquence vérifiée par exécution : **`SaveRealityKeys()`, et donc `EnsureRealityKeys()`, et donc `Install()` du moteur Xray, échouaient à coup sûr avec `x509: unknown key type while marshaling PKCS#8: [32]uint8`** sur toute machine sans clés préexistantes — c'est-à-dire toujours, puisque rien ne pouvait jamais les écrire une première fois. **Corrigé** : stockage direct des 32 octets bruts dans un bloc PEM dédié (`X25519 PRIVATE KEY`), sans passer par x509/PKCS8. `LoadRealityKeys()` simplifié en conséquence (suppression de la logique de fallback fragile qui tentait de compenser le problème sans jamais pouvoir réussir).

2. **`streamSettings.realitySettings.privateKey` (config serveur Xray-core) n'était jamais rempli.** `internal/clientcfg/clientcfg.go` générait `"privateKey": ""` avec le commentaire "Will be generated at install time", mais **rien, nulle part dans le code, ne le remplissait jamais**. `engines/xray/xray_binary.go` injectait à la place un champ `publicKey` qui n'existe pas dans le schéma des inbounds REALITY d'Xray-core (le serveur n'a besoin que de sa clé privée ; la clé publique ne sert qu'au client). Un serveur Xray-core démarré avec `privateKey` vide ne peut pas faire de handshake REALITY. **Corrigé** : `Configure()` injecte désormais la vraie clé privée (`keys.PrivateKeyBase64()`, encodage base64url brut — le format exact attendu par Xray-core, identique à celui produit par son propre outil `xray x25519`) quand `security == "reality"`, et retourne une erreur explicite si les clés REALITY sont introuvables (au lieu d'écrire silencieusement une config cassée).

3. **Lien client VLESS (`vlessLink()`, `internal/clientcfg/clientcfg.go`) contenait littéralement `PUBLIC_KEY_PLACEHOLDER`** dans le champ `pbk=` — confirmé comme bug réel, pas une supposition. **Corrigé** : la fonction charge maintenant la vraie clé publique REALITY (`xray.LoadRealityKeys(xray.RealityDir())`, encodage `PublicKeyForVLESS()` = base64url, le format standard des liens `vless://`) et retourne une erreur explicite si les clés sont indisponibles, plutôt qu'un lien silencieusement cassé.

4. **Bug supplémentaire découvert en vérifiant "le binaire Xray réellement exécuté correspond à ce qui est attendu" (instruction explicite de cette phase)** : `getArchSuffix()` renvoyait `"linux-arm64"` pour les machines arm64, mais **cet asset n'existe pas** dans les releases XTLS/Xray-core — vérifié en interrogeant directement GitHub (`https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-arm64.zip.dgst` → 404). Le vrai nom d'asset est `Xray-linux-arm64-v8a.zip`. **Conséquence : l'installation du moteur Xray échouait à coup sûr (téléchargement 404) sur toute machine arm64** — une cible officiellement supportée par le projet. **Corrigé.**

5. **`getExpectedSHA256()` retournait toujours une chaîne vide** — vérification SHA256 désactivée de fait pour un binaire téléchargé et exécuté en root. **Corrigé** : les vraies valeurs SHA-256 ont été récupérées directement depuis les fichiers `.dgst` signés publiés par XTLS/Xray-core pour la version épinglée (`v26.3.27`), lues en contenu brut (pas via résumé IA, pour éviter toute erreur de transcription sur une donnée qui doit être exacte au caractère près) :
   - `Xray-linux-64.zip` → `23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae`
   - `Xray-linux-arm64-v8a.zip` → `4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c`

   Un commentaire dans le code explique où ces valeurs viennent et qu'elles **doivent être remises à jour manuellement** si `xrayCoreVersion` change (sinon l'installation échouera explicitement par mismatch SHA256 — comportement sûr par défaut, préférable à un binaire non vérifié).

**Tests ajoutés** (le package `engines/xray` n'avait **aucun** test avant cette session) :
- `engines/xray/reality_test.go` : round-trip génération→sauvegarde→chargement des clés REALITY (aurait immédiatement détecté le bug #1) ; idempotence d'`EnsureRealityKeys` ; cohérence des encodages base64.
- `engines/xray/xray_binary_test.go` : le mapping arch→nom d'asset est figé par un test (aurait détecté le bug #4) ; les deux checksums SHA256 connus sont vérifiés non-vides et au bon format (32 octets décodés).
- `internal/clientcfg/clientcfg_test.go` : `TestGenerateXrayLink` renforcé pour vérifier que le lien ne contient plus jamais `PUBLIC_KEY_PLACEHOLDER` et contient bien la vraie clé publique générée ; nouveau test `TestGenerateXrayLinkWithoutRealityKeys` vérifiant qu'une erreur explicite est renvoyée en l'absence de clés (plutôt qu'un lien cassé silencieux).

### 1.4 Hygiène connexe découverte en cours de route

`.gitignore` : 6 règles (`/labosurf`, `/engine`, `/labosurf-mgr.exe`, `/engines/udp/labosurf`, `/engines/udp/labosurf-server`, `/engines/udp/engine`) étaient précédées d'un espace en tête de ligne — en syntaxe `.gitignore`, un espace en tête **fait partie du motif** (seuls les espaces en fin de ligne sont ignorés), donc ces 6 règles n'ont jamais été actives. Corrigé (suppression des espaces). Un binaire de test de 13 Mo (`engines/udp/engine`, généré par mes propres commandes `go build`/`go test` durant cette session) qui n'était de ce fait pas ignoré a été supprimé.

---

## 2. FICHIERS MODIFIÉS

| Fichier | Nature |
|---|---|
| `engines/udp/server.go` | Fix deadlock `Close()` + fix race `handleTunnelPacket` + fix `validateBackendAddress` (loopback) |
| `engines/udp/tunnel_integration_test.go` | `mockTUN.Close()` réellement fonctionnel, `written` thread-safe, nouveau test `TestServerCloseThenRestart` |
| `cmd/labosurf/menu.go` | 4× `nil` → `context.Background()` |
| `cmd/labosurf/menu_nilctx_test.go` | **Nouveau** — test de non-régression (analyse statique) |
| `engines/xray/reality.go` | Fix `PrivateKeyPEM`/`LoadRealityKeys` (génération de clé jamais fonctionnelle), ajout `PrivateKeyBase64()` |
| `engines/xray/xray_binary.go` | Fix injection `privateKey` serveur, fix arch suffix arm64, vrais checksums SHA256, `RealityDir()` partagé |
| `engines/xray/reality_test.go` | **Nouveau** — tests round-trip clés REALITY |
| `engines/xray/xray_binary_test.go` | **Nouveau** — tests arch suffix / SHA256 |
| `internal/clientcfg/clientcfg.go` | Fix `vlessLink()` (vraie clé REALITY au lieu du placeholder) |
| `internal/clientcfg/clientcfg_test.go` | Test existant renforcé + nouveau test (clés absentes → erreur) |
| `.gitignore` | Fix règles inactives (espaces en tête de ligne) |
| `internal/license/license_test.go`, `engines/udp/license.go`, `engines/udp/license_cli_test.go` | Modifications **préexistantes** d'une session antérieure (gofmt + test licence), non touchées cette session — laissées telles quelles, hors périmètre |

Fichiers non modifiés (pré-existants dans l'arborescence, non liés à cette session) : `AUDIT_FINAL_LABOSURF_PRO.md`, `FILES_TO_DELETE.md`, `UNUSED_FILES.md`, `find_unused.py`.

---

## 3. TESTS EXÉCUTÉS ET RÉSULTAT DE CHAQUE TEST

| Commande | Résultat |
|---|---|
| `go build ./...` (racine) | ✅ exit 0 |
| `go vet ./...` (racine) | ✅ propre |
| `go build ./...` (`engines/udp`) | ✅ exit 0 |
| `go vet ./...` (`engines/udp`) | ✅ propre |
| `go test -run TestTunnelMultipleClients -v ./...` (`engines/udp`, **sans** `-race`) | **Avant fix** : timeout (exit 124, 20s+). **Après fix** : ✅ PASS en 0.21s |
| `go test -race -run TestTunnelMultipleClients -v ./...` (`engines/udp`) | **Avant fix** : timeout après 10 min (panic + stack trace complète capturée, voir audit précédent §6). **Après fix** : ✅ PASS en 0.21s, aucune race détectée |
| `go test -race -run TestServerCloseThenRestart -v ./...` (`engines/udp`) | ✅ PASS (0.01s) — vérifié aussi en réintroduisant volontairement le deadlock (échec confirmé avant re-correction) |
| `go test -race ./...` (`engines/udp`, suite complète) | ✅ **181/181 PASS, 0 FAIL**, aucune race détectée (exit 0, 4.5s) |
| `go test ./...` (racine, suite complète) | ✅ **61/61 PASS, 0 FAIL** |
| `go test ./cmd/labosurf/... -v` | ✅ PASS — inclut `TestMenuNeverPassesNilContext` |
| `go test ./cmd/labosurf/... -run TestMenuNeverPassesNilContext -v` avec `nil` réintroduit temporairement | ✅ **FAIL comme attendu** (preuve que le test détecte réellement la régression) |
| `go test ./engines/xray/... -v` | ✅ PASS — 5 tests, tous nouveaux (round-trip clés, idempotence, arch suffix, SHA256) |
| `go test ./internal/clientcfg/... -v` | ✅ PASS — 13 tests, dont les 2 nouveaux/renforcés sur Xray |

**Aucun test n'a été supprimé ou désactivé pour faire passer la suite.** Aucun timeout artificiel n'a été utilisé pour masquer le deadlock — il a été corrigé à la source, et le test de non-régression utilise un timeout de 5s uniquement pour échouer rapidement en cas de régression future (pas pour "cacher" un problème).

---

## 4. PROBLÈMES ENCORE PRÉSENTS (non traités cette session, hors périmètre P0 UDP/Menu/Xray)

Repris de l'audit précédent, non corrigés ici (périmètre de cette phase = UDP, Menu, Xray uniquement) :

- **Hysteria** : bug de bornes d'octets sur le calcul du `sessionID` (`server.go:182,212` vs `303`) — le trafic post-authentification ne peut jamais être transmis.
- **DNSTT** : aucune vérification d'authentification (`PublicKey`/`PrivateKey` déclarés mais jamais lus).
- **SlowDNS** : `buildDNSResponse(nil, ...)` retourne toujours `nil` — le sens retour du tunnel est mort.
- **SSH** : `applySysProcAttr` reste un stub, aucun drop de privilèges.
- **Moteurs hybrides** : voir §5 ci-dessous.

---

## 5. MOTEURS HYBRIDES — STATUT VÉRIFIÉ, NON CORRIGÉ (décision documentée)

Consigne de cette phase : *"Ne corrige cette partie que si les corrections précédentes sont stabilisées"* et *"distingue clairement ce qui est réellement opérationnel de ce qui reste simulé/stub"*.

**Statut re-vérifié par lecture directe du code actuel** (`internal/engineutil/composite_engine.go`, inchangé cette session) : **toujours cassé, exactement comme documenté dans l'audit précédent.**

```go
// composite_engine.go:101-102 (inchangé)
// TODO: Récupérer l'endpoint réel du transport (ex: 127.0.0.1:port)
e.transportEndpoint = "127.0.0.1:0" // placeholder
```

**Pourquoi je n'ai pas corrigé ce point cette session** : contrairement aux trois priorités précédentes, ce défaut n'est pas une erreur ponctuelle corrigeable en quelques lignes — il est **architectural**. L'interface `engine.Engine` (`internal/engine/engine.go`) n'a aucune méthode pour qu'un moteur transport expose son adresse d'écoute réelle après `Start()`. Une correction propre nécessiterait :
1. Étendre l'interface `Engine` (ex: méthode `Endpoint() (string, error)` ou lecture de `Status().ListenAddr`/`Port`),
2. Implémenter cette exposition dans les 6 moteurs concrets (`udp`, `xray`, `hysteria`, `dnstt`, `slowdns`, `ssh`) — actuellement `Status().ListenAddr`/`Port` existent dans le type mais ne sont peuplés nulle part,
3. Modifier `CompositeEngine.Configure()` pour lire cette valeur avec une logique de polling/retry (le port peut ne pas être immédiatement disponible juste après `Start()`).

C'est un changement transverse à 7 fichiers minimum, pas une correction ciblée — cela va à l'encontre de la consigne *"pas de réécriture massive"* pour cette phase, et **les trois priorités précédentes n'étaient elles-mêmes pas stabilisées avant cette session** (deadlock UDP actif, menu qui panique, clés Xray jamais générables). Conformément à l'instruction *"si un problème ne peut pas être corrigé proprement dans cette phase, documente-le au lieu de bricoler une fausse solution"*, je documente ce point plutôt que d'implémenter un correctif partiel qui donnerait une fausse impression de fonctionnement.

**Fonctionnalité hybride actuelle, honnêtement caractérisée** : la validation de composition (rôles VPN/transport, cardinalité) est réelle et fonctionne ; l'orchestration démarrage/arrêt des sous-moteurs dans l'ordre fonctionne ; **la circulation réelle de trafic entre les composants ne fonctionne pas** (endpoint toujours `127.0.0.1:0`). C'est un moteur composite qui démarre plusieurs process indépendants sans les relier réellement — statut **SIMULÉ**, inchangé depuis l'audit précédent.

---

## 6. FONCTIONNALITÉS RÉELLEMENT OPÉRATIONNELLES (vérifiées cette session par test réel)

- **Moteur UDP** : cycle complet Start → trafic (handshake, auth HMAC, routage IP bidirectionnel) → Close → redémarrage → nouveau trafic, **sans deadlock, sans race condition** (`-race` propre sur 181 tests). Mode proxy TCP vers un backend loopback (le cas par défaut documenté) fonctionne à nouveau.
- **Menu d'administration** : les actions Démarrer/Redémarrer/Installer/Configurer un moteur depuis le menu interactif ne peuvent plus paniquer sur un `context` nil.
- **Génération de clés REALITY (Xray)** : génère, sauvegarde et recharge réellement une paire de clés X25519 valide — **ce n'était le cas pour aucune version antérieure du code testée**.
- **Config serveur Xray-core** : contient désormais une vraie `privateKey` REALITY (condition nécessaire, non suffisante en l'absence de tests E2E avec un vrai VPS, pour qu'Xray-core puisse effectivement démarrer le handshake REALITY).
- **Lien client VLESS** : porte la vraie clé publique REALITY du serveur, plus jamais un placeholder.
- **Téléchargement du binaire Xray-core** : URL d'asset correcte pour amd64 **et** arm64 (l'arm64 était cassé — 404 systématique), vérification SHA256 réellement active avec de vrais checksums récupérés depuis la source officielle.

---

## 7. FONCTIONNALITÉS ENCORE SIMULÉES / STUB

- **Moteurs hybrides** (VPN+Transport) : orchestration réelle, trafic simulé — voir §5.
- **Hysteria, DNSTT, SlowDNS** : protocoles maison non conformes aux protocoles de référence, bugs de transmission P0 non corrigés (hors périmètre de cette phase).
- **SSH** : protocole réel, isolation multi-compte simulée (pas de drop de privilèges).
- **Vérification E2E Xray avec un vrai client REALITY (v2rayN/Xray-core)** : toujours non testée — aucun VPS/environnement disponible dans cette session pour valider le handshake REALITY de bout en bout. Les corrections apportées lèvent les obstacles connus qui empêchaient structurellement ce test de pouvoir un jour réussir (clé jamais générée, clé jamais injectée côté serveur, lien client cassé, asset arm64 introuvable), mais **aucune de ces corrections n'a été validée par un handshake réel** — seulement par lecture de code, format de données (longueurs/encodages) et tests unitaires locaux.

---

## 8. RISQUES RESTANTS

- **Checksums SHA256 Xray** : récupérés via un outil de fetch web dont le contenu texte est normalement résumé par un modèle IA (risque de transcription) — j'ai contourné ce risque en lisant le **fichier brut sauvegardé sur disque** (pas le résumé) et en vérifiant sa longueur exacte (64 caractères hex) par commande shell pour les deux valeurs, mais aucune vérification croisée avec une deuxième source indépendante n'a été faite. Recommandation : avant un déploiement réel, revérifier ces deux valeurs manuellement (`curl -fsSL .../Xray-linux-64.zip.dgst`) depuis une machine avec accès réseau direct.
- **`getExpectedSHA256()` doit être mise à jour à chaque changement de `xrayCoreVersion`** — sinon toute mise à jour de version cassera l'installation (échec explicite par mismatch, pas un risque silencieux, mais un point de maintenance à ne pas oublier).
- **Le fix du deadlock UDP n'a été validé qu'en environnement de test (mock TUN + suite `-race`)**, pas sur un vrai VPS avec une vraie interface TUN Linux — le raisonnement (fermeture du fd débloque un `Read()` en cours) est standard pour `os.File` sur Linux, mais n'a pas été observé en conditions réelles.
- **Moteurs hybrides toujours non fonctionnels** — risque inchangé, correctement documenté (§5) plutôt que masqué.
- Les 4 autres moteurs (Hysteria, DNSTT, SlowDNS, SSH) conservent leurs défauts P0/P1 identifiés dans l'audit précédent, non traités cette session.

---

## 9. PROCHAINE PRIORITÉ RECOMMANDÉE

Dans l'ordre :

1. **Test réel sur VPS Linux** du cycle UDP Start→trafic→Stop→Restart et d'une installation Xray complète (téléchargement, clés, config, démarrage, handshake REALITY avec un client v2rayN/Xray-core réel) — c'est la seule façon de transformer les corrections de cette session en preuve de fonctionnement réel plutôt qu'en preuve de cohérence de code.
2. **SlowDNS** (`buildDNSResponse(nil, ...)` toujours `nil`) : c'est le correctif P0 le plus simple et le plus isolé des trois moteurs DNS restants — un bon prochain morceau à corriger avant Hysteria/DNSTT qui demandent plus de travail (bornes de session, implémentation d'authentification complète).
3. **SSH** : implémenter `Credential{Uid,Gid}` dans `applySysProcAttr` — correctif ciblé, à fort impact sécurité.
4. Interface `Engine.Endpoint()` + hybrides — seulement après stabilisation des moteurs individuels restants, comme documenté en §5.

---

*Rapport de phase 1, généré à la fin de la session de corrections. Chaque affirmation est adossée à une commande exécutée et observée dans cette session, ou à une lecture directe du code source actuel.*
