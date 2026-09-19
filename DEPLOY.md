# Publication LABOSURF PRO

Ce document s'adresse au **mainteneur** du dépôt (accès en écriture + authentification GitHub). Remplacez les chemins ci-dessous par l'emplacement réel de votre clone (`$HOME/LABOSURF_PRO`, `~/LABOSURF_PRO`, ou tout autre dossier).

## Déploiement local

### Linux / Android (Termux) / WSL
```bash
cd LABOSURF_PRO   # ou : cd $HOME/LABOSURF_PRO
./tools/deploy.sh "LABOSURF PRO: publication"
```
Prérequis : Git, Go, authentification GitHub (SSH ou token). Sous Termux, installez d'abord `pkg install git golang`.

### Windows natif (PowerShell)
```powershell
cd LABOSURF_PRO
./tools/deploy.ps1 "LABOSURF PRO: publication"
```
Depuis WSL, `./tools/deploy.sh` fonctionne directement (voir ci-dessus).

## Release (GitHub Actions)

### Créer une release
```bash
git tag vX.Y.Z          # ex. v1.2.4 — utiliser le prochain numéro réel, jamais réutiliser un tag existant
git push origin vX.Y.Z
```

### Ce que fait le workflow (`.github/workflows/release.yml`)
1. **Tests** : `go vet ./...` + `go test ./internal/...` + `go build ./...`, puis dans `engines/udp` : `go vet ./...` + `go test` avec un filtre `-run` (tests réseau flaky exclus volontairement — voir le fichier pour la liste exacte)
2. **Build UDP Engine** : 3 binaires (linux/amd64, linux/arm64, android/arm64) avec clé publique embarquée — c'est ce binaire qui est publié sous le nom `labosurf-<arch>` et installé sur le VPS/Android
3. **Build Gestionnaire** : 3 binaires (amd64/arm64/android) `labosurf-mgr-<arch>` — publiés mais **non installés** par `labosurf-pro.sh` (voir [limitation connue](README.md#limitation-connue-gestionnaire-multi-moteurs))
4. **Build Moteurs natifs** : 9 moteurs × 3 archs = 27 binaires
   - `labosurf-udp`, `labosurf-xray`, `labosurf-hysteria`, `labosurf-tuic`, `labosurf-hysteria2`, `labosurf-wireguard`, `labosurf-slowdns`, `labosurf-dnstt`, `labosurf-ssh`
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
| `labosurf-<moteur>-<arch>` | 27 binaires moteurs natifs |
| `license_pub.key` | Clé publique Ed25519 (vérification) |
| `license_pub.key.example` | Exemple de clé publique |
| `SHA256SUMS` | Checksums de tous les artefacts |

### Vérification locale avant release
```bash
./test_release_local.sh
```
Reproduit approximativement le workflow CI localement (tests, builds, checksums, validation ELF/clé) — mais lance `go test ./...` sans le filtre `-run` que la CI applique dans `engines/udp`, donc ce n'est pas une simulation exacte (voir [Développement](README.md#build-local) dans le README).

## Configuration préalable

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

L'installateur (`labosurf-pro.sh`) télécharge la clé publique depuis `release/license_pub.key` sur la branche `main` (et non depuis les assets d'une release) : le `git push` ci-dessus suffit à mettre à jour les nouvelles installations, sans republier de release. Avant chaque changement de paire de clés, vérifier que `internal/license/license.go` (`EmbeddedVerifyKeyHex`) contient la même clé.

⚠️ **JAMAIS** commiter `labosurf_admin.key` (clé privée) — reste dans le dépôt Maker privé uniquement.

## Installation VPS (post-release)

### OS supportés pour l'installation VPS

Voir la [matrice de compatibilité complète](README.md#matrice-de-compatibilité) dans le README — ne pas dupliquer ce tableau ici pour éviter qu'il diverge.

### Script automatique
```bash
curl -fsSL https://raw.githubusercontent.com/PHILIPPO237/LABOSURF_PRO/main/labosurf-pro.sh | sudo bash
```

### Étapes manuelles (si besoin)
1. Télécharger les binaires depuis `https://github.com/PHILIPPO237/LABOSURF_PRO/releases/tag/vX.Y.Z`
2. Vérifier `sha256sum -c SHA256SUMS`
3. Installer `labosurf` (binaire `engines/udp`, PAS `labosurf-mgr` — voir [limitation connue](README.md#limitation-connue-gestionnaire-multi-moteurs)) + les moteurs voulus dans `/usr/local/bin/`
4. Copier `license_pub.key` vers `/etc/labosurf/`
5. Configurer `/etc/labosurf/config.json` (ports, domaine, backend)
6. `labosurf license activate -token <TOKEN>` (pas de préfixe `engine` — ce sous-comande n'existe pas dans le binaire `labosurf` installé)
7. `systemctl enable --now labosurf-<moteur>` pour chaque moteur
8. Ouvrir manuellement les ports firewall des moteurs installés autres que UDP/portail (l'installateur automatique n'ouvre que 5667/UDP et 8080/TCP — voir [Firewall](README.md#firewall-ce-qui-est-automatique-et-ce-qui-ne-lest-pas))

## Ports par défaut
| Service | Port | Protocole | Moteur |
|---------|------|-----------|--------|
| UDP Engine | 5667 | UDP | udp |
| Xray (VLESS/Trojan) | 443 | TCP | xray |
| Hysteria (maison) | 8443 | UDP | hysteria |
| TUIC (officiel) | 443 | UDP | tuic |
| Hysteria2 (officiel) | 443 | UDP | hysteria2 — collision UDP/443 avec TUIC assumée, voir README |
| WireGuard | 51820 | UDP | wireguard — nécessite `wireguard-tools` système, voir README |
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