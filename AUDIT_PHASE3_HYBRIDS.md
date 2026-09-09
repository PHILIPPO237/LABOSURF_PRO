# AUDIT PHASE 3 — MOTEURS HYBRIDES / CHAÎNAGE RÉEL (2026-09-09)

**Portée** : suite directe de `AUDIT_PHASE2_CORRECTIONS.md` (SlowDNS/Hysteria/DNSTT/SSH corrigés et testés) et de `AUDIT_PHASE1_CORRECTIONS.md` (UDP/Menu/Xray). Cette phase traite le dernier point ouvert des audits précédents : les moteurs hybrides (`internal/engineutil/composite_engine.go`), dont le trafic ne circulait jamais réellement entre composants. Aucun commit n'a été fait — tout reste dans l'arbre de travail.

---

## 1. ÉTAT INITIAL

Avant cette session, `CompositeEngine` (le moteur qui pilote les hybrides comme `slowdns-ssh` ou `dnstt-xray`) savait :
- installer/configurer/démarrer/arrêter ses sous-moteurs dans un certain ordre ;
- valider la cardinalité d'une composition (au plus 1 transport, au plus 1 VPN) via `ValidateHybrid`.

Il ne savait **pas** faire circuler du trafic entre les composants. `Configure()` fixait un endpoint `"127.0.0.1:0"` en dur (placeholder jamais résolu) et l'injectait dans un champ `outbound.transport_endpoint` qu'aucun moteur ne lit jamais. C'était documenté comme le point ouvert principal dans les trois rapports précédents.

## 2. PROBLÈME IDENTIFIÉ

Confirmé par lecture directe puis par écriture de tests réels (pas seulement relu) :

1. **Endpoint fictif** — `composite_engine.go:101-102` (ancien code) : `e.transportEndpoint = "127.0.0.1:0"`, jamais l'adresse réelle d'un composant démarré.
2. **Double démarrage du transport** — l'ancien `Configure()` appelait `transport.Start(ctx)` lui-même (pour "obtenir" l'endpoint), puis le vrai `Start()` du composite démarrait le même transport **une seconde fois** quand l'utilisateur cliquait réellement sur "Démarrer" dans le menu — un `net.ListenUDP` en double sur le même port, donc un échec systématique (`address already in use`) dès que `Configure()` avait déjà tourné une fois. Jamais détecté auparavant faute de test qui appelle réellement `Configure()` puis `Start()` en séquence.
3. **Sens de câblage inversé** — le code injectait un endpoint dans la config du **VPN** (`outbound.transport_endpoint`), alors que le mécanisme réellement fonctionnel (audité en Phase 2) est l'inverse : `dnstt`/`slowdns` relaient déjà, en conditions réelles, les octets tunnelés vers une adresse **TCP `backend`** configurable (`net.Dial("tcp", cfg.Backend)`). Aucun moteur ne lit jamais `outbound.transport_endpoint` — ni `hysteria.Configure()`, ni `udp.Configure()`, ni `xray.Configure()` (vérifié dans le code des trois).
4. **Bug plus grave découvert en écrivant les tests de cette phase** (§9) : `CompositeEngine.component(name)` appelait `engine.Get(name)`, qui retourne **une instance neuve à chaque appel** (documenté dans `internal/engine/registry.go` : *"le moteur est construit à chaque demande d'instance"*). Résultat vérifié par exécution : `CompositeEngine.Stop()` n'arrêtait **jamais réellement** le sous-moteur démarré par `Start()` (il appelait `Stop()` sur une instance fraîche jamais démarrée, donc un no-op silencieux), et `Status()`/`HealthCheck()` inspectaient elles aussi des instances jamais démarrées. Ce défaut existait déjà avant cette session (il n'a pas été introduit par la Phase 3), mais aucun rapport précédent ne l'avait détecté car aucun test ne démarrait puis arrêtait réellement un hybride.

## 3. ARCHITECTURE EXISTANTE (comment chaque moteur fonctionne réellement)

| Moteur | Écoute | Réception des données | Transmission | Lifecycle Start/Stop |
|---|---|---|---|---|
| **UDP** (`engines/udp`) | `net.ListenUDP` ouvert **synchrone** dans `NewServer()` | Paquets UDP propriétaires (auth HMAC) | Relais TCP vers un backend configuré | `Start()` non-bloquant (goroutine), `Close()` ferme le TUN puis attend (fixé en Phase 1) |
| **Xray** (`engines/xray`) | Binaire externe `xray run -config ...`, port fixé dans le JSON de config (pas un `net.Listen` Go) | Selon le protocole configuré (VLESS/REALITY sur TCP par défaut ici) | Géré en interne par Xray-core | `Start()` lance un `exec.Cmd`, sleep 500ms + vérif process vivant ; `Stop()` `cmd.Wait()`/kill après 5s |
| **Hysteria** (`engines/hysteria`) | `net.ListenUDP` **synchrone** dans `NewHysteriaServer()` | Protocole UDP maison (HELLO/AUTH/DATA) | Relais UDP vers son propre `Backend` (session par session) | `Start()` non-bloquant (goroutine `Run`) ; `Close()` ferme `conn` |
| **DNSTT** (`engines/dnstt`) | `net.ListenUDP` **synchrone** dans `NewDNSTTServer()` | DNS sur sous-domaines (base32), auth ed25519 (Phase 2) | `net.Dial("tcp", cfg.Backend)` — relais TCP réel | Idem, `Close()` ferme `conn` |
| **SlowDNS** (`engines/slowdns`) | `net.ListenUDP` **synchrone** dans `NewSlowDNSServer()` | DNS sur sous-domaines, auth ed25519 (Phase 2) | `net.Dial("tcp", cfg.Backend)` — relais TCP réel | Idem |
| **SSH** (`engines/ssh`) | `net.Listen("tcp", ...)` **asynchrone**, à l'intérieur de `Run()` (goroutine) — pas dans `NewServer()` | Vrai protocole SSH (`golang.org/x/crypto/ssh`) | Shell/exec réel, drop de privilèges (Phase 2) | Idem, mais l'endpoint n'est PAS connu immédiatement après `Start()` (seul cas asynchrone des 6) |

Point clé pour le chaînage : **`dnstt` et `slowdns` sont les deux seuls moteurs qui relaient déjà réellement des octets vers une adresse TCP configurable (`Backend`)** — mécanisme audité et confirmé fonctionnel en Phase 2 (tests `TestDNSTTValidKeyAuthorized`, `TestSlowDNSRoundTrip`). Ce n'est pas un mécanisme inventé pour cette phase.

**Comment `CompositeEngine` sélectionne/démarre ses moteurs (avant cette session)** : `Role(name)` classe chaque moteur (`RoleTransport` = dnstt/slowdns, `RoleVPN` = xray/hysteria/**udp**, `RoleAccount` = ssh). `Start()` démarrait tous les `RoleTransport`, puis tous les `RoleVPN`, puis le reste — sans jamais relier leurs adresses réelles entre eux.

**Pourquoi `"127.0.0.1:0"` ne permettait aucun chaînage réel** : ce n'est ni une adresse résolvable (`:0` = "n'importe quel port", jamais une adresse qu'on peut dialer) ni même lue par un moteur consommateur — la chaîne était un pur artefact cosmétique.

## 4. ARCHITECTURE RETENUE

**Découverte critique en amont (voir §13 pour le détail complet)** : le moteur `udp` vit dans `engines/udp`, un **module Go séparé** (`module labosurf/engine`, binaire autonome `labosurf-udp`), qui n'est **jamais importé par `cmd/labosurf`** (le binaire qui pilote les hybrides). `engine.Names()` dans ce process ne contient donc jamais `"udp"` — il ne peut structurellement pas participer à un hybride avec l'architecture actuelle. Ce n'est pas un défaut introduit ici ; c'est un fait architectural pré-existant, documenté mais non corrigé (correction = fusion de modules, hors du périmètre "pas de refactor massif" de cette phase — voir §13).

Pour les 5 moteurs réellement composables dans le même process (`xray`, `hysteria`, `dnstt`, `slowdns`, `ssh`), l'architecture retenue :

1. **Interface `engine.Endpointer`** (nouvelle, optionnelle — voir §5.1) : chaque moteur qui a un socket réel peut exposer `Endpoint() (engine.Endpoint, bool)` — jamais un placeholder, `false` tant que ce n'est pas prêt.
2. **`CompositeEngine` identifie transport + backend** (`pickTransportAndBackend`) : le transport (dnstt/slowdns) et le composant qui doit lui servir de backend (VPN d'abord si présent, sinon compte SSH).
3. **`Start()` démarre le backend en premier**, interroge son `Endpoint()` réel (avec réessai borné — nécessaire pour SSH, seul cas asynchrone), **refuse de continuer si ce n'est pas un endpoint TCP** (hysteria expose de l'UDP — chaînage structurellement impossible, erreur claire plutôt que faux succès), puis reconfigure le transport avec cette adresse réelle injectée dans son **vrai** champ `"backend"` JSON (celui que `dnstt`/`slowdns` lisent réellement — pas un champ inventé) et le démarre.
4. **`CompositeEngine.component()` met en cache une instance par composant** (correctif du bug §2.4/§9) — condition nécessaire pour que `Stop()`/`Status()` agissent sur le même processus que celui démarré par `Start()`.

## 5. MODIFICATIONS EFFECTUÉES

### 5.1 `internal/engine/engine.go`
Ajout de `Endpoint` (struct `{Network, Addr string}`) et `Endpointer` (interface optionnelle à un seul membre, `Endpoint() (Endpoint, bool)`). **Interface séparée d'`Engine`**, pas une méthode ajoutée au contrat existant : évite d'imposer une implémentation à tout type `Engine` (dont `CompositeEngine` lui-même) qui n'a pas d'endpoint réseau propre à exposer — modification strictement additive, zéro impact sur les implémenteurs existants.

### 5.2 Cinq moteurs — implémentation d'`Endpointer`
- **`engines/{hysteria,dnstt,slowdns}/server.go`** : ajout d'un accesseur `Addr() (*net.UDPAddr, bool)` protégé par mutex sur le `*Server` interne, plus un champ `closed bool` pour qu'`Addr()` redevienne `false` après `Close()` (jamais un endpoint pour un moteur arrêté).
- **`engines/ssh/server.go`** : `Addr()` existant (Phase 2) étendu avec `AddrOk() (net.Addr, bool)` + même garde `closed`.
- **`engines/{hysteria,dnstt,slowdns,ssh}/engine.go`** : ajout d'un `sync.Mutex` protégeant les champs `server`/`cancel`/`done` du wrapper (accès désormais concurrent avec `Endpoint()`), et d'une méthode `Endpoint()` qui relaie l'accesseur du serveur interne.
- **`engines/xray/xray_binary.go`** : `Endpoint()` lit le port réellement configuré dans le fichier JSON écrit par `Configure()` (`configuredPort()`, extrait de la logique déjà présente dans `HealthCheck()` — pas dupliquée), puis vérifie par une **vraie tentative de connexion TCP** (300ms) que le process écoute réellement dessus avant de déclarer l'endpoint prêt — jamais une déduction optimiste basée sur le délai fixe de `Start()`. `mu sync.Mutex` ajouté pour protéger `cmd`/`cancel`/`done` (accès non protégés dans le code d'origine, corrigés au passage).

### 5.3 `internal/engineutil/composite_engine.go` — réécriture ciblée
- `Configure()` ne démarre plus rien (supprime le `transport.Start(ctx)` erroné) ; elle configure chaque composant et mémorise la config (`lastCfg`) pour `Start()`.
- `Start()` implémente le mécanisme décrit en §4 (`pickTransportAndBackend`, `waitForEndpoint`, `injectBackend`).
- `component()` met en cache une instance par nom (correctif §2.4), et une nouvelle méthode exportée `Component(name)` permet d'inspecter de l'extérieur l'instance réellement pilotée (utile pour `Status()` étendu et pour les tests).
- `Status()` expose désormais `ListenAddr` = l'adresse réelle du transport une fois démarré (jamais un placeholder).

### 5.4 Bug de concurrence pré-existant découvert et corrigé — double `Close()` (§9)
`engines/{hysteria,dnstt,slowdns,ssh}/server.go` : chaque serveur avait DEUX chemins qui ferment le même descripteur (`s.conn`/`s.listener`) — une goroutine interne réagissant à `ctx.Done()` (nécessaire quand l'appelant annule le contexte sans passer par `Close()`), et la méthode `Close()` elle-même. Les deux pouvaient s'exécuter en concurrence, l'un des deux recevant `"use of closed network connection"`, remontée comme une erreur de `Stop()`/`Restart()` alors que l'arrêt s'était en réalité bien passé. Corrigé avec un `sync.Once` (`closeConn`/`closeListener`) partagé par les deux chemins.

### 5.5 Hygiène connexe
- `cmd/labosurf/menu.go` : commentaire corrigé (`udp` retiré de la liste des moteurs composables, avec explication — voir §4).
- `engines/dnstt/server_test.go` : `TestDNSTTValidKeyAuthorized` rendu robuste à **deux** fenêtres de course pré-existantes distinctes, trouvées par sondage répété (`-race`, 5 à 10 exécutions consécutives) plutôt qu'en lisant simplement le code :
  1. Le test lisait `accepted` une seule fois immédiatement après l'ack, sans laisser le temps à la goroutine `Accept()` du backend de test d'incrémenter son compteur sous charge système — corrigé par un sondage borné à 2s au lieu d'une lecture unique.
  2. Plus subtil : `client.SetDeadline(...)` n'était posé qu'une seule fois, tout au début du test (avant l'envoi de la requête) ; au moment d'atteindre la boucle d'attente du paquet retour poussé de façon asynchrone, ce délai pouvait avoir presque expiré (le scheduling est nettement plus lent sous `-race`, surtout avec plusieurs packages de tests instrumentés tournant en parallèle) — une fois expiré, `client.Read()` ne bloque plus jamais, transformant la boucle d'attente en spin CPU qui n'attend plus réellement le paquet. Corrigé en renouvelant explicitement le deadline juste avant d'entrer dans cette boucle.
  Aucun des deux n'est un défaut du serveur DNSTT lui-même (chemin de données confirmé fonctionnel en Phase 2 et reconfirmé ici) — uniquement des hypothèses de timing trop optimistes dans le test.

## 6. MOTEURS CONCERNÉS

| Moteur | Modifié cette phase | Rôle dans le chaînage |
|---|---|---|
| dnstt | Oui (`Endpointer`, correctif double-close) | Transport (câblable) |
| slowdns | Oui (idem) | Transport (câblable) |
| ssh | Oui (idem) | Backend valide (TCP réel) |
| xray | Oui (`Endpointer` uniquement) | Backend valide en théorie (TCP), non testé en intégration (binaire externe non téléchargé dans ce sandbox — voir §13) |
| hysteria | Oui (`Endpointer`, correctif double-close) | VPN reconnu par le composite, **backend TCP impossible** (protocole UDP) — désormais rejeté explicitement au lieu d'un faux succès |
| **udp** | **Non touché** | Ne peut pas participer à un hybride (module séparé — §4, §13) |

## 7. CHAÎNAGE ET CIRCULATION DES DONNÉES

Schéma réellement démontré cette phase (voir §8/§9) :

```
CLIENT UDP (protocole DNSTT réel)
  ↓ requête authentifiée (signature ed25519)
DNSTT (endpoint UDP réel, découvert via Endpointer)
  ↓ net.Dial("tcp", backend)  — backend = endpoint SSH réel, injecté par CompositeEngine
SSH (endpoint TCP réel, découvert via Endpointer)
  ↓ bannière protocolaire "SSH-2.0-Go\r\n" émise par le vrai serveur SSH
DNSTT (relais retour, push asynchrone)
  ↓
CLIENT (bannière reçue à travers le tunnel)
```

Le schéma générique demandé (CLIENT → A → B → C → BACKEND) est démontré avec **2 sauts réels** (transport → backend) — c'est la profondeur de chaîne que le mécanisme actuel (cardinalité "1 transport + 1 backend") permet ; enchaîner un 3e maillon réel nécessiterait qu'un composant soit à la fois backend d'un transport ET transport d'un autre backend, ce qu'aucun moteur actuel n'implémente (aucun n'a de champ "backend redirigé vers un autre transport DNS"). Documenté comme limite en §13, pas contourné par un faux mécanisme.

## 8. TESTS AJOUTÉS

| Fichier (nouveau) | Contenu |
|---|---|
| `engines/hysteria/engine_endpoint_test.go` | `TestHysteriaEngineWrapperEndpointIsReal` : Start → Endpoint réel → preuve d'occupation réelle du port (tentative de re-écoute doit échouer) → Restart propre → Stop propre → port réellement libéré |
| `engines/dnstt/engine_endpoint_test.go` | Idem pour dnstt |
| `engines/slowdns/engine_endpoint_test.go` | Idem pour slowdns |
| `engines/ssh/engine_endpoint_test.go` | Idem pour ssh (TCP, attente bornée car listener asynchrone) |
| `engines/dnstt/composite_chain_test.go` | `TestCompositeEngineChainsDNSTTToRealSSHBackend` : câblage réel vérifié sur disque (`backend` = endpoint ssh réel, jamais le placeholder TEST-NET) **puis** un vrai client UDP parlant le protocole DNSTT traverse toute la chaîne et reçoit la bannière du vrai serveur SSH. `TestCompositeEngineRestartRewiresRealBackend` : le même chemin de données fonctionne à nouveau après un cycle `Stop()`→`Start()` complet |
| `internal/engineutil/composite_engine_test.go` | `TestCompositeEngineRejectsIncompatibleChain` : hysteria (UDP) derrière slowdns (relais TCP uniquement) → `Start()` échoue avec une erreur explicite, ne prétend jamais réussir. `TestCompositeEngineComponentReturnsStableInstance` : verrouille le correctif du bug §2.4/§9 |

**Aucun test ne suppose qu'un "Start() sans erreur" prouve un trafic réel.** Chaque test qui prétend démontrer un chemin de données envoie et reçoit réellement des octets identifiables (bannière SSH authentique, jamais fabriquée par le test) ; chaque test qui prétend démontrer un endpoint réel tente activement de re-occuper le même port pour prouver qu'il l'était.

## 9. RÉSULTATS DES TESTS

```
go test ./engines/hysteria/... -run EndpointIsReal -v       PASS (Start/Endpoint/Restart/Stop, 3 exécutions consécutives)
go test ./engines/dnstt/...    -run EndpointIsReal -v       PASS (idem)
go test ./engines/slowdns/...  -run EndpointIsReal -v       PASS (idem)
go test ./engines/ssh/...      -run EndpointIsReal -v       PASS (idem)
go test ./engines/dnstt/...    -run TestCompositeEngine -v  PASS — bannière SSH réelle reçue à travers le tunnel dnstt (2 tests, 3 exécutions consécutives)
go test ./internal/engineutil/... -run TestCompositeEngine -v PASS — refus honnête du chaînage hysteria+slowdns ; instance stable de Component()
go test ./...                                                61+ PASS, 0 FAIL (10 exécutions consécutives, aucun flake résiduel)
```

**Bug intermédiaire trouvé et corrigé pendant cette session** (avant d'atteindre l'état final ci-dessus) : le double-`Close()` (§5.4) faisait échouer `Restart()`/`Stop()` de façon intermittente sur les 4 moteurs concernés — reproduit, diagnostiqué, corrigé, revérifié par 3 exécutions consécutives de chaque test concerné après correction.

**Flakes pré-existants trouvés et corrigés** : `TestDNSTTValidKeyAuthorized` (Phase 2) échouait de façon intermittente — d'abord ~1 fois sur 10 sous `go test ./...` sans `-race`, puis une seconde cause distincte encore plus visible sous `-race` (jusqu'à 1 fois sur 5 en exécution isolée). Les deux causes sont identifiées et corrigées en §5.5 (fenêtres de course dans le TEST, pas dans le serveur). Revérifié : 8/8 exécutions isolées sous `-race` propres, puis 4 exécutions consécutives de la suite complète `go test -race ./...` propres, puis 10 exécutions consécutives de `go test ./...` (sans `-race`) propres.

## 10. RÉSULTATS DU RACE DETECTOR

```
go test -race -count=1 ./...                    (racine)      0 FAIL, aucune race détectée
cd engines/udp && go test -race -count=1 ./...   (module udp)  0 FAIL, aucune race détectée
```

## 11. BUILD/VET

```
go build ./...   (racine)     exit 0
go vet ./...     (racine)     propre
cd engines/udp && go build ./... && go vet ./...    exit 0, propre
```

## 12. CROSS-COMPILATION

Les 21 binaires annoncés par le projet (`labosurf`, `labosurf-dnstt`, `labosurf-hysteria`, `labosurf-slowdns`, `labosurf-ssh`, `labosurf-xray` + module `engines/udp`, × `linux/amd64`, `linux/arm64`, `android/arm64`) recompilés avec succès après toutes les modifications de cette phase — `exit 0` sur les 21, architecture réelle vérifiée par `file` sur un échantillon (ELF ARM aarch64 statiquement lié pour `linux/arm64`, ELF PIE avec interpréteur `/system/bin/linker64` pour `android/arm64`). Aucune régression de la chaîne de build/cross-compilation.

## 13. PROBLÈMES RESTANTS

- **Le moteur `udp` ne peut pas participer aux hybrides** (§4) — limitation architecturale pré-existante (module Go séparé, jamais importé par `cmd/labosurf`), non corrigée ici conformément à la consigne "pas de refactor massif sans nécessité" : la corriger proprement demande une décision délibérée (fusionner `engines/udp` dans le module racine, ou exposer le moteur udp via un autre mécanisme de composition que des appels Go in-process) — hors du périmètre de cette phase, à trancher par le mainteneur.
- **Hysteria ne peut structurellement pas servir de backend à un transport DNS** avec le mécanisme actuel (UDP-only vs relais TCP-only) — désormais rejeté honnêtement par `Start()` (§9) plutôt que silencieusement cassé, mais reste non fonctionnel en tant que combinaison hybride. Corriger nécessiterait soit un mode de relais UDP côté transport, soit un mode TCP côté hysteria — changement de protocole, hors périmètre.
- **Chaînage à 3 maillons réels non démontré** (§7) : le mécanisme actuel supporte "1 transport + 1 backend", pas de relais en cascade (transport → backend qui est lui-même transport d'un 3e composant). Aucun moteur actuel n'a de champ pour "rediriger vers un autre transport" — introduire ça serait une extension de protocole par moteur, pas une correction de câblage.
- **Xray non testé en intégration réelle dans ce chaînage** : `Endpoint()` d'Xray est implémenté et unitairement cohérent (lit le port configuré, vérifie par un vrai dial TCP), mais aucun test de cette phase ne démarre un vrai process Xray-core chaîné derrière dnstt/slowdns — ça demanderait de télécharger le binaire officiel dans l'environnement de test (réseau, plusieurs Mo), jugé hors de portée raisonnable pour un test unitaire rapide. Le mécanisme de câblage lui-même (générique, basé sur `Endpointer`) est identique à celui prouvé avec ssh — seule l'intégration bout-en-bout avec le vrai binaire Xray reste non observée.
- **`internal/engineutil/compat.go` (`ValidateHybrid`) n'a volontairement pas été modifié** : il continue à autoriser l'enregistrement de `hysteria+slowdns` (cardinalité respectée), conformément au contrat déjà testé par `TestRemoveHybrid` (`compat_test.go`, préexistant) qui enregistre explicitement cette combinaison. La détection de l'incompatibilité réseau réelle a délibérément été placée dans `Start()` (§4, §9) plutôt que dans la validation d'enregistrement, pour ne pas casser ce test existant ni le contrat "le guide de compatibilité avertit, il n'interdit pas" déjà en place.

## 14. RISQUES

- Le correctif double-`Close()` (§5.4) touche le chemin d'arrêt de 4 moteurs déjà en production logique (audités Phase 2) — revérifié par 3 exécutions consécutives de chaque test de cycle de vie plus la suite complète 10×, mais reste un changement sur du code déjà "validé" dans un rapport antérieur ; à surveiller en usage réel prolongé (le mécanisme `sync.Once` est standard et bien compris, risque jugé faible).
- Le correctif de mise en cache dans `component()` (§2.4/§5.3) change un comportement latent de `CompositeEngine` qui n'était testé nulle part avant cette session — aucune régression observée sur les tests existants (`compat_test.go` inchangé et toujours vert), mais c'est le genre de correctif dont l'impact réel ne se mesure complètement qu'à l'usage prolongé via le menu interactif.
- `Endpoint()` d'Xray dépend d'un dial TCP de 300ms pour confirmer qu'il écoute réellement — sur une machine très chargée ou un VPS à latence loopback anormale, ce délai pourrait théoriquement ne pas suffire ; non observé, mais non plus testé en conditions réelles (voir §13).
- Comme en Phase 1/2 : rien de tout ceci n'a été validé sur un vrai VPS Linux avec un vrai client externe — uniquement en local (loopback, mêmes contraintes que les phases précédentes).

## 15. PROCHAINE ÉTAPE

Dans l'ordre :

1. **Test réel sur VPS Linux** du chaînage `dnstt-ssh`/`slowdns-ssh` avec un vrai client dnstt/slowdns externe — seule façon de transformer la preuve de cette session (loopback, process Go natifs) en preuve de fonctionnement réel réseau.
2. **Décider du sort du moteur `udp` vis-à-vis des hybrides** (§13) : fusion de module ou mécanisme de composition alternatif — nécessite une décision produit, pas une correction technique isolée.
3. **Xray dans un hybride réel** : télécharger le binaire officiel dans un environnement de test dédié et vérifier le chaînage `dnstt-xray`/`slowdns-xray` de bout en bout (le mécanisme de câblage est déjà en place, seule l'intégration reste à observer).
4. Revoir le README/documentation utilisateur pour indiquer clairement quelles combinaisons hybrides sont réellement fonctionnelles aujourd'hui (`{dnstt,slowdns} + ssh`, potentiellement `+ xray`) vs celles qui s'enregistrent mais ne peuvent pas transporter de trafic (`{dnstt,slowdns} + hysteria`, tout ce qui implique `udp`).

---

*Rapport de Phase 3, généré à la fin de la session de travail sur les moteurs hybrides. Chaque affirmation est adossée à une commande exécutée et observée dans cette session, ou à une lecture directe du code source actuel. Aucun commit git n'a été effectué.*
