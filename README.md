# LABOSURF PRO

**Laboratoire du FreeSurf — PHILIPPO237**

LABOSURF PRO est une plateforme multi-moteurs pour services VPN et tunnels réseau, conçue pour être déployée sur VPS Linux et gérée via interface CLI interactive (Termius/SSH) depuis Android ou PC.

## Moteurs disponibles

| Moteur | Protocole | Port | Type | Description |
|--------|-----------|------|------|-------------|
| **UDP** | Labosurf UDP | 5667/UDP | VPN | Moteur VPN UDP natif (transport propriétaire, chiffrement HMAC) |
| **Xray** | VLESS / Trojan | 443/TCP | VPN | Proxy VLESS/Trojan natif (compatible clients V2ray/Xray) |
| **Hysteria** | Hysteria2 | 8443/UDP | VPN | Relais UDP haute performance (auth HMAC, obfuscation XOR) |
| **SlowDNS** | DNS Tunnel | 53/UDP | Transport | Tunnel DNS sur UDP (auth Ed25519, backend TCP) |
| **DNSTT** | DNS Tunnel | 53/UDP | Transport | Tunnel DNS quasi-indétectable (sessions, fragmentation) |
| **SSH** | SSH | 22/TCP | Accès | Serveur SSH natif (auth Ed25519, shell non-root) |

### Moteurs hybrides (VPN + Transport)
Combinez un VPN avec un transport pour tunneliser le trafic :
- `xray-slowdns` — VLESS via tunnel DNS SlowDNS
- `xray-dnstt` — VLESS via tunnel DNS DNSTT
- `hysteria-slowdns` — Hysteria via tunnel DNS SlowDNS
- `hysteria-dnstt` — Hysteria via tunnel DNS DNSTT

Les hybrides sont validés à la création : **un seul VPN + un seul transport**, avec vérification des capacités (provides/requires).

## Déploiement

### Installation VPS (Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/PHILIPPO237/LABOSURF_PRO/main/labosurf-pro.sh | sudo bash
```

L'installateur :
1. Détecte l'OS (Ubuntu 24.04+, Debian, etc.) et l'architecture
2. Télécharge les binaires (moteurs + gestionnaire) depuis la release GitHub
3. Vérifie les checksums SHA-256
4. Installe les services systemd pour chaque moteur sélectionné
5. Ouvre les ports firewall (ufw/iptables/nftables)
6. Demande la licence d'activation
7. Configure les services et les démarre

### Installation Android / Termux

```bash
curl -fsSL https://raw.githubusercontent.com/PHILIPPO237/LABOSURF_PRO/main/labosurf-android.sh | bash
```

Installe le gestionnaire `labosurf` et les binaires moteurs dans `$PREFIX/bin/`. La commande `menu` devient disponible.

### Commande principale

```bash
menu
```

Ouvre l'interface d'administration interactive (Termius sur Android/PC, SSH standard).

## Gestion des moteurs

```bash
# Lister les moteurs
labosurf engine list

# Statut d'un moteur
labosurf engine status <nom>

# Démarrer/arrêter/redémarrer
labosurf engine start <nom>
labosurf engine stop <nom>
labosurf engine restart <nom>

# Créer un hybride
labosurf engine hybrid create xray slowdns

# Supprimer un hybride
labosurf engine hybrid remove xray-slowdns
```

### Moteurs autonomes (binaires standalone)

Chaque moteur a son propre binaire autonome :
- `labosurf-udp` — serveur UDP Engine
- `labosurf-xray` — serveur Xray (VLESS/Trojan)
- `labosurf-hysteria` — serveur Hysteria
- `labosurf-slowdns` — serveur SlowDNS
- `labosurf-dnstt` — serveur DNSTT
- `labosurf-ssh` — serveur SSH

Usage : `labosurf-<moteur> install|configure|start|run|stop|restart|status|health|logs|update|uninstall`

## Licence et activation

- **Algorithme** : Ed25519 (clé publique embarquée dans les binaires)
- **Fenêtre d'activation** : 3 heures après émission
- **Activation** : liée au `machine.id` du VPS (une licence = une machine)
- **Gestion** : `labosurf engine license activate <token>|status|verify`

Le générateur de licences (`LABOSURF_LICENSE_MAKER`) est un projet séparé, réservé à l'administrateur. Il ne doit jamais être déployé sur les VPS clients.

## Comptes et abonnements

- **Comptes** : identifiant, mot de passe, expiration, quota (Go/illimité), max connexions, max IPs
- **Abonnements** : offre (durée, quota, limites), renouvellement possible
- **Grants** : par moteur (UDP, Xray, Hysteria, SlowDNS, DNSTT, SSH) — activation/désactivation granulaire
- **Quotas** : persistés sur disque, survivent aux redémarrages
- **Secrets** : UUID (Xray), mots de passe (Hysteria), paires Ed25519 (SlowDNS/DNSTT/SSH) — gérés automatiquement

## Développement et publication

### Environnements
- **Android** : Termux + git + go (`/storage/emulated/0/MT2/FREE-SURF/LABOSURF_PRO/`)
- **WSL/Kali** : `cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO`
- **PC Linux natif** : identique

### Script de déploiement
```bash
./tools/deploy.sh
```
Vérifie le dépôt, lance `go test ./...`, commit + push. GitHub Actions compile et publie.

### Build local de test
```bash
./test_release_local.sh
```
Simule le workflow CI complet (tests, builds multi-arch, checksums, validation ELF/clé embarquée).

## GitHub Actions

Le workflow `.github/workflows/release.yml` (tag `vX.Y.Z` ou `workflow_dispatch`) :
1. `go test ./...` + `go vet ./...`
2. Build serveur UDP (amd64/arm64/android) + clé publique embarquée
3. Build gestionnaire multi-moteurs (amd64/arm64/android)
4. Build 6 moteurs natifs × 3 architectures = 18 binaires
5. SHA256SUMS unique pour tous les artefacts
6. `gh release create` avec assets + notes auto

## Architecture

```
LABOSURF_PRO/
├── cmd/
│   ├── labosurf/              # Gestionnaire central (menu, engine CLI)
│   └── labosurf-<moteur>/     # Binaires autonomes par moteur
├── internal/
│   ├── engine/                # Interface Engine + Registre + Manager
│   ├── engineutil/            # CompositeEngine, hybrides, compatibilité
│   ├── engineudp/             # Superviseur UDP Engine
│   ├── enginecli/             # CLI partagée (install/start/stop/...)
│   ├── store/                 # Store central (comptes, offres, grants, quota)
│   ├── secret/                # Génération UUID/Ed25519/tokens
│   ├── srvcfg/                # Profil serveur (IP/ports)
│   ├── clientcfg/             # Génération config serveur + lien client
│   └── license/               # Vérification licence plateforme
├── engines/
│   ├── udp/                   # Moteur UDP natif (sous-module Go)
│   ├── xray/                  # VLESS/Trojan natif
│   ├── hysteria/              # Hysteria2 natif
│   ├── slowdns/               # DNS Tunnel natif
│   ├── dnstt/                 # DNSTT natif
│   └── ssh/                   # SSH natif (golang.org/x/crypto/ssh)
├── .github/workflows/release.yml
├── labosurf-pro.sh            # Installateur VPS
├── labosurf-android.sh        # Installateur Android
├── test_release_local.sh      # Simulation CI locale
└── tools/deploy.sh            # Script de publication
```

## Sécurité

- **Aucune clé privée** dans le dépôt public (`.gitignore` protège `*.key`)
- **Clé publique unique** : `b2a42adec83b1a15191c6a8686fac83894846d6a9c02d75e7a4d3d094cc97e4c`
- **Shell SSH non-root** : utilisateur `labosurf` via systemd `User=`
- **Licence vérifiée** au démarrage de tout moteur (plateforme + UDP)
- **Quotas persistés** : `UsedBytes` écrit dans `users_db.json` (atomique tmp+rename)
- **Pas de secrets dans les logs** : mots de passe/tokens masqués
- **Ports par défaut** : 5667/UDP (UDP), 443 (Xray), 8443 (Hysteria), 53 (SlowDNS/DNSTT), 22 (SSH)

## Tests et CI

```bash
# Tests complets
go test ./...
go vet ./...

# Simulation CI locale
./test_release_local.sh

# Tests UDP Engine (nécessite root + TUN)
cd engines/udp && LABOSURF_REAL_TEST=1 go test ./...
```

## Support

- **Architectures** : linux/amd64, linux/arm64, android/arm64
- **OS cibles** : Ubuntu 24.04+, Debian 12+, Android 10+ (Termux)
- **Shell** : bash/zsh (menu interactif)

## Identité

- Produit : **LABOSURF PRO**
- Structure : **Laboratoire du FreeSurf**
- Concepteur : **PHILIPPO237**
- Telegram : `t.me/Philippo237`
- GitHub : `github.com/PHILIPPO237`