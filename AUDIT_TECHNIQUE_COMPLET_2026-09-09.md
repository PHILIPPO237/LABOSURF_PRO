# AUDIT TECHNIQUE COMPLET — LABOSURF_PRO (2026-09-09)

**Méthode** : ce rapport n'accepte comme preuve ni `go build` réussi, ni un processus démarré, ni un port ouvert, ni un message "success". Chaque verdict ci-dessous est adossé soit à une commande réellement exécutée (avec sortie observée), soit à une lecture directe du code source actuel (file:line cité). Les rapports précédents du dépôt (`AUDIT_FINAL_LABOSURF_PRO.md`, `RAPPORT_AUDIT_UDP.md`, `RAPPORT_XRAY_VLESS.md`, `RAPPORT_CORRECTION_LICENCE.md`, `RAPPORT_CORRECTIONS_UDP.md`, `UNUSED_FILES.md`) ont été utilisés uniquement comme pistes de départ, **jamais comme vérité tenue pour acquise** — chaque affirmation qu'ils contenaient a été revérifiée sur le code courant, et plusieurs se sont révélées fausses ou dépassées (détail §14).

---

## 1. RÉSUMÉ EXÉCUTIF

LABOSURF_PRO compile intégralement (`go build ./...`, `go vet ./...` propres sur les deux modules Go du dépôt), et le système de licence Ed25519 est **réellement** vérifié cryptographiquement (pas un stub). Au-delà de ça, le tableau est nettement plus mitigé que ce que les rapports précédents du dépôt laissaient penser :

- **3 des 6 moteurs réseau (Hysteria, DNSTT, SlowDNS) ont un chemin de données cassé de façon déterministe** — pas "non testé", **cassé** : un bug de bornes d'octets rend Hysteria incapable de transmettre le moindre paquet après authentification ; DNSTT ne vérifie aucune authentification malgré un champ de clé publique déclaré ; SlowDNS ne peut jamais renvoyer de paquet retour (`buildDNSResponse(nil, ...)` retourne toujours `nil`).
- **Le moteur UDP (le plus mature, seul avec des tests) a un deadlock reproductible à 100 %** sur `Server.Close()` — confirmé personnellement par exécution réelle (`go test -run TestTunnelMultipleClients` : timeout après 20s, code de sortie 124, avec ET sans `-race`). C'est une régression introduite par le correctif anti-race-condition de la session précédente.
- **Les moteurs hybrides (VPN+Transport) sont non fonctionnels** — confirmé par lecture directe : `internal/engineutil/composite_engine.go:102` fixe l'endpoint du transport à `127.0.0.1:0` (placeholder), et l'interface `Engine` n'a même pas de méthode pour exposer un endpoint réel. Un commit antérieur (`b7be7b8`) prétendait corriger ce point ; ce n'est pas le cas.
- **Le menu d'administration interactif (le point d'entrée principal documenté) panique** dès qu'on démarre, redémarre, installe ou configure un moteur, à cause d'un `context.Context` nil passé à des fonctions qui appellent `context.WithCancel(nil)` (`cmd/labosurf/menu.go:353,367,406,426`).
- **Le lien client VLESS généré pour Xray contient littéralement la chaîne `"PUBLIC_KEY_PLACEHOLDER"`** au lieu de la vraie clé REALITY — un client Xray réel ne peut pas se connecter avec ce lien.
- **SSH ne droppe jamais les privilèges** (TODO explicite dans le code) — toute session shell authentifiée hérite des privilèges du process serveur (root en déploiement typique).

Le point positif principal : **le moteur Xray est un vrai wrapper autour du binaire officiel Xray-core** (téléchargé, pas réimplémenté maison) — c'est l'architecture la plus solide pour la compatibilité protocolaire, mais sa vérification SHA256 est actuellement désactivée (placeholder vide) et son lien client est cassé comme indiqué ci-dessus.

**Réponse à la question posée par la mission** : *"Qu'est-ce qui empêche actuellement LABOSURF_PRO de créer et administrer des serveurs réellement fonctionnels ?"*
→ Le menu d'administration plante à la première tentative de démarrage d'un moteur (P0 nouveau). Même en contournant ça via la CLI non-interactive, le moteur phare (UDP) ne peut pas être arrêté/redémarré proprement (deadlock P0). Et pour tout moteur autre qu'UDP/SSH/Xray, le trafic ne circule tout simplement pas (bugs P0 dans Hysteria/DNSTT/SlowDNS). Le projet **build** et **s'installe**, mais aucun moteur autre qu'UDP et SSH n'a de chemin de données prouvé fonctionnel, et même UDP a un défaut critique de cycle de vie.

---

## 2. CE QUI FONCTIONNE (CONFIRMÉ)

| Élément | Preuve |
|---|---|
| Build complet (2 modules Go) | `go build ./...` (racine) et `cd engines/udp && go build ./...` (module séparé, go 1.26.0) : exit 0, aucune erreur |
| `go vet` complet | Propre sur les deux modules |
| Vérification cryptographique Ed25519 de la licence | `internal/license/license.go:190` appelle `ed25519.Verify()` avant tout usage des champs métier ; 16/16 + 31+ tests PASS réexécutés dans cette session ; rejet confirmé de signature invalide, payload altéré, mauvaise clé, licence expirée, malformée, signature vide |
| Xray = vrai binaire Xray-core officiel (pas une réimplémentation maison) | `engines/xray/xray_binary.go` télécharge `github.com/XTLS/Xray-core/releases/...`, exécute `xray run -config ...` — architecture saine pour la compatibilité protocolaire |
| SSH = vrai protocole SSH (`golang.org/x/crypto/ssh`) | Auth par clé publique ed25519 réelle par compte, avec expiration (`engines/ssh/server.go:79-111`) |
| Authentification HMAC-SHA256 challenge/réponse du moteur UDP | Observée en fonctionnement réel (log complet capturé), avec anti-spoofing IP source vérifié |
| Cross-compilation réelle vers linux/arm64 et android/arm64 | Binaires ELF réels produits et vérifiés (`file` confirme l'architecture cible, y compris l'interpréteur Android `/system/bin/linker64`) |
| Persistance store (comptes, quotas) | `internal/store/store.go` : écriture atomique tmp+rename, `sync.RWMutex` correctement posé sur toutes les opérations |
| Scan de secrets | Aucune clé privée commitée dans l'historique git (`git log --all --diff-filter=A` vide pour `*admin*.key`/`*.pem`), `.gitignore` correct |
| Installateur Android/Termux honnête sur son périmètre | `labosurf-android.sh` n'installe que la CLI d'administration (pas de serveur VPN sur Android) — cohérent avec le fait qu'Android non-rooté ne peut pas ouvrir `/dev/net/tun` (le projet le documente lui-même dans `ANDROID_CLIENT.md`) |
| Téléchargements avec vérification SHA-256 réelle (installateur principal, binaires `labosurf`) | `labosurf-pro.sh: download_asset()` vérifie contre un manifeste `SHA256SUMS` + smoke test avant installation |

---

## 3. CE QUI FONCTIONNE PARTIELLEMENT

| Élément | État réel |
|---|---|
| Moteur UDP | Handshake, auth, routage IP bidirectionnel démontrés en local (TUN simulé) ; **mais** `Close()`/`Restart()` peut deadlocker indéfiniment (§6) |
| Moteur Xray | Vrai binaire officiel, config REALITY générée server-side, **mais** SHA256 de téléchargement désactivé et lien client VLESS cassé (§7) |
| Hybrides — validation de composition | La matrice provides/requires existe et bloque les combinaisons multi-VPN/multi-transport invalides, **mais** la circulation réelle du trafic n'existe pas (§9) |
| Gestion des abonnés/comptes | CRUD complet et réaliste dans `internal/store`, **mais** les chemins CRUD centraux (`CreateAccount`, `DeleteAccount`, `Renew`) n'ont pas de tests dédiés |
| CI/CD | Compile et cross-compile réellement tous les artefacts déclarés (18 binaires moteurs + gestionnaire, 3 architectures), **mais** `workflow_dispatch` manuel casse sur une variable non définie (§8) |

---

## 4. CE QUI EST SIMULÉ / STUB

| Élément | Preuve |
|---|---|
| Protocole Hysteria | Protocole UDP maison avec magic numbers propriétaires (`"HHAL"`, `"HHAT"`, etc.), pas QUIC/TLS 1.3 — **incompatible avec un client Hysteria2 réel** malgré un nom identique |
| Protocole DNSTT | Tunnel base32-sur-DNS maison, pas le protocole Noise+KCP du vrai dnstt |
| Protocole SlowDNS | Tunnel DNS maison avec signature ed25519 (ça, c'est réel), mais pas le protocole SlowDNS de référence |
| `applySysProcAttr` (SSH) | `engines/ssh/server.go:22-30` — stub explicite, ne pose aucun `Credential{Uid,Gid}` |
| `getExpectedSHA256()` (Xray) | `engines/xray/xray_binary.go:78-92` — retourne toujours une chaîne vide ; la vérification est de fait désactivée |
| `vlessLink()` (Xray) | `internal/clientcfg/clientcfg.go:122-128` — clé REALITY toujours remplacée par le littéral `PUBLIC_KEY_PLACEHOLDER` |
| `transportEndpoint` des hybrides | `internal/engineutil/composite_engine.go:101-102` — toujours `127.0.0.1:0`, TODO explicite dans le code |
| `tun_android.go` | Implémentation ioctl réelle de `/dev/net/tun`, mais documentée par le projet lui-même (`ANDROID_CLIENT.md`) comme non fonctionnelle sur Android non-rooté — code compilé (nécessaire au cross-build android/arm64) mais fonctionnellement inatteignable dans le design produit actuel (Android = CLI admin seulement) |

---

## 5. CE QUI NE FONCTIONNE PAS

| Élément | Preuve concrète |
|---|---|
| Menu d'administration interactif — démarrer/redémarrer/installer/configurer un moteur | `cmd/labosurf/menu.go:353,367,406,426` passent `nil` comme `context.Context` ; tous les moteurs appellent `context.WithCancel(ctx)` dans leur `Start()`, ce qui **panique** sur `nil` (`context.WithCancel(nil)` → `"cannot create context from nil parent"`, reproduit et confirmé). Aucun `recover()` dans le chemin du menu |
| `Server.Close()`/`Restart()` du moteur UDP | Deadlock reproductible à 100 % — voir §6, preuve complète avec stack trace |
| Transmission de données Hysteria après authentification | `engines/hysteria/server.go:182,212` (Hello/Auth) calculent le sessionID sur `pkt[4:12]` (8 octets), `server.go:303` (Data) le calcule sur `pkt[4:20]` (16 octets) — la session n'est **jamais** retrouvée, tout paquet de données est silencieusement rejeté (`server.go:307-313`) |
| Authentification DNSTT | `DNSTTUser.PublicKey`/`PrivateKey` (`engines/dnstt/server.go:33-38`) sont déclarés mais **jamais lus ni vérifiés** — `handleQuery` accepte tout client non authentifié et ouvre un tunnel vers le backend configuré |
| Trafic retour SlowDNS | `buildDNSResponseForPayload()` appelle `buildDNSResponse(nil, responseData)` (`engines/slowdns/config.go:110-116`) ; la première ligne de `buildDNSResponse` est `if len(query) < 12 { return nil }` (config.go:78) — `len(nil) == 0`, donc **retourne toujours nil**, sans exception. Le sens backend→client du tunnel est mort de façon inconditionnelle |
| Circulation réelle de trafic dans un hybride VPN+Transport | Aucun mécanisme de récupération d'endpoint réel — voir §4 |
| Lien client VLESS Xray fonctionnel | Contient toujours `PUBLIC_KEY_PLACEHOLDER` — un client REALITY réel échoue le handshake |

---

## 6. PROBLÈME LE PLUS CRITIQUE (NOUVEAU, NON DOCUMENTÉ AILLEURS) — DEADLOCK `Server.Close()` DU MOTEUR UDP

**Fichier** : `engines/udp/server.go`
**Fonctions concernées** : `Close()` (ligne ~100-134), `tunLoop()` (ligne ~223-260), `tun_linux.go: Read()` (ligne 110-115)

**Cause** : la session de travail précédente a corrigé une data race réelle en ajoutant `s.tunWG.Wait()` dans `Close()` **avant** de fermer le TUN :

```go
// server.go:113
s.tunWG.Wait()          // attend que tunLoop() se termine...
// ligne 116-123 : ferme le TUN SEULEMENT APRÈS
s.tunMu.Lock()
if s.tun != nil { s.tun.Close(); s.tun = nil }
s.tunMu.Unlock()
```

Mais `tunLoop()` est bloqué dans `tun.Read(buffer)` (server.go:245), un appel bloquant **sans deadline** sur `os.File.Read` (`tun_linux.go:110-115` ne pose aucun `SetReadDeadline`). Ce `Read()` ne peut se débloquer que si le TUN est fermé — mais le TUN n'est fermé qu'**après** que `tunWG.Wait()` ait réussi. C'est un ordre d'attente circulaire : `Close()` attend `tunLoop`, `tunLoop` attend que `Close()` ferme le TUN. **Deadlock inconditionnel dès qu'aucun paquet n'arrive sur le TUN au moment de l'arrêt** — ce qui est la situation normale sur un serveur VPS peu chargé.

**Preuve d'exécution réelle** (cette session, reproduit deux fois) :
```
$ cd engines/udp && timeout 20 go test -run TestTunnelMultipleClients -v ./...
[...test passe son scénario, log "Arrêt UDP Engine..." s'affiche...]
$ echo $?
124   # tué par timeout — la commande n'est jamais revenue
```

Et avec `-race` (timeout du test lui-même à 10 min, stack trace complète capturée) :
```
goroutine 21 [sync.WaitGroup.Wait]:
  labosurf/engine.(*Server).Close.func1()
      engines/udp/server.go:113 +0x30d
goroutine 5 [chan receive]:
  labosurf/engine.(*mockTUN).Read(...)
      engines/udp/tunnel_integration_test.go:32
  labosurf/engine.(*Server).tunLoop(...)
      engines/udp/server.go:245
panic: test timed out after 10m0s
FAIL	labosurf/engine	600.337s
```

**Conséquence en production** : `labosurf engine stop udp` ou `restart udp` sur un serveur réel peut se bloquer indéfiniment, sans message d'erreur, dès que le TUN n'a pas de paquet entrant au moment exact de l'arrêt. C'est l'exact opposé de l'objectif final de la mission ("arrêt / redémarrage fiable").

**Correction minimale recommandée** : inverser l'ordre — fermer le TUN (ce qui débloquera le `Read()` en cours) **avant** d'attendre `tunWG.Wait()`, avec le mutex adapté pour éviter de réintroduire la race d'origine (ex. : fermer sous `tunMu.Lock()`, puis attendre `tunWG.Wait()` sans le mutex tenu). Alternative plus robuste : poser un `SetReadDeadline` périodique sur le TUN pour que `tunLoop` revienne régulièrement vérifier `ctx.Done()` même sans trafic.

**Test de confirmation** : relancer `go test -run TestTunnelMultipleClients -v ./...` (avec et sans `-race`) et vérifier qu'il se termine en moins de 5 secondes avec `PASS`, au lieu de timeout.

---

## 7. MOTEUR XRAY/VLESS — DÉTAIL

Architecture saine (wrapper du vrai Xray-core officiel, pas de réimplémentation), mais deux défauts bloquent une utilisation réelle :

1. **`getExpectedSHA256()` (`engines/xray/xray_binary.go:78-92`) retourne toujours `""`** → la vérification SHA256 du binaire Xray-core téléchargé (`downloadAndInstallBinary`, ligne 220) ne s'exécute jamais (`if expectedSHA != ""`). Le binaire exécuté en root n'est authentifié que par TLS du téléchargement HTTPS, sans épinglage. **P1 sécurité.**
2. **`vlessLink()` (`internal/clientcfg/clientcfg.go:122-128`) n'a même pas de paramètre pour la clé REALITY** — elle est appelée `vlessLink(uuid, host, port)` (lignes 72, 317) et injecte systématiquement le littéral `PUBLIC_KEY_PLACEHOLDER` dans le champ `pbk=` de l'URI. **Tout lien client généré par la plateforme est cassé** ; un client Xray/v2rayN réel échouera le handshake REALITY avec ce lien. **P0 — jamais signalé dans les rapports précédents.**

---

## 8. CI / BUILD / BINAIRES — DÉTAIL

- Les 3 architectures annoncées (linux/amd64, linux/arm64, android/arm64) sont réellement cross-compilées pour les 6 moteurs + le gestionnaire (vérifié en relançant les commandes localement, binaires ELF réels produits).
- **Bug confirmé** : `.github/workflows/release.yml:130` référence `$INPUT_VERSION` sans jamais le définir via `env:` — sous `set -u`, un déclenchement manuel (`workflow_dispatch`) échoue avant `gh release create`. Les releases déclenchées par tag (`git push origin vX.Y.Z`) ne sont pas affectées. **P1.**
- `labosurf-pro.sh:498-539` : un échec d'installation de moteur tiers n'est que loggé en warning, et la validation finale (`final_check()`) ne vérifie que le service UDP de base, jamais les services par moteur sélectionnés — un moteur mal installé est rapporté "installé" à tort. **P1.**
- Les fichiers `*_stub.go` et `tun_android.go` que `UNUSED_FILES.md` recommandait de supprimer sont en réalité nécessaires (build tags `//go:build android`, `//go:build !linux`, etc.) pour que la cross-compilation android/arm64 réussisse — **cette recommandation de nettoyage était incorrecte et ne doit pas être appliquée telle quelle.**

---

## 9. MOTEURS HYBRIDES — DÉTAIL

Confirmé par lecture directe (résout la contradiction entre `AUDIT_FINAL_LABOSURF_PRO.md` qui disait "cassé" et le message du commit `b7be7b8` qui prétendait l'avoir corrigé — **`AUDIT_FINAL` avait raison, le message de commit était trompeur**) :

```go
// internal/engineutil/composite_engine.go:98-102
if err := transport.Start(ctx); err != nil { ... }
// TODO: Récupérer l'endpoint réel du transport (ex: 127.0.0.1:port)
e.transportEndpoint = "127.0.0.1:0" // placeholder
```

Le défaut est **architectural**, pas juste un oubli ponctuel : l'interface `engine.Engine` (`internal/engine/engine.go:115-158`) n'a **aucune méthode** pour qu'un moteur transport expose son adresse d'écoute réelle après `Start()`. `EngineStatus` a bien des champs `Port`/`ListenAddr`, mais `CompositeEngine.Configure()` ne les lit jamais.

De plus, la matrice provides/requires (`internal/engineutil/compat.go`) n'est utilisée que pour générer des avertissements informatifs — elle n'est jamais appelée pour bloquer réellement une composition incompatible au démarrage.

**Aucun test ne démarre réellement deux moteurs et ne vérifie qu'un octet traverse de l'un à l'autre** (`internal/engineutil` : 7/7 tests PASS, mais tous sur la logique de validation/warnings, aucun sur le trafic réel).

**Verdict : hybrides = cosmétiques, pas fonctionnels.**

---

## 10. LICENCE ET SÉCURITÉ — DÉTAIL

- Vérification Ed25519 **réelle**, confirmée par lecture de `internal/license/license.go:190` (`ed25519.Verify()` appelé avant tout champ métier) et par ré-exécution des tests (16/16 + 31+ PASS).
- Bypass explicite et documenté : `LABOSURF_DEV=1` dans `labosurf-pro.sh:380` saute l'activation — c'est un flag de dev visible dans le script shell, pas une faille cachée dans le code crypto Go.
- **Modèle de licence "1 clé = 1 install, 3h" n'a aucune autorité serveur** : `Activate()` (`internal/license/license.go:214-242`) écrit juste un fichier reçu local (`.install_<ID>.receipt`). Aucun appel réseau nulle part dans le code de licence (`grep` de `net.`/`http` vide). **Une même licence peut donc être réutilisée sur autant de machines qu'on veut**, ou après suppression du fichier de reçu / changement de `LABOSURF_DATA_DIR`. **P2 — limitation de modèle métier, pas un défaut cryptographique.**
- Scan de secrets : aucun leak dans l'historique git. Un fichier `internal/license/labosurf_admin.key` (clé privée) existe localement, non commité, correctement gitignored — recommandation : le supprimer de cet environnement (il ne devrait exister que dans `LABOSURF_LICENSE_MAKER`). Un autre fichier local `internal/license/labosurf_pub.key` contient une clé publique **différente** de celle utilisée en production (`7b27e598...`) — probablement un résidu de test local, à supprimer pour éviter toute confusion. **P3.**

---

## 11. ARCHITECTURE — APTITUDE À L'ÉVOLUTION VERS UNE API

L'architecture (interface `engine.Engine` uniforme, `internal/store` central, `internal/srvcfg`/`internal/clientcfg` séparés de la logique moteur) est raisonnablement propre pour brancher une future couche API sans réécriture complète — **à condition de régler d'abord les défauts identifiés ici**, en particulier :
- Le passage de `context.Context` doit être audité partout (le bug `nil` du menu montre qu'il n'est pas fiable aujourd'hui) avant qu'une API HTTP ne s'appuie sur les mêmes chemins de code.
- L'interface `Engine` devra gagner une méthode d'exposition d'endpoint réel (nécessaire de toute façon pour réparer les hybrides) — une API aura le même besoin pour renvoyer un statut précis au client.
- `internal/store` a déjà le verrouillage nécessaire (`sync.RWMutex`) pour être appelé depuis des handlers HTTP concurrents.

Pas de refonte nécessaire, mais les correctifs P0/P1 de ce rapport sont un prérequis raisonnable avant d'exposer ces chemins de code via une API.

---

## 12. COMPATIBILITÉ ANDROID / TERMUX / PC

| Aspect | État |
|---|---|
| Installateur Android (`labosurf-android.sh`) | Honnête sur son périmètre : CLI admin uniquement, chemins `$PREFIX` corrects, pas de systemd, vérification SHA256 identique à l'installateur Linux |
| Serveur VPN sur Android | Non prévu par le design (nécessiterait `VpnService`, documenté comme guide futur dans `ANDROID_CLIENT.md`, pas implémenté — c'est cohérent, pas un manque caché) |
| `tun_android.go` | Compile (nécessaire au build android/arm64) mais non fonctionnel sur Android non-rooté par admission du projet lui-même — code mort en pratique |
| Installateur VPS (`labosurf-pro.sh`) | Linux/systemd uniquement, échoue explicitement et proprement sur non-Debian (`apt`/`systemd` requis) — comportement documenté et assumé, pas un bug caché |
| Supervision | systemd sur Linux VPS ; **aucune supervision applicative sur Android/Termux** (pas de restart-on-crash dans `internal/engine/manager.go`) — cohérent avec le fait qu'Android n'exécute pas les moteurs, mais à garder en tête si ça change |

---

## 13. QUALITÉ DU CODE

- 3 TODO significatifs trouvés dans le code non-test (`composite_engine.go:101`, `engines/ssh/server.go:28`, `engines/udp/engine.go:63`) — tous les trois correspondent à des défauts déjà documentés ci-dessus, aucun TODO caché supplémentaire.
- `applySysProcAttr(cmd)` appelé deux fois de suite dans `engines/ssh/server.go:273,278` (doublon inoffensif mais à nettoyer). **P4.**
- Aucune fuite de ressources/goroutine détectée au-delà du deadlock §6.
- `internal/store` : verrouillage correct partout.
- Zéro fichier `_test.go` pour `hysteria`, `dnstt`, `slowdns`, `ssh` — **aucun des bugs P0 listés en §5 n'aurait été caché par un test de boucle locale basique** (session-ID, `buildDNSResponse(nil,...)`, auth DNSTT absente) — ce sont exactement le genre de bugs qu'un test d'intégration minimal (client↔serveur en loopback, vérifier qu'un octet traverse dans les deux sens) aurait attrapés immédiatement.

---

## 14. DOCUMENTATION VS RÉALITÉ

| Affirmation documentée | Statut réel |
|---|---|
| README : "Xray — Proxy VLESS/Trojan natif (compatible clients V2ray/Xray)" | [PARTIEL] — vrai binaire officiel, mais lien client cassé (§7) et Trojan a été retiré du code (seul VLESS est généré dans `clientcfg.go`, la description dans `xray_binary.go:521` mentionne encore Trojan/VMess/Shadowsocks à tort) |
| README : "Hysteria — Relais UDP haute performance" | SIMULÉ — protocole maison incompatible avec Hysteria2, ET trafic post-auth cassé (§5) |
| README : "SlowDNS — auth Ed25519, backend TCP" | [PARTIEL] — auth ed25519 réelle, mais retour de trafic mort (§5) |
| README : "DNSTT — Tunnel DNS quasi-indétectable (sessions, fragmentation)" | SIMULÉ — sessions existent mais authentification absente (§5) |
| README : "SSH — auth Ed25519, shell non-root" | [PARTIEL] — auth ed25519 réelle et CONFIRMÉE ; "non-root" est FAUX en l'état, `applySysProcAttr` est un stub (§4) |
| README : "Hybrides validés à la création" | [PARTIEL] — validation de rôles oui, fonctionnement réel non (§9) |
| PROTOCOL.md | Remarquablement honnête — documente lui-même l'absence de chiffrement et l'absence de protection anti-rejeu du protocole UDP. Aucune sur-promesse trouvée dans ce document. |
| ANDROID_CLIENT.md | Honnête — présenté explicitement comme un guide pour un client à construire, pas une fonctionnalité existante. |
| `AUDIT_FINAL_LABOSURF_PRO.md` (rapport précédent) | Verdict hybrides confirmé exact ; verdict "licence FONCTIONNEL" confirmé exact |
| `RAPPORT_AUDIT_UDP.md` (rapport précédent) | "TestTunnelMultipleClients ✅ PASS*" — **FAUX dans l'état actuel du code** : ce test hangue désormais de façon déterministe (régression introduite après ce rapport, voir §6) |
| `RAPPORT_CORRECTIONS_UDP.md` (rapport précédent) | Prédisait "go test -race ... PASS prévu" sans l'avoir exécuté — **infirmé** : le fix a introduit un deadlock au lieu de le résoudre |
| `UNUSED_FILES.md` (rapport précédent) | Recommandation de suppression des fichiers `*_stub.go`/`tun_android.go` — **incorrecte**, ces fichiers sont nécessaires aux build tags multi-plateformes |

---

## 15. TABLEAU FINAL

| Fonction | Code présent | Fonctionne réellement | Testé | Niveau de confiance | Problèmes |
|---|---|---|---|---|---|
| Gestion des abonnés | Oui | Oui (CRUD complet) | Partiel (grants/secrets testés, CRUD cœur non testé) | Moyen-élevé | Pas de test dédié `CreateAccount`/`DeleteAccount`/`Renew` |
| Licences (Ed25519) | Oui | **Oui** | Oui (47+ tests) | Élevé | Modèle "1 clé=1 install" sans autorité serveur (P2) |
| Création de serveurs (profil) | Oui | Oui | Oui | Élevé | — |
| UDP | Oui | Partiel (data path OK, cycle de vie cassé) | Oui (mais suite complète hangue) | Moyen | **Deadlock Close()/Restart() (P0)** |
| Xray | Oui (vrai binaire officiel) | Non prouvé (lien client cassé) | Non | Faible-moyen | Lien VLESS cassé (P0), SHA256 désactivé (P1) |
| Hysteria | Oui (protocole maison) | **Non** (trafic post-auth cassé) | Aucun | Très faible | Bug bornes sessionID (P0) |
| SlowDNS | Oui (protocole maison) | **Non** (retour trafic mort) | Aucun | Très faible | `buildDNSResponse(nil,...)` (P0) |
| DNSTT | Oui (protocole maison) | **Non** (aucune auth) | Aucun | Très faible | Auth non implémentée (P0) |
| SSH | Oui (vrai protocole) | Oui pour le protocole, non pour l'isolation | Aucun | Moyen | Pas de drop de privilèges (P0 sécurité) |
| Moteurs hybrides | Oui (orchestration) | **Non** (pas de vrai trafic) | Partiel (validation seulement) | Très faible | Endpoint placeholder (P0), pas dans l'interface Engine |
| Binaires | Oui | Oui (cross-compilation vérifiée) | Oui | Élevé | — |
| Compilation | Oui | Oui | Oui | Élevé | — |
| Installation VPS | Oui | Partiel | Partiel | Moyen | Échecs de moteur non détectés (P1), workflow_dispatch cassé (P1) |
| Démarrage (menu interactif) | Oui | **Non — panique** | Non | Très faible | `context` nil (P0, nouveau) |
| Démarrage (CLI directe) | Oui | Oui | Non | Moyen | Contourne le bug du menu |
| Arrêt | Oui | **Non fiable (UDP)** | Oui (révèle le bug) | Faible (UDP), Moyen (autres) | Deadlock UDP (P0) |
| Forwarding | Oui | Partiel (UDP/SSH oui, 3 autres non) | Partiel | Faible-moyen | Voir lignes moteurs |
| Supervision | Oui (systemd Linux) | Oui sur Linux VPS, absente en Android | Non | Moyen | Pas de restart-on-crash applicatif |
| Android/Termux | Oui (CLI admin) | Oui pour son périmètre restreint | Non | Moyen | Périmètre volontairement limité, cohérent |
| Linux/PC | Oui | Oui | Oui | Élevé | — |

---

## 16. CLASSEMENT DES PROBLÈMES

### P0 — bloque complètement le fonctionnement

| # | Fichier | Fonction | Cause | Conséquence | Correction | Test de confirmation |
|---|---|---|---|---|---|---|
| 1 | `engines/udp/server.go:113` | `Close()` | `tunWG.Wait()` attend `tunLoop` qui attend que `Close()` ferme le TUN (ordre circulaire) | `stop`/`restart` du moteur UDP peut se bloquer indéfiniment en production | Fermer le TUN avant d'attendre le WaitGroup, ou poser un `SetReadDeadline` périodique sur le TUN | `go test -run TestTunnelMultipleClients -v ./...` doit finir en <5s |
| 2 | `cmd/labosurf/menu.go:353,367,406,426` | `menuStart`/`menuRestart`/`menuInstall`/`menuConfigure` | `nil` passé comme `context.Context` à des fonctions qui font `context.WithCancel(nil)` | Le menu interactif (point d'entrée principal documenté) panique dès qu'on démarre/redémarre/installe/configure un moteur | Remplacer `nil` par `context.Background()` | Appeler `e.Start(nil)` dans un test unitaire avant/après le fix |
| 3 | `internal/engineutil/composite_engine.go:101-102` | `Configure()` | `transportEndpoint` toujours `"127.0.0.1:0"`, aucune méthode `Endpoint()` sur l'interface `Engine` | Aucun hybride VPN+Transport ne peut jamais faire circuler de trafic réel | Ajouter une méthode d'exposition d'endpoint réel à l'interface `Engine`, la lire après `transport.Start()` | Démarrer un hybride réel dans un test, vérifier que l'endpoint injecté correspond au port réellement ouvert |
| 4 | `engines/hysteria/server.go:182,212,303` | `handleHello`/`handleAuth` vs `handleData` | Bornes d'octets différentes pour calculer le `sessionID` (`pkt[4:12]` vs `pkt[4:20]`) | Aucune donnée ne peut être transmise après authentification | Unifier le calcul du sessionID sur les mêmes bornes partout | Test loopback : auth puis envoi d'un paquet de données, vérifier réception côté backend |
| 5 | `engines/dnstt/server.go` (`handleQuery`, `DNSTTUser`) | Auth absente | `PublicKey`/`PrivateKey` déclarés mais jamais lus/vérifiés | Tunnel ouvert à tout client non authentifié vers le backend configuré | Implémenter la vérification de clé publique avant tout forwarding | Test : requête sans clé valide doit être rejetée |
| 6 | `engines/slowdns/config.go:78,110-116` | `buildDNSResponseForPayload` → `buildDNSResponse(nil, ...)` | `buildDNSResponse` retourne toujours `nil` si `query` est `nil` (`len(nil)<12`) | Aucune réponse ne peut jamais être renvoyée au client — tunnel unidirectionnel mort | Faire circuler la vraie requête DNS entrante jusqu'à `buildDNSResponse`, pas `nil` | Test loopback : vérifier qu'un paquet backend→client est effectivement émis |
| 7 | `internal/clientcfg/clientcfg.go:122-128` | `vlessLink()` | Pas de paramètre pour la clé REALITY, littéral `PUBLIC_KEY_PLACEHOLDER` toujours injecté | Tout lien client Xray distribué est cassé pour un vrai client REALITY | Ajouter le paramètre clé publique REALITY (déjà généré côté serveur par `EnsureRealityKeys`) et l'injecter réellement | Générer un lien, vérifier que `pbk=` correspond à la vraie clé publique du serveur |
| 8 | `engines/ssh/server.go:22-30` | `applySysProcAttr` | Stub, aucun `Credential{Uid,Gid}` posé | Toute session SSH authentifiée obtient les privilèges du process serveur (root en déploiement typique) — aucune isolation multi-compte malgré le modèle de comptes | Résoudre l'UID/GID de l'utilisateur cible et poser `syscall.Credential` | Lancer une session avec un compte non-root configuré, vérifier `id`/`whoami` dans le shell obtenu |

### P1 — fonctionnalité principale inutilisable

- `engines/xray/xray_binary.go:78-92` : `getExpectedSHA256()` toujours vide → vérification SHA256 désactivée pour un binaire exécuté en root.
- `.github/workflows/release.yml:130` : `$INPUT_VERSION` non défini → `workflow_dispatch` manuel échoue.
- `labosurf-pro.sh:498-539` : échec d'installation de moteur tiers seulement loggé, validation finale ne couvre pas les moteurs sélectionnés.
- `engines/hysteria/server.go:320-326` : HMAC calculé avec le mot de passe du premier utilisateur activé, pas celui de la session authentifiée — casse l'intégrité multi-utilisateur.
- `engines/slowdns/server.go:213-232` : `findUserBySessionID` retourne "le premier utilisateur valide" (commenté explicitement dans le code) au lieu de dériver l'identité de la session — pas d'isolation multi-utilisateur.
- `engines/dnstt/server.go:224-225` : `backendToClient` déclarée, lancée en goroutine, corps vide — code mort inoffensif mais trompeur.
- Aucun test d'intégration end-to-end pour les hybrides ni pour hysteria/dnstt/slowdns/ssh — les bugs P0 ci-dessus auraient été attrapés par un test loopback basique.
- `internal/store` : CRUD cœur (`CreateAccount`, `DeleteAccount`, `Renew`, `Subscribe`) sans tests dédiés.

### P2 — problème important mais contournable

- Modèle de licence "1 clé = 1 install, 3h" sans autorité serveur — réutilisable sur plusieurs machines (`internal/license/license.go:214-242`).
- Services systemd par moteur tournent tous en `User=root` sans `DynamicUser`/`Credential` (cohérent avec le manque de drop de privilèges applicatif).
- `internal/license/labosurf_admin.key` présent localement (non commité, correctement gitignored) — à supprimer de cet environnement par hygiène.
- Aucune supervision applicative (restart-on-crash) — dépend entièrement de systemd, absent sur Android.

### P3 — amélioration

- `internal/license/labosurf_pub.key` contient une clé différente de la clé de production — résidu de test à supprimer.
- Description de `XrayCoreEngine` (`xray_binary.go:521`) mentionne encore Trojan/VMess/Shadowsocks alors que seul VLESS est généré.
- `release/build.sh` obsolète par rapport à la CI (ne build que le binaire UDP amd64/arm64).
- `UNUSED_FILES.md`/`FILES_TO_DELETE.md` du dépôt contiennent des recommandations incorrectes (fichiers stub nécessaires) — à ne pas appliquer sans revérification.

### P4 — optimisation / nettoyage

- `engines/ssh/server.go:273,278` : `applySysProcAttr(cmd)` appelé deux fois de suite (doublon inoffensif).
- `dist/` contient des binaires de test locaux obsolètes (gitignored, sans impact).

---

## 17. TESTS EFFECTUÉS CETTE SESSION (exhaustif)

```
go build ./...                                          (racine)                exit 0
go vet ./...                                             (racine)                exit 0
go test ./...                                            (racine)                7 packages OK, 14 "no test files"
cd engines/udp && go build ./...                                                 exit 0
cd engines/udp && go vet ./...                                                   exit 0
cd engines/udp && go test -run TestTunnelMultipleClients -v ./...   (sans -race) TIMEOUT (exit 124, 20s)
cd engines/udp && go test -race -run TestTunnelMultipleClients -v ./...          TIMEOUT (10min, panic + stack trace)
go build ./engines/hysteria/... ./engines/dnstt/... ./engines/slowdns/... ./engines/ssh/... ./engines/xray/...   exit 0
go vet   (idem)                                                                  exit 0
go test ./internal/license/... -v                                                16/16 PASS
go test ./engines/udp/... -run License -v                                        31+/31+ PASS
go test ./internal/engineutil/... ./internal/engine/... ./internal/store/... ./internal/srvcfg/... -v   PASS (aucun test sur trafic réel)
GOOS=linux  GOARCH=arm64   go build -o /tmp/test-arm64   ./...  (engines/udp)    ELF arm64 réel produit
GOOS=android GOARCH=arm64  go build -o /tmp/test-android ./...  (engines/udp)    ELF arm64 Android réel produit (linker64)
grep -rn secrets/keys sur tout le dépôt + git log --all --diff-filter=A          aucun leak trouvé
```

## 18. TESTS MANQUANTS (les plus critiques)

- Un test loopback minimal par moteur (hysteria/dnstt/slowdns/ssh) qui envoie un octet client→serveur→backend et vérifie sa réception — aurait attrapé les 3 bugs P0 de transport en quelques minutes de travail.
- Un test d'intégration hybride réel : démarrer VPN+Transport, vérifier qu'un octet traverse effectivement la chaîne.
- Un test du chemin `menu.go` (ou refactor pour le rendre testable) qui aurait attrapé le `context` nil immédiatement.
- Un test de cycle de vie complet UDP (`Start` → `Stop` → `Start` à nouveau) sous charge nulle, qui aurait attrapé le deadlock immédiatement (le test existant `TestTunnelMultipleClients` l'attrape déjà — il suffit de le faire tourner sans le filtrer/l'exclure).
- Tests dédiés CRUD `internal/store` (comptes).

## 19. PLAN DE CORRECTION PRIORITAIRE

1. **Corriger le deadlock `Close()` UDP** (§16 P0#1) — le plus urgent car il touche le moteur le plus mature et casse l'objectif final "arrêt/redémarrage fiable".
2. **Corriger le `context` nil dans `menu.go`** (§16 P0#2) — un `context.Background()` à la place de 4 `nil`, correction triviale mais bloque 100% des utilisateurs du menu interactif.
3. **Corriger le lien VLESS Xray** (§16 P0#7) — sans ça, Xray (le moteur le plus solide protocolairement) reste inutilisable par un vrai client.
4. **Réactiver la vérification SHA256 Xray** (§16 P1) — sécurité, avant tout déploiement.
5. **Décider du sort de Hysteria/DNSTT/SlowDNS** : soit corriger les 3 bugs P0 (bornes sessionID, auth DNSTT, `buildDNSResponse`), soit les marquer clairement "expérimental/non fonctionnel" dans le README tant qu'ils ne le sont pas — la documentation actuelle sur-promet.
6. **Implémenter le drop de privilèges SSH** (§16 P0#8) — risque de sécurité concret en déploiement réel.
7. **Ajouter une méthode d'endpoint réel à l'interface `Engine`** puis corriger les hybrides (§16 P0#3) — nécessite une petite extension d'interface, à faire une fois que les moteurs individuels sont stabilisés.
8. Ajouter les tests loopback minimaux par moteur (§18) en parallèle de chaque correction, pour verrouiller la non-régression.

---

*Rapport produit par audit direct (commandes exécutées et observées) + 4 revues indépendantes en parallèle (moteurs hysteria/dnstt/slowdns/ssh ; build/CI/binaires/installateurs ; licence/secrets ; hybrides/cycle de vie VPS), chacune revérifiant le code source courant sans se fier aux rapports antérieurs du dépôt.*
