#!/usr/bin/env bash
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO
git add .
git commit -m 'feat: nouveau modèle licence = accès script installation (1 clé = 1 install, 3h)

- Supprime 3 gates runtime (server.go, main.go udp, cmd/labosurf)
- Nouveau: receipt.go (reçu installation) + registry.go (registre local)
- Nouvelles fonctions: VerifyToken/Activate/Status (package license)
- Nouvelles fonctions: UseLicense/ListReceipts/ClearReceipts (receipt.go)
- Registre local: Add/MarkUsed/Revoke/IsRevoked/IsUsed (registry.go)
- CLI udp: activate=reçu, status=reçus, verify+fenêtre, deactivate=supprime reçus
- Installateur: verify + reçu local, 1 clé = 1 install, pas de machine.id/activation.json
- Tests: install_test.go (nouveau modèle), license_test.go, integration_test.go mis à jour
- Supprime startup_test.go, activation.go, devMode, checkLicense
- Config: receipt_dir au lieu activation/machine_id/registry'
git push 2>&1
git log --oneline -2