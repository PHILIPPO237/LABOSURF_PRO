# Publication LABOSURF PRO

## Android

Depuis le dossier local du projet :

```bash
cd /storage/emulated/0/MT2/FREE-SURF/LABOSURF_PRO/
./tools/deploy.sh "LABOSURF PRO: publication"
```

Le terminal Android doit disposer de Git et d'un accès authentifié au dépôt GitHub.

## PC / Kali WSL

```bash
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO
./tools/deploy.sh "LABOSURF PRO: publication"
```

Depuis PowerShell, `tools/deploy.ps1` peut lancer le même flux via WSL.

## Release

Créer et pousser un tag :

```bash
git tag v1.1.0
git push origin v1.1.0
```

GitHub Actions exécute les tests, compile les binaires Linux AMD64/ARM64 et crée la release.

Avant la première release, configurer la variable de dépôt `LABOSURF_LICENSE_PUBKEY` avec la clé publique Ed25519 de production (64 caractères hex). La clé privée reste exclusivement dans l'outil Keygen privé du créateur.
