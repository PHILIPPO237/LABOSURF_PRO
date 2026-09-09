# AUDIT PHASE 2 — CORRECTIONS LABOSURF_PRO (2026-09-09, reprise après interruption)

**Portée** : suite directe de `AUDIT_PHASE1_CORRECTIONS.md` (UDP/Menu/Xray). Cette phase traitait **SlowDNS, Hysteria, DNSTT, SSH**, puis la vérification des binaires, puis l'analyse (pas la correction) des moteurs hybrides. La session précédente a été interrompue en cours de route ; ce rapport documente d'abord ce qui avait déjà été sauvegardé avant l'interruption (retrouvé tel quel dans l'arbre de travail non commité), puis le travail de vérification effectué à la reprise.

**Important** : aucun commit git n'a été fait à aucun moment de Phase 1 ni de Phase 2 — tout le travail des deux phases reste dans l'arbre de travail (`git status` : modifications non indexées + fichiers non suivis). C'est un choix délibéré (consigne explicite : ne pas commiter automatiquement), pas un oubli.

---

## 1. ÉTAT RETROUVÉ À LA REPRISE (avant toute action de cette session)

Inspection de `git status` / `git diff --stat` / lecture directe du code : **les quatre correctifs P0 de SlowDNS, Hysteria, DNSTT et SSH étaient déjà entièrement présents dans l'arbre de travail**, chacun avec son fichier de test dédié (nouveau, `engines/<moteur>/server_test.go`), preuve que la session précédente avait terminé ce travail avant l'interruption — pas seulement commencé :

| Moteur | Bug P0 corrigé (retrouvé déjà appliqué) | Test dédié retrouvé |
|---|---|---|
| **SlowDNS** | `qdCount` lu au mauvais offset DNS (6 au lieu de 4) → rejetait toute requête ; `extractSubdomain` ne gardait que `parts[0]` → payload (session+signature=80 octets) toujours tronqué ; `buildDNSResponseForPayload(nil, ...)` → retour toujours mort. Retour désormais implémenté en file d'attente (`pendingOut`) vidée à la prochaine requête poll du client (contrainte requête/réponse du DNS) | `engines/slowdns/server_test.go` (301 lignes) |
| **Hysteria** | Bornes `sessionID` incohérentes (`pkt[4:12]` vs `pkt[4:20]`) → session jamais retrouvée après auth ; HMAC data vérifié avec le mot de passe du 1er utilisateur activé au lieu de celui de la session ; fuite de connexion backend (vérif de présence en map après `delete()`) ; `sessionID` recopié depuis sa forme hex tronquée au lieu des octets bruts | `engines/hysteria/server_test.go` (260 lignes) |
| **DNSTT** | Authentification totalement absente (`PublicKey`/`PrivateKey` jamais lus) → désormais signature ed25519 du sessionID exigée avant toute création de session ; `extractSubdomain` tronqué à `parts[0]` (même bug que SlowDNS) ; noms DNS construits par concaténation de points ASCII littéraux au lieu de l'encodage fil DNS (labels préfixés par longueur) | `engines/dnstt/server_test.go` (257 lignes) |
| **SSH** | `applySysProcAttr` stub sans `Credential` → aucune session ne droppait jamais les privilèges. Résolution dynamique de l'UID/GID via `os/user.Lookup(runAsUser)` (compte configurable, `labosurf` par défaut), repli explicite et journalisé (pas silencieux) si le compte système n'existe pas. Bonus découvert en corrigeant : `shell`/`exec` répondaient par une requête `x-accept` inventée au lieu du `SUCCESS` SSH attendu par un vrai client — un client SSH réel restait bloqué indéfiniment | `engines/ssh/server_test.go` (241 lignes) |

Un correctif de course transverse (`s.cancel` protégé par `s.mu`, même motif que le fix UDP de phase 1) avait aussi été appliqué aux 4 moteurs — cohérent avec un usage réel de `-race` pendant la session précédente.

Tous les autres fichiers modifiés (`engines/xray/*`, `internal/clientcfg/*`, `internal/license/license_test.go`, `engines/udp/*`, `cmd/labosurf/menu.go`) correspondent exactement au travail déjà documenté dans `AUDIT_PHASE1_CORRECTIONS.md` — revérifiés, rien d'inattendu.

**Aucune correction n'a donc été refaite dans cette session** : la vérification a confirmé que le travail SlowDNS/Hysteria/DNSTT/SSH était déjà complet et fonctionnel, conformément à la consigne de ne jamais refaire une correction déjà présente et validée.

---

## 2. TRAVAIL DE CETTE SESSION (reprise) : VÉRIFICATION

Comme demandé, la reprise a porté sur la vérification (les correctifs eux-mêmes étant déjà en place), puis l'analyse des hybrides.

### 2.1 Vérification build / vet / tests (tous moteurs)

| Commande | Résultat |
|---|---|
| `go build ./...` (racine) | ✅ exit 0 |
| `go vet ./...` (racine) | ✅ propre |
| `go test ./...` (racine, sans `-race`) | ✅ toutes les suites PASS (aucune "no test files" inattendue) |
| `go test -race ./...` (racine) | ✅ **toutes les suites PASS**, aucune race détectée, y compris `engines/dnstt`, `engines/hysteria`, `engines/slowdns`, `engines/ssh` |
| `go test ./engines/{dnstt,hysteria,slowdns,ssh}/... -v` | ✅ 8/8 tests PASS individuellement (voir détail §3) |
| `cd engines/udp && go build/vet/test -race ./...` | ✅ propre, PASS |

### 2.2 PRIORITÉ 5 — Vérification des binaires (terminée cette session)

Objectif : confirmer que les correctifs des 4 moteurs n'ont rien cassé dans la chaîne de build/cross-compilation/binaires, et lever le risque documenté en Phase 1 (§8 de l'audit technique : checksums Xray jamais revérifiés depuis une deuxième source).

- **Cross-compilation complète re-testée** : les 7 binaires (`labosurf`, `labosurf-dnstt`, `labosurf-hysteria`, `labosurf-slowdns`, `labosurf-ssh`, `labosurf-xray`, + module séparé `engines/udp`) × 3 architectures (`linux/amd64`, `linux/arm64`, `android/arm64`) = **21 binaires, tous produits avec exit 0**, aucune erreur. Architecture réelle vérifiée par `file` sur un échantillon (ELF ARM aarch64 statiquement lié pour linux/arm64, ELF PIE avec interpréteur `/system/bin/linker64` pour android/arm64) — cohérent avec l'audit précédent.
- **Aucun binaire commité par erreur dans git** : `git ls-files | xargs file` ne renvoie aucun ELF/PE32/Mach-O. `dist/`, `labosurf`, `labosurf.exe` correctement gitignorés (vérifié avec `git check-ignore -v`).
- **Checksums SHA256 Xray-core, revérifiés depuis une source réellement indépendante** (accès réseau direct `curl` vers les `.dgst` officiels signés de XTLS/Xray-core v26.3.27, pas un résumé IA — le risque exact identifié en Phase 1 §8) :
  - `Xray-linux-64.zip` → `SHA2-256 = 23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae` — **identique** à la valeur codée dans `engines/xray/xray_binary.go`.
  - `Xray-linux-arm64-v8a.zip` → `SHA2-256 = 4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c` — **identique**.
  - Les deux URLs d'assets résolvent en `HTTP 200` (pas de régression du bug 404 arm64 corrigé en Phase 1).
- **`dist/` local** contient des binaires obsolètes (datés du 4 septembre, donc antérieurs aux correctifs SlowDNS/Hysteria/DNSTT/SSH) — sans impact, dossier gitignored, artefacts de build locaux uniquement, pas livrés.

**Conclusion priorité 5 : terminée, aucune anomalie trouvée.** Les correctifs des 4 moteurs n'ont introduit aucune régression de build/cross-compilation, et les checksums Xray sont désormais confirmés par une deuxième source indépendante (risque de Phase 1 levé).

### 2.3 Analyse (pas correction) de l'architecture CompositeEngine / hybrides

Confirmé par lecture directe que rien n'a changé depuis l'audit technique et la Phase 1 : `internal/engineutil/composite_engine.go` et `internal/engine/engine.go` ont un `git diff` **vide** — aucune des deux phases n'y a touché.

- `composite_engine.go:101-102` fixe toujours `e.transportEndpoint = "127.0.0.1:0"` (placeholder, TODO explicite inchangé).
- L'interface `engine.Engine` (`internal/engine/engine.go:115-158`) n'a toujours aucune méthode pour qu'un moteur transport expose son adresse d'écoute réelle après `Start()`.
- Nuance découverte en marge de la Phase 2 : `engines/ssh/server.go` a désormais une méthode `Addr()` (ajoutée pour permettre au test `TestSSHStartStopRestart` d'attendre que le listener soit réellement prêt), qui expose exactement le genre d'information qu'une future méthode `Engine.Endpoint()` devrait exposer — mais elle n'est card branchée nulle part dans `CompositeEngine`, et les 5 autres moteurs (udp, xray, hysteria, dnstt, slowdns) n'ont pas d'équivalent. Ce n'est pas une correction du problème hybride, juste une pièce isolée qui pourrait servir de modèle le jour où ce chantier sera engagé.
- Le verdict reste **inchangé** : hybrides = orchestration de démarrage/arrêt réelle, validation de composition (rôles/cardinalité) réelle, **circulation de trafic entre composants toujours inexistante**. Correction nécessitant une extension d'interface + implémentation dans 6 moteurs concrets + logique de polling côté `CompositeEngine.Configure()` — changement transverse à 7 fichiers minimum, **volontairement non engagé cette session**, conformément à l'instruction de n'analyser ce point qu'en tout dernier lieu et à la décision déjà motivée en Phase 1 §5 (ne pas bricoler un correctif partiel donnant une fausse impression de fonctionnement).

---

## 3. DÉTAIL DES TESTS PAR MOTEUR (exécutés cette session, résultats observés)

```
=== engines/dnstt ===
TestDNSTTValidKeyAuthorized      PASS — data path bidirectionnel fonctionnel (auth valide)
TestDNSTTInvalidKeyRejected      PASS — signature invalide rejetée, aucune session/connexion créée

=== engines/hysteria ===
TestHysteriaRoundTrip            PASS — data path bidirectionnel fonctionnel (client→backend→client)
TestHysteriaRejectsBadHMAC       PASS — mauvais mot de passe rejeté, pas de session

=== engines/slowdns ===
TestSlowDNSRoundTrip             PASS — data path bidirectionnel fonctionnel (backend TCP)
TestSlowDNSRejectsBadSignature   PASS — signature invalide → NXDOMAIN, aucune session

=== engines/ssh ===
TestSSHRealPrivilegeDrop            PASS — session tourne réellement sous UID non-root (nobody/65534)
TestSSHFallbackWhenRunAsUserMissing PASS — repli journalisé si compte absent, session non cassée
TestSSHRejectsUnknownKey            PASS — clé publique inconnue rejetée
TestSSHStartStopRestart             PASS — cycle start→connexion→stop→restart→connexion complet
```

8/8 tests PASS, y compris sous `-race` (aucune race), pour les 4 moteurs.

---

## 4. PROBLÈMES RESTANTS (hors périmètre de Phase 1 + Phase 2, non traités — pour mémoire)

Repris tels quels de l'audit technique, aucun n'a été touché ni par Phase 1 ni par Phase 2 :

- **Moteurs hybrides** : circulation de trafic non fonctionnelle (§2.3 ci-dessus) — nécessite l'extension d'interface `Engine.Endpoint()`.
- **`.github/workflows/release.yml:130`** : `$INPUT_VERSION` non défini sous `set -u` → `workflow_dispatch` manuel échoue (releases par tag non affectées). Vérifié inchangé cette session (`git diff --stat .github/` vide).
- **Modèle de licence "1 clé = 1 install, 3h"** sans autorité serveur (réutilisable sur plusieurs machines) — limitation de modèle métier documentée, pas un défaut cryptographique.
- **`labosurf-pro.sh:498-539`** : échec d'installation d'un moteur tiers seulement loggé en warning, `final_check()` ne couvre pas les moteurs sélectionnés.
- **`FILES_TO_DELETE.md` / `UNUSED_FILES.md`** (fichiers non suivis, pré-existants) : contiennent des recommandations de suppression incorrectes (fichiers `*_stub.go`/`tun_android.go` en réalité nécessaires aux build tags multi-plateformes) — à ne pas appliquer sans revérification, non modifiés cette session.
- **Vérification E2E réelle** (VPS Linux avec vrai TUN, vrai client Xray/v2rayN REALITY, vrais clients Hysteria2/dnstt/SlowDNS externes) : toujours non faite dans cet environnement — tout ce qui précède est vérifié par build, cross-compilation, tests unitaires/loopback et vérification de checksums en source indépendante, pas par un déploiement réel.

---

## 5. FICHIERS CONCERNÉS (résumé, tous non commités)

**Modifiés (suivis par git, non indexés)** : `.gitignore`, `cmd/labosurf/menu.go`, `engines/dnstt/server.go`, `engines/hysteria/server.go`, `engines/slowdns/{config.go,server.go}`, `engines/ssh/{config.go,server.go}`, `engines/udp/{license.go,license_cli_test.go,server.go,tunnel_integration_test.go}`, `engines/xray/{reality.go,xray_binary.go}`, `internal/clientcfg/{clientcfg.go,clientcfg_test.go}`, `internal/license/license_test.go`.

**Nouveaux (non suivis)** : `AUDIT_FINAL_LABOSURF_PRO.md`, `AUDIT_PHASE1_CORRECTIONS.md`, `AUDIT_TECHNIQUE_COMPLET_2026-09-09.md`, `AUDIT_PHASE2_CORRECTIONS.md` (ce fichier), `FILES_TO_DELETE.md`, `UNUSED_FILES.md`, `find_unused.py`, `cmd/labosurf/menu_nilctx_test.go`, `engines/{dnstt,hysteria,slowdns,ssh}/server_test.go`, `engines/xray/{reality_test.go,xray_binary_test.go}`.

Aucun fichier n'a été supprimé, aucune correction fonctionnelle existante n'a été modifiée ou retirée cette session.

---

## 6. PROCHAINE ÉTAPE RECOMMANDÉE

Dans l'ordre :

1. **Décider du sort documentaire** de Hysteria/DNSTT/SlowDNS/SSH dans le README — leur protocole reste "maison" (pas conforme aux protocoles de référence Hysteria2/dnstt/SlowDNS), mais le chemin de données et l'authentification sont désormais **réellement fonctionnels en local** (loopback), ce qui n'était vrai pour aucun des quatre avant Phase 2.
2. **Test réel sur VPS Linux** (toujours recommandé depuis Phase 1, non réalisable dans cet environnement) : valider un cycle complet de chaque moteur avec un vrai client externe, en particulier le handshake REALITY d'Xray avec v2rayN/Xray-core réel.
3. **Interface `Engine.Endpoint()` + hybrides** — seulement une fois les 5 points ci-dessus stabilisés en conditions réelles, comme documenté en §2.3. Nécessite d'étendre l'interface, l'implémenter dans les 6 moteurs (SSH a déjà un `Addr()` réutilisable comme modèle), puis brancher `CompositeEngine.Configure()` avec une logique de polling.
4. Corriger le bug `$INPUT_VERSION` de `release.yml` (P1, isolé, non lié aux moteurs) avant la prochaine release manuelle par `workflow_dispatch`.

Aucun commit n'a été effectué (conformément à la consigne). L'arbre de travail reste dans l'état vérifié ci-dessus, prêt à être revu et commité par l'utilisateur.
