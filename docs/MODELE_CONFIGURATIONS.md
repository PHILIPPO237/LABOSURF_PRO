# Modèle des configurations LABOSURF PRO

Ce document est le **modèle de référence** des configurations générées par
les assistants de LABOSURF PRO (`[2] CONFIGURER` du sous-menu des moteurs).
Il explique chaque champ pour que tout le monde puisse adapter les valeurs
sans maîtriser le format JSON de Xray-core ni de freeway-gate.

> Conseil : il n'est pas nécessaire d'écrire ces fichiers à la main — les
> assistants remplissent ces champs pour toi. Ce modèle sert à comprendre
> ce que l'on te demande et à vérifier la config d'une machine existante.

---

## 1. Configuration serveur Xray (`config.json`)

Fichier écrit dans `/etc/labosurf/engines/xray/config.json`, chargé par le
binaire Xray-core au démarrage.

### 1.1 Exemple réaliste — VLESS + REALITY + TCP (le mode recommandé)

```json
{
  "log": { "loglevel": "warning" },
  "inbounds": [
    {
      "port": 443,
      "listen": "0.0.0.0",
      "protocol": "vless",
      "settings": {
        "clients": [
          {
            "id": "b13e7f2a-… (uuid du store, voir GESTION DES UTILISATEURS)",
            "email": "alice@labosurf",
            "flow": "xtls-rprx-vision",
            "enabled": true
          }
        ],
        "decryption": "none",
        "fallbacks": []
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "show": false,
          "dest": "www.microsoft.com:443",
          "xver": 0,
          "serverNames": ["vpn.example.com"],
          "privateKey": "",
          "shortIds": [""]
        }
      },
      "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"] }
    }
  ],
  "outbounds": [
    { "protocol": "freedom", "tag": "direct" },
    { "protocol": "blackhole", "tag": "block" }
  ],
  "routing": {
    "domainStrategy": "AsIs",
    "rules": [
      { "type": "field", "outboundTag": "block", "protocol": ["bittorrent"] }
    ]
  },
  "dns": {
    "servers": [
      "https+local://8.8.8.8/dns-query",
      "https+local://1.1.1.1/dns-query",
      "localhost"
    ]
  }
}
```

### 1.2 Signification des champs (bloc inbound)

| Champ | Rôle | Valeur exemple |
|-------|------|----------------|
| `port` | Port d'écoute de l'inbound | `443` |
| `listen` | Adresse d'écoute. `0.0.0.0` = public (front direct). `127.0.0.1` = interne quand un tunnel (dnstt/slowdns) ou un reverse proxy (freeway-gate/Cloudflare) relaie vers lui | `0.0.0.0` |
| `protocol` | Protocole de l'inbound | `vless` |
| `clients[].id` | **UUID du compte** : vient du store central (ne pas inventer) | `b13e7f2a-…` |
| `clients[].flow` | Flow XTLS : `xtls-rprx-vision` (REALITY+TCP) ou `""` (aucun) | `xtls-rprx-vision` |
| `clients[].enabled` | Compte actif ou suspendu | `true` |
| `decryption` | Toujours `none` (VLESS) | `none` |
| `fallbacks` | Réponses aux requêtes non-clientes (ex. le vrai site) | `[]` |
| `streamSettings.network` | Transport : `tcp`, `ws`, `grpc`, `kcp`, `xhttp` | `tcp` |
| `streamSettings.security` | Sécurité : `reality`, `tls`, `none` | `reality` |
| `realitySettings.dest` | **Vrai site** servi aux visiteurs / garde REJECT (anti-détection) | `www.microsoft.com:443` |
| `realitySettings.serverNames` | SNI accepté (sous-domaine du profil, **DNS-only obligatoire**) | `["vpn.example.com"]` |
| `realitySettings.privateKey` | Clé privée REALITY : **laissée vide ici** — le moteur injecte la vraie clé (générée à l'installation) | `""` |
| `realitySettings.shortIds` | Identifiant court du serveur (vide = ok) | `[""]` |
| `sniffing` | Détection HTTP/TLS/QUIC pour le routage | activé |

### 1.3 Variantes du bloc `streamSettings` selon le transport

**WebSocket (ws)** — client → `wss://…` :
```json
"streamSettings": {
  "network": "ws",
  "security": "tls",
  "wsSettings": { "path": "/ws-tvlabo", "headers": {} },
  "tlsSettings": {
    "serverName": "vpn.example.com",
    "certificates": [
      { "certificateFile": "/etc/labosurf/engines/xray/cert.pem",
        "keyFile": "/etc/labosurf/engines/xray/key.pem" }
    ]
  }
}
```
> Derrière Cloudflare (sous-domaine ☁ proxysé) : `security` reste `tls`,
> l'inbound écoute en `127.0.0.1`, c'est Cloudflare qui termine le TLS.

**gRPC** :
```json
"streamSettings": { "network": "grpc", "security": "reality",
  "grpcSettings": { "serviceName": "labosurf" } }
```

**XHTTP / splithttp** (anti-filtrage, `mode` dans `auto`, `packet-up`,
`stream-up`, `stream-one`) :
```json
"streamSettings": { "network": "xhttp", "security": "reality",
  "xhttpSettings": { "path": "/tvlabo/xyz", "host": "", "mode": "auto" } }
```

**Sécurité TLS sans REALITY** : `"security": "tls"` + `tlsSettings` avec
`certificateFile`/`keyFile`. Le certificat peut être **auto-signé** (généré
par l'assistant, client en `allowInsecure`) ou **fourni** (Let's Encrypt).

---

## 2. Configuration freeway-gate (`config.json`)

Fichier écrit dans `/etc/labosurf/engines/freeway-gate/config.json`, chargé
par le binaire freeway-gate (reverse proxy CONNECT multi-opérateur
zero-rating MTN + Orange Maxit).

### 2.1 Exemple (défaut installé)

```json
{
  "listen": "127.0.0.1:8080",
  "hosts": {
    "mtn": ["proxy-mtn.", "mtn."],
    "orange": ["proxy-orange.", "orange."]
  },
  "profiles": {
    "mtn": {
      "target": "http://127.0.0.1:80",
      "headers": {
        "user-agent": "Mozilla/5.0 (Linux; Android 14; %MODEL%) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.7871.46 Mobile Safari/537.36",
        "accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "accept-encoding": "gzip, deflate, br",
        "accept-language": "fr-CM,fr;q=0.9,en-US;q=0.8",
        "referer": "https://one.mtn.cm/",
        "connection": "keep-alive",
        "sec-ch-ua": "\"Chromium\";v=\"150\", \"Google Chrome\";v=\"150\", \"Not?A_Brand\";v=\"99\"",
        "sec-ch-ua-mobile": "?1",
        "sec-ch-ua-platform": "\"Android\"",
        "sec-ch-ua-platform-version": "\"14.0.0\"",
        "sec-ch-ua-model": "\"%MODEL%\""
      },
      "ua_models": ["SM-A515F", "SM-A125F", "SM-G780F", "M2101K6G", "CPH2399"],
      "chains": [],
      "chain_target": ""
    },
    "orange": {
      "target": "http://127.0.0.1:81",
      "headers": {
        "user-agent": "OrangeMaxit/8.0.0 (Linux; Android 13; %MODEL%)",
        "accept": "application/json,text/plain,*/*",
        "accept-encoding": "gzip, deflate, br",
        "accept-language": "fr-CM,fr;q=0.9,en-US;q=0.8",
        "x-requested-with": "com.orange.myorange.cm",
        "x-caller-app-id": "maxit-cm-android",
        "referer": "https://maxit.orange.cm/",
        "connection": "keep-alive"
      },
      "ua_models": ["SM-A515F", "SM-A125F", "Pixel 7", "Redmi Note 12"],
      "auto_device_id": true,
      "chains": []
    }
  },
  "default_operator": "mtn",
  "rate_limit_max": 300
}
```

### 2.2 Signification des champs

| Champ | Rôle | Valeur exemple |
|-------|------|----------------|
| `listen` | Adresse:port du proxy CONNECT (côté client) | `127.0.0.1:8080` |
| `hosts.mtn` / `hosts.orange` | Noms d'hôtes proxy reconnus pour le zero-rating | `["proxy-mtn.", "mtn."]` |
| `profiles.mtn.target` | **Origine HTTP interne** servie aux clients MTN (le **port 80**) | `http://127.0.0.1:80` |
| `profiles.orange.target` | Origine HTTP interne servie aux clients Orange (le **port 81**) | `http://127.0.0.1:81` |
| `profiles.<op>.headers` | En-têtes HTTP imposés à la requête (fingerprint opérateur) | voir exemple |
| `profiles.<op>.ua_models` | Modèles d'appareils à injecter dans `%MODEL%` | `["SM-A515F"]` |
| `profiles.<op>.chains` | Chaînage CONNECT côte serveur (réservé) | `[]` |
| `profiles.<op>.chain_target` | Cible du chaînage (réservé) | `""` |
| `profiles.<op>.auto_device_id` | Générer un `device_id` aléatoire par requête | `true` |
| `default_operator` | Profil appliqué quand aucun hôte ne matche | `mtn` |
| `rate_limit_max` | Limite globale de requêtes | `300` |

> Les ports `80`/`81` sont tes **origines HTTP@toi** : ce que tu fais écouter
> sur le VPS en 80 (portail MTN) et 81 (portail Orange). Ils sont réglables
> via l'assistant freeway-gate et ne collident pas avec les ports moteurs.

---

## 3. Points de vigilance

- **Ne pas toucher `realitySettings.privateKey`** : le moteur injecte la vraie
  clé (générée à l'installation dans `/etc/labosurf/engines/xray/reality/`).
- **REALITY exige un sous-domaine en DNS-only** (nuage gris Cloudflare) :
  jamais un domaine proxysé (nuage orange), Cloudflare termine le TLS.
- **Derrière Cloudflare/tunnel** : `listen` = `127.0.0.1`, transports
  compatibles `ws` / `xhttp`.
- Les **UUID** des clients sont ceux du store central (jamais inventés).
- Ces blocs sont générés automatiquement par les assistants `[2] CONFIGURER`
  de chaque moteur.