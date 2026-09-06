# Publication LABOSURF PRO

## Déploiement local

### Android / Termux
```bash
cd /storage/emulated/0/MT2/FREE-SURF/LABOSURF_PRO/
./tools/deploy.sh "LABOSURF PRO: publication"
```
Prérequis : Termux, Git, Go, authentification GitHub (SSH ou token).

### PC / Kali WSL
```bash
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO
./tools/deploy.sh "LABOSURF PRO: publication"
```
Depuis PowerShell : `tools/deploy.ps1` (lance via WSL).

### Windows natif
```powershell
cd C:\Users\atsan\OneDrive\Bureau\LABOSURF_PRO
./tools/deploy.ps1 "LABOSURF PRO: publication"
```

## Release (GitHub Actions)

### Créer une release
```bash
git tag v1.2.0
git push origin v1.2.0
```

### Ce que fait le workflow (`.github/workflows/release.yml`)
1. **Tests** : `go vet ./...` + `go test ./internal/...` + `go test ./engines/udp/...`
2. **Build UDP Engine** : 3 binaires (linux/amd64, linux/arm64, android/arm64) avec clé publique embarquée
3. **Build Gestionnaire** : 3 binaires (amd64/arm64/android) pour `labosurf` multi-moteurs
4. **Build Moteurs natifs** : 6 moteurs × 3 archs = 18 binaires
   - `labosurf-udp`, `labosurf-xray`, `labosurf-hysteria`, `labosurf-slowdns`, `labosurf-dnstt`, `labosurf-ssh`
5. **Checksums** : `SHA256SUMS` unique pour tous les artefacts + `license_pub.key` + exemple
6. **Release GitHub** : `gh release create` avec assets + notes auto

### Artefacts publiés par release
| Fichier | Description |
|---------|-------------|
| `labosurf-linux-amd64` | Serveur UDP Engine (amd64) |
| `labosurf-linux-arm64` | Serveur UDP Engine (arm64) |
| `labosurf-android-arm64` | Serveur UDP Engine (Android) |
| `labosurf-mgr-linux-amd64` | Gestionnaire multi-moteurs (amd64) |
| `labosurf-mgr-linux-arm64` | Gestionnaire multi-moteurs (arm64) |
| `labosurf-mgr-android-arm64` | Gestionnaire multi-moteurs (Android) |
| `labosurf-<moteur>-<arch>` | 18 binaires moteurs natifs |
| `license_pub.key` | Clé publique Ed25519 (vérification) |
| `license_pub.key.example` | Exemple de clé publique |
| `SHA256SUMS` | Checksums de tous les artefacts |

### Vérification locale avant release
```bash
./test_release_local.sh
```
Simule le workflow CI complet localement (tests, builds, checksums, validation ELF/clé).

## Configuration préalable

### GitHub Repository Variables
| Variable | Description |
|----------|-------------|
| `LABOSURF_LICENSE_PUBKEY` | (Obsolète) Clé publique hex 64 chars — maintenant lue depuis `release/license_pub.key` |

### Fichiers commités requis
- `release/license_pub.key` — Clé publique de production (64 hex, committée via exception `.gitignore`)
- `engines/udp/labosurf_pub.key` — Fallback identique

### `.gitignore` exceptions
```gitignore
# Clés publiques de vérification (non secrètes, trackées)
!release/license_pub.key
!engines/udp/labosurf_pub.key
```

## Clés de signature

### Génération (LABOSURF_LICENSE_MAKER - dépôt privé)
```bash
cd /path/to/LABOSURF_LICENSE_MAKER
go build -o license-maker .
./license-maker
# Génère labosurf_admin.key (privé, 0600) + labosurf_pub.key (public)
```

### Déploiement clé publique
```bash
cp labosurf_pub.key /path/to/LABOSURF_PRO/release/license_pub.key
cp labosurf_pub.key /path/to/LABOSURF_PRO/engines/udp/labosurf_pub.key
git add release/license_pub.key engines/udp/labosurf_pub.key
git commit -m "chore: update license public key"
git push
```

⚠️ **JAMAIS** commiter `labosurf_admin.key` (clé privée) — reste dans le dépôt Maker privé uniquement.

## Installation VPS (post-release)

### OS supportés pour l'installation VPS

| Distribution | Statut | Gestionnaire de paquets | Notes |
|--------------|--------|------------------------|-------|
| **Ubuntu** 20.04, 22.04, 24.04+ | ✅ Officiel | apt | Testé en CI (24.04) |
| **Debian** 11 (Bullseye), 12 (Bookworm) | ✅ Officiel | apt | |
| **Linux Mint** 20, 21+ | ✅ Officiel | apt | Basé sur Ubuntu LTS |
| **Raspberry Pi OS** (Raspbian) | ✅ Officiel | apt | ARM64/ARMHF |
| **Debian-based dérivées** (Pop!_OS, Elementary, etc.) | ✅ Compatible | apt | Hérite de Debian/Ubuntu |

| Distribution | Statut | Note |
|--------------|--------|------|
| CentOS / RHEL / Rocky / AlmaLinux | ⚠️ Expérimental | Utilise `dnf`/`yum` — non testé, installation `apt` échouera |
| Fedora | ⚠️ Expérimental | Utilise `dnf` — non testé |
| Alpine Linux | ❌ Non supporté | Utilise `apk` + `musl libc` |
| Arch Linux / Manjaro | ❌ Non supporté | Utilise `pacman` |

> **Note** : Le script d'installation utilise `apt` et `systemd`. Pour les distributions non-Debian, l'installation **échouera**.

### Script automatique
```bash
curl -fsSL https://raw.githubusercontent.com/PHILIPPO237/LABOSURF_PRO/main/labosurf-pro.sh | sudo bash
```

### Étapes manuelles (si besoin)
1. Télécharger binaires depuis `https://github.com/PHILIPPO237/LABOSURF_PRO/releases/tag/vX.Y.Z`
2. Vérifier `sha256sum -c SHA256SUMS`
3. Installer `labosurf` + `labosurf-mgr` + moteurs dans `/usr/local/bin/`
4. Copier `license_pub.key` vers `/etc/labosurf/`
5. Configurer `/etc/labosurf/config.json` (ports, domaine, backend)
6. `labosurf engine license activate <TOKEN>`
7. `systemctl enable --now labosurf-<moteur>` pour chaque moteur
8. Configurer firewall (ports 5667/UDP, 443, 8443, 53/UDP, 22/TCP)

## Ports par défaut
| Service | Port | Protocole | Moteur |
|---------|------|-----------|--------|
| UDP Engine | 5667 | UDP | udp |
| Xray (VLESS/Trojan) | 443 | TCP | xray |
| Hysteria | 8443 | UDP | hysteria |
| SlowDNS | 53 | UDP | slowdns |
| DNSTT | 53 | UDP | dnstt |
| SSH | 22 | TCP | ssh |
| Portail HTTP | 8080 | TCP | udp |

## Checklist pré-release
- [ ] `go test ./...` passe
- [ ] `go vet ./...` propre
- [ ] `./test_release_local.sh` réussit
- [ ] Clé publique mise à jour dans `release/` et `engines/udp/`
- [ ] Tag sémantique `vX.Y.Z` créé
- [ ] Push tag déclenche GitHub Actions
- [ ] Release GitHub créée avec tous les assets
- [ ] `sha256sum -c SHA256SUMS` valide sur les assets téléchargés
- [ ] Test installation VPS propre (Ubuntu 24.04)

## Rollback
Si la release est défectueuse :
```bash
git tag -d vX.Y.Z
git push origin :refs/tags/vX.Y.Z
gh release delete vX.Y.Z --yes
```

Puis corriger, nouveau tag, nouveau push.