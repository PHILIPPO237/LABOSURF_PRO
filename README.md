# LABOSURF PRO

**Laboratoire du FreeSurf — PHILIPPO237**

LABOSURF PRO est une plateforme multi-moteurs pour services VPN et tunnels réseau, déployée sur un VPS Linux via un installateur shell, puis administrée en CLI par SSH — depuis un PC, ou depuis Android avec un client SSH comme **Termius**.

Dernière version stable : **v1.3.0** — <https://github.com/PHILIPPO237/LABOSURF_PRO/releases/latest>

> LABOSURF PRO est un projet distinct de l'outil interne de génération de licences de l'administrateur (dépôt privé séparé, non public). Les numéros de version des deux projets sont indépendants.

## Moteurs disponibles

| Moteur | Protocole | Port par défaut | Type | Description |
|--------|-----------|------|------|-------------|
| **UDP** | Labosurf UDP | 5667/UDP | VPN | Moteur VPN UDP natif (transport propriétaire, chiffrement HMAC) |
| **Xray** | VLESS / Trojan | 443/TCP | VPN | Proxy VLESS/Trojan natif (compatible clients V2ray/Xray) |
| **Hysteria** (maison) | trame UDP propriétaire (magic bytes) | 8443/UDP | VPN | Réimplémentation Go maison — **PAS le protocole Hysteria2 officiel** (pas de QUIC réel, pas d'obfuscation Salamander) |
| **TUIC** | TUIC v5 | 443/UDP | VPN | Binaire officiel `tuic-server` (QUIC/TLS, auth UUID + mot de passe) — pas une réimplémentation |
| **Hysteria2** (officiel) | Hysteria2 (TLS-over-QUIC) | 443/UDP | VPN | Binaire officiel `hysteria` (apernet/hysteria, obfuscation Salamander) — distinct du moteur "Hysteria" maison ci-dessus. Collision de port 443/UDP avec TUIC assumée (voir [Firewall](#firewall-ce-qui-est-automatique-et-ce-qui-ne-lest-pas)) |
| **WireGuard** | WireGuard (Noise) | 51820/UDP | VPN | Pilote les outils **système** `wg`/`wg-quick` (module noyau) — **aucun binaire téléchargé**, nécessite `wireguard-tools` installé manuellement sur le VPS (non couvert par l'installateur automatique aujourd'hui). Pas de support Android |
| **SlowDNS** | DNS Tunnel | 53/UDP | Transport | Tunnel DNS sur UDP (auth Ed25519, backend TCP) |
| **DNSTT** | DNS Tunnel | 53/UDP | Transport | Tunnel DNS quasi-indétectable (sessions, fragmentation) |
| **SSH** | SSH | 22/TCP | Accès | Serveur SSH natif (auth Ed25519, shell non-root) |

Chaque moteur est un **binaire autonome** (`labosurf-<moteur>`), déployé et supervisé par son propre service systemd — voir [Gestion des moteurs](#gestion-des-moteurs).

### Moteurs hybrides (Transport + Backend)

Le code supporte la composition d'un transport DNS avec un backend, via `internal/engineutil.CompositeEngine`. **Réellement câblées et testées** (endpoint réel, pas de mock) :
`dnstt-ssh`, `slowdns-ssh` (backend SSH) et `dnstt-xray`, `slowdns-xray` (backend Xray, prouvé par un test réseau réel désactivé par défaut — `LABOSURF_TEST_REAL_XRAY=1`).

La génération de la configuration groupée de production (`ApplyServerConfig`/`buildGroupedConfig`) représente désormais correctement **chaque composant** de ces quatre hybrides (corrigé — un composant transport ne perd plus silencieusement sa propre configuration au profit de celle du backend).

**Non chaînables avec l'architecture actuelle** (VPN purement UDP à trame discrète, aucun adaptateur développé) : `hysteria`, `hysteria2`, `tuic`, `wireguard`, `udp` — ces cinq moteurs sont des VPN **terminaux**, jamais un composant transport d'un hybride. Voir `ETUDE_PROTOCOLes_COMPATIBLES.md` pour l'analyse complète (compatible/incompatible/nécessite nouvelle architecture, par paire).

⚠️ **Cette fonctionnalité n'est pas accessible aujourd'hui via l'installation VPS standard.** Le CLI qui expose la création/suppression d'hybrides (`cmd/labosurf`, publié en release sous le nom `labosurf-mgr-<arch>`) n'est pas téléchargé par `labosurf-pro.sh`. Voir [Limitation connue : gestionnaire multi-moteurs](#limitation-connue-gestionnaire-multi-moteurs).

## Matrice de compatibilité

### Systèmes d'exploitation (VPS)

| Distribution | Statut | Détails |
|--------------|--------|---------|
| **Ubuntu 24.04 LTS** | ✅ Testé en CI | Le pipeline GitHub Actions installe et vérifie la release sur cette version |
| **Ubuntu 22.04 / 20.04 LTS** | ✅ Compatible (non testé en CI) | Même mécanisme `apt`/`systemd` ; binaires Go statiques (`CGO_ENABLED=0`, aucune dépendance glibc) |
| **Ubuntu 18.04 LTS** | ⚠️ Non supporté officiellement (EOL) | Support standard Ubuntu terminé en 2023 ; jamais testé ni maintenu par ce projet — à vos risques |
| **Debian 12 (Bookworm) / 11 (Bullseye)** | ✅ Compatible (non testé en CI) | Base `apt`/`systemd` identique à Ubuntu |
| **Linux Mint 21 / 20** | ✅ Compatible (non testé en CI) | Basé sur Ubuntu LTS, `/etc/os-release` reconnu explicitement par l'installateur |
| **Raspberry Pi OS (64-bit)** | ⚠️ Expérimental | `apt`/`systemd` présents ; seule l'architecture **arm64** est publiée en release — jamais testé sur matériel Raspberry Pi réel |
| **Raspberry Pi OS (32-bit / armhf)** | ❌ Non supporté | Aucun binaire `armhf`/`armv7l` n'est publié ; l'installateur refuse l'architecture (`arch_suffix` ne reconnaît que `amd64`/`arm64`) |
| **Pop!_OS / elementary OS / dérivées Debian** | ⚠️ Probablement compatible (non testé) | `ID` non reconnu par `check_os()` → avertissement affiché, mais l'installation continue via `apt` (héritage Ubuntu/Debian) ; jamais validé formellement |
| **Fedora / RHEL / Rocky / AlmaLinux / CentOS** | ❌ Non supporté | Utilisent `dnf`/`yum` ; le script appelle `apt-get` directement → échec propre à l'étape [4/11] Dependencies |
| **Alpine Linux** | ❌ Non supporté | `apk` + musl libc, init `OpenRC` (pas de `systemd`) |
| **Arch Linux / Manjaro** | ❌ Non supporté | `pacman`, incompatible avec `apt-get` |

> L'installateur détecte la distribution via `/etc/os-release` (champ `ID`) ; seuls `debian`, `ubuntu`, `linuxmint`, `raspbian` sont reconnus sans avertissement. Toute distribution non basée sur `apt` + `systemd` fait échouer l'installation.

### Android

**Ce dont un utilisateur a besoin sur Android : un client SSH, rien d'autre.** Il reçoit de l'administrateur un jeton d'activation, installe LABOSURF PRO sur son propre VPS avec le script d'installation (voir [Installation VPS](#installation-vps-linux)), puis administre ce VPS au quotidien depuis son téléphone avec **Termius** (client SSH dédié — hôtes/clés sauvegardés, SFTP, clavier adapté) ou tout autre client SSH Android :

```bash
ssh root@<votre-vps>
menu          # ou : labosurf
```

Aucune installation locale sur le téléphone n'est nécessaire.

### WSL (Windows Subsystem for Linux)

| Usage | Statut |
|---|---|
| A. Environnement de développement/build (compiler, lancer les tests Go) | ✅ Fonctionne comme n'importe quelle distribution Linux |
| B. Installer et faire tourner LABOSURF PRO comme serveur VPN dans WSL | ⚠️ Expérimental, non recommandé en production |
| C. Utiliser WSL comme client pour administrer un VPS distant en SSH | ✅ Fonctionne (client SSH standard, aucune dépendance LABOSURF particulière) |

Détails pour le cas B : `systemd` n'est activé par défaut sur aucune distribution WSL (nécessite `[boot] systemd=true` dans `/etc/wsl.conf`, disponible seulement sur WSL récent) ; le réseau WSL2 est NAT par défaut, donc un VPN "serveur" tournant dans WSL n'est pas directement joignable depuis Internet sans redirection de ports côté Windows ; le support de `/dev/net/tun` dépend du noyau WSL2 utilisé. **Ne pas utiliser WSL comme substitut à un vrai VPS pour un déploiement réel.**

## Prérequis

- VPS ou machine Linux avec accès `root`/`sudo`
- Distribution `apt`-based avec `systemd` (voir matrice ci-dessus)
- Adresse IP publique atteignable si le service doit être exposé sur Internet
- Une clé d'activation LABOSURF PRO fournie par l'administrateur (Ed25519, valide 3 heures, usage unique — voir [Licence et activation](#licence-et-activation))

## Installation VPS (Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/PHILIPPO237/LABOSURF_PRO/main/labosurf-pro.sh | sudo bash
```

L'installateur exécute 11 étapes numérotées :

1. **System Check** — détecte l'OS et l'architecture (`amd64`/`arm64`)
2. **License Validation** — télécharge le binaire vérificateur + la clé publique depuis la release, puis demande la clé d'activation (avant tout autre changement système)
3. **Environment Preparation** — charge le module `tun`, active `net.ipv4.ip_forward`, détecte l'interface WAN
4. **Dependencies** — installe via `apt-get` : `ca-certificates curl wget openssl iptables coreutils openssh-server`
5. **Download Components** — télécharge les binaires des moteurs sélectionnés (vérification SHA-256 systématique via `SHA256SUMS`)
6. **Install Binaries** — installe les services systemd par moteur
7. **Configure LABOSURF PRO** — écrit `/etc/labosurf/config.json` et la commande `menu`
8. **Install Services** — installe et active le service central + les services moteurs
9. **Start Services** — démarre tous les services
10. **Health Check** — vérifie réellement (`systemctl is-active`) que chaque service sélectionné tourne ; échoue si un moteur n'a pas démarré
11. **Finalization**

### Firewall : ce qui est automatique et ce qui ne l'est pas

L'installateur ouvre automatiquement, via `iptables` (ou `nft` en secours si `iptables` est absent) :
- **5667/UDP** (moteur UDP natif)
- **8080/TCP** (portail HTTP intégré)

**Il n'ouvre pas automatiquement** les ports des autres moteurs (443/TCP Xray, 8443/UDP Hysteria, 443/UDP TUIC, 443/UDP Hysteria2, 51820/UDP WireGuard, 53/UDP SlowDNS/DNSTT, 22/TCP SSH). Si un firewall applicatif (`ufw`, groupe de sécurité cloud, etc.) bloque ces ports par défaut, vous devez les ouvrir manuellement pour chaque moteur installé.

⚠️ **TUIC et Hysteria2 partagent le même port par défaut (443/UDP)** — les deux ne peuvent pas être actifs simultanément sans reconfigurer le port de l'un des deux (profil serveur, menu central). Ce n'est pas un bug : c'est la convention officielle des deux protocoles, documentée dans `ETUDE_PROTOCOLes_COMPATIBLES.md`.

⚠️ **WireGuard nécessite le paquet système `wireguard-tools`** (`wg`/`wg-quick`) et le module noyau WireGuard (mainline depuis Linux 5.6). **L'installateur automatique ne l'installe pas aujourd'hui** — si vous sélectionnez le moteur WireGuard, installez-le manuellement avant de lancer `install`/`start` (ex. `apt install wireguard-tools` sur Debian/Ubuntu), sinon le moteur échoue avec une erreur explicite ("commande 'wg' introuvable").

## Utilisation à distance (PC ou Android)

Administration via SSH standard vers le VPS — depuis un PC (terminal, OpenSSH) :

```bash
ssh root@<votre-vps>
menu          # ou : labosurf
```

Depuis Android, le résultat est identique avec **Termius** (ou tout autre client SSH) : créez un hôte avec l'IP du VPS et l'utilisateur `root` (ou l'utilisateur configuré), connectez-vous, puis lancez `menu` — c'est le workflow recommandé pour la majorité des utilisateurs (voir [Android](#android)).

## Gestion des moteurs

Chaque moteur installé (`labosurf-xray`, `labosurf-hysteria`, `labosurf-tuic`, `labosurf-hysteria2`, `labosurf-wireguard`, `labosurf-slowdns`, `labosurf-dnstt`, `labosurf-ssh`, `labosurf-udp`) tourne comme **service systemd indépendant** :

```bash
# Via systemd (méthode recommandée après installation)
systemctl start   labosurf-<moteur>
systemctl stop    labosurf-<moteur>
systemctl restart labosurf-<moteur>
systemctl status  labosurf-<moteur>
journalctl -u labosurf-<moteur> -f

# Directement via le binaire du moteur (fonctionne aussi hors systemd)
labosurf-<moteur> status|health|logs|update|uninstall
```

Lister les moteurs installés sur la machine :

```bash
ls /usr/local/bin/labosurf-*
systemctl list-units 'labosurf-*'
```

### Limitation connue : gestionnaire multi-moteurs

Le code contient un CLI unifié (`labosurf engine list|status|start|stop|restart|hybrid create/remove`), implémenté dans `cmd/labosurf` et publié en release sous le nom `labosurf-mgr-<arch>`. **Ce binaire n'est pas téléchargé par `labosurf-pro.sh`** : le binaire réellement installé sous `/usr/local/bin/labosurf` provient d'`engines/udp` et n'a que les sous-commandes `udp`, `admin`, `license`, `portal` et un menu interactif. Conséquence : la commande `labosurf engine ...` et les moteurs hybrides ne sont pas utilisables sur une installation VPS standard aujourd'hui — utilisez `systemctl`/`labosurf-<moteur>` comme décrit ci-dessus. Corriger cet écart nécessite une décision sur l'architecture de release (quel binaire nommer `labosurf`) et n'a pas été fait dans cette mission de documentation.

## Configuration

- Configuration centrale : `/etc/labosurf/config.json` (port d'écoute, portail HTTP, TUN, mode d'authentification)
- Configuration par moteur : `/etc/labosurf/engines/<moteur>.conf` (URL + SHA-256 du binaire tierce, généré vide par l'installateur — à compléter par l'opérateur pour les moteurs qui en dépendent)
- Comptes/quotas : `/etc/labosurf/users_db.json`

## Licence et activation

Le modèle de licence protège **l'accès au script d'installation**, pas l'exécution du serveur :

- **Algorithme** : Ed25519 (clé publique embarquée dans les binaires + distribuée via `license_pub.key` dans chaque release)
- **Fenêtre de validité du jeton** : 3 heures après émission
- **Usage** : 1 clé = 1 installation. Un reçu (`/etc/labosurf/.install_<id>.receipt`) empêche la réutilisation de la même clé
- **Après installation** : le serveur démarre et tourne librement, **sans vérification de licence au runtime**
- **Générateur de licences** : outil séparé et privé, réservé à l'administrateur — ne jamais le déployer sur un VPS client

## Comptes et abonnements

- **Comptes** : identifiant, mot de passe, expiration, quota (Go/illimité), max connexions, max IPs
- **Abonnements** : offre (durée, quota, limites), renouvellement possible
- **Grants** : par moteur (UDP, Xray, Hysteria, TUIC, Hysteria2, WireGuard, SlowDNS, DNSTT, SSH) — activation/désactivation granulaire
- **Quotas** : persistés sur disque (`users_db.json`), survivent aux redémarrages
- **Secrets** : UUID (Xray), mots de passe (Hysteria), UUID + mot de passe (TUIC), paires Ed25519 (SlowDNS/DNSTT/SSH), paire Curve25519 + adresse VPN unique (WireGuard — pool `10.66.0.0/24`) — gérés automatiquement

## Mise à jour

Il n'existe pas de mécanisme de mise à jour automatique (le menu interactif option `[6] MISE À JOUR` est un simple rappel, pas un updater). Pour mettre à jour :

```bash
# Ré-exécuter l'installateur (redemande une clé d'activation neuve)
curl -fsSL https://raw.githubusercontent.com/PHILIPPO237/LABOSURF_PRO/main/labosurf-pro.sh | sudo bash

# Ou remplacer manuellement un binaire précis puis redémarrer son service
systemctl stop labosurf-<moteur>
# télécharger le nouvel asset depuis releases/latest/download, vérifier SHA256SUMS
install -m 0755 labosurf-<moteur>-linux-<arch> /usr/local/bin/labosurf-<moteur>
systemctl start labosurf-<moteur>
```

## Désinstallation

Il n'y a pas de script de désinstallation global. Retrait manuel :

```bash
systemctl disable --now labosurf.service
systemctl disable --now labosurf-<moteur>.service   # pour chaque moteur installé
rm -f /etc/systemd/system/labosurf.service /etc/systemd/system/labosurf-*.service
systemctl daemon-reload

rm -f /usr/local/bin/labosurf /usr/local/bin/labosurf-* /usr/local/bin/menu
rm -rf /etc/labosurf /opt/labosurf

userdel -r labosurf 2>/dev/null || true   # utilisateur système du moteur SSH
```

## Diagnostic

```bash
systemctl status labosurf
systemctl status labosurf-<moteur>
journalctl -u labosurf -n 100 --no-pager
journalctl -u labosurf-<moteur> -n 100 --no-pager

labosurf license status -receipt-dir /etc/labosurf
```

Problèmes fréquents :
- **404 sur un asset pendant l'installation** : la release GitHub `latest` n'a pas (ou plus) tous les assets attendus — vérifier <https://github.com/PHILIPPO237/LABOSURF_PRO/releases/latest>
- **Clé d'activation refusée** : jeton expiré (fenêtre de 3h dépassée) ou déjà utilisé sur cette machine (voir le reçu dans `/etc/labosurf/`)
- **Un moteur démarre mais n'est pas joignable de l'extérieur** : vérifier le firewall (`ufw`/groupe de sécurité cloud) — seuls 5667/UDP et 8080/TCP sont ouverts automatiquement, voir [Firewall](#firewall-ce-qui-est-automatique-et-ce-qui-ne-lest-pas)

## Développement

```bash
git clone https://github.com/PHILIPPO237/LABOSURF_PRO.git
cd LABOSURF_PRO
```

- Go 1.22+ requis (`go.mod` : `go 1.22`)
- Module racine (`labosurf`) + sous-module séparé `engines/udp` (son propre `go.mod`)

Environnements de développement possibles : Linux natif, WSL (cas A ci-dessus), macOS — la compilation Go fonctionne à l'identique partout ; seul l'endroit où vous clonez le dépôt change (`$HOME/LABOSURF_PRO` ou tout autre chemin de votre choix).

### Build local

```bash
go build ./...
go vet ./...
go test ./internal/...
cd engines/udp && go vet ./... && go test ./...
```

> `./test_release_local.sh` et `./tools/deploy.sh` existent comme scripts de confort pour l'auteur du dépôt, mais lancent `go test ./...` **sans** le filtre `-run` que la CI applique à `engines/udp` (voir `.github/workflows/release.yml`) — ils ne sont donc pas une simulation exacte du pipeline CI et peuvent échouer sur des tests que la CI ignore volontairement (tests réseau marqués flaky).

### Publication (mainteneur du dépôt)

```bash
./tools/deploy.sh "message de commit"
```
Vérifie le dépôt (`git diff --check`), lance les tests disponibles, commit et push. GitHub Actions prend ensuite le relais sur un push de tag.

## Release

Le workflow `.github/workflows/release.yml` se déclenche sur un tag `vX.Y.Z` (ou manuellement via `workflow_dispatch`) :

1. `go vet ./...` + `go test ./internal/...` + `go build ./...`, puis `go vet`/`go test` filtré dans `engines/udp`
2. Build du binaire UDP Engine (`linux/amd64`, `linux/arm64`, `android/arm64`) avec la clé publique embarquée — c'est ce binaire qui est publié sous le nom `labosurf-<arch>` et installé par `labosurf-pro.sh`/`labosurf-android.sh`
3. Build du gestionnaire multi-moteurs (`labosurf-mgr-<arch>`, cf. [limitation connue](#limitation-connue-gestionnaire-multi-moteurs))
4. Build des 9 moteurs natifs × 3 architectures
5. `SHA256SUMS` unique pour tous les artefacts + `license_pub.key` + `license_pub.key.example`
6. `gh release create` avec les assets et notes automatiques

```bash
git tag v1.2.4          # exemple — utiliser le prochain numéro réel
git push origin v1.2.4
```

## Architecture du dépôt

```
LABOSURF_PRO/
├── cmd/
│   ├── labosurf/              # Gestionnaire multi-moteurs (labosurf-mgr en release — voir limitation connue)
│   └── labosurf-<moteur>/     # Binaires autonomes par moteur (installés par labosurf-pro.sh)
├── internal/
│   ├── engine/                # Interface Engine + Registre + Manager
│   ├── engineutil/             # CompositeEngine, hybrides, compatibilité
│   ├── engineudp/              # Superviseur UDP Engine
│   ├── enginecli/              # CLI partagée (install/start/stop/...)
│   ├── store/                  # Store central (comptes, offres, grants, quota)
│   ├── secret/                 # Génération UUID/Ed25519/tokens
│   ├── srvcfg/                 # Profil serveur (IP/ports)
│   ├── clientcfg/               # Génération config serveur + lien client
│   └── license/                # Vérification licence plateforme
├── engines/
│   ├── udp/                    # Moteur UDP natif (module Go séparé) — binaire installé comme "labosurf"
│   ├── xray/                   # VLESS/Trojan natif (binaire officiel Xray-core)
│   ├── hysteria/                # Réimplémentation Go maison (PAS le protocole Hysteria2 officiel)
│   ├── tuic/                    # TUIC v5 (binaire officiel tuic-server)
│   ├── hysteria2/                # Hysteria2 OFFICIEL (binaire officiel apernet/hysteria)
│   ├── wireguard/                # WireGuard (outils système wg/wg-quick, aucun binaire téléchargé)
│   ├── slowdns/                 # DNS Tunnel natif
│   ├── dnstt/                    # DNSTT natif
│   └── ssh/                      # SSH natif (golang.org/x/crypto/ssh)
├── .github/workflows/release.yml
├── labosurf-pro.sh             # Installateur VPS
├── test_release_local.sh        # Script de confort (≠ simulation exacte de la CI, voir Développement)
└── tools/deploy.sh              # Script de publication du mainteneur
```

## Sécurité

- **Aucune clé privée** dans le dépôt public (`.gitignore` protège `*.key`)
- **Clé publique actuelle** (`release/license_pub.key`, identique à `engines/udp/labosurf_pub.key`) : `7b27e59816d60f38a7299e226c714a3cb31a011f91f424099368506ded209595`
- **Shell SSH non-root** : utilisateur système `labosurf` via systemd `User=`
- **Licence vérifiée une seule fois** au moment de l'installation (voir [Licence et activation](#licence-et-activation)) — pas de contrôle au démarrage du serveur
- **Quotas persistés** : `UsedBytes` écrit dans `users_db.json` (écriture atomique tmp+rename)
- **Pas de secrets dans les logs** : mots de passe/tokens masqués
- **Ports ouverts automatiquement par l'installateur** : uniquement 5667/UDP et 8080/TCP (voir [Firewall](#firewall-ce-qui-est-automatique-et-ce-qui-ne-lest-pas))

## Roadmap et limitations connues

**Disponible aujourd'hui** (code présent, testé, intégré au registre de moteurs) : UDP, Xray, Hysteria (maison), TUIC officiel, Hysteria2 officiel, WireGuard, SlowDNS, DNSTT, SSH, ainsi que les 4 hybrides transport→backend listés ci-dessus (`dnstt-ssh`, `slowdns-ssh`, `dnstt-xray`, `slowdns-xray`).

**Étudié mais PAS intégré** (aucun code moteur, uniquement une étude d'architecture dans `ETUDE_PROTOCOLes_COMPATIBLES.md`) : OpenVPN, Shadowsocks (comme moteur indépendant), NaiveProxy, MASQUE, reverse proxy en tant que couche, variantes Xray (XHTTP, Trojan/VMess via Xray, REALITY hors TCP). **Ne pas considérer ces éléments comme disponibles** tant qu'aucun paquet `engines/<nom>` correspondant n'existe dans ce dépôt.

**Limitations connues** :
- Gestionnaire multi-moteurs (`labosurf-mgr`) non installé par l'installateur standard (voir [limitation connue](#limitation-connue-gestionnaire-multi-moteurs)) — les hybrides ne sont donc pas composables depuis une installation VPS standard aujourd'hui.
- WireGuard nécessite `wireguard-tools` installé manuellement (voir [Firewall](#firewall-ce-qui-est-automatique-et-ce-qui-ne-lest-pas)) et n'est pas disponible sur Android.
- TUIC et Hysteria2 partagent le port UDP/443 par défaut — un seul des deux peut tourner sans reconfiguration de port.
- La génération automatique des secrets par compte (`EnsureEngineSecrets`) ne couvre pas encore tous les noms d'hybrides possibles (ex. futurs hybrides autres que les 4 listés ci-dessus) — voir `ARCHITECTURE_HYBRIDES.md` pour le détail.

Voir `ETUDE_PROTOCOLes_COMPATIBLES.md` pour l'analyse complète (matrice de compatibilité, [CONFIRMÉ]/[COMPATIBLE THÉORIQUEMENT]/[À TESTER]/[INCOMPATIBLE]/[NÉCESSITE NOUVELLE ARCHITECTURE]) et `ARCHITECTURE_HYBRIDES.md` pour l'architecture de chaînage détaillée.

## Support

- **Architectures publiées** : `linux/amd64`, `linux/arm64`, `android/arm64`
- **OS officiellement testé** : Ubuntu 24.04 (CI) — voir la [matrice de compatibilité](#matrice-de-compatibilité) pour le reste
- **Shell** : bash (menu interactif, testé avec `zsh` non garanti)
- **Contact** : Telegram `t.me/Philippo237`

## Identité

- Produit : **LABOSURF PRO**
- Structure : **Laboratoire du FreeSurf**
- Concepteur : **PHILIPPO237**
- Telegram : `t.me/Philippo237`
- GitHub : `github.com/PHILIPPO237`
