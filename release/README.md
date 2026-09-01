# LABOSURF PRO — Release keys

`license_pub.key` doit contenir **uniquement la clé publique Ed25519** utilisée par la release cliente.

La clé privée correspondante est réservée au créateur/administrateur et ne doit jamais être commitée,
placée dans le ZIP de distribution ou installée sur un VPS client.

L'installateur `labosurf-pro.sh` récupère la clé publique depuis ce chemin de release.
