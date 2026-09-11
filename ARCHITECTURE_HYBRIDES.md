# Architecture de chaînage des moteurs hybrides — LABOSURF PRO

Ce document décrit, en se basant exclusivement sur le code réellement
présent dans le dépôt (rien de supposé), l'architecture de chaînage des
moteurs hybrides telle qu'elle existait avant cette étape, puis la
généralisation apportée pour représenter des chaînes A → B, A → B → C, et
plus profondes, sans dupliquer d'architecture pour chaque combinaison.

**Périmètre de cette étape** : construire et tester le socle de chaînage
générique. Aucun des cinq hybrides à trois moteurs évoqués pour la suite
(TUIC+SSH+Xray, TUIC+DNSTT+Xray, Hysteria2+SSH+Xray, Hysteria2+DNSTT) ni les
combinaisons UDP+Hysteria2/UDP+TUIC/UDP+Xray n'est créé ni déclaré
fonctionnel ici — ils sont seulement **évaluables** avec le mécanisme décrit
plus bas, et l'évaluation honnête (§7) est qu'aucun n'est aujourd'hui
réellement chaînable de bout en bout avec le catalogue de moteurs actuel.

---

## 1. Architecture trouvée avant cette étape

### 1.1 Contrat `engine.Engine` et `engine.Endpointer`

`internal/engine/engine.go` définit l'interface commune (`Name`, `Version`,
`Install`, `Configure`, `Start`, `RunForeground`, `Stop`, `Restart`,
`Status`, `HealthCheck`, `Logs`, `Update`, `Uninstall`) et l'interface
optionnelle `Endpointer` (`Endpoint() (Endpoint, bool)`), vérifiée par
assertion de type. Règle stricte et déjà respectée avant cette étape :
`Endpoint()` ne renvoie **jamais** d'adresse fictive — seulement une adresse
confirmée réelle, ou `false`.

### 1.2 Registre (`internal/engine/registry.go`)

Un registre global (`map[string]Factory`) enregistré par effet de bord d'un
`init()` dans chaque paquet moteur. `Get(name)` retourne une instance
**neuve** à chaque appel ; un appelant qui pilote un cycle de vie complet
doit conserver sa propre instance (c'est ce que fait `CompositeEngine` via
son cache interne `subs`, et `cmd/labosurf/menu.go` pour un moteur simple).

### 1.3 Rôles et capacités (`internal/engineutil/compat.go`)

Avant cette étape, `EngineCapability` portait `Provides`, `Requires`,
`Protocol`, `Port`. `Role(name)` classe chaque moteur en `RoleTransport`
(dnstt, slowdns), `RoleVPN` (xray, hysteria, tuic, udp) ou `RoleAccount`
(ssh). `ValidateHybrid` limite une composition à **au plus un** moteur de
chaque rôle transport/VPN (règle produit inchangée par cette étape).
`CompatibilityCheck` est un **guide** non bloquant (avertissements affichés
dans le menu), pas une porte d'exécution.

### 1.4 Le mécanisme de chaînage réel : `CompositeEngine`

`internal/engineutil/composite_engine.go` est la seule pièce qui câble
réellement du trafic entre deux moteurs. Avant cette étape, le mécanisme
était strictement **Transport → Backend**, à un seul niveau :

1. `pickTransportAndBackend` trouve, **par rôle, indépendamment de la
   position dans `Components`**, le moteur `RoleTransport` (dnstt ou
   slowdns — les deux seuls à savoir relayer, voir §1.5) et un backend
   préféré (VPN d'abord, puis compte).
2. Le backend démarre en premier, son endpoint réel est lu via
   `engine.Endpointer` (`waitForEndpoint`, avec timeout, jamais un
   placeholder).
3. Si cet endpoint n'est pas `"tcp"`, `Start()` **refuse** de chaîner avec
   une erreur explicite plutôt que de prétendre réussir (verrouillé par
   `TestCompositeEngineRejectsIncompatibleChain`).
4. Le transport est reconfiguré avec cette adresse réelle (champ JSON
   générique `"backend"`, lu par `dnstt.DNSTTConfig.Backend` /
   `slowdns.SlowDNSConfig.Backend`), puis démarré.

Un **troisième composant**, s'il existait dans `Components`, démarrait
jusqu'ici indépendamment, jamais câblé — documenté mais jamais généralisé.

**C'est donc `Transport → Backend`, pas `A → B` générique** : la paire est
trouvée par rôle (deux rôles seulement participent), pas par une notion
d'ordre ou de capacité arbitraire, et strictement limitée à une seule paire.

### 1.5 Qui relaie réellement, aujourd'hui

Un seul mécanisme de relais existe dans tout le dépôt : `net.Dial("tcp",
cfg.Backend)`, implémenté indépendamment dans `engines/dnstt/server.go` et
`engines/slowdns/server.go`. Aucun autre moteur (xray, hysteria, tuic, ssh,
udp) ne sait relayer vers un composant suivant : ce sont tous des
composants **terminaux** (ils consomment du trafic, ils n'en relaient
jamais). Fait vérifié par lecture de chaque `engines/*/*.go`, pas supposé.

### 1.6 Correction d'une inexactitude documentée

Le commentaire historique de `composite_engine.go` affirmait que le moteur
`"udp"` « vit dans un module Go séparé... jamais importé par cmd/labosurf ».
C'est **inexact** aujourd'hui : `internal/engineudp` (wrapper
`engineutil.SystemEngine` qui supervise en sous-processus le serveur
historique `engines/udp`) **est** enregistré (`engine.Register("udp", ...)`)
et **est** importé par `cmd/labosurf/main.go` (`_
"labosurf/internal/engineudp"`). `"udp"` apparaît donc bien dans
`engine.Names()` et est proposable dans le menu de création d'hybride.

Ceci dit, la conclusion pratique reste la même, pour une raison
différente : `engineutil.SystemEngine` (le type embarqué par `UDPEngine`)
n'implémente **pas** `engine.Endpointer`. `"udp"` ne peut donc
structurellement ni servir de backend câblé (son endpoint réel n'est jamais
lisible), ni relayer (il ne lit pas de champ `"backend"` générique) — même
limite qu'avant, mais due à l'absence d'`Endpointer`, pas à une absence du
registre. Ce commentaire a été corrigé dans le code (voir
`composite_engine.go`).

### 1.7 Lifecycle existant déjà correct pour la généralisation

- **Stop()** arrête déjà les composants en **ordre inverse** de
  `Components` — exactement ce que demande une chaîne A → B → C à l'arrêt.
- **HealthCheck()** agrège déjà l'état de tous les composants.
- **Status()** agrège déjà `Installed`/`Running`, et rapportait déjà
  `ListenAddr` = l'adresse réelle du transport câblé (jamais un
  placeholder).

Ces trois mécanismes n'ont pas eu besoin d'être réécrits pour la
généralisation : ils étaient déjà génériques par construction.

---

## 2. Architecture retenue

**Décision : étendre `CompositeEngine`, pas le remplacer par un nouveau
type `Chain`.** `CompositeEngine` reste le seul type qui implémente
`engine.Engine` pour un hybride, s'enregistre dans le même registre, et
reste piloté par le même menu. Une nouvelle abstraction `Chain` séparée
aurait dupliqué le registre, le cache d'instances (`subs`), et le contrat
`engine.Engine` déjà obligatoire pour tout composant — sans rien gagner en
clarté. À la place, deux extensions additives :

1. **`internal/engineutil/compat.go`** — `EngineCapability` gagne deux
   champs déclaratifs, `Network` (type de socket réellement lié par
   `Endpoint()`) et `RelaysTo` (type de réseau vers lequel ce moteur sait
   relayer, vide s'il est terminal), plus une fonction pure `CanConnect(front,
   back) (bool, string)`. **Aucun champ existant, aucune fonction
   existante (`Role`, `ValidateHybrid`, `CompatibilityCheck`,
   `SuggestPrimaryRecommends`) n'a été modifié.**

2. **`internal/engineutil/chain.go`** (nouveau) — représentation purement
   déclarative d'une chaîne ordonnée : `ChainLink`, `ComputeChainLinks`,
   `PortConflict`, `DetectPortConflicts`, `ChainReport`, `EvaluateChain`. Ce
   fichier ne démarre rien, n'enregistre rien : c'est le "moteur de
   compatibilité étendu" (§7) qui répond à "cette composition peut-elle
   être relayée de bout en bout ?" avant toute tentative réelle.

3. **`internal/engineutil/composite_engine.go`** — `Start()` généralisé en
   deux étages :
   - l'étage historique (§1.4) reste **inchangé bit à bit** dans son
     comportement observable (mêmes tests, même verrouillage), seule la
     comparaison réseau finale utilise désormais `EngineCapability.RelaysTo`
     au lieu du littéral `"tcp"` codé en dur ;
   - `wireAdjacentChain` (nouveau, non exporté) généralise **le même
     mécanisme** à toute paire **adjacente** restante de `Components` dont
     le composant "front" déclare `RelaysTo != ""` — déclenchement basé sur
     la capacité déclarée (comme l'étage historique se déclenche dès qu'un
     transport existe), verdict toujours basé sur l'endpoint **réel** du
     composant suivant (jamais une supposition statique).
   - `Start()` nettoie désormais les composants déjà démarrés si une étape
     ultérieure échoue (§6), et calcule l'endpoint frontal de toute la
     chaîne (`Components[0]`) à la fin, quel que soit le mécanisme qui l'a
     câblé.

Avec le catalogue de moteurs actuel, `wireAdjacentChain` est **inerte
au-delà de la paire déjà couverte par l'étage historique** (seuls dnstt et
slowdns déclarent `RelaysTo`, et `ValidateHybrid` limite à un seul moteur
`RoleTransport` par composition) : une vraie chaîne A → B → C avec relais à
**chaque** étage n'est pas démontrable avec les moteurs produit
aujourd'hui. Le mécanisme est néanmoins prouvé fonctionnel pour une
profondeur de 3 avec de vrais moteurs de test (§8), et s'activera de
lui-même, sans modification de ce fichier, le jour où un moteur "relais
intermédiaire" existera.

---

## 3. `Chain` : représentation générique (déclarative)

```
Chain (= CompositeEngine.Components, ordonné front -> back)
 ├── Component A   (Components[0], entrée : ce qu'un client compose)
 ├── Component B
 └── Component C   (Components[len-1], backend terminal)
```

Chaque composant, en tant que `engine.Engine` (+ `engine.Endpointer`
optionnel) et entrée `EngineCapability`, porte déjà toutes les propriétés
demandées :

| Propriété | Où |
|---|---|
| identité | `Engine.Name()` |
| rôle | `engineutil.Role(name)` |
| configuration | `Engine.Configure()` |
| endpoint d'entrée | ce que le composant *précédent* lui envoie (implicite : son propre listen) |
| endpoint de sortie | `Engine.(engine.Endpointer).Endpoint()` — l'endpoint que le composant *suivant* doit exposer, comparé à `RelaysTo` |
| protocole | `EngineCapability.Protocol` |
| transport | `EngineCapability.Network` (tcp/udp réellement lié) |
| capabilities | `EngineCapability{Provides, Requires, Network, RelaysTo}` |
| dépendances | `EngineCapability.Requires` (ex : `hysteria` requiert `udp-transport`) |
| lifecycle | `Engine.{Install,Configure,Start,Stop,Restart,Uninstall}` |
| health | `Engine.HealthCheck()` |

`ComputeChainLinks`/`EvaluateChain` (chain.go) calculent, pour une
composition ordonnée, chaque liaison adjacente candidate via `CanConnect` —
sans jamais démarrer ni enregistrer quoi que ce soit.

---

## 4. `CanConnect` : condition nécessaire, jamais suffisante

```go
func CanConnect(front, back string) (bool, string)
```

Compare `EngineCapabilitiesMap[front].RelaysTo` au
`EngineCapabilitiesMap[back].Network` **déclarés**. C'est un pré-filtre
statique (menu, `EvaluateChain`), jamais la porte d'exécution réelle :
`CompositeEngine.Start()`/`wireAdjacentChain` revérifient **toujours**
l'endpoint réellement exposé (`waitForEndpoint`) avant de câbler quoi que ce
soit — exactement la même discipline que celle déjà appliquée à l'étage
historique avant cette étape. Aucune condition dispersée du type `if name
== "xray"` n'a été ajoutée : tout passe par `EngineCapabilitiesMap`.

---

## 5. Adapters

La consigne demande de définir un `Adapter` explicite **si et seulement si
techniquement justifié**, et explicitement de ne pas en créer un artificiel
pour faire accepter une combinaison impossible.

**Aucun adapter concret n'est créé dans cette étape.** L'unique
incompatibilité connue aujourd'hui — un relais TCP-only (dnstt/slowdns) ne
peut pas atteindre un VPN purement UDP (hysteria, tuic) — ne se résout pas
par un adaptateur léger de traduction de protocole : `hysteria`/`tuic`
parlent un protocole applicatif (Hysteria2, TUIC v5) qui n'a pas
d'équivalent "même contenu, juste en TCP". Combler cet écart exigerait un
vrai démon de proxy TCP↔UDP avec état (un nouveau composant à part entière,
pas un adaptateur mince) — hors périmètre de cette étape ("ne crée pas
encore tous les nouveaux hybrides").

**Quand un adapter réel sera un jour justifié** (ex : un futur moteur qui
convertit réellement un flux), il n'a pas besoin d'un nouveau type : il
s'agit simplement d'un composant supplémentaire de la chaîne qui implémente
`engine.Engine` (+ `engine.Endpointer`) comme n'importe quel autre — donc
avec son propre lifecycle et son propre health via les interfaces
**déjà existantes**, pas une seconde représentation concurrente. Sa fiche
de capacité (`EngineCapability`) documenterait alors : `Network` (entrée),
`RelaysTo` (sortie), `Protocol`, et son rôle dans la doc décrirait la
conversion effectuée et ses limites. Introduire dès maintenant un type
`Adapter` inutilisé aurait d'ailleurs été signalé comme code mort par
l'outillage du dépôt (`find_unused.py`, `UNUSED_FILES.md`).

---

## 6. Lifecycle d'une chaîne

Pour toute paire adjacente câblée (historique ou généralisée), l'ordre est :

```
1. démarrer le composant le plus en aval (le prochain relais, ou le backend terminal)
2. vérifier son endpoint réel (engine.Endpointer)
3. configurer le composant amont avec cet endpoint réel ("backend" JSON)
4. démarrer le composant amont
5. (à la fin de Start()) lire l'endpoint réel de Components[0] -> ListenAddr de toute la chaîne
```

Pour une chaîne A → B → C entièrement câblée, cela se déroule bien dans
l'ordre demandé par la consigne : démarrer C, le vérifier, configurer B vers
C, démarrer B, (le vérifier), configurer A vers B, démarrer A — voir
`TestCompositeEngineChainsThreeRealStages`.

**Arrêt** : `Stop()` arrête déjà les composants en ordre inverse de
`Components` (inchangé, déjà correct avant cette étape).

**Échec d'un maillon** : `Start()` porte désormais un nettoyage (`defer`)
qui arrête tous les composants déjà démarrés par CET appel avant de
remonter l'erreur — la chaîne n'est jamais laissée partiellement "ON".
Vérifié par `TestCompositeEngineChainCleansUpStartedComponentsOnFailure`.
`HealthCheck()` reste agrégé (échoue si un seul composant échoue).

---

## 7. Ports

`DetectPortConflicts(components, prof)` (chain.go) compare, pour une
composition, le `(réseau, port)` **réellement configuré** de chaque
composant (`srvcfg.Profile.Port(name)`, qui retombe sur `DefaultPorts()` —
aucun port n'est codé en dur par combinaison) et son `Network` déclaré.
Exemple réel et vérifié (`TestDetectPortConflicts`) : `slowdns` et `dnstt`
partagent par défaut `udp/53` — signalé en conflit dès qu'ils coexistent
dans une composition (déjà empêché en amont par `ValidateHybrid`, qui
limite à un seul `RoleTransport` — cette détection reste utile pour toute
composition future qui ne passerait pas par ce garde-fou, ou pour les
combinaisons UDP+* du §7 ci-dessous). `xray` (tcp/443) et `tuic` (udp/443)
partagent le même numéro de port mais des réseaux différents : pas de
conflit, cohérence réseau réelle (TCP:443 et UDP:443 coexistent).

---

## 8. Évaluation honnête des chaînes futures demandées

`EvaluateChain` a été exercé (tests `TestEvaluateChainFutureCombosNotYetChainable`)
sur les combinaisons citées pour la suite. **Aucune n'est déclarée
fonctionnelle** — c'est le résultat, pas une simplification du test :

| Combinaison | Verdict | Pourquoi |
|---|---|---|
| TUIC + SSH + Xray | non chaînable | les trois sont terminaux (`RelaysTo` vide) : aucune paire adjacente n'est câblable, quel que soit l'ordre |
| TUIC + DNSTT + Xray | non chaînable | testé dans l'ordre `dnstt, tuic, xray` : dnstt sait relayer mais seulement vers du `tcp`, or son voisin adjacent tuic n'expose que de l'`udp` (paire 1 refusée) ; tuic est terminal, il ne peut pas relayer vers xray (paire 2 refusée) |
| Hysteria2 + SSH + Xray | non chaînable | les trois sont terminaux |
| Hysteria2 + DNSTT | non chaînable | hysteria est terminal (ne relaie pas) ; dnstt ne relaie que vers du `tcp`, hysteria n'expose que de l'`udp` — incompatible dans les deux ordres |
| UDP + Hysteria2 / UDP + TUIC / UDP + Xray | non chaînable | `udp` (`internal/engineudp`) est terminal (`RelaysTo` vide) **et** n'implémente pas `engine.Endpointer` (§1.6) : il ne peut même pas servir de backend câblé, encore moins de relais |

Ce tableau n'est pas un verdict définitif sur l'utilité produit de ces
combinaisons (elles peuvent très bien démarrer **en parallèle**, comme tout
hybride sans paire relayable aujourd'hui) — seulement sur leur chaînage
**réel** avec le mécanisme actuel. Faire évoluer ce verdict demande soit un
nouveau moteur relais UDP (hors périmètre ici), soit une extension future
et honnête de `EngineCapabilitiesMap`/`CanConnect`, jamais une déclaration
optimiste.

---

## 9. Preuve d'architecture : chaîne réelle à 3 étages

Aucun moteur produit ne permet de démontrer une chaîne A → B → C avec relais
à chaque étage (§2, §8). `chain_test.go` le démontre donc avec de **vrais**
moteurs de test (`archtest-a`, `archtest-b`, `archtest-c`) qui implémentent
réellement `engine.Engine` + `engine.Endpointer` : vrais sockets TCP
(`net.Listen`), vrai relais bidirectionnel (`io.Copy`), vrai composant
terminal (écho). Seul leur "protocole" est trivial — le mécanisme exercé
(`wireAdjacentChain`, le même code que celui utilisé en production) est,
lui, le code réel.

`TestCompositeEngineChainsThreeRealStages` : un client TCP se connecte à
l'endpoint réel de `archtest-a`, envoie un payload, et reçoit en retour
l'écho **exact** émis par `archtest-c` — preuve que les octets ont
réellement traversé A → B → C et sont revenus intacts. `Status().ListenAddr`
de la composite est vérifié égal à l'endpoint réel de `archtest-a`.

`TestCompositeEngineChainRefusesIncompatibleLinkAtAnyDepth` : le même
mécanisme généralisé refuse une chaîne `archtest-a` → `hysteria`
(relais TCP-only vers un endpoint UDP réel) avec une erreur claire, plutôt
que de démarrer les deux en silence.

`TestCompositeEngineChainCleansUpStartedComponentsOnFailure` : après cet
échec, le composant déjà démarré (`hysteria`) n'expose plus d'endpoint réel
— nettoyage vérifié, pas supposé.

---

## 10. CLI

`cmd/labosurf/menu.go` (`menuStatusDeep` → `printChainBreakdown`) affiche,
pour **tout** hybride réellement composé (détection générique par assertion
de type `*engineutil.CompositeEngine`, jamais un `if name == "tuic-ssh-xray"`
codé en dur), l'état réel de chaque composant puis l'état agrégé de la
chaîne :

```
● ON   TUIC
● ON   SSH
● ON   XRAY
● ON   CHAIN
```

Ce bloc ne s'affiche que pour un hybride que l'utilisateur a réellement créé
via "CRÉER UN MOTEUR HYBRIDE" — aucune des cinq futures combinaisons n'a été
ajoutée au menu ni pré-déclarée comme si elle était fonctionnelle.

---

## 11. Limites assumées de cette étape

- Une vraie chaîne A → B → C avec relais à **chaque** étage n'existe pas
  encore parmi les moteurs produit (§2, §8) — seul le mécanisme générique
  qui le permettrait est construit et prouvé (§9).
- `CanConnect`/`EvaluateChain` sont des pré-filtres déclaratifs : la seule
  autorité réelle reste l'endpoint constaté au démarrage
  (`engine.Endpointer`), inchangé depuis avant cette étape.
- Aucun adapter concret n'existe (§5) : aucune conversion de protocole
  TCP↔UDP n'est implémentée.
- Les cinq combinaisons à trois moteurs et les trois combinaisons UDP+*
  demandées pour la suite restent, avec le catalogue actuel, **non
  chaînables** de bout en bout (§8) — elles ne sont ni créées ni ajoutées
  au menu dans cette étape.
- `ValidateHybrid`/`CompatibilityCheck` (le guide de compatibilité
  existant) ne sont pas modifiés : `EvaluateChain` est un mécanisme
  **additionnel**, pas un remplacement.

---

## 12. Suite — audit des 7 combinaisons demandées et premier hybride réel prouvé

Cette section documente l'étape suivante : transformer les combinaisons
retenues en moteurs hybrides **réellement fonctionnels**, en écartant
explicitement toute fausse compatibilité.

### 12.1 Audit précis des 7 combinaisons

| # | Combinaison | Verdict | Raison bloquante |
|---|---|---|---|
| 1 | TUIC + SSH + Xray | **Impossible / non pertinent** | 2 moteurs `RoleVPN` (TUIC, Xray) → `ValidateHybrid` refuse (`ErrMultipleVPNs`, verrouillé par `TestFutureCombosCardinality`). Même sans cette règle, les 3 sont terminaux (`RelaysTo` vide) : aucune paire adjacente n'est câblable dans aucun ordre. |
| 2 | TUIC + DNSTT + Xray | **Impossible tel que formulé** — sous-partie `DNSTT → Xray` **réalisée et prouvée** (§12.3) | Même blocage de cardinalité (TUIC+Xray = 2 `RoleVPN`). |
| 3 | Hysteria2 + SSH + Xray | **Impossible / non pertinent** | Identique au #1 : 2 `RoleVPN` (Hysteria, Xray), aucun des 3 ne relaie. |
| 4 | Hysteria2 + DNSTT | **Impossible / non pertinent** | Seule combinaison à cardinalité valide (1 VPN + 1 transport, `TestFutureCombosCardinality`) — bloquée par une incompatibilité de **couche protocolaire**, pas seulement de réseau : Hysteria2 est un protocole à **trame discrète** (`engines/hysteria/server.go` : chaque datagramme UDP est une unité auto-porteuse `magic+sessionID+sequence+payload`, lue via `ReadFromUDP`), tandis que dnstt/slowdns ne transportent qu'un **flux d'octets sans frontière de message**. Un relais server-side ne peut pas retrouver les frontières de paquet perdues dans ce flux sans qu'un framing équivalent soit ajouté **côté client aussi** — ce qui reviendrait à inventer un protocole propriétaire des deux côtés (l'anti-modèle déjà écarté pour TUIC), pas « un relais ». `CanConnect` refuse déjà les deux sens (`TestFutureCombosLinkCompatibility`). |
| 5 | UDP + Hysteria2 | **Impossible / non pertinent** | 2 `RoleVPN`, et le moteur `"udp"` n'est structurellement ni transport ni backend câblable (§12.2). |
| 6 | UDP + TUIC | **Impossible / non pertinent** | Identique au #5. |
| 7 | UDP + Xray | **Impossible / non pertinent** | Identique au #5. |

### 12.2 `"udp"` (`internal/engineudp`) n'est pas un transport

Inspection complète de `engines/udp` (jamais faite en détail avant cette
étape) : c'est un **VPN complet et propriétaire**, avec sa propre trame
(`tunnel.go` : en-tête `version+ClientID`), son propre routage IP
(`tunnel_router.go`, `tun_linux.go`, un vrai périphérique TUN) — pas une
couche de transport générique, contrairement à ce que suggérait sa capacité
héritée `Provides: ["udp-transport", ...]`. C'est un moteur **terminal**,
de la même catégorie que xray/hysteria/tuic. Confirmé par exécution
(`TestUDPEngineHasNoRealEndpoint`) : son wrapper enregistré
(`engineutil.SystemEngine`) n'implémente même pas `engine.Endpointer`.

### 12.3 Extension du socle nécessaire : `CompositeEngine.ComponentConfig`

En tentant de câbler réellement `dnstt → xray` (la seule paire
« possible directement » de la matrice), le mécanisme historique à un seul
blob JSON **partagé** (`ConfigureAll`) s'est révélé insuffisant : il ne
fonctionne que si tous les composants savent lire des clés compatibles d'un
même objet — vrai par coïncidence pour dnstt+ssh (les deux lisent un même
tableau `"users"`), **faux** pour xray, dont le schéma
(`{"log":...,"inbounds":[...],"outbounds":[...]}`) n'a rien de commun avec
celui de dnstt (`{"domain":...,"backend":...}`).

`CompositeEngine` gagne donc un champ `ComponentConfig
map[string]engine.EngineConfig` : une configuration JSON propre à un
composant nommé, prioritaire sur la configuration partagée pour lui. En son
absence (`nil`), le comportement est **strictement identique** à avant
(tous les composants reçoivent la même configuration partagée) — vérifié
par toute la suite de tests existante, inchangée. Le champ interne
`lastCfg` (un seul blob) devient `lastCfgByComponent` (une valeur par
composant), utilisé par le câblage (`injectBackend`) pour ré-injecter le
`"backend"` réel dans la configuration **propre** du composant "front",
plutôt que dans un blob potentiellement étranger à son schéma. Test dédié :
`TestCompositeEngineComponentConfigOverride` (`internal/engineutil/chain_test.go`).

### 12.4 Preuve réelle : `dnstt → xray`

`engines/dnstt/composite_chain_xray_test.go` (`TestCompositeEngineChainsDNSTTToRealXrayBackend`)
télécharge et exécute le **vrai binaire xray-core officiel** (v26.3.27,
`Install()` réel, aucun mock), le configure en VLESS `tcp`/`security=none`
(un mode xray-core légitime, choisi pour isoler le mécanisme de chaînage de
la configuration REALITY de production — fonctionnalité xray-core déjà
existante et indépendante), le câble via `CompositeEngine` +
`ComponentConfig`, puis envoie une **vraie requête VLESS binaire** (encodée
à la main : version, UUID brut, commande TCP, adresse) à travers le
protocole DNSTT réel. Chaîne exercée, en conditions réelles :

```
client UDP → dnstt (réel) → [TCP câblé par CompositeEngine] → xray-core (réel, VLESS)
    → [TCP, "freedom" outbound d'xray-core — pas notre code] → vrai serveur cible TCP
```

Preuve obtenue (exécution réelle, log xray-core à l'appui —
`accepted tcp:127.0.0.1:46795 [direct]`) : la bannière distinctive émise
par le serveur cible traverse réellement toute la chaîne dans les deux sens
et revient au client via le tunnel DNSTT. **`dnstt → xray` (et par le même
mécanisme générique, `slowdns → xray`) est donc un hybride réellement
fonctionnel**, pas seulement architecturalement plausible.

Ce test dépend du réseau (téléchargement ~21 Mo, ~130 Ko/s observé dans cet
environnement, ~4-5 minutes) et d'un vrai binaire tiers : contrairement au
reste de la suite (entièrement hors-ligne et rapide), il est **désactivé
par défaut** (`go test ./...` le passe automatiquement, aucun risque de
rendre la suite standard flaky ou lente) et s'active explicitement avec
`LABOSURF_TEST_REAL_XRAY=1`. Exécuté avec succès dans cette session.

### 12.5 Écart découvert (pré-existant) : `ApplyServerConfig` ne génère pas de configuration groupée valide pour un hybride

En traçant le chemin réel qu'emprunterait la configuration en production
(menu central → "PROFIL SERVEUR" → `internal/clientcfg.ApplyServerConfig`),
un écart **pré-existant** (non introduit par cette étape, présent avant
même l'intégration TUIC) a été découvert et doit être signalé honnêtement :

`ApplyServerConfig(ctx, s, "dnstt-xray", prof)` appelle
`buildGroupedConfig("dnstt-xray", accounts, prof)`, qui tombe dans son cas
`default` (`isHybridName` vrai) et retourne
**`buildGroupedConfig("xray", ...)`** — la config **xray seule**
(`{"log":...,"inbounds":[...]}`). Cette config est ensuite diffusée **telle
quelle** aux deux composants via `ConfigureAll` (`e.Configure(ctx,
xrayOnlyConfig)`) : dnstt la reçoit aussi, alors qu'elle ne contient ni
`domain`, ni `port`, ni `users` — `dnstt.Configure()` ne la rejette pas
(JSON non vide), mais `dnstt.Start()` échouerait ensuite faute de config
exploitable. **Ce chemin de production ne produit donc pas une
configuration valide pour un hybride transport+VPN, quel que soit le
backend.**

Ceci touche également l'hybride `dnstt-ssh` déjà existant et déjà testé :
`primaryVPN("dnstt-ssh")` retourne `""` (ssh est `RoleAccount`, pas
`RoleVPN`), donc `buildGroupedConfig` retombe sur le placeholder générique
`{"engine":"dnstt-ssh"}` — également insuffisant pour dnstt. Le test réel
existant (`composite_chain_test.go`) et le nouveau test réel de cette étape
(§12.4) prouvent tous deux le **mécanisme de câblage** avec une
configuration correctement construite **à la main** (`sharedChainConfig`),
mais **aucun test actuel n'exerce `ApplyServerConfig` lui-même** pour un
hybride — ce chemin de génération de configuration n'a donc, à ce jour,
**jamais été prouvé fonctionnel de bout en bout**, y compris pour les
hybrides déjà en production.

Corriger `buildGroupedConfig`/`ApplyServerConfig` pour qu'il assemble une
configuration **par composant** (probablement en s'appuyant sur le nouveau
`CompositeEngine.ComponentConfig`) est un travail réel et distinct, non
entrepris dans cette étape (hors périmètre de l'audit demandé), mais c'est
désormais le prochain point bloquant identifié pour qu'un hybride
transport+VPN soit réellement déployable depuis le menu central, pas
seulement câblable en test.

### 12.6 Bilan

Sur les 7 combinaisons demandées : **aucune** n'est réalisable telle que
formulée (cardinalité ou couche protocolaire). Aucun faux adaptateur n'a
été créé pour contourner ces limites. La seule extension réellement
justifiée et prouvée est l'usage de `xray` comme nouveau backend derrière
dnstt/slowdns — rendue possible par une vraie amélioration du socle
(`ComponentConfig`), pas par un composant relais supplémentaire.

## 13. Correction de l'écart §12.5 : `ApplyServerConfig` représente désormais
    chaque composant d'un hybride

Cette étape corrige précisément l'écart décrit en §12.5, pour les quatre
hybrides visés (`dnstt-ssh`, `slowdns-ssh`, `dnstt-xray`, `slowdns-xray`) :
`buildGroupedConfig`, appelé pour un nom hybride, tombait dans son cas
`default` et retournait soit la config du VPN principal **seul** (perdant
la forme attendue par le transport), soit un placeholder générique
`{"engine": engineName}` (perdu par les deux composants) — jamais une
configuration par composant, alors même que
`CompositeEngine.ComponentConfig` existait déjà et n'attendait que d'être
alimenté (voir §12.3).

**Avant** : `ApplyServerConfig(ctx, s, "dnstt-xray", prof)` écrivait, sur le
fichier de configuration de `dnstt` comme sur celui de `xray`, la config
Xray-core complète (`{"log":...,"inbounds":[...]}`) — dnstt ne recevait
jamais `domain`/`port`/`users`. Pour `dnstt-ssh`, les deux composants
recevaient `{"engine":"dnstt-ssh"}`.

**Après** : `internal/clientcfg/clientcfg.go` détecte que le moteur résolu
est un `*engineutil.CompositeEngine` et construit, via la nouvelle fonction
`buildComponentConfigs`, une configuration **par composant** — en
réutilisant telle quelle, pour chacun, la fonction `buildGroupedConfig`
existante (celle déjà validée pour ce moteur en tant que moteur simple) —
puis peuple `CompositeEngine.ComponentConfig` avant d'appeler
`Configure()`. `dnstt` reçoit désormais `{"domain":...,"port":...,
"users":[...]}`, `xray` reçoit sa configuration VLESS/REALITY complète avec
l'UUID réel du compte, `ssh` reçoit `{"mode":"authorized_keys","port":22,
"users":[...]}` — chacun sa propre forme, jamais celle d'un autre
composant, jamais un placeholder.

**Ce qui rend cette correction possible sans modifier `CompositeEngine`** :
le champ `ComponentConfig` et sa consommation dans `Configure()` (§12.3)
étaient déjà complets et fonctionnels — seul `clientcfg.go` ne le peuplait
jamais. Aucun fichier hors `internal/clientcfg/clientcfg.go` (+ ses tests)
n'a été modifié pour cette correction.

**Nouvelle fonction `aliasGrantForComponent`** : un compte ne porte
aujourd'hui qu'un seul grant, sous le nom hybride littéral (ex.
`"dnstt-xray"` — `cmd/labosurf/menu.go:promptEngineAttach` n'accorde jamais
de grant séparé par composant). Pour que chaque appel de
`buildGroupedConfig(component, ...)` (qui lit `acc.Grants[component]`)
retrouve la bonne identité (uuid, clé publique...), `aliasGrantForComponent`
fournit une copie des comptes où `Grants[component]` est un alias du grant
hybride — cohérent avec `hybridClientLink`, qui lit déjà
`acc.Grants[hybridName]` pour le même champ.

**Ce que cette correction NE fait PAS** — à ne jamais présenter comme
acquis :
- Elle ne prouve **aucun câblage réseau réel** : c'est une correction de la
  **génération de configuration**, strictement distincte du câblage réel
  (`CompositeEngine.Start()`, inchangé) et d'un test réseau réel (aucun
  nouveau test réseau n'a été ajouté ici).
- Elle ne corrige pas la génération de secrets pour les grants hybrides :
  `internal/store/secrets.go:EnsureEngineSecrets` ne couvre aujourd'hui,
  pour les noms hybrides, que `"xray-slowdns"`/`"xray-dnstt"` (un ordre de
  nommage différent de celui utilisé partout ailleurs dans ce document,
  `"dnstt-xray"`/`"slowdns-xray"`) — aucun cas ne couvre `"dnstt-ssh"`,
  `"slowdns-ssh"`, `"dnstt-xray"`, `"slowdns-xray"`. Un compte disposant
  d'un grant sur l'un de ces quatre hybrides sans uuid/clé déjà fournis
  manuellement recevra donc une configuration **correctement formée mais
  avec des champs d'identité vides** (ex. `uuid-<id-compte>` de repli côté
  xray, `public_key`/`private_key` vides côté dnstt/slowdns). Ceci est un
  écart préexistant, distinct de celui corrigé ici, volontairement non
  traité dans cette étape (changerait le modèle de données des grants —
  décision à ne pas prendre unilatéralement).
- **`dnstt-ssh`/`slowdns-ssh` partagent un risque de collision de champ** :
  si un jour peuplés via secrets automatiques, `dnstt`/`slowdns` et `ssh`
  liraient tous deux `public_key`/`private_key` **depuis le même grant** —
  une même paire de clés se retrouverait utilisée à la fois pour
  l'authentification du tunnel DNS et pour l'authentification SSH, ce qui
  n'est pas souhaitable en production. Signalé ici, non résolu.

Tests ajoutés (`internal/clientcfg/clientcfg_test.go`) : génération pure
(`buildComponentConfigs`) pour les 4 combinaisons visées, vérifiant
explicitement l'absence de perte silencieuse du transport et l'absence de
contamination croisée entre formes de composants ; un test de bout en bout
(`TestApplyServerConfigHybridWritesRealFiles`) vérifiant les fichiers
réellement écrits sur disque via le registre réel ; un test de
non-régression du moteur simple (`TestApplyServerConfigSimpleEngineUnchanged`).
`go build ./...`, `go vet ./...` et `go test ./...` passent intégralement.
