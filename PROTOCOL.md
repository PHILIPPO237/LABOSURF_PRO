# Protocole de tunnel LABOSURF PRO

Ce document décrit le protocole UDP utilisé entre un client VPN LABOSURF
et le serveur LABOSURF PRO sur le VPS.

---

## 1. Vue d'ensemble

- **Transport** : UDP (port par défaut **5667/UDP**)
- **Modèle** : handshake d'authentification en clair, puis transport de
  paquets IP bruts encapsulés dans un en-tête propriétaire
- **Chiffrement** : aucun chiffrement de payload dans la version actuelle.
  L'authentification utilise HMAC-SHA256 (challenge/réponse). Le tunnel
  transporte les paquets IP en clair après authentification.

> ⚠️ **Limitation connue** : le protocole ne chiffre pas le trafic IP. Pour
> une confidentialité de niveau production, une couche de chiffrement
> (par exemple DTLS ou une variante de WireGuard) devra être ajoutée
> ultérieurement. Le protocole actuel convient à un usage de test et à
> des environnements où la confidentialité du tunnel n'est pas critique.

---

## 2. Phase d'authentification

L'authentification est un échange de 4 messages texte en clair :

```
CLIENT                                SERVEUR
  │                                     │
  │ ────────── "HELLO" ───────────────► │
  │                                     │ génère nonce aléatoire 32 octets
  │ ◄──── "CHALLENGE <nonce_hex>" ────  │
  │                                     │
  │  calcule HMAC-SHA256(nonce, mdp)    │
  │ ──────── "AUTH <hmac_hex>" ───────► │ vérifie HMAC
  │                                     │ alloue IP tunnel
  │ ◄─────── "AUTH_OK <ip>" ──────────  │
  │                                     │
```

### Messages de contrôle

| Message | Direction | Description |
|---------|-----------|-------------|
| `HELLO` | C→S | Demande d'authentification |
| `CHALLENGE <hex>` | S→C | Nonce aléatoire 32 octets, hexadécimal (64 chars) |
| `AUTH <hex>` | C→S | HMAC-SHA256 du nonce avec le mot de passe du compte |
| `AUTH_OK <ip>` | S→C | Authentification réussie, IP tunnel assignée |
| `AUTH_OK` | S→C | Authentification réussie (pas de TUN configuré) |
| `AUTH_FAIL` | S→C | Authentification refusée |
| `MAX_CONNECTIONS` | S→C | Limite de connexions simultanées atteinte |
| `MAX_IPS` | S→C | Limite d'adresses IP simultanées atteinte |
| `TUNNEL_IP_UNAVAILABLE` | S→C | Pool d'adresses IP épuisé |
| `ACCOUNT_EXPIRED` | S→C | Le compte a expiré |
| `QUOTA_EXCEEDED` | S→C | Le quota de trafic est atteint |
| `PING` | C→S | Keepalive (toutes les 25 secondes) |
| `PONG` | S→C | Réponse au keepalive |
| `NO_SESSION` | S→C | Session absente ou expirée |
| `BACKEND_UNAVAILABLE` | S→C | Backend TCP injoignable (mode proxy uniquement) |

### Calcul HMAC

```
response = HMAC-SHA256(key=password, message=nonce_bytes)
```

Le nonce est transmis en hexadécimal mais HMAC est calculé sur les
**octets bruts** (pas sur la chaîne hex).

Le challenge est **à usage unique** : il est supprimé dès la première
tentative de vérification. Un challenge expire après 2 minutes.

---

## 3. Format des paquets tunnel

Après authentification, chaque datagramme UDP transporte un paquet IP
encapsulé :

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
├───────────────┬───────────────────────┬─────────────────────────
│   Version(1)  │     Réservé(3)        │      ClientID(8)
├───────────────┴───────────────────────┴─────────────────────────
│                      ClientID (suite)
├─────────────────────────────────────────────────────────────────
│                    Payload IP (variable)
└─────────────────────────────────────────────────────────────────
```

| Champ | Taille | Description |
|-------|--------|-------------|
| Version | 1 octet | Version du protocole tunnel = **1** |
| Réservé | 3 octets | Doit être à zéro. Ignoré en réception. |
| ClientID | 8 octets | SHA-256(client_ip:port)[0:8], big-endian |
| Payload | variable | Paquet IPv4 brut (20 à 65495 octets) |

### ClientID

Le `ClientID` est un identifiant opaque de session calculé comme :

```
ClientID = SHA-256("ip_source:port_source")[0:8]   (big-endian uint64)
```

Le serveur calcule le ClientID attendu à partir de l'adresse UDP source
et le compare à celui du paquet. Un paquet avec un ClientID incorrect
est **rejeté silencieusement**.

### Tailles

| Paramètre | Valeur |
|-----------|--------|
| En-tête tunnel | 12 octets |
| Taille max datagramme UDP | 65507 octets |
| Payload max | 65495 octets |
| MTU TUN par défaut | 1500 |
| MSS TCP clampé | MTU - 120 (= 1380 par défaut) |

---

## 4. Data path VPN

### 4.1 Aller (Client → Internet)

```
Application client
    │
    ▼
TUN client (paquet IP brut)
    │
    ▼  EncodeTunnelPacket(clientID, paquetIP)
Datagramme UDP → VPS:5667
    │
    ▼  DecodeTunnelPacket
    │  vérification session active
    │  vérification ClientID
    │  vérification anti-spoofing (src IP == IP tunnel allouée)
    │  vérification quota/expiration
    ▼
TUN serveur (écriture du paquet IP)
    │
    ▼  routage Linux + NAT/MASQUERADE
    ▼
Internet
```

### 4.2 Retour (Internet → Client)

```
Internet
    │
    ▼  routage Linux (conntrack NAT)
TUN serveur (lecture du paquet IP)
    │
    ▼  parseDstIP(paquet)
    │  TunnelIPPool.Lookup(dstIP) → clientID
    │  SessionManager.Get(clientID) → adresse UDP
    │  SessionManager.Authorize(clientID) → quota/expiration
    ▼  EncodeTunnelPacket(clientID, paquetIP)
Datagramme UDP → client_ip:client_port
    │
    ▼  DecodeTunnelPacket
    │  vérification ClientID
    ▼
TUN client (écriture du paquet IP)
    │
    ▼
Application client
```

---

## 5. Sécurité

### 5.1 Authentification

- Challenge/réponse HMAC-SHA256
- Nonce aléatoire 32 octets (crypto/rand)
- Usage unique (consommé à la première tentative)
- Expiration après 2 minutes

### 5.2 Protection contre l'usurpation

- **ClientID** : vérifié à chaque paquet tunnel (doit correspondre à
  l'adresse UDP source)
- **Anti-spoofing source IP** : le paquet IPv4 doit avoir une adresse
  source égale à l'IP tunnel allouée à la session. Tout autre paquet
  est rejeté et journalisé.
- **Session liée à l'adresse UDP** : si l'adresse IP ou le port du
  client change, il doit se ré-authentifier.

### 5.3 Quota et expiration

- Chaque paquet (aller et retour) est vérifié contre les règles du
  compte : expiration et quota de trafic.
- En cas de dépassement, le client reçoit un message de contrôle
  (`ACCOUNT_EXPIRED` ou `QUOTA_EXCEEDED`) et la session est fermée.

### 5.4 Limites

- Les paquets de plus de 65507 octets sont impossibles en UDP/IPv4
  (limite protocolaire).
- Les paquets tunnel dont la version n'est pas 1 sont rejetés.
- Les paquets non-IPv4 (version ≠ 4) sont rejetés en mode VPN.

### 5.5 Ce qui n'est PAS encore sécurisé

- **Pas de chiffrement du payload** : le trafic IP est en clair dans
  le tunnel UDP.
- **Pas de protection anti-rejeu** : un attaquant qui capture un paquet
  tunnel valide peut le rejouer (le ClientID sera correct).
- **Pas de vérification d'intégrité** : un paquet modifié en transit
  ne sera pas détecté au niveau tunnel (le checksum IP le détectera
  au niveau paquet, mais pas les modifications de l'en-tête tunnel).

Ces limitations sont acceptables pour un prototype mais doivent être
adressées avant un déploiement en production avec des exigences de
confidentialité.

---

## 6. Gestion des sessions

| Paramètre | Valeur par défaut |
|-----------|-------------------|
| Timeout d'inactivité | 60 secondes |
| Keepalive client | 25 secondes |
| Durée de vie du challenge | 2 minutes |
| Session liée à | adresse UDP (IP + port) |
| IP tunnel | unique, stable pendant la session, libérée à l'expiration |

La session expire si aucun paquet (données ou keepalive) n'est reçu
pendant 60 secondes. Le client envoie un `PING` toutes les 25 secondes
pour maintenir la session active et conserver le mapping NAT ouvert.

---

## 7. Compatibilité ZIVPN

**LABOSURF PRO n'est PAS compatible avec ZIVPN.**

Les deux produits utilisent UDP comme transport, mais :

| Critère | LABOSURF PRO | ZIVPN (connu) |
|---------|-------------|---------------|
| Handshake | HELLO/CHALLENGE/AUTH HMAC | Propriétaire, non documenté |
| Format paquets | En-tête 12 octets + IP brut | Propriétaire, non documenté |
| Authentification | HMAC-SHA256 | Propriétaire |
| Chiffrement | Aucun | Inconnu (probablement oui) |

Pour utiliser LABOSURF PRO, il faut un client qui parle le protocole
décrit dans ce document. Le client Linux/Go fourni (`labosurf vpn
connect`) en est l'implémentation de référence.

---

## 8. Configuration réseau côté serveur

Le serveur configure automatiquement au démarrage :

1. **Interface TUN** : adresse IP (ex: 10.77.0.1/24), MTU, état UP
2. **IPv4 forwarding** : `/proc/sys/net/ipv4/ip_forward = 1`
3. **Détection WAN** : interface de la route par défaut
4. **Firewall** : nftables (préféré) ou iptables (fallback)
   - Chaîne FORWARD dédiée : TUN↔WAN accepté, MSS clamping
   - Chaîne NAT dédiée : MASQUERADE du réseau VPN vers WAN
5. **Nettoyage** : les règles sont supprimées à l'arrêt propre

L'interface WAN n'est **jamais supposée être `eth0`** — elle est
détectée dynamiquement à chaque démarrage via `/proc/net/route` ou
`ip route get 8.8.8.8`.
