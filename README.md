# LABOSURF PRO

**Laboratoire du FreeSurf — PHILIPPO237**

LABOSURF PRO est le produit principal. Le **UDP Engine** est le premier moteur réseau ; l'architecture est volontairement modulaire afin d'accueillir d'autres moteurs plus tard.

## Déploiement

Le dépôt public contient le code et l'installateur. Les binaires de production sont publiés comme assets d'une **GitHub Release**. L'installateur télécharge automatiquement l'asset correspondant à l'architecture Linux du VPS.

### Installation depuis GitHub

Après publication d'une release contenant `labosurf-linux-amd64`, `labosurf-linux-arm64` et `license_pub.key` :

```bash
curl -fsSL https://raw.githubusercontent.com/PHILIPPO237/LABOSURF_PRO/main/labosurf-pro.sh | sudo bash
```

L'installateur demande ensuite la licence d'activation via le terminal et configure le service systemd.

### Commande principale

```bash
menu
```

`menu` ouvre l'interface d'administration interactive, conçue pour Termius sur Android ou PC et pour les autres clients SSH.

## UDP Engine

- protocole interne UDP Engine ;
- port par défaut : **5667/UDP** ;
- authentification par challenge/réponse HMAC ;
- comptes clients avec expiration, quota, connexions simultanées et nombre d'IP simultanées ;
- `MaxIPs = 0` signifie **ILLIMITÉ** ;
- blocage/déblocage d'un compte sans suppression ;
- rechargement du store avant authentification afin que les changements du menu soient pris en compte par le service sans redémarrage.

> Important : écouter sur `5667/UDP` ne garantit pas à lui seul la compatibilité avec une application tierce telle que **ZIVPN**. Le client doit parler le protocole de tunnel implémenté par LABOSURF PRO. Le test réel avec ZIVPN doit être effectué sur un VPS.

## Licence et Keygen

La signature utilise **Ed25519**. Le VPS client ne reçoit que la clé publique.

- le Keygen/signataire de production doit rester dans un dépôt privé séparé ;
- la clé privée de signature ne doit jamais être commitée dans le dépôt public ;
- la fenêtre d'activation initiale est de **3 heures** ;
- après activation réussie, l'installation est liée à son `machine.id` et reste utilisable tant que la licence ne porte pas de date d'expiration post-activation ;
- la vérification de licence reste locale côté VPS.

La fenêtre de 3 heures et la durée d'utilisation après activation sont deux notions différentes.

## Création des comptes

La fiche client affiche notamment :

- identifiant ;
- adresse IP/nom d'hôte du VPS ;
- mot de passe ;
- expiration ;
- quota (GB ou illimité) ;
- nombre d'IP autorisées simultanément ;
- nombre de connexions simultanées ;
- lien portail si activé.

Le port UDP interne (`5667`) n'est pas affiché dans la fiche client.

## Publication depuis Android ou PC

Le même projet local peut être publié depuis Android ou depuis Kali/WSL sur Windows.

### Android

Chemin prévu dans l'environnement de travail :

```text
/storage/emulated/0/MT2/FREE-SURF/LABOSURF_PRO/
```

### PC / Kali WSL

Le dossier Windows peut être vu depuis Kali avec :

```bash
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO
```

Le script commun de publication est :

```bash
./tools/deploy.sh
```

Il vérifie le dépôt, exécute les tests Go lorsqu'ils sont disponibles localement, puis commit et push les modifications vers GitHub. GitHub Actions réalise ensuite les tests et les builds de release.

## GitHub Actions

Le workflow `.github/workflows/release.yml` :

1. lance `go test ./...` ;
2. construit AMD64 et ARM64 ;
3. embarque la clé publique de vérification ;
4. produit les sommes SHA-256 ;
5. crée la GitHub Release pour un tag `vX.Y.Z`.

Avant la première release, renseigner la variable de dépôt `LABOSURF_LICENSE_PUBKEY` avec **la clé publique uniquement**.

## Sauvegarde / mise à jour

Les données runtime résident sous `/etc/labosurf`. Toute future procédure de mise à jour doit sauvegarder au minimum la configuration, le store utilisateurs et l'activation avant remplacement du binaire.

## Identité

- Produit : **LABOSURF PRO**
- Structure : **Laboratoire du FreeSurf**
- Concepteur : **PHILIPPO237**
- Telegram : `t.me/Philippo237`
- GitHub : `github.com/PHILIPPO237`
