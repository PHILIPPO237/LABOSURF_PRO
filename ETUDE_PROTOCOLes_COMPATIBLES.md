# Étude des protocoles compatibles — LABOSURF PRO

**Version 2** — mise à jour du 2026-09-11, remplace intégralement la version du
2026-09-10. Cette version élargit l'étude initiale (protocoles supplémentaires)
à une étude globale d'extension : robustesse, modularité, architecture Xray
détaillée (Protocol/Transport/Security/Flow), couche reverse proxy,
modélisation des capacités, génération de configuration, UX, roadmap.

Basée sur une lecture réelle du code du dépôt (état de travail actuel, non
commité inclus), sur `ARCHITECTURE_HYBRIDES.md` et
`AUDIT_ETAT_ACTUEL_LABOSURF_PRO.md` déjà produits, sur la version 1 de ce même
document, sur une inspection de l'archive de référence
`free-basics-chain-proxy` (voir §15 — usage volontairement restreint), et sur
la documentation officielle de chaque protocole/outil externe cité (sources
indiquées à chaque affirmation externe).

Aucun fichier de code n'a été modifié pour produire cette étude ; aucun
commit n'a été créé ; aucun binaire n'a été téléchargé ; `go.mod` n'a pas été
touché.

**Échelle d'étiquetage utilisée dans tout le document :**

- **[CONFIRMÉ]** — vérifié par lecture directe du code du dépôt, ou par une
  source officielle explicite pour un protocole externe.
- **[COMPATIBLE THÉORIQUEMENT]** — cohérent avec les propriétés documentées
  du protocole/outil, mais non démontré dans ce dépôt par un test réel.
- **[À TESTER]** — l'analyse ne permet pas de trancher sans un test réseau
  réel ; signalé explicitement en §16.
- **[INCOMPATIBLE]** — impossible avec l'architecture actuelle, raison
  structurelle donnée.
- **[NÉCESSITE NOUVELLE ARCHITECTURE]** — dépasse l'extension du modèle
  `CompositeEngine`/`EngineCapability` actuel.

---

## 1. Résumé exécutif

LABOSURF PRO est aujourd'hui un ensemble de 7 moteurs simples (`udp`, `xray`,
`hysteria`, `slowdns`, `dnstt`, `ssh`, `tuic`) plus un mécanisme de chaînage
générique (`CompositeEngine`) qui ne câble réellement qu'**un seul type de
liaison** : un transport DNS (`dnstt`/`slowdns`) relayant en TCP brut vers un
backend TCP (`ssh` en production, `xray` prouvé par test réseau désactivé
par défaut). Cette étude, beaucoup plus large que la version 1, établit
quatre conclusions structurantes :

1. **Le moteur `xray` n'exploite aujourd'hui qu'une combinaison unique du
   binaire Xray-core officiel : VLESS + TCP brut + REALITY + flow
   `xtls-rprx-vision`, avec `dest`/`serverNames` codés en dur sur
   `www.microsoft.com`** [CONFIRMÉ, code lu en entier]. Le commentaire
   `engines/xray/engine.go:6-8` qui énumère "VLESS, Trojan, VMess,
   Shadowsocks... WebSocket, gRPC, HTTP/2, HTTP/3... TLS, XTLS, REALITY"
   décrit les capacités du **binaire tiers**, pas ce que LABOSURF génère
   réellement — aucune de ces variantes n'est câblée dans
   `internal/clientcfg/clientcfg.go`. C'est l'écart le plus important
   identifié par cette étude, plus significatif que l'ajout de tout nouveau
   moteur externe.

2. **REALITY et un reverse proxy à terminaison TLS générique (Nginx/Caddy en
   mode HTTP classique) sont structurellement incompatibles**, confirmé par
   deux recherches indépendantes de la documentation officielle Xray/REALITY
   : REALITY exige que le serveur Xray reçoive le **ClientHello TLS brut et
   non modifié** pour pouvoir soit authentifier un vrai client REALITY, soit
   relayer un probe non authentifié vers le vrai site de camouflage (`dest`).
   Un reverse proxy qui termine le TLS casse ce mécanisme, quel que soit
   l'outil utilisé. **En revanche, un relais TCP/SNI-passthrough sans
   déchiffrement (Nginx `ssl_preread`, HAProxy `req.ssl_sni`, Caddy via le
   plugin `caddy-l4`) préserve le ClientHello intact et reste structurellement
   compatible avec REALITY** — c'est une nuance essentielle que cette étude
   documente précisément, conformément à la consigne de ne jamais déclarer
   une compatibilité TLS par simple analogie.

3. **Un "reverse proxy passthrough SNI" n'est pas une nouvelle abstraction
   architecturale — c'est le même patron que `dnstt`/`slowdns` aujourd'hui**
   (un composant qui relaie des octets bruts vers un backend fixe déterminé
   côté serveur), avec une seule extension modeste nécessaire (sélection du
   backend par domaine, pas par un unique champ `"backend"`). En revanche,
   **un reverse proxy à terminaison TLS classique (compatible XHTTP/WS/gRPC)
   n'est pas un "relais" au sens `CompositeEngine` du tout** — c'est un
   second moteur terminal indépendant, coordonné uniquement par allocation
   de port, sans mécanisme de câblage nouveau à construire.

4. **Le bug critique `ApplyServerConfig`/`buildGroupedConfig`** déjà identifié
   dans l'audit précédent reste, après cette étude plus large, le blocage
   technique le plus urgent : tant qu'il n'est pas corrigé, aucune des
   nouvelles combinaisons étudiées ici (Xray-variantes comprises) ne sera
   réellement déployable depuis le menu central, même une fois câblée en
   test.

Cette étude examine 12 candidats externes demandés, détaille en profondeur
l'architecture Protocol/Transport/Security/Flow de Xray, analyse le reverse
proxy comme couche (pas comme moteur), construit une matrice TCP/UDP/QUIC et
une matrice de compatibilité finale, propose une extension conceptuelle du
modèle de capacités, et livre une roadmap justifiée. **Le but explicite n'est
pas d'ajouter le maximum de moteurs, mais de séparer proprement moteur,
protocole, transport, sécurité, flow, reverse proxy, frontend, backend et
hybride** — cette séparation est le fil conducteur de tout le document.

---

## 2. Architecture actuelle

*(Synthèse condensée — le détail complet exhaustif reste dans
`AUDIT_ETAT_ACTUEL_LABOSURF_PRO.md` et la v1 de ce document ; cette section
se concentre sur ce qui bloque l'ajout propre de nouveaux moteurs.)*

### 2.1 Moteurs présents

| Moteur | Nature | Rôle | Réseau réel |
|---|---|---|---|
| `udp` | Go natif, VPN propriétaire | `RoleVPN`, terminal, **pas d'`Endpointer`** | udp |
| `xray` | **Binaire tiers officiel** (Xray-core) supervisé | `RoleVPN`, terminal | tcp |
| `hysteria` | Go natif, réimplémentation maison (pas le vrai Hysteria2) | `RoleVPN`, terminal | udp |
| `slowdns` | Go natif | `RoleTransport`, relaie vers tcp | udp (écoute), relais tcp |
| `dnstt` | Go natif | `RoleTransport`, relaie vers tcp | udp (écoute), relais tcp |
| `ssh` | Go natif (`golang.org/x/crypto/ssh`) | `RoleAccount`, terminal | tcp |
| `tuic` | **Binaire tiers officiel** (tuic-server) supervisé, non commité | `RoleVPN`, terminal | udp (QUIC) |

**[CONFIRMÉ]** Deux modèles d'intégration coexistent : binaire tiers officiel
téléchargé + vérifié SHA-256 + supervisé en sous-processus (`xray`, `tuic`),
et implémentation Go pure sans dépendance externe hors
`golang.org/x/crypto` (`udp`, `hysteria`, `slowdns`, `dnstt`, `ssh`).

### 2.2 Chaînes réellement fonctionnelles

**[CONFIRMÉ]** Un seul mécanisme de relais existe dans tout le dépôt :
`net.Dial("tcp", cfg.Backend)`, implémenté indépendamment dans
`engines/dnstt/server.go` et `engines/slowdns/server.go`. Chaînes prouvées :
`dnstt`/`slowdns` → `ssh` (production), `dnstt`/`slowdns` → `xray` (test réel,
désactivé par défaut). Tous les autres moteurs (`udp`, `hysteria`, `tuic`)
sont des composants terminaux (`RelaysTo == ""`).

### 2.3 `EngineCapability` / `CompositeEngine` / validation

**[CONFIRMÉ]** `EngineCapability{Provides, Requires, Protocol, Port, Network,
RelaysTo}` (`internal/engineutil/compat.go`). `CanConnect(front, back)`
compare `front.RelaysTo` à `back.Network`. `ValidateHybrid` limite à un seul
moteur par rôle transport/VPN (`ErrMultipleTransports`/`ErrMultipleVPNs`).
`CompositeEngine.Start()` câble en deux étages (`pickTransportAndBackend` puis
`wireAdjacentChain`), toujours en revérifiant l'endpoint réel via
`engine.Endpointer` avant de câbler quoi que ce soit — `CanConnect` n'est
jamais la porte d'exécution, seulement un pré-filtre. `ComponentConfig
map[string]engine.EngineConfig` existe et fonctionne dans `Configure()` (une
config JSON propre par composant, prioritaire sur la config partagée) mais
**n'est alimenté par aucun code de production** (voir 2.5).

### 2.4 Génération de configuration client/serveur

**[CONFIRMÉ]** `internal/clientcfg/clientcfg.go` (537 lignes) : `Generate()`
produit un lien client + un aperçu serveur par compte ; `ApplyServerConfig`
(lignes 364-385) → `buildGroupedConfig` (389-537, chemin de **production
réel**) → `e.Configure(ctx, EngineConfig{JSON})`. Cas explicites par moteur
simple ; pour un hybride (nom avec tiret), le cas `default` (528-536)
retombe sur la config du **VPN principal seul** (`primaryVPN`) ou sur un
placeholder `{"engine": engineName}` si aucun VPN primaire.

### 2.5 Limitations qui bloquent explicitement l'ajout propre de nouveaux moteurs

Cette sous-section répond directement à la consigne "identifier explicitement
les points qui bloquent actuellement l'ajout propre de nouveaux moteurs" :

1. **[CONFIRMÉ] Le moteur `xray` mélange Protocol/Transport/Security/Flow en
   un seul chemin de code figé** (`xrayServerConfig`/cas `EngineXray` de
   `buildGroupedConfig`) — il n'existe aucune structure de données
   intermédiaire représentant ces quatre axes séparément. Ajouter une seule
   variante (par exemple VLESS+WS+TLS simple) obligerait aujourd'hui à
   dupliquer toute la fonction de génération plutôt que de composer des
   options — exactement le problème que la Partie 3 doit résoudre
   conceptuellement.
2. **[CONFIRMÉ] `ComponentConfig` (le mécanisme qui permettrait une
   configuration par composant dans un hybride) existe dans
   `CompositeEngine` mais n'est jamais alimenté par `clientcfg.go`** — tout
   nouveau moteur chaînable ajouté aujourd'hui hériterait immédiatement du
   même bug que `dnstt-xray`/`dnstt-ssh` (configuration groupée invalide en
   production).
3. **[CONFIRMÉ] `srvcfg.Profile.Domains` existe mais n'est utilisé que par
   `firstDomain()` pour dnstt/slowdns — jamais par le moteur `xray`**, dont
   `dest`/`serverNames` REALITY restent codés en dur. Un nouveau moteur qui
   aurait besoin d'un domaine réel (ex. reverse proxy avec certificat Let's
   Encrypt, SNI REALITY dynamique) ne trouverait aujourd'hui aucun point
   d'entrée conventionnel pour le lire.
4. **[CONFIRMÉ] Aucune notion de "port public partagé" n'existe** : chaque
   moteur simple réserve son propre port (`DefaultPorts()`), aucun mécanisme
   ne permet à deux moteurs de coexister proprement sur le port 443 (un pour
   HTTP classique, un pour REALITY) sans un routeur en amont — pertinent
   directement pour la Partie 4/5.
5. **[CONFIRMÉ] Le menu (`cmd/labosurf/menu.go`) traite tout moteur via la
   même interface générique `engine.Engine`** (voir §12) — c'est une force
   pour l'extensibilité (un nouveau moteur n'a besoin d'aucun code de menu
   spécifique), mais cela signifie aussi qu'aucune UX différenciée n'existe
   aujourd'hui pour présenter des **variantes** d'un même moteur (ex.
   plusieurs configurations Xray) — un point à traiter en Partie 10/12.

---

## 3. Nouveaux moteurs — analyse systématique des 12 candidats

Pour chaque candidat : rôle, transport, TCP/UDP/QUIC, terminal ou capable de
relayer, besoin serveur/client, binaire externe ou lib Go, multi-architecture,
config serveur/client, compatibilité `CompositeEngine`/DNSTT/SlowDNS/SSH/Xray,
usage possible en frontend/backend derrière un reverse proxy, besoin de
nouvelle abstraction, priorité, verdict.

### 3.1 Tableau d'identité et de réseau

| # | Candidat | Protocole ou moteur ? | Transport | TCP/UDP/QUIC | Terminal / Relais | Serveur requis | Client requis |
|---|---|---|---|---|---|---|---|
| 1 | WireGuard | Protocole (Noise IK) + moteur à ajouter | UDP natif | UDP | Terminal | Oui (`wireguard-go` ou kernel) | Oui (client WG standard) |
| 2 | OpenVPN | Protocole + moteur à ajouter | TCP **ou** UDP (config.) | TCP/UDP | Terminal | Oui (`openvpn`) | Oui (client OpenVPN) |
| 3 | Shadowsocks | Protocole (déjà supporté par le binaire Xray-core présent, non exposé) | TCP (AEAD) | TCP | Terminal | Oui (Xray-core, déjà présent, OU `shadowsocks-rust`) | Client SS standard |
| 4 | Trojan | Protocole (déjà supporté par Xray-core présent, non exposé) | TCP (TLS mimé) | TCP | Terminal | Oui (Xray-core, déjà présent) | Client Trojan standard |
| 5 | VMess | Protocole (déjà supporté par Xray-core présent, non exposé) | TCP (+ WS/gRPC) | TCP | Terminal | Oui (Xray-core, déjà présent) | Client VMess standard |
| 6 | SOCKS5 | Protocole simple (RFC 1928), moteur Go natif réaliste | TCP (+UDP ASSOCIATE rare) | TCP | Terminal **ou** relais réinterprété (voir v1 §5) | Oui (implémentation légère) | Client SOCKS5 standard |
| 7 | HTTP CONNECT | Protocole simple, moteur Go natif réaliste | TCP | TCP | Identique à SOCKS5 | Oui (implémentation légère) | Navigateur/client HTTP proxy |
| 8 | Hysteria2 officiel | Protocole (TLS-over-QUIC + obfuscation Salamander) [CONFIRMÉ, doc officielle] | QUIC | QUIC (UDP) | Terminal | Oui (`hysteria` binaire officiel `apernet/hysteria`) | Client Hysteria2 standard |
| 9 | TUIC | **Déjà présent** dans LABOSURF (non commité) — étudié ici pour complétude | QUIC natif | QUIC (UDP) | Terminal | Déjà intégré (`tuic-server`) | Client TUIC v5 |
| 10 | NaiveProxy | Protocole (pile réseau Chromium, HTTPS mimé) | TCP | TCP | Terminal | Oui (binaire officiel `naive`) | Client Naive/Caddy-compatible |
| 11 | MASQUE | Protocole IETF (CONNECT-UDP over HTTP/3) | QUIC/HTTP3 | QUIC | Terminal, conçu comme relais UDP générique côté standard, mais aucune implémentation de référence simple | Oui, mais écosystème immature | Client MASQUE (rare) |
| 12 | AmneziaWG | Fork de WireGuard avec obfuscation DPI [CONFIRMÉ, doc officielle] | UDP natif | UDP | Terminal | Oui (`amneziawg-go`) | Client AmneziaWG |

### 3.2 Tableau d'intégration et de compatibilité

| # | Candidat | Binaire externe ou lib Go ? | Multi-arch | Config serveur | Config client | `CompositeEngine` compatible ? | DNSTT compatible ? | SlowDNS compatible ? | SSH compatible ? | Xray compatible ? |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | WireGuard | Binaire officiel `wireguard-go` (Go, userspace) [CONFIRMÉ, doc officielle] | Oui, y compris Android | INI (`wg.conf`) | INI + clé publique | **[COMPATIBLE THÉORIQUEMENT]** comme moteur terminal simple | **[INCOMPATIBLE]** (UDP à trame discrète) | **[INCOMPATIBLE]** (idem) | Sans objet (pas de relais SSH→WG) | **[INCOMPATIBLE]** en chaîne (2 `RoleVPN`) |
| 2 | OpenVPN | Binaire officiel, **pas de release statique multi-arch officielle** | **Incertain/faible** (obstacle réel) | Fichier `.ovpn` + PKI | Fichier `.ovpn` | **[COMPATIBLE THÉORIQUEMENT]** simple ; **[COMPATIBLE THÉORIQUEMENT]** chaîné en mode TCP uniquement | **[COMPATIBLE THÉORIQUEMENT]** (mode TCP) / **[INCOMPATIBLE]** (mode UDP) | Idem | Sans objet | **[INCOMPATIBLE]** en chaîne simultanée (2 `RoleVPN`) |
| 3 | Shadowsocks | Binaire déjà présent (Xray-core) ou `shadowsocks-rust` (releases statiques musl/arm64) | Oui | JSON (natif Xray ou SS) | Lien `ss://` | **[COMPATIBLE THÉORIQUEMENT]** | **[COMPATIBLE THÉORIQUEMENT]** | **[COMPATIBLE THÉORIQUEMENT]** | Sans objet | Extension du moteur existant, pas un chaînage |
| 4 | Trojan | Binaire déjà présent (Xray-core) | Oui | JSON Xray (inbound trojan) | Lien `trojan://` | **[COMPATIBLE THÉORIQUEMENT]** | **[COMPATIBLE THÉORIQUEMENT]** | **[COMPATIBLE THÉORIQUEMENT]** | Sans objet | Extension du moteur existant |
| 5 | VMess | Binaire déjà présent (Xray-core) | Oui | JSON Xray (inbound vmess) | Lien `vmess://` | **[COMPATIBLE THÉORIQUEMENT]** | **[COMPATIBLE THÉORIQUEMENT]** | **[COMPATIBLE THÉORIQUEMENT]** | Sans objet | Extension du moteur existant |
| 6 | SOCKS5 | Aucun, Go natif réaliste | Oui (Go pur) | JSON simple | Host/port(+auth) | **[COMPATIBLE THÉORIQUEMENT]** si réinterprété "backend fixe" | **[COMPATIBLE THÉORIQUEMENT]** (avec réinterprétation) | Idem | Sans objet | Sans rapport direct |
| 7 | HTTP CONNECT | Aucun, Go natif réaliste (`net/http`+`Hijacker`) | Oui | JSON simple | Host/port(+auth) | Identique à SOCKS5 | Identique | Identique | Sans objet | Sans rapport direct |
| 8 | Hysteria2 officiel | Binaire officiel `apernet/hysteria`, releases **Android arm64/amd64/armv7 + Linux multi-arch** [CONFIRMÉ, doc officielle — couverture Android plus large que TUIC actuel] | Oui, large | JSON/YAML officiel | Lien `hysteria2://` | **[COMPATIBLE THÉORIQUEMENT]** comme moteur terminal simple | **[INCOMPATIBLE]** (QUIC à trame discrète) | **[INCOMPATIBLE]** | Sans objet | **[INCOMPATIBLE]** en chaîne simultanée |
| 9 | TUIC | Déjà intégré (`tuic-server` v1.0.0) | Oui (déjà prouvé) | JSON officiel | Lien `tuic://` | **[CONFIRMÉ]** terminal, non chaînable | **[INCOMPATIBLE]** (QUIC à trame discrète) | **[INCOMPATIBLE]** | Sans objet | **[INCOMPATIBLE]** en chaîne simultanée |
| 10 | NaiveProxy | Binaire officiel (ou plugin Caddy), releases disponibles mais écosystème plus restreint | Partiel, à vérifier | JSON (format Caddy ou standalone) | JSON client | **[COMPATIBLE THÉORIQUEMENT]** | **[COMPATIBLE THÉORIQUEMENT]** (byte-stream TCP après handshake) | Idem | Sans objet | Sans rapport direct |
| 11 | MASQUE | Aucune implémentation de référence simple, écosystème Go quasi inexistant | Non évalué | Non standardisé en pratique | Non standardisé | **[NÉCESSITE NOUVELLE ARCHITECTURE]** | **[NÉCESSITE NOUVELLE ARCHITECTURE]** | Idem | Sans objet | Sans rapport direct |
| 12 | AmneziaWG | Fork officiel `amneziawg-go` (Go, même cœur crypto que WireGuard + 4 couches d'obfuscation) [CONFIRMÉ, doc officielle] | Oui (héritée de wireguard-go) | INI étendu (paramètres d'obfuscation) | INI étendu | **[COMPATIBLE THÉORIQUEMENT]** comme moteur terminal simple | **[INCOMPATIBLE]** | **[INCOMPATIBLE]** | Sans objet | **[INCOMPATIBLE]** en chaîne simultanée |

### 3.3 Frontend / backend / nouvelle abstraction / priorité / verdict

| # | Candidat | Utilisable derrière un reverse proxy ? | Frontend possible ? | Backend possible ? | Nouvelle abstraction requise ? | Priorité | Verdict |
|---|---|---|---|---|---|---|---|
| 1 | WireGuard | Non (UDP, pas de reverse proxy HTTP pertinent) | Non (protocole binaire, pas de routage par domaine) | Oui, comme terminal | Non pour l'ajout simple ; Oui pour un futur chaînage (moteur relais UDP) | **HAUTE** | **COMPATIBLE** (ajout simple) / **NÉCESSITE NOUVELLE ARCHITECTURE** (chaînage) |
| 2 | OpenVPN (TCP) | Oui (byte-stream TCP, y compris derrière un reverse proxy HTTP CONNECT historique — c'est sa raison d'être) | Non | Oui, comme backend chaîné | Non | **MOYENNE** (frein = packaging binaire) | **PARTIEL** (protocole OK, packaging à résoudre) |
| 3 | Shadowsocks | Oui (byte-stream TCP) | Non | Oui | Non | **HAUTE** | **COMPATIBLE** |
| 4 | Trojan | Oui, nativement conçu pour se déguiser derrière un reverse proxy HTTPS | Non | Oui | Non (extension du moteur `xray`) | **MOYENNE** | **COMPATIBLE** |
| 5 | VMess | Oui via WS/gRPC | Non | Oui | Non (extension du moteur `xray`) | **BASSE** (redondant avec VLESS+REALITY) | **COMPATIBLE**, faible valeur ajoutée |
| 6 | SOCKS5 | Sans objet (protocole d'accès, pas destiné à être proxifié HTTP) | Oui, comme point d'administration | Oui, si réinterprété | Non structurellement, mais réinterprétation sémantique à documenter | **BASSE** | **PARTIEL** |
| 7 | HTTP CONNECT | Sans objet | Oui | Oui, si réinterprété | Identique à SOCKS5 | **BASSE** | **PARTIEL** |
| 8 | Hysteria2 officiel | Non (QUIC, aucun reverse proxy HTTP standard ne relaie du QUIC natif sans lui-même parler QUIC) | Non | Oui, comme terminal | Non pour l'ajout simple | **HAUTE** | **COMPATIBLE** (ajout simple) |
| 9 | TUIC | Non (même raison, QUIC natif) | Non | Oui, comme terminal (déjà le cas) | Aucune (déjà intégré) | Déjà en cours | **COMPATIBLE** (ajout simple, déjà fait) |
| 10 | NaiveProxy | Oui, conçu explicitement pour ressembler à du HTTPS standard | Non | Oui | Non | **BASSE** | **COMPATIBLE**, écosystème plus restreint |
| 11 | MASQUE | Non pertinent avec l'état actuel de l'écosystème | Non | Non réaliste actuellement | Oui, complète | **BASSE** | **NÉCESSITE NOUVELLE ARCHITECTURE** |
| 12 | AmneziaWG | Non (UDP) | Non | Oui, comme terminal | Non pour l'ajout simple | **MOYENNE** (après WireGuard) | **COMPATIBLE** (ajout simple) |

---

## 4. Xray / XHTTP / TLS / REALITY / XTLS — étude approfondie

**Conformément à la consigne, XHTTP et XTLS ne sont pas traités comme des
moteurs indépendants** : ils sont deux axes de configuration du même moteur
`xray` déjà présent (respectivement Transport et Flow).

### 4.1 État réel du code aujourd'hui — écart entre commentaire et implémentation

**[CONFIRMÉ, code lu en entier par vérification dédiée.]**
`engines/xray/engine.go:6-8` :

```go
// Protocoles supportés : VLESS, Trojan, VMess, Shadowsocks, etc.
// Transports : TCP, WebSocket, gRPC, HTTP/2, HTTP/3, QUIC
// Sécurité : TLS, XTLS, REALITY
```

C'est une **description des capacités du binaire Xray-core tiers**, pas du
code de génération LABOSURF. Les deux seules fonctions qui construisent
réellement une configuration Xray (`xrayServerConfig` et le cas `EngineXray`
de `buildGroupedConfig`, `internal/clientcfg/clientcfg.go:180-247` et
`:391-462`) ne génèrent **que** :

```go
"streamSettings": map[string]any{
    "network": "tcp",
    "security": "reality",
    "realitySettings": map[string]any{
        "show": false, "dest": "www.microsoft.com:443", "xver": 0,
        "serverNames": []string{"www.microsoft.com"},
        "privateKey": "", "shortIds": []string{""},
    },
},
```

`dest`/`serverNames` sont codés en dur, `srvcfg.Profile.Domains` n'est jamais
lu par ce code. Le lien client (`clientcfg.go:145-155`) confirme la même
étroitesse : `flow`, `security=reality`, `sni` (fixe), `fp=chrome` (fixe),
`pbk` (réel), `sid=` (toujours vide), `type=tcp`, `headerType=none` —
**absents : `spx` (spiderX), tout paramètre `path`/`host`/`serviceName`**.
Aucune occurrence de `"ws"`, `"grpc"`, `"httpupgrade"`, `"xhttp"`,
`"security":"tls"` (sans reality), `"protocol":"vmess"`, `"protocol":"trojan"`
ou `"protocol":"shadowsocks"` nulle part dans `engines/xray/` ni
`internal/clientcfg/` (grep confirmé vide). Seul le champ `flow` est
overridable par compte (`grantString(acc, "xray", "flow")`).

**Conséquence directe** : LABOSURF n'expose aujourd'hui **qu'une seule**
combinaison Xray, la plus robuste contre la censure (VLESS+TCP+REALITY+
Vision), mais rien d'autre — ni les variantes plus simples (utiles derrière
un reverse proxy HTTP classique, un CDN, ou pour un serveur sans domaine
réel dédié), ni les autres protocoles pourtant déjà présents dans le binaire
téléchargé.

### 4.2 PROTOCOL (dimension 1) — sources officielles

**[CONFIRMÉ, doc officielle]** VLESS (déjà utilisé), VMess, Trojan,
Shadowsocks sont tous supportés nativement par le binaire Xray-core officiel
déjà téléchargé et supervisé par LABOSURF (`engines/xray/xray_binary.go`,
confirmé aucune nouvelle dépendance nécessaire). Ajouter un inbound
supplémentaire de l'un de ces protocoles est une extension de la fonction de
génération de configuration existante, pas un nouveau moteur.

### 4.3 TRANSPORT (dimension 2) — sources officielles

**[CONFIRMÉ, doc officielle]** Six transports Xray-core, configurés via
`streamSettings.network` :

| Transport | Nature | Compatible reverse proxy HTTP générique ? |
|---|---|---|
| **TCP (raw)** | Défaut, TCP brut, obfuscation d'en-tête HTTP optionnelle | **Non** — exige un accès TCP direct |
| **WebSocket (ws)** | HTTP/1.1, requête/réponse factice | **Oui** — proxifiable via Nginx/Caddy avec TLS terminé côté proxy |
| **HTTPUpgrade** | Handshake HTTP-upgrade puis flux brut, plus léger que WS | **Oui** |
| **gRPC** | HTTP/2, multiplexage optionnel | **Oui**, via un reverse proxy compatible HTTP/2 |
| **XHTTP (ex-SplitHTTP)** | Transport moderne HTTP/2 et HTTP/3-aware, ~30 champs | **Oui**, via un reverse proxy moderne HTTP/2+ |
| **mKCP** | UDP avec contrôle de congestion propre | **Non** — exige un accès UDP direct |

Sources : [Transport — Xray-core \| Core Tutorial](https://core-tutorial.argsment.com/xray/transport),
[XTLS/Xray-core (dépôt officiel)](https://github.com/xtls/xray-core).

**XHTTP en détail [CONFIRMÉ, doc officielle]** — trois modes de framing avec
auto-négociation par défaut : `packet-up` (chaque écriture = une requête POST
séparée, le plus compatible avec CDN/serveurs web génériques), `stream-up`
(un seul POST longue durée), `stream-one` (connexion de réponse maintenue sur
le même lien). D'après la discussion officielle
["XHTTP: Beyond REALITY"](https://github.com/XTLS/Xray-core/discussions/4113) :
TLS+H2 classique → `stream-up` ; **REALITY → `stream-one`** ; QUIC/H3 via CDN
→ `packet-up`. Interdiction documentée : ne pas activer `mux.cool` avec
XHTTP (seul XUDP pur accepté). **XHTTP fonctionne officiellement avec
XTLS-REALITY** — confirmé par une seconde source indépendante
([Doprax — XHTTP for VLESS](https://www.doprax.com/blog/xhttp-for-vless-what-it-is-why-it-exists-and-how-to-use-it/)).

### 4.4 SECURITY (dimension 3) — REALITY en détail, sources officielles

**[CONFIRMÉ, doc officielle, deux recherches indépendantes convergentes.]**
Mécanisme exact : le serveur Xray inspecte lui-même chaque ClientHello TLS
brut reçu sur le port configuré. S'il reconnaît une preuve d'authenticité
intégrée par un vrai client REALITY (dérivée de la clé publique/privée
X25519 et du `shortId`), il établit le tunnel. **Sinon, il relaie la
connexion telle quelle vers un vrai site tiers légitime** (`dest`, ex.
`www.microsoft.com:443`), qui répond avec son propre certificat authentique
— un observateur voit une connexion TLS parfaitement normale vers ce site
réel. `serverNames` est la liste blanche de SNI acceptés (tout SNI hors
liste est aussi relayé vers `dest`). Sources :
[REALITY/README.en.md (XTLS/REALITY, dépôt officiel)](https://github.com/XTLS/REALITY/blob/main/README.en.md),
[REALITY.ENG.md (Xray-examples, officiel)](https://github.com/XTLS/Xray-examples/blob/main/VLESS-TCP-XTLS-Vision-REALITY/REALITY.ENG.md),
[core-tutorial.argsment.com/xray/reality](https://core-tutorial.argsment.com/xray/reality).

**Réponse précise à la question posée par la mission (ne pas déclarer REALITY
compatible avec un reverse proxy par simple analogie TLS)** :

- **[INCOMPATIBLE]** REALITY derrière un reverse proxy à **terminaison TLS**
  générique (Nginx/Caddy en mode `http{}`/`reverse_proxy` classique) — le
  proxy déchiffre puis rechiffre (ou transmet en clair après déchiffrement),
  donc le serveur Xray ne voit **jamais** le ClientHello original du vrai
  client, ni ne peut relayer un probe non authentifié vers `dest` avec les
  octets d'origine. Le mécanisme entier de camouflage s'effondre.
- **[COMPATIBLE THÉORIQUEMENT]** REALITY derrière un **relais TCP/SNI-
  passthrough sans déchiffrement** (Nginx `stream{}` + `ssl_preread`, HAProxy
  `mode tcp` + `req.ssl_sni`, ou Caddy via le plugin `caddy-l4`) — ce mode ne
  touche à aucun octet du ClientHello, il route uniquement sur la base du SNI
  lu en clair (le SNI n'est pas chiffré en TLS 1.2/1.3 standard, sauf ECH).
  Documenté comme cas d'usage réel pour multiplexer plusieurs services sur le
  port 443. **Non prouvé par un test dans ce dépôt — voir §16.**

Sources reverse proxy : [ngx_stream_ssl_preread_module (Nginx, officiel)](https://nginx.org/en/docs/stream/ngx_stream_ssl_preread_module.html),
[HAProxy SNI routing](https://blog.none.at/blog/2019/2019-05-17-haproxy-sni-routing/),
[Caddy-L4 SNI routing (plugin `mholt/caddy-l4`)](https://medium.com/@panda1100/how-to-setup-layer-4-reverse-proxy-to-multiplex-tls-traffic-with-sni-routing-a226c8168826).

### 4.5 FLOW / XTLS (dimension 4) — sources officielles

**[CONFIRMÉ, doc officielle]** `xtls-rprx-vision` élimine la signature
"TLS-in-TLS" détectable par les systèmes de censure. **Exige `Transport:
tcp`** — aucune documentation officielle ne présente Vision comme compatible
avec XHTTP ou WebSocket ; la discussion #4113 ne mentionne Vision que dans un
contexte historique, jamais comme option pour XHTTP. Combinaison de référence
officielle/communautaire : **REALITY + `xtls-rprx-vision` + uTLS, toujours en
TCP**. Sources : [VLESS (XTLS Vision) — Project X](https://xtls.github.io/en/config/outbounds/vless.html),
["Vision and Reality, Which?" (discussion officielle)](https://github.com/XTLS/Xray-core/discussions/2166),
[VLESS-TCP-XTLS-Vision-REALITY (exemples officiels)](https://github.com/XTLS/Xray-examples/blob/main/VLESS-TCP-XTLS-Vision-REALITY/REALITY.ENG.md).

**C'est exactement la combinaison que LABOSURF utilise déjà** — LABOSURF a
donc, dès le départ, choisi la configuration Xray officiellement la plus
robuste, mais **la seule**.

### 4.6 Combinaisons valides/invalides documentées officiellement

**Valides (documentées explicitement) :**
- VLESS + TCP + REALITY + `xtls-rprx-vision` — configuration actuelle de
  LABOSURF, référence officielle.
- VLESS + XHTTP + REALITY — explicitement intentionnel (discussion #4113),
  mode `stream-one` par défaut.
- VLESS/Trojan/VMess + WS/HTTPUpgrade/gRPC + TLS classique — setups standards
  des exemples officiels.
- Xray peut démultiplexer plusieurs configurations sur un **même port
  d'écoute** (ex. VLESS-TCP-XTLS-Vision et VLESS-XHTTP-REALITY coexistant).

**Invalides/non pertinentes (documentées explicitement) :**
- `xtls-rprx-vision` + tout transport autre que TCP (XHTTP, WS, gRPC,
  HTTPUpgrade).
- `mux.cool` + XHTTP (seul XUDP pur accepté).
- REALITY (comme tout TLS) derrière un reverse proxy à terminaison TLS
  générique (§4.4).

### 4.7 Architecture conceptuelle proposée

**Conclusion directe demandée par la mission** : le moteur reste **un
seul**, `xray`. Ses variantes doivent être représentées par une **couche de
configuration à quatre axes orthogonaux**, pas par de nouveaux moteurs :

```
XrayVariant {
    Protocol  : vless | vmess | trojan | shadowsocks
    Transport : raw | ws | grpc | httpupgrade | xhttp
    Security  : none | tls | reality
    Flow      : none | vision            // valide seulement si Transport=raw ET Security∈{tls,reality}
}
```

**Règles de validité à faire respecter par le code de génération** (pas
seulement documentées, réellement vérifiées avant génération) :
- `Flow=vision` ⇒ `Transport=raw` (sinon rejet explicite, pas une config
  silencieusement invalide).
- `Security=reality` ⇒ `Transport ∈ {raw, xhttp}` (les deux seuls
  officiellement documentés comme testés avec REALITY) ; les autres
  transports avec REALITY sont **[À TESTER]**, pas à exclure par principe
  mais à ne pas présenter comme prouvés.
- `Protocol=shadowsocks` ⇒ `Security` généralement `none` (Shadowsocks a son
  propre chiffrement AEAD, superposer TLS est rare et non nécessaire) — à
  documenter comme recommandation, pas comme interdiction dure.

Cette structure est un **sous-ensemble strict** de l'extension
`EngineCapability` proposée en Partie 8 — elle ne doit pas devenir un second
système de typage parallèle. Elle correspond à un besoin déjà anticipé par
le champ `flow` déjà overridable par compte dans le code actuel (§4.1) : il
s'agit d'étendre ce même principe (override par compte/par déploiement) aux
trois autres axes, avec validation des combinaisons.

**Ce que cela change concrètement** : `xrayServerConfig`/`buildGroupedConfig`
(cas `EngineXray`) passeraient d'une fonction à sortie figée à une fonction
qui **compose** `streamSettings` à partir de `XrayVariant`, et le lien client
`vless://...` gagnerait les paramètres actuellement absents (`spx`, `path`,
`host`, `serviceName`) conditionnés par `Transport`. Aucun changement de
`CompositeEngine`/`compat.go` n'est nécessaire pour cela — c'est un
changement interne à `engines/xray`/`clientcfg.go` uniquement.

---

## 5. Reverse Proxy

**Étudié comme une COUCHE d'entrée, pas comme un moteur** — conformément à la
consigne.

### 5.1 Comparaison Nginx / Caddy / HAProxy

| Critère | Nginx | Caddy | HAProxy |
|---|---|---|---|
| TCP proxy générique | `stream {}` (natif) | Non natif — plugin `caddy-l4` (build custom via `xcaddy`) | `mode tcp` (natif, cœur historique) |
| HTTP reverse proxy | `http {}` + `proxy_pass` (natif) | `reverse_proxy` (natif, très simple) | `mode http` (natif) |
| WebSocket | Oui (`proxy_set_header Upgrade`) | Oui, nativement (`reverse_proxy` gère l'upgrade) | Oui (`mode http`, passthrough naturel) |
| HTTP/2 | Oui | Oui, natif | Oui (depuis 2.1) |
| gRPC | Oui (`grpc_pass`) | Oui, natif | Oui, en passthrough opaque (pas de traduction protocolaire, suffisant pour un simple relais) |
| XHTTP | Compatible (transport HTTP/2+ générique, aucune spécificité XHTTP requise côté proxy) | Idem | Idem |
| **TLS termination** | Oui (`http{}`) | Oui, **avec automatisation ACME intégrée** (avantage UX net) | Oui (`mode http` + certs fichier, pas d'ACME natif) |
| **TLS passthrough (SNI, sans déchiffrement)** | **Natif** : `ngx_stream_ssl_preread` [CONFIRMÉ, doc officielle] | **Plugin tiers requis** (`caddy-l4`, build custom) — pas dans le cœur | **Natif** : `req.ssl_sni` + ACL [CONFIRMÉ, doc officielle] |
| Routage par domaine | Oui (`server_name`) | Oui (natif, très ergonomique) | Oui (`req.ssl_sni`, ACL) |
| Routage par port | Oui | Oui | Oui |
| Health checks actifs | **Limité en version OSS** (passif via `max_fails`/`fail_timeout` ; actif = Nginx Plus commercial) | Basique | **Natif et réputé le plus complet** (`check`, `rise`/`fall`, agents de santé) |
| Failover | Oui (passif, `backup`) | Oui | Oui, avancé |
| Logs | `access_log`/`error_log`, matures | Structurés JSON par défaut, modernes | Très détaillés, orientés observabilité |
| Certificats | Fichier ou ACME externe (certbot) | **ACME automatique intégré** (zero-touch) | Fichier, pas d'ACME natif |
| Performance (débit) | Très bon, event-driven | ~25 % de moins que HAProxy dans certains benchmarks cités | **Le plus élevé** dans les benchmarks cités |
| Mémoire | Faible au repos | Un peu plus élevée (~40 Mo cité) pour le confort ACME | **La plus faible** (~34,6 Mo cité) |
| Intégration Linux | Native, paquet quasi universel | Native, binaire unique | Native, paquet quasi universel |
| Multi-architecture | Oui (arm/arm64 largement packagé) | Oui | Oui |
| Gestion depuis LABOSURF | Superviser un processus + template de config + reload signal — même patron que `xray`/`tuic` | Idem, mais nécessite un **build custom** si `caddy-l4` est requis (pas un simple téléchargement de binaire officiel générique) | Idem, binaire officiel standard |

Sources : [nginx.org — ngx_stream_ssl_preread_module](https://nginx.org/en/docs/stream/ngx_stream_ssl_preread_module.html),
[HAProxy SNI routing](https://blog.none.at/blog/2019/2019-05-17-haproxy-sni-routing/),
[Caddy-L4 SNI routing](https://medium.com/@panda1100/how-to-setup-layer-4-reverse-proxy-to-multiplex-tls-traffic-with-sni-routing-a226c8168826),
comparatifs de performance : [computingforgeeks.com](https://computingforgeeks.com/web-server-proxy-benchmark/),
[bigmike.help](https://bigmike.help/en/posts/102/).

### 5.2 Reverse proxy Go natif — écarté

Réimplémenter un reverse proxy générique (parsing TLS/SNI, gestion HTTP/2,
ACME) en Go maison romprait le modèle actuel "binaire tiers officiel
éprouvé, jamais une réimplémentation" (déjà appliqué à `xray`/`tuic`) pour un
domaine où des bugs de sécurité (parsing de ClientHello notamment) ont un
coût élevé. **[COMPATIBLE THÉORIQUEMENT] mais non recommandé** — superviser
HAProxy (passthrough) ou Nginx (les deux modes) suit un patron déjà validé,
sans réinventer un composant critique.

### 5.3 `ProxyLayer` ou `Frontend`/`ReverseProxy`/`Router` séparés ?

**Recommandation issue de cette étude, en évitant l'abstraction inutile
explicitement demandée par la mission** : **aucun des deux** au sens de
nouveaux types formels. Deux modes bien distincts existent, chacun se
rattachant à un concept **déjà présent** dans l'architecture :

1. **Mode passthrough (SNI, sans déchiffrement)** — structurellement
   identique à ce que `dnstt`/`slowdns` font déjà aujourd'hui : un composant
   qui écoute publiquement et relaie des octets bruts vers un backend fixe.
   **Ce n'est pas une nouvelle abstraction** — c'est un nouveau moteur de la
   même famille `RoleTransport`/`RelaysTo`, avec une seule extension modeste
   nécessaire : sélectionner le backend par **domaine (SNI)** plutôt que par
   un unique champ `"backend"` fixe (aujourd'hui, un `CompositeEngine` n'a
   qu'un seul backend par composition — la solution la plus simple, sans
   toucher au cœur du mécanisme, est de créer **une composition hybride par
   domaine** plutôt que d'étendre `CompositeEngine` lui-même).
2. **Mode terminaison TLS classique (compatible XHTTP/WS/gRPC/HTTPUpgrade)**
   — **ce n'est pas un relais au sens `CompositeEngine`/`RelaysTo` du tout**.
   Le reverse proxy termine le TLS et engage sa **propre** connexion vers
   Xray (qui écoute alors en interne, TLS désactivé côté Xray puisque déjà
   terminé en amont). C'est un second moteur **terminal indépendant**, lié
   au port public, coordonné avec `xray` uniquement par **allocation de
   port** (`srvcfg`) — aucun mécanisme de câblage `CompositeEngine` requis.

**Conclusion : ni `ProxyLayer` ni `Frontend`/`ReverseProxy`/`Router` ne sont
nécessaires comme nouveaux types de code.** Le reverse proxy devient
simplement un **8ᵉ moteur possible** (deux variantes de rôle selon le mode),
présenté dans le menu exactement comme les 7 moteurs actuels — voir Partie
10.

---

## 6. Compatibilité Reverse Proxy + Xray (analyse séparée par architecture)

| Architecture | Compatible ? | Conditions | TLS terminé où ? | Infos à préserver | Fonctions Xray cassées | Avantages | Inconvénients | Complexité | Recommandation |
|---|---|---|---|---|---|---|---|---|---|
| **Reverse Proxy → Xray + VLESS + RAW (TCP)** | **[INCOMPATIBLE]** en mode terminaison ; **[COMPATIBLE THÉORIQUEMENT]** en mode passthrough SNI | Le RAW/TCP exige un accès TCP direct — seul le passthrough préserve ça | Nulle part (passthrough) ou au proxy (terminaison, mais alors RAW n'a plus de sens : Xray recevrait du texte en clair sans TLS) | Le ClientHello brut si passthrough | Toutes (REALITY, Vision) si terminaison | Simplicité si passthrough | Aucun bénéfice réel de la terminaison sur du RAW | Faible (passthrough) | Passthrough uniquement, sinon inutile |
| **Reverse Proxy → Xray + XHTTP + TLS (classique, pas REALITY)** | **[COMPATIBLE THÉORIQUEMENT]** | XHTTP est HTTP/2+-aware, conçu pour être proxifié | **Au reverse proxy** | Rien de spécial — XHTTP ne dépend pas d'un ClientHello spécifique | Aucune fonction Xray cassée par nature (pas de REALITY/Vision ici) | ACME automatique (Caddy), routage multi-domaine facile, mutualisation du port 443 pour plusieurs services | Un composant de plus à sécuriser/maintenir | Moyenne | **Recommandée** pour un déploiement multi-domaine/multi-service classique |
| **Reverse Proxy → Xray + XHTTP + REALITY** | **[INCOMPATIBLE]** si le proxy termine le TLS ; **[COMPATIBLE THÉORIQUEMENT]** si passthrough SNI pur | REALITY exige le ClientHello brut quel que soit le transport applicatif choisi par-dessus (§4.4) | Jamais au proxy (sinon REALITY cassé) | ClientHello intact | REALITY entièrement, si terminaison | Aucun avantage à ajouter un proxy terminant ici | Casse silencieusement la sécurité si mal compris — **risque documenté explicitement par la mission** | Élevée (nuance facile à rater) | Passthrough uniquement, jamais de terminaison |
| **Reverse Proxy → Xray + WebSocket** | **[COMPATIBLE THÉORIQUEMENT]** | Cas d'usage historique le plus courant pour VLESS/VMess/Trojan+WS | **Au reverse proxy** (TLS classique, pas REALITY) | Rien de spécial | Aucune (WS n'utilise ni REALITY ni Vision dans la pratique documentée) | Très large compatibilité CDN (Cloudflare etc.), écosystème mature | Latence/overhead WS un peu plus élevé que raw | Faible | Recommandée si compatibilité CDN/proxy tiers requise |
| **Reverse Proxy → Xray + gRPC** | **[COMPATIBLE THÉORIQUEMENT]** | Nécessite un reverse proxy HTTP/2-aware (les trois le sont) | **Au reverse proxy** | Rien de spécial | Aucune | Bonne résistance à la détection (ressemble à du trafic gRPC légitime) | Écosystème un peu moins répandu côté clients que WS | Moyenne | Alternative valable à WS |

**Rappel explicite (consigne de la mission)** : REALITY n'est **jamais**
déclaré compatible avec un reverse proxy générique par simple présence de
TLS des deux côtés — seule la variante **passthrough sans déchiffrement**
préserve sa compatibilité, et ce point n'a **pas** été vérifié par un test
réel dans ce dépôt (§16).

---

## 7. Compatibilité avec les chaînes actuelles

### 7.1 Chaînes existantes (rappel, [CONFIRMÉ])

| Chaîne | Statut |
|---|---|
| DNSTT → SSH | **[CONFIRMÉ]** production |
| DNSTT → Xray | **[CONFIRMÉ]** test réel, désactivé par défaut |
| SlowDNS → SSH | **[CONFIRMÉ]** même mécanisme que DNSTT→SSH |
| SlowDNS → Xray | **[CONFIRMÉ]** même mécanisme que DNSTT→Xray |

### 7.2 Chaînes théoriques étudiées

| Chaîne | Type de flux | TCP/UDP/QUIC | Relais nécessaire ? | Statefulness | Sessions préservées ? | Nouvelle couche nécessaire ? | Verdict |
|---|---|---|---|---|---|---|---|
| Reverse Proxy → Xray | Byte-stream TCP (WS/gRPC/XHTTP) ou passthrough (RAW/REALITY) | TCP | Oui (le reverse proxy EST le relais) | Stateless côté relais HTTP ; stateful pour le passthrough (une session TCP = une connexion) | Oui, préservées (une connexion TCP = une session, pas de multiplexage cassé) | Non — patron déjà connu (§5.3) | **[COMPATIBLE THÉORIQUEMENT]** selon mode |
| Reverse Proxy → SSH | Byte-stream TCP | TCP | Oui | Stateful (session shell persistante) | Oui | Non | **[COMPATIBLE THÉORIQUEMENT]** (passthrough ou terminaison, SSH n'a pas de contrainte type REALITY) |
| Reverse Proxy → autres backends TCP (Shadowsocks, OpenVPN-TCP, Trojan, NaiveProxy) | Byte-stream TCP | TCP | Oui | Selon le protocole backend | Oui, si passthrough ou si le protocole backend ne dépend pas du ClientHello (Shadowsocks/OpenVPN-TCP : oui ; Trojan : dépend, car Trojan **authentifie via le TLS lui-même** — voir nuance ci-dessous) | Non | **[COMPATIBLE THÉORIQUEMENT]** avec la même nuance REALITY pour Trojan (Trojan utilise un vrai certificat TLS, moins strict que REALITY, mais la terminaison en amont change qui voit le certificat — à vérifier au cas par cas, **[À TESTER]**) |
| Reverse Proxy → TUIC | QUIC natif | QUIC/UDP | Oui, mais **aucun reverse proxy HTTP standard ne relaie du QUIC natif sans lui-même parler QUIC/HTTP3 nativement** | Stateful (sessions QUIC) | Non préservées par un simple relais TCP (TUIC n'est pas TCP) | **Oui** | **[NÉCESSITE NOUVELLE ARCHITECTURE]** — au mieux un futur relais QUIC-aware (type reverse proxy HTTP/3), hors périmètre actuel |
| Reverse Proxy → Hysteria2 | QUIC natif | QUIC/UDP | Identique à TUIC | Identique | Non préservées | **Oui** | **[NÉCESSITE NOUVELLE ARCHITECTURE]** |
| Reverse Proxy → WireGuard | UDP à trame discrète, pas de couche HTTP | UDP | Un "reverse proxy" au sens HTTP n'a aucun sens ici — il faudrait un relais UDP générique (pas un reverse proxy HTTP) | Stateful (sessions Noise) | Non préservées par un relais TCP ; un relais UDP dédié pourrait les préserver | **Oui** | **[NÉCESSITE NOUVELLE ARCHITECTURE]** |
| Reverse Proxy → OpenVPN | Dépend du mode : TCP (compatible, voir ligne "autres backends TCP") ou UDP (identique à WireGuard) | TCP ou UDP | Selon le mode | Selon le mode | Selon le mode | Non (mode TCP) / Oui (mode UDP) | **[COMPATIBLE THÉORIQUEMENT]** (TCP) / **[NÉCESSITE NOUVELLE ARCHITECTURE]** (UDP) |
| Reverse Proxy → UDP (moteur LABOSURF propriétaire) | UDP à trame propriétaire | UDP | Un relais UDP dédié serait nécessaire, **et** `udp` n'implémente même pas `engine.Endpointer` | Stateful | Non préservées | **Oui**, et bloqué même en amont par l'absence d'`Endpointer` (rappel audit précédent) | **[NÉCESSITE NOUVELLE ARCHITECTURE]** |

**Rappel de la consigne respectée** : aucune des lignes "NÉCESSITE NOUVELLE
ARCHITECTURE" n'a été requalifiée en "adaptateur" — un vrai relais TCP↔UDP ou
un relais QUIC-aware est un développement réseau à part entière, identifié
comme tel, pas minimisé.

---

## 8. UDP / QUIC

### 8.1 Matrice TCP / UDP / QUIC

| Composant | Réseau réel | Terminal ? | Frontend possible ? | Backend possible ? | Nécessite un proxy UDP dédié pour être chaîné ? | Chaînable avec l'architecture actuelle ? |
|---|---|---|---|---|---|---|
| `udp` (LABOSURF propriétaire) | UDP | Oui | Non (pas d'`Endpointer`) | Non (pas d'`Endpointer`) | Oui (et insuffisant seul, `Endpointer` manquant) | **[INCOMPATIBLE]** |
| `tuic` (déjà intégré) | QUIC (UDP) | Oui | Non | Oui, mais non relayable | Oui | **[INCOMPATIBLE]** en chaîne |
| Hysteria2 officiel (candidat) | QUIC (UDP) | Oui | Non | Oui, mais non relayable | Oui | **[INCOMPATIBLE]** en chaîne |
| WireGuard (candidat) | UDP à trame discrète | Oui | Non | Oui, mais non relayable | Oui | **[INCOMPATIBLE]** en chaîne |
| AmneziaWG (candidat) | UDP à trame discrète obfusquée | Oui | Non | Oui, mais non relayable | Oui | **[INCOMPATIBLE]** en chaîne |
| MASQUE (candidat) | QUIC/HTTP3 | Conçu pour relayer de l'UDP arbitraire côté standard IETF, mais aucune implémentation simple disponible | Non réaliste actuellement | Non réaliste actuellement | Oui, et l'écosystème logiciel manque | **[NÉCESSITE NOUVELLE ARCHITECTURE]** |

### 8.2 Synthèse

**[CONFIRMÉ]** Tous les composants UDP/QUIC étudiés (existants et candidats)
sont des **terminaux** au sens LABOSURF — aucun ne peut aujourd'hui servir de
frontend (point d'entrée chaînable) ni de relais vers un composant suivant.
La seule voie pour les chaîner un jour derrière `dnstt`/`slowdns` est un
**nouveau moteur relais UDP dédié**, explicitement classé
**[NÉCESSITE NOUVELLE ARCHITECTURE]** — jamais un simple adapter, conforme à
la position déjà prise dans `ARCHITECTURE_HYBRIDES.md` §5 et reconfirmée par
cette étude.

---

## 9. Matrice de compatibilité (Partie 11 de la commande)

| Composant | Type | Protocol | Transport | Security | Network | Frontend ? | Backend ? | Relay ? | Xray compatible ? | DNSTT compatible ? | SlowDNS compatible ? | Reverse Proxy compatible ? | `CompositeEngine` compatible ? | Architecture actuelle suffisante ? | Nouvelle architecture nécessaire ? | Priorité |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `xray` (VLESS+REALITY+Vision, existant) | Moteur | VLESS | raw | reality | tcp | Non | Oui | Non | — | Oui | Oui | Passthrough seulement | Oui | Oui | Non | — |
| `xray` variantes (VLESS+XHTTP+TLS, Trojan, VMess, SS) | Extension de moteur | VLESS/Trojan/VMess/SS | raw/ws/grpc/httpupgrade/xhttp | none/tls/reality | tcp | Non | Oui | Non | — | Oui | Oui | Oui (mode terminaison) | Oui | **Non** (dimension config manquante) | Non (extension interne) | **HAUTE** |
| WireGuard | Nouveau moteur | WireGuard | natif | Noise | udp | Non | Oui | Non | Non (2 VPN) | Non | Non | Non pertinent | Oui (ajout simple) | Oui (ajout simple) | Oui (chaînage) | **HAUTE** |
| OpenVPN (TCP) | Nouveau moteur | OpenVPN | TCP | TLS classique | tcp | Non | Oui | Non | Non (2 VPN) | Oui | Oui | Oui | Oui | Oui (protocole) / Non (packaging) | Non (protocole) | **MOYENNE** |
| OpenVPN (UDP) | Nouveau moteur | OpenVPN | UDP | TLS classique | udp | Non | Oui | Non | Non | Non | Non | Non pertinent | Oui (ajout simple) | Oui (ajout simple) | Oui (chaînage) | **BASSE** |
| Shadowsocks (nouveau moteur ou extension xray) | Moteur ou extension | Shadowsocks | tcp | AEAD propre | tcp | Non | Oui | Non | Extension possible | Oui | Oui | Oui | Oui | Oui | Non | **HAUTE** |
| Hysteria2 officiel | Nouveau moteur | Hysteria2 | QUIC | TLS/QUIC + Salamander | udp | Non | Oui | Non | Non (2 VPN) | Non | Non | Non pertinent | Oui (ajout simple) | Oui (ajout simple) | Oui (chaînage) | **HAUTE** |
| TUIC (déjà intégré) | Moteur (déjà présent) | TUIC v5 | QUIC | TLS/QUIC | udp | Non | Oui | Non | Non (2 VPN) | Non | Non | Non pertinent | Oui | Oui | Non | Déjà en cours |
| SOCKS5 | Nouveau moteur | SOCKS5 | tcp | aucune (nu) | tcp | Oui (admin) | Oui (réinterprété) | Réinterprété | Sans rapport | Oui (réinterprété) | Oui (réinterprété) | Sans objet | Oui, avec réinterprétation documentée | Oui | Non | **BASSE** |
| HTTP CONNECT | Nouveau moteur | HTTP CONNECT | tcp | aucune (nu) | tcp | Oui (admin) | Oui (réinterprété) | Réinterprété | Sans rapport | Identique | Identique | Sans objet | Identique | Oui | Non | **BASSE** |
| Trojan (extension xray) | Extension de moteur | Trojan | tcp/ws/xhttp | TLS classique | tcp | Non | Oui | Non | Extension | Oui | Oui | Oui | Oui | Oui | Non | **MOYENNE** |
| VMess (extension xray) | Extension de moteur | VMess | tcp/ws/grpc | TLS classique | tcp | Non | Oui | Non | Extension | Oui | Oui | Oui | Oui | Oui | Non | **BASSE** |
| NaiveProxy | Nouveau moteur | NaiveProxy | tcp | TLS (mime Chrome) | tcp | Non | Oui | Non | Sans rapport | Oui | Oui | Oui | Oui | Oui | Non | **BASSE** |
| AmneziaWG | Nouveau moteur | AmneziaWG | udp obfusqué | Noise + obfuscation | udp | Non | Oui | Non | Non (2 VPN) | Non | Non | Non pertinent | Oui (ajout simple) | Oui (ajout simple) | Oui (chaînage) | **MOYENNE** |
| MASQUE | — | CONNECT-UDP/HTTP3 | QUIC | TLS 1.3/QUIC | udp | Non réaliste | Non réaliste | Conçu pour, écosystème absent | Sans rapport | Non | Non | Non | Non | Non | Oui, complète | **BASSE** |
| Reverse Proxy (passthrough SNI) | Nouveau moteur (rôle transport) | — | tcp | passthrough (aucun déchiffrement) | tcp | **Oui** | Non (c'est un frontend) | **Oui** | Oui (préserve REALITY) | Sans rapport direct | Sans rapport direct | — | Oui (même famille que dnstt/slowdns, extension modeste "backend par domaine") | Oui, avec extension modeste | Non | **HAUTE** |
| Reverse Proxy (terminaison TLS) | Nouveau moteur terminal indépendant | — | tcp/http | tls classique (ACME possible) | tcp | **Oui** | Non (c'est un frontend) | Non (coordination par port, pas par `RelaysTo`) | Oui (WS/gRPC/XHTTP uniquement, jamais REALITY) | Sans rapport direct | Sans rapport direct | — | Non applicable (pas un chaînage `CompositeEngine`) | Oui, coordination par port suffisante | Non | **HAUTE** |

---

## 10. Architecture cible proposée

**Principe directeur, hérité de la v1 et renforcé par cette étude : ne rien
casser des moteurs existants, réutiliser le patron déjà éprouvé (interfaces,
registre, `EngineCapability`, `CompositeEngine`) et n'introduire une nouvelle
abstraction que là où l'analyse la justifie réellement.**

### 10.1 Ce qui ne change pas

- Le contrat `engine.Engine`/`engine.Endpointer` reste inchangé — tout
  nouveau moteur (WireGuard, Hysteria2 officiel, Shadowsocks, reverse proxy)
  l'implémente à l'identique.
- Le registre (`internal/engine/registry.go`) reste inchangé.
- `CompositeEngine`/`compat.go`/`chain.go` ne nécessitent **aucune**
  modification structurelle pour absorber les nouveaux moteurs terminaux
  (WireGuard, Hysteria2 officiel, AmneziaWG, OpenVPN) ni le reverse proxy en
  mode passthrough (même famille que `dnstt`/`slowdns`).

### 10.2 Ce qui doit changer, et où précisément

1. **`engines/xray` + `internal/clientcfg/clientcfg.go`** : introduire la
   structure `XrayVariant{Protocol, Transport, Security, Flow}` (§4.7) et
   faire composer `xrayServerConfig`/`buildGroupedConfig`(cas `EngineXray`) à
   partir de cette structure, avec validation des combinaisons invalides
   documentées (§4.6). **Aucun changement à `CompositeEngine`.**
2. **`internal/clientcfg/clientcfg.go` — correction du bug critique déjà
   documenté** (`buildGroupedConfig` cas `default` pour les hybrides) : faire
   assembler une configuration **par composant** en s'appuyant sur
   `CompositeEngine.ComponentConfig` (déjà prêt côté `composite_engine.go`,
   jamais alimenté). **Condition préalable à tout nouveau hybride utile en
   production**, Xray-variantes comprises.
3. **`internal/srvcfg/srvcfg.go`** : faire lire `Profile.Domains` par le
   moteur `xray` (SNI/REALITY dynamiques) au lieu du `www.microsoft.com` codé
   en dur — nécessaire dès qu'un opérateur veut personnaliser sa cible de
   camouflage, et prérequis naturel pour la coordination de port avec un
   futur reverse proxy (§10.4).
4. **Nouveau moteur `reverseproxy-passthrough`** (mode SNI) : implémente
   `engine.Engine`+`engine.Endpointer`, `Role() == RoleTransport`-équivalent,
   `RelaysTo: "tcp"` — s'intègre au `CompositeEngine` existant sans
   modification de celui-ci. Backend sélectionné par domaine via une
   composition hybride dédiée par domaine (pas une extension du champ
   `"backend"` en carte).
5. **Nouveau moteur `reverseproxy-terminating`** (mode TLS classique,
   optionnel, HAUTE priorité seulement si un besoin multi-domaine WS/gRPC/
   XHTTP est confirmé) : moteur terminal indépendant, coordination par port
   uniquement (`srvcfg`), **hors `CompositeEngine`**.
6. **Nouveaux moteurs terminaux simples** (WireGuard, Hysteria2 officiel,
   Shadowsocks/Trojan/VMess comme extensions `xray`, OpenVPN, AmneziaWG,
   NaiveProxy) : patron `tuic` standard, aucune extension architecturale.

### 10.3 Ce qui reste explicitement hors périmètre

- Un moteur relais UDP générique (pour chaîner `dnstt`/`slowdns` vers
  WireGuard/Hysteria2/TUIC) — **[NÉCESSITE NOUVELLE ARCHITECTURE]**, non
  entrepris ici, à valider par un besoin produit avant tout développement.
- Un relais QUIC-aware pour reverse proxy → TUIC/Hysteria2 — même statut.
- MASQUE dans son ensemble.

---

## 11. Modèle de capacités proposé (extension conceptuelle, pas de code)

### 11.1 Limites du modèle actuel

`EngineCapability{Provides, Requires, Protocol, Port, Network, RelaysTo}`
suffit pour un moteur "monolithique" (un `Protocol` = tout le moteur), mais
ne peut pas représenter :
- Un moteur avec plusieurs variantes internes (Xray + 4 axes).
- Un composant qui est à la fois "point d'entrée public" et "backend
  possible selon le contexte" (le reverse proxy peut être les deux selon le
  mode).
- Un composant dont le `RelaysTo` dépend d'une donnée dynamique (domaine)
  plutôt que d'un type réseau fixe.

### 11.2 Extension conceptuelle proposée

**Ne pas remplacer `EngineCapability` — l'étendre avec des champs optionnels
qui ne changent rien au comportement des moteurs existants s'ils sont
absents** (même philosophie que l'ajout non cassant de `Network`/`RelaysTo`
documenté dans `ARCHITECTURE_HYBRIDES.md` §2) :

```
EngineCapability (existant, inchangé) {
    Provides, Requires, Protocol, Port, Network, RelaysTo
}

// Champs optionnels envisageables, à n'ajouter QUE si un moteur réel
// en a besoin — jamais spéculativement :

    Accepts    []string   // variantes Protocol que ce moteur peut servir
                           // (ex: xray → ["vless","vmess","trojan","shadowsocks"])
    Frontend   bool       // peut être le point d'entrée public direct
    Backend    bool       // peut être backend d'un composant en amont
    StreamType string     // "raw-bytes" | "framed-datagram" | "http-terminated"
                           // — formalise la distinction déjà faite
                           // implicitement par CanConnect (byte-stream vs trame
                           // discrète), la rend explicite et documentée plutôt
                           // qu'implicite dans le code
```

### 11.3 Comment ce modèle représenterait les cas demandés

- **`Xray + VLESS + XHTTP + REALITY`** : un seul `EngineCapability` pour
  `xray` avec `Accepts` large ; la variante précise (Protocol/Transport/
  Security/Flow) est portée par `XrayVariant` (§4.7), **une couche
  distincte**, pas par `EngineCapability` lui-même — `EngineCapability`
  répond à "qu'est-ce que ce moteur PEUT faire en général" (compatibilité de
  chaînage), `XrayVariant` répond à "comment ce moteur est CONFIGURÉ cette
  fois" (détail applicatif). Les deux ne doivent pas être fusionnés.
- **`TUIC`** : `EngineCapability{Network:"udp", RelaysTo:"", StreamType:
  "framed-datagram"}` — inchangé dans l'esprit, `StreamType` rend explicite
  pourquoi `CanConnect` le refuse comme backend d'un transport TCP-only, sans
  changer le résultat du calcul actuel (qui fonctionne déjà correctement sans
  ce champ, via `Network`).
- **`DNSTT → Xray`** : inchangé, `RelaysTo:"tcp"` / `Network:"tcp"`,
  `StreamType:"raw-bytes"` des deux côtés — cohérent, rien ne change dans le
  calcul de compatibilité.
- **`Reverse Proxy → Xray`** : `reverseproxy-passthrough` aurait
  `Frontend:true, Backend:false, RelaysTo:"tcp", StreamType:"raw-bytes"` (mode
  SNI) — rejoint exactement la même famille que `dnstt`. Le mode terminaison
  n'a pas besoin de `RelaysTo` du tout (`Frontend:true, Backend:false`,
  coordination hors `CompositeEngine`).

**Le but explicite atteint** : une seule structure de moteur (`xray`) sert
toutes les variantes Xray ; `TUIC`/`DNSTT→Xray`/`Reverse Proxy→Xray` restent
représentables sans multiplier les moteurs artificiels, et sans jamais
déclarer un couple compatible sans base réelle (`StreamType` rend explicite
la règle déjà appliquée implicitement).

---

## 12. Génération de configuration

### 12.1 Ce que le système doit être capable de représenter

D'après l'analyse de `ApplyServerConfig`/`buildGroupedConfig`/
`ComponentConfig` (§2.4, §4.1), le système de génération doit représenter
correctement **cinq formes**, dont deux seulement sont couvertes aujourd'hui :

| Forme | Couverte aujourd'hui ? |
|---|---|
| Moteur simple (xray, ssh, udp...) | **Oui** [CONFIRMÉ] |
| Hybride transport+VPN (dnstt-xray) | **Non** — bug critique déjà documenté |
| Frontend/reverse proxy | **Non** — n'existe pas encore |
| Variante Xray (protocol/transport/security/flow) | **Non** — un seul chemin figé |
| Hybride avec reverse proxy en amont | **Non** — dépend des deux points précédents |

### 12.2 Changements architecturaux nécessaires AVANT toute implémentation

1. **Corriger `buildGroupedConfig` pour les hybrides** en s'appuyant sur
   `ComponentConfig` — condition préalable absolue, indépendante des
   nouveaux moteurs étudiés ici, déjà identifiée dans l'audit précédent.
2. **Introduire `XrayVariant`** comme paramètre d'entrée de
   `xrayServerConfig`/`buildGroupedConfig`(cas `EngineXray`) — sans cela, un
   compte ne peut recevoir qu'une seule variante figée pour tout le serveur,
   jamais un choix par compte/déploiement.
3. **Étendre `buildGroupedConfig` pour reconnaître un composant "frontend"**
   (reverse proxy) dans une composition — aujourd'hui, `isHybridName`/
   `primaryVPN` supposent implicitement "transport DNS + VPN", un reverse
   proxy frontend n'entre dans aucune des deux catégories actuelles.
4. **Lire `srvcfg.Profile.Domains`** dans le code Xray (et dans un futur
   moteur reverse proxy) au lieu de valeurs codées en dur — prérequis pour
   toute configuration réaliste multi-domaine.

Aucun de ces points ne nécessite de toucher `CompositeEngine`/`compat.go`
eux-mêmes — ils sont tous circonscrits à `clientcfg.go`/`engines/xray`/
`srvcfg.go`, cohérent avec le principe "ne rien casser" de la Partie 9.

---

## 13. UX / menu

### 13.1 Écart factuel important avec la structure supposée par la mission

**[CONFIRMÉ, code lu en entier]** Le menu central réel
(`cmd/labosurf/menu.go:42-83`, fonction `runCentralMenu`) est :

```
1  🔧 GESTION DES MOTEURS
2  👥 GESTION DES UTILISATEURS
3  🖥️ ÉTAT GLOBAL
4  ⚙️ PROFIL SERVEUR
9  ℹ️ À PROPOS
0  ❌ QUITTER
```

**Ce n'est pas exactement** `[01] GESTION DES MOTEURS / [02] GESTION DES
UTILISATEURS / [03] MAINTENANCE / [04] MISE À JOUR / [00] QUITTER` tel
qu'énoncé dans la commande — il n'existe aujourd'hui aucune entrée
"MAINTENANCE" de premier niveau, et "MISE À JOUR" n'est pas un menu
principal mais l'**option 8 à l'intérieur du sous-menu de chaque moteur**
(`runSingleEngineMenu`, lignes 315-407). Cette étude respecte la consigne
"conserver le menu principal actuel" en se basant sur le **menu réel
vérifié**, pas sur la structure supposée par la commande — signalé
explicitement pour éviter toute confusion future.

### 13.2 Mécanisme déjà générique — bonne nouvelle pour l'extensibilité

**[CONFIRMÉ]** `runSingleEngineMenu(e engine.Engine)` est un **seul** menu
générique (INSTALLER/CONFIGURER/DÉMARRER/ARRÊTER/REDÉMARRER/ÉTAT
DÉTAILLÉ/JOURNAUX/MISE À JOUR/DÉSINSTALLER) piloté uniquement via
l'interface `engine.Engine` — il s'applique **déjà** identiquement à un
moteur simple et à un hybride `CompositeEngine` (`printChainBreakdown`
affiche l'état ON/OFF par composant pour tout `*CompositeEngine` détecté par
assertion de type, aucun `if name == "..."` codé en dur). **Tout nouveau
moteur simple (WireGuard, Hysteria2 officiel, reverse proxy) hérite
automatiquement de ce menu sans code UX spécifique.**

### 13.3 Ce qui manque pour présenter les variantes Xray et le reverse proxy

1. **Variantes Xray** : le sous-menu CONFIGURER de `xray` devrait proposer un
   choix (Protocol/Transport/Security/Flow) avant de générer la config —
   aujourd'hui, CONFIGURER n'a aucun choix à faire (une seule sortie
   possible). C'est un ajout **à l'intérieur** du menu existant, pas un
   nouveau menu.
2. **Reverse proxy** : à présenter comme un **8ᵉ moteur** dans
   `runEngineMenu()` (qui liste déjà `engine.Names()` trié, mélangeant
   moteurs simples et hybrides) — cohérent avec la conclusion de la Partie 5
   ("le reverse proxy est une couche représentée comme un moteur", pas un
   nouveau menu principal). Le mode passthrough apparaît comme composant
   possible dans `runHybridCreateMenu` (comme `dnstt`/`slowdns` aujourd'hui) ;
   le mode terminaison apparaît comme moteur simple autonome.
3. **Statut ON/OFF, configuration, installation, suppression** : déjà
   couverts génériquement par `runSingleEngineMenu`/`printChainBreakdown` —
   aucune nouvelle UX à construire pour ces aspects.

**Aucun nouveau menu principal n'est proposé, conformément à la consigne.**

---

## 14. Roadmap

Chaque priorité est justifiée à partir de l'étude, pas reprise mécaniquement
de l'exemple de la commande.

### P0 — Corriger les fondations actuelles

- **Corriger `ApplyServerConfig`/`buildGroupedConfig` pour les hybrides**
  (§12.2 point 1). Justification : **tout** le reste de cette roadmap en
  dépend — un nouveau moteur chaînable, aussi bien conçu soit-il, restera
  "câblable en test, pas déployable en production" tant que ce point n'est
  pas réglé. C'est la seule action dont l'absence bloque toutes les autres.

### P1 — Xray variants (Protocol/Transport/Security/Flow)

Justification : c'est l'écart le plus important identifié par cette étude
(§1, §4.1) — LABOSURF prétend (via un commentaire de code) offrir VLESS,
VMess, Trojan, Shadowsocks, WS, gRPC, HTTP/2/3, TLS, XTLS, REALITY, alors que
le code n'en câble qu'une seule combinaison. Corriger cet écart **ne demande
aucun nouveau binaire, aucune nouvelle dépendance** — c'est le meilleur
rapport effort/valeur de toute l'étude, avant même l'ajout d'un seul nouveau
moteur externe.

### P1 — Hysteria2 officiel

Justification : résout une incohérence de nommage déjà documentée dans deux
audits précédents (le moteur `hysteria` actuel n'est pas le vrai protocole),
réutilise l'infrastructure TLS existante (`EnsureHysteriaCerts`, port 8443),
et bénéficie d'une couverture Android/multi-arch **officiellement documentée
plus large** que TUIC actuellement intégré (§3.1, point 8) — un vrai gain
utilisateur pour un effort d'intégration minimal, patron déjà validé (`tuic`).

### P1 — WireGuard

Justification : protocole le plus largement reconnu et déployé,
implémentation Go officielle mature (`wireguard-go`), aucune dépendance
nouvelle hors binaire supervisé — patron `xray`/`tuic` standard.

### P1 — Reverse Proxy (mode passthrough uniquement dans un premier temps)

Justification : contrairement à ce qu'un survol rapide suggérerait, le mode
passthrough **ne nécessite aucune nouvelle abstraction architecturale**
(§5.3) — c'est une extension du patron `dnstt`/`slowdns` déjà éprouvé. Il
débloque un vrai besoin produit (mutualiser le port 443 entre plusieurs
services/domaines) **sans risquer de casser REALITY**, à condition de ne
jamais implémenter le mode terminaison en même temps sans le documenter
séparément (risque de confusion explicitement signalé §6). Le mode
terminaison (P2, ci-dessous) est une extension distincte, plus tard.

### P2 — OpenVPN (mode TCP)

Justification : compatibilité protocolaire confirmée (byte-stream TCP,
chaînable derrière `dnstt`/`slowdns` avec le mécanisme déjà prouvé), mais
freinée par un vrai obstacle logistique (absence de binaires statiques
multi-arch officiels) qui demande un travail de packaging avant tout code
d'intégration — priorité intermédiaire, pas bloquante pour le reste.

### P2 — Shadowsocks/Xray variants (Trojan, VMess, Shadowsocks comme extensions du moteur `xray`)

Justification : une fois P1 "Xray variants" livré, ajouter ces trois
protocoles au même mécanisme de composition est un travail incrémental très
faible (le binaire est déjà téléchargé, déjà supervisé) — logiquement après
P1 Xray variants, pas avant, pour éviter de construire ces extensions sur
l'ancien mécanisme figé qu'il faudra de toute façon refondre.

### P2 — Reverse Proxy (mode terminaison TLS)

Justification : valeur réelle (compatibilité CDN large, ACME automatique)
mais dépend d'un besoin produit confirmé (multi-domaine/multi-service) — à
ne construire qu'après le mode passthrough (P1), qui couvre déjà le cas
d'usage le plus proche de l'architecture existante.

### P3 — autres composants

- **AmneziaWG** : après WireGuard simple (amélioration incrémentale, pas un
  premier pas).
- **SOCKS5 / HTTP CONNECT** : utilité produit à confirmer avant
  développement (outils d'administration/diagnostic plutôt que fonctionnalité
  VPN grand public), et nécessitent une décision de conception explicite sur
  la réinterprétation de leur sémantique standard.
- **NaiveProxy** : écosystème plus restreint, pas de raison de le prioriser
  avant les options ci-dessus.
- **MASQUE** : écosystème Go immature, aucune action recommandée avant une
  évolution significative de l'écosystème externe.
- **Moteur relais UDP générique / relais QUIC-aware** : uniquement après
  validation explicite d'un besoin produit — ce sont de vrais développements
  réseau, pas des extensions.

---

## 15. Décisions recommandées

1. **Ne pas créer de nouveaux moteurs pour Trojan/VMess/Shadowsocks** — les
   traiter comme des extensions du moteur `xray` existant (§4.7, §10.2).
2. **Ne pas créer `ProxyLayer`/`Frontend`/`Router` comme nouveaux types** —
   le reverse proxy est un moteur de plus (deux variantes selon le mode),
   pas une nouvelle catégorie architecturale (§5.3).
3. **Ne jamais permettre une configuration REALITY derrière un reverse proxy
   en mode terminaison** — à faire respecter par la validation de
   configuration elle-même (§4.7), pas seulement par la documentation.
4. **Corriger le bug `buildGroupedConfig` avant tout nouveau hybride** — sans
   quoi chaque nouvelle combinaison étudiée ici reproduira le même échec
   silencieux déjà documenté pour `dnstt-xray`/`dnstt-ssh`.
5. **Ne pas construire de moteur relais UDP générique ni de relais
   QUIC-aware sans besoin produit confirmé** — ce sont des développements
   réseau significatifs, pas des extensions à ajouter "pendant qu'on y est".
6. **Documenter explicitement, si SOCKS5/HTTP CONNECT sont un jour
   implémentés, que leur comportement diffère du standard** (destination
   forcée vers un backend fixe, pas choisie par le client) — même type de
   piège de confusion déjà signalé pour `Provides:["udp-transport"]` du
   moteur `udp`.
7. **Étendre `EngineCapability` uniquement par champs optionnels
   non cassants** (§11.2), jamais par un remplacement — cohérent avec la
   façon dont `Network`/`RelaysTo` ont déjà été ajoutés sans rien casser.

---

## 16. Éléments volontairement exclus

1. **Le mécanisme de contournement de facturation opérateur observé dans
   l'archive de référence `free-basics-chain-proxy`.** Inspection réelle de
   l'archive (`README.md`, `server.js`) : ce projet (dont le README
   s'identifie comme appartenant à "Philippo237", promoteur du "Laboratoire
   du Free-Surf") est un reverse proxy Node.js qui route le trafic VLESS/
   XHTTP selon le sous-domaine (`mtn.proxy.*`/`orange.proxy.*`) pour
   **imiter le trafic zero-rated "Free Basics" de MTN Cameroun et "Pass Max
   It" d'Orange Cameroun** (le README documente une injection d'en-têtes
   d'identification d'application, ex. `x-iorg-bsid`, `User-Agent` factice,
   package Android imité — mécanisme absent du `server.js` actuellement sur
   disque, qui délègue explicitement "le chaînage... côté client
   (proxySettings)", mais confirmé par la description du projet lui-même).
   **Ce mécanisme constitue un contournement de facturation opérateur au
   sens strictement prohibé par la mission elle-même** ("Ne cherche pas à
   contourner la facturation... d'un opérateur"). Cette étude n'a retenu de
   cette archive **que** les concepts génériques et légitimes de reverse
   proxy qu'elle illustre incidemment : routage par en-tête `Host`,
   passthrough WebSocket, page de camouflage/décoy pour le trafic non
   reconnu (technique légitime d'obfuscation, comparable au principe même de
   REALITY), en-têtes anti-buffering pour le streaming, limitation de débit,
   déploiement zéro-coupure. **Le mécanisme d'usurpation d'identité
   applicative pour échapper à la facturation n'a pas été étudié davantage
   ni recommandé sous quelque forme que ce soit dans ce rapport.**
2. **Tout adaptateur "de complaisance"** qui aurait présenté une combinaison
   structurellement incompatible (UDP à trame discrète derrière un relais
   TCP, QUIC derrière un reverse proxy HTTP classique) comme "compatible
   avec adaptation" alors qu'elle nécessite en réalité un nouveau composant
   réseau à part entière — systématiquement classée
   **[NÉCESSITE NOUVELLE ARCHITECTURE]** dans ce rapport (§7, §8), jamais
   requalifiée pour paraître plus simple qu'elle ne l'est.
3. **VMess comme priorité** : techniquement compatible (§3, §9) mais
   volontairement classé priorité BASSE — redondant avec VLESS+REALITY déjà
   en place, n'apporte pas de nouvelle combinaison de chaînage inédite.
4. **MASQUE comme candidat à développer maintenant** : écosystème Go
   quasi-inexistant, aucune implémentation de référence simple à superviser
   — exclu de la roadmap active (P3, sans action recommandée), à surveiller
   seulement.

---

## 17. Points nécessitant une preuve par test réel

Signalés explicitement, conformément à la consigne — aucun de ces points
n'est présenté comme prouvé ailleurs dans ce rapport :

1. **REALITY derrière un relais TCP/SNI-passthrough (Nginx `ssl_preread` ou
   HAProxy `req.ssl_sni`)** — cohérent avec la documentation officielle
   (§4.4, §6), mais **jamais testé dans ce dépôt**. Un test réel (client
   REALITY réel → relais passthrough → serveur Xray réel, vérification que
   le handshake REALITY aboutit et que le camouflage vers `dest` fonctionne
   toujours pour un probe non authentifié) est nécessaire avant toute
   annonce de compatibilité en production.
2. **XHTTP + REALITY en mode `stream-one`** — documenté officiellement comme
   la combinaison par défaut recommandée (§4.3), mais aucun test réel dans ce
   dépôt (le moteur `xray` actuel ne génère même pas encore de configuration
   XHTTP, prérequis à ce test).
3. **Trojan derrière un reverse proxy en mode terminaison** — la nuance
   signalée en §7 (Trojan authentifie via le TLS lui-même, contrairement à
   Shadowsocks/OpenVPN-TCP dont l'authentification est indépendante de la
   couche TLS) n'a pas été tranchée par une source officielle consultée ni
   par un test — à vérifier avant toute recommandation ferme.
4. **OpenVPN en mode TCP chaîné derrière `dnstt`/`slowdns`** — compatible en
   théorie (byte-stream TCP), mais jamais testé avec un vrai binaire OpenVPN
   dans ce dépôt, contrairement à `dnstt→xray` qui dispose déjà d'un test
   réel désactivé par défaut.
5. **Couverture multi-architecture réelle de `shadowsocks-rust` et
   `apernet/hysteria`** pour les cibles précises utilisées par LABOSURF
   (`linux-arm64-v8a` notamment, nom de cible spécifique déjà utilisé dans
   `xray_binary.go`) — la documentation officielle confirme une large
   couverture générale (§3.1, §3.2), mais la correspondance exacte avec les
   noms de cibles/architectures déjà pinnées dans LABOSURF n'a pas été
   vérifiée binaire par binaire.
6. **Licences précises des binaires tiers candidats** (WireGuard/
   `wireguard-go`, `apernet/hysteria`, `shadowsocks-rust`, NaiveProxy,
   OpenVPN) au regard d'une éventuelle redistribution par LABOSURF — les
   grandes lignes sont connues (§3.2, §14 de la v1) mais une vérification
   juridique précise n'a pas été faite ici.
7. **Performance réelle comparée Nginx/Caddy/HAProxy dans le contexte
   spécifique d'un VPS LABOSURF** (mémoire limitée, cible mobile/Android
   incluse) — les chiffres cités (§5.1) proviennent de benchmarks génériques
   tiers, pas d'une mesure dans l'environnement LABOSURF réel.

---

## 18. Mise à jour P2 — WireGuard réellement intégré (corrige §3/§4/§9/§12 ci-dessus)

Les entrées "WireGuard" des sections précédentes (§3.1-3.3, §9, §12) dataient
de l'étude initiale et **supposaient** une intégration via la bibliothèque Go
`wireguard-go` embarquée (option B alors envisagée). Après une intégration
réelle (mission P2), cette hypothèse s'est révélée **incorrecte** — corrigée
ici, sans réécrire les tableaux ci-dessus pour préserver l'historique de
l'étude.

### 18.1 Décision d'architecture réellement retenue

**[CONFIRMÉ]** Recherche directe sur les dépôts officiels (`gh api
repos/WireGuard/wireguard-go/releases` et `.../wireguard-tools/releases`) :
**aucun des deux ne publie de GitHub Release avec binaires précompilés**
(contrairement à `apernet/hysteria`/`tuic-protocol/tuic`) — `[]` dans les
deux cas. Distribution officielle = paquets système (module noyau mainline
depuis Linux 5.6, `wireguard-tools` empaqueté nativement par la quasi-totalité
des distributions) ou compilation depuis les sources.

Le module Go `golang.zx2c4.com/wireguard` existe et se résout via le proxy
Go officiel (confirmé : `v0.0.0-20260522210424-ecfc5a8d5446`, aucun tag
semver), mais deux faits l'écartent comme base d'intégration : (1) son
propre README ne documente qu'un usage en **ligne de commande**, aucune API
Go d'intégration tierce publique n'est fournie ni garantie stable dans le
temps (pas de semver) ; (2) ce même README affirme explicitement, pour
Linux : *"this will run on Linux; however you should instead use the kernel
module, which is faster and better integrated into the OS"* — le projet
déconseille lui-même cette voie sur la plateforme cible principale de
LABOSURF PRO (VPS Linux).

**Stratégie retenue : option A — outils système `wg`/`wg-quick` (module
noyau), pilotés en sous-processus** — même modèle que l'invocation
`openssl` déjà utilisée par `internal/engineutil/certutil.go` pour les
certificats TLS des autres moteurs. Génération/dérivation des clés
WireGuard (Curve25519) en **Go pur** (`internal/secret.X25519Keypair`, via
`golang.org/x/crypto/curve25519` — déjà une dépendance du dépôt, **aucune
nouvelle dépendance go.mod**).

**Limite assumée** : contrairement à xray/tuic/hysteria2/udp, cette
intégration ne couvre PAS Android/Termux (`/dev/net/tun` généralement
inaccessible sans root hors application système signée, `wg-quick`/module
noyau non empaquetés en Termux standard) — ce moteur cible exclusivement un
VPS Linux avec les outils système présents.

### 18.2 Fiche moteur (format demandé)

```
WireGuard
  Type: VPN
  Network: UDP
  Terminal: oui
  RelaysTo: aucun
  Port par défaut: 51820/UDP (convention officielle, aucune collision
                    avec udp/5667, xray/tcp:443, hysteria/8443, tuic
                    et hysteria2/udp:443)
  Binaire tiers téléchargé: AUCUN — outils système wg/wg-quick (module
                    noyau), détectés via exec.LookPath, jamais installés
                    automatiquement par LABOSURF PRO
  Prérequis système: paquet wireguard-tools (apt/dnf/pacman selon la
                    distribution) + module noyau WireGuard (mainline
                    depuis Linux 5.6, DKMS sur les noyaux plus anciens)
```

`DNSTT → WireGuard`, `SlowDNS → WireGuard`, `SSH → WireGuard` : **[INCOMPATIBLE]**
avec l'architecture actuelle (RelaysTo vide, UDP à trame Noise discrète) —
confirmé et verrouillé par test (`TestCanConnectWireGuardTerminal`,
`internal/engineutil/compat_test.go`). Aucun adaptateur développé.

Voir `engines/wireguard/engine.go` (commentaire de tête complet) pour
l'analyse détaillée des options comparées et leur justification.

---

## Sources externes citées

- Xray-core, transports : [core-tutorial.argsment.com/xray/transport](https://core-tutorial.argsment.com/xray/transport), [github.com/xtls/xray-core](https://github.com/xtls/xray-core)
- XHTTP : [XTLS/Xray-core discussion #4113](https://github.com/XTLS/Xray-core/discussions/4113), [Doprax — XHTTP for VLESS](https://www.doprax.com/blog/xhttp-for-vless-what-it-is-why-it-exists-and-how-to-use-it/), [XHTTP Transmission Guide](https://github.com/net4people/bbs/issues/440)
- REALITY : [XTLS/REALITY README](https://github.com/XTLS/REALITY/blob/main/README.en.md), [Xray-examples REALITY.ENG.md](https://github.com/XTLS/Xray-examples/blob/main/VLESS-TCP-XTLS-Vision-REALITY/REALITY.ENG.md), [core-tutorial.argsment.com/xray/reality](https://core-tutorial.argsment.com/xray/reality), [How REALITY works](https://objshadow.pages.dev/en/posts/how-reality-works/)
- XTLS Vision : [xtls.github.io VLESS outbound](https://xtls.github.io/en/config/outbounds/vless.html), [discussion #2166](https://github.com/XTLS/Xray-core/discussions/2166)
- WireGuard : [noiseprotocol.org](https://noiseprotocol.org/), [github.com/WireGuard/wireguard-go](https://github.com/WireGuard/wireguard-go), [Wikipedia — WireGuard](https://en.wikipedia.org/wiki/WireGuard)
- Hysteria2 : [v2.hysteria.network/docs/developers/Protocol](https://v2.hysteria.network/docs/developers/Protocol/), [github.com/apernet/hysteria/releases](https://github.com/apernet/hysteria/releases)
- TUIC : [zhuquejiasu.com — Deep Dive into Tuic Protocol](https://www.zhuquejiasu.com/en/blog/deep-dive-into-tuic-protocol-high-performance-proxy-architecture-based-on-quic-and-performance-b)
- AmneziaWG : [docs.amnezia.org](https://docs.amnezia.org/documentation/amnezia-wg/), [github.com/amnezia-vpn/amneziawg-go](https://github.com/amnezia-vpn/amneziawg-go), [AmneziaWG 2.0 — DEV Community](https://dev.to/bivlked/amneziawg-20-self-host-an-obfuscated-wireguard-vpn-that-bypasses-dpi-4692)
- Nginx : [ngx_stream_ssl_preread_module](https://nginx.org/en/docs/stream/ngx_stream_ssl_preread_module.html)
- HAProxy : [How does SNI Routing work in HAProxy](https://blog.none.at/blog/2019/2019-05-17-haproxy-sni-routing/)
- Caddy : [Caddy-L4 SNI routing](https://medium.com/@panda1100/how-to-setup-layer-4-reverse-proxy-to-multiplex-tls-traffic-with-sni-routing-a226c8168826)
- Performance reverse proxy : [computingforgeeks.com benchmark](https://computingforgeeks.com/web-server-proxy-benchmark/), [bigmike.help comparison](https://bigmike.help/en/posts/102/)

---

*Fin du rapport (version 2). Aucun fichier de code n'a été modifié, aucun
commit n'a été créé, aucun binaire n'a été téléchargé, `go.mod` n'a pas été
touché pour produire cette étude.*
