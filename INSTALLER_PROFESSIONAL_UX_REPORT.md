# LABOSURF PRO — Installateur professionnel : rapport

Date : 2026-09-09
Fichier concerné : `labosurf-pro.sh` (racine du dépôt LABOSURF_PRO)
Portée : présentation, ordonnancement des étapes, robustesse terminal et
honnêteté du contrôle final de l'installateur. **Aucun changement** au
format de licence, à la cryptographie, à la clé publique, à la
compatibilité LABOSURF_PRO ↔ LABOSURF_LICENSE_MAKER, ni aux moteurs
(UDP/SSH/Xray/Hysteria/SlowDNS/dnstt) — ce point est vérifié en détail à
la section 6 et 9.

---

## 1. État initial de l'installateur

Avant cette session, `labosurf-pro.sh` (878 lignes, déjà présent dans
l'arbre de travail non commité) était **déjà en grande partie
réécrit** dans une session de travail antérieure sur ce même dépôt :
bannière, écran de licence avec bordures Unicode/ASCII, spinner, étapes
numérotées [n/11], écran final, gestion d'erreur structurée `die()`,
fallback ASCII/couleur, et deux scripts de test dédiés
(`test_installer_ux.sh`, `test_install_license_gate.sh`) existaient déjà
mais n'avaient encore **jamais été exécutés** dans cette session — et
le rapport demandé par la mission (`INSTALLER_PROFESSIONAL_UX_REPORT.md`)
n'existait pas.

Cette session a donc consisté à :
1. Auditer le script existant contre la spécification complète de la
   mission (flux, écran de licence, animations, couleurs, erreurs, fin).
2. **Exécuter réellement** les tests Go et les deux scripts de test shell
   existants.
3. Corriger **deux bugs réels** découverts par cette exécution (détaillés
   §5 et §8) — pas des changements cosmétiques, des crashs / fuites
   d'échappement ANSI que les tests ont mis en évidence.
4. Vérifier que LABOSURF_LICENSE_MAKER est resté intact.
5. Rédiger ce rapport.

Aucun `git commit` n'a été fait (conformément à la consigne « NE fais pas
de commit automatique ») : tout reste dans l'arbre de travail, à côté du
travail non commité pré-existant issu de la mission d'audit de licence
précédente (fichiers `AUDIT_*.md`, changements dans `engines/*`,
`internal/*` — **non touchés** par cette session).

## 2. Architecture de l'installation

Flux implémenté dans `main()` (lignes ~762-868), strictement conforme au
flux demandé section 2 de la mission :

```
require_root → print_intro → print_plan
  [1/11] System Check              (check_os)
  [2/11] License Validation        (prepare_dirs, download validator+pubkey,
                                     activate_license — BLOQUANT)
  [3/11] Environment Preparation   (setup_network)
  [4/11] Dependencies              (install_deps)
  [5/11] Download Components       (select_engines, install_engine_binary)
  [6/11] Install Binaries          (install_engine_service, gen_engine_conf,
                                     install_ssh_user)
  [7/11] Configure LABOSURF PRO    (write_default_config, install_menu_command)
  [8/11] Install Services          (install_service_unit, enable_engine_service,
                                     install_engine_thirdparty)
  [9/11] Start Services            (start_core_service, systemctl restart <eng>)
  [10/11] Health Check             (final_check — vérifie service central
                                     ET chaque moteur sélectionné)
  [11/11] Finalization             (print_done)
```

Point clé : la validation de licence est l'étape **[2/11]**, avant toute
dépendance système, tout changement réseau (iptables/nft/sysctl) et tout
téléchargement de moteur. Si elle échoue, `die()` est appelé et le
script quitte avec un code non nul — rien au-delà des répertoires privés
`/opt/labosurf` et `/etc/labosurf` (créés pour recevoir la clé publique et
le binaire vérificateur) n'a été modifié sur le système.

## 3. Nouvelle interface

- Bannière ASCII-art "LABOSURF PRO" en dégradé Unicode (bloc plein) avec
  repli sur texte simple si le terminal ne supporte pas l'Unicode ou est
  trop étroit (`print_intro`, `UNICODE_OK` + largeur du terminal).
- Plan d'installation affiché avant le début (`print_plan`) : les 11
  étapes listées avec l'icône "en attente" (○ / `.`).
- Écran de licence dédié (`print_license_screen`) avec cadre à double
  bordure, icône clé, séparateurs, et bloc de contact Telegram — reproduit
  fidèlement l'exemple de la mission (section 3).
- Écran final (`print_done`) avec cadre à double bordure "INSTALLATION
  COMPLETED", liste des étapes réellement franchies (`COMPLETED_STEPS`,
  rempli uniquement après succès réel de chaque étape), et un récapitulatif
  des commandes utiles (aucun secret affiché).

## 4. Système de couleurs

Détection au lancement (lignes 49-63) :
- Couleurs actives seulement si stdout est un TTY **et** `tput colors`
  rapporte ≥ 8 couleurs ; sinon toutes les variables de couleur sont
  vides (`RESET/BOLD/GREEN/CYAN/...=''`).
- Palette : vert = succès, rouge = échec, jaune = avertissement, cyan =
  info/en cours, blanc = titres d'étape, gris (`DIM`) = texte secondaire.
- Aucune couleur codée en dur hors de ce bloc central — tout le reste du
  script utilise ces variables, donc la dégradation est cohérente partout.

## 5. Animations

- Spinner (`spinner()`, lignes 249-278) : anime une tâche en tâche de
  fond réelle (`run_step` lance la commande dans un sous-shell `&` et
  affiche le spinner tant que le PID est vivant) — **jamais** de fausse
  progression, jamais de `sleep` artificiel ajouté au temps réel
  d'installation. Le seul `sleep 0.08` sert à cadencer le *rendu* du
  spinner, pas à ralentir l'opération elle-même.
- Dégradation propre : si stdout n'est pas un TTY (`IS_TTY_OUT=0`), le
  spinner n'anime rien — il attend simplement la fin du PID et imprime
  ✓/✗ une seule fois (pas de `\r` qui pollue un journal/pipe).
- **Bug réel corrigé dans cette session** (voir §8) : `cleanup()`,
  `step_ok()` et `step_fail()` appelaient `tput cnorm` sans condition,
  y compris quand stdout n'est pas un terminal — ce qui injectait des
  séquences d'échappement (`\e[?12l\e[?25h`) dans une sortie censée être
  du texte pur (log redirigé, `| cat`, capture de script). Corrigé pour
  n'appeler `tput` que si `IS_TTY_OUT=1`, cohérent avec le reste du
  fichier.

## 6. Validation de licence

**Logique cryptographique strictement inchangée** — vérifiée ligne par
ligne dans `activate_license()` (labosurf-pro.sh:551-587) :
- Toujours un jeton unique demandé sur `/dev/tty` (jamais stdin, pour
  rester compatible avec `curl | bash`).
- Toujours vérifié via `labosurf license verify -token ... -print-id`
  (Ed25519, fenêtre de 3h) avec la clé publique téléchargée depuis la
  release GitHub — aucune clé privée n'est présente dans l'installateur.
- Toujours "1 clé = 1 installation" via un fichier reçu
  (`.install_<id>.receipt`) empêchant la réutilisation.
- **Aucun contournement** : ni variable d'environnement, ni flag caché.
  Le test `test_install_license_gate.sh` grep explicitement le fichier
  pour s'assurer qu'aucune référence active à un ancien bypass
  (`LABOSURF_DEV`) ne subsiste (seule une mention en commentaire
  historique demeure) — test exécuté et passé (§10).

Ce qui a changé n'est que la **présentation** : écran dédié avec cadre,
message d'aide Telegram, message de comptage `1 key = 1 installation •
valid for 3 hours`. Un bug réel de robustesse a été corrigé dans
`read_license_token()` (voir §8) — sans toucher à la logique de
vérification elle-même.

## 7. Message de contact Telegram

`TELEGRAM_CONTACT="https://t.me/Philippo237"` (labosurf-pro.sh:32) est
affiché uniquement dans `print_license_screen()`, comme information pour
obtenir une clé auprès de l'administrateur. Il n'intervient à aucun
moment dans `activate_license()` : aucun appel réseau vers Telegram,
aucune lecture d'un canal Telegram, aucune variable liée à Telegram dans
le chemin de décision. La seule voie d'autorisation reste l'appel à
`labosurf license verify`.

## 8. Gestion des erreurs

`die()` (labosurf-pro.sh:304-315) affiche un bloc structuré : titre en
rouge "Installation failed", l'étape courante `[n/11] Label`, et la
raison (toujours une chaîne humaine fixe, jamais un jeton/secret — vérifié
pour chaque site d'appel de `die()` dans le fichier). `run_step()` capture
stdout/stderr de la commande défaillante dans un fichier temporaire et en
affiche les 80 premières lignes comme "Diagnostic output" avant d'appeler
`die()`, sans jamais afficher de succès pour une opération qui a échoué.

**Deux bugs réels trouvés et corrigés dans cette session** (mis en
évidence en exécutant réellement `test_installer_ux.sh`, pas par lecture
de code) :

1. **`read_license_token()` plantait au lieu d'échouer proprement**
   quand aucun `/dev/tty` n'est disponible (environnement totalement non
   interactif) : `local t` sans valeur initiale + redirection
   `< /dev/tty` échouant produisait, sous `set -u`, une erreur
   `t: unbound variable` au lieu du message voulu "No activation key
   provided. Installation cancelled." — un crash n'est PAS un blocage
   propre, même s'il bloque bien l'installation dans les faits. Corrigé :
   `local t=""` + `read ... 2>/dev/null || true`, ce qui restaure le
   comportement documenté : jeton vide → `die "No activation key
   provided..."`, échec fermé et lisible.
2. **`cleanup()` / `step_ok()` / `step_fail()` injectaient des séquences
   ANSI même sans terminal** (`tput cnorm` inconditionnel) — voir §5.
   Corrigé en gardant ces appels derrière `[[ "$IS_TTY_OUT" -eq 1 ]]`,
   comme le fait déjà `step_spin()`/`spinner()` pour `tput civis`.

Ces deux corrections sont des corrections de **robustesse réelle**, pas
des changements cosmétiques : la première pouvait faire échouer
l'installation avec un message technique confus plutôt que le message
professionnel attendu ; la seconde pouvait corrompre un journal
d'installation redirigé vers un fichier (`labosurf-pro.sh > install.log`)
avec des octets d'échappement illisibles.

## 9. Compatibilité VPS/SSH

Aucun changement aux moteurs (UDP/SSH/Xray/Hysteria/SlowDNS/dnstt) ni à
`setup_network()` (détection d'interface WAN, iptables/nft, sysctl) —
ces fonctions sont identiques à avant cette session dans leur logique;
seule la présentation autour (titres d'étape, spinner) a changé.
`term_width()` retombe sur 80 colonnes si `tput cols` échoue (SSH sans
pty correctement négocié). Le script fonctionne :
- avec ou sans TTY (`IS_TTY_OUT`) ;
- avec ou sans couleurs (`tput colors < 8` ou absent) ;
- avec ou sans Unicode (détection de locale `UTF-8`) ;
- avec ou sans `/dev/tty` pour la saisie de licence (échec fermé, jamais
  ouvert — voir §6 et §8).

## 10. Tests réalisés

**Tous les tests suivants ont été réellement exécutés dans cette
session** (pas de simulation ni de supposition) :

| Commande | Résultat |
|---|---|
| `go build ./...` (racine) | ✅ OK, aucune sortie |
| `go vet ./...` (racine) | ✅ OK, aucune sortie |
| `go test ./...` (racine) | ✅ `ok` sur tous les packages testés |
| `go test -race ./...` (racine) | ✅ `ok` sur tous les packages testés |
| `go build ./...` (engines/udp, module séparé) | ✅ OK |
| `go vet ./...` (engines/udp) | ✅ OK |
| `go test -timeout 2m -run '<filtre licence/intégration>' .` (engines/udp, même filtre que la CI release.yml) | ✅ `ok` (3.7s) |
| `bash -n labosurf-pro.sh` | ✅ syntaxe valide |
| `bash test_installer_ux.sh` | ✅ 8 PASS / 0 FAIL (après corrections §8) — **avant** correction : 3 PASS / 5 FAIL |
| `bash test_install_license_gate.sh` | ✅ 13 PASS / 0 FAIL |
| Rendu manuel : bannière + écran de licence en `LC_ALL=C LANG=C TERM=dumb` | ✅ repli ASCII pur correct (`+`, `-`, `.`, `[KEY]`) |
| Rendu manuel : bannière + écran de licence dans un pty avec couleurs/Unicode (`script` + `LANG=en_US.UTF-8 TERM=xterm-256color`) | ✅ blocs Unicode et codes ANSI corrects |
| Rendu manuel : `run_step` sur une tâche rapide puis une tâche en échec | ✅ spinner anime, ✓ vert puis ✗ rouge + `die()` structuré |

`test_install_license_gate.sh` compile et exécute le **vrai** binaire
`engines/udp` (celui réellement téléchargé par l'installateur en
production, pas `cmd/labosurf`) et le **vrai** binaire
`LABOSURF_LICENSE_MAKER` (dépôt frère, jamais réécrit) pour générer un
jeton réel, puis rejoue exactement l'appel shell fait par
`activate_license()` avec :

- CAS 7 (clé valide, vrai jeton du vrai License Maker) → **installation
  continue**, reçu écrit ;
- CAS 1 (aucune clé) → **bloquée** ;
- CAS 2 (clé vide/espaces) → **bloquée** ;
- CAS 3 (clé malformée) → **bloquée** ;
- CAS 4/5 (signature altérée) → **bloquée** ;
- CAS 6 (jeton expiré, fenêtre de 3h dépassée) → **bloquée** ;
- Bonus (réutilisation de la même clé valide) → **bloquée** dès la 2ᵉ
  tentative (1 clé = 1 install) ;
- Vérification statique : aucune référence active à un bypass
  `LABOSURF_DEV` ou similaire.

Chacun de ces 8 scénarios est passé **réellement**, contre le vrai code
de vérification cryptographique, pas contre une simulation.

Tests **non exécutés** dans cette session (nécessitent un vrai VPS/root/
systemd/apt, hors de portée de cet environnement de développement) :
installation complète de bout en bout sur un VPS réel, démarrage réel des
services systemd, comportement réel sous une interruption SIGINT en plein
téléchargement. Ces points sont couverts par la structure du code
(`trap cleanup EXIT INT TERM`, `set -Eeuo pipefail`, vérifications SHA-256
avant tout `install`) mais pas par une exécution réelle sur VPS — à
vérifier lors d'un déploiement réel avant diffusion publique.

## 11. Résultats

- **Avant** les corrections de cette session : `test_installer_ux.sh`
  échouait sur 5 des 8 scénarios (fuite ANSI dans la sortie non-TTY,
  et un crash "unbound variable" au lieu d'un blocage propre sans
  `/dev/tty`).
- **Après** : 8/8 et 13/13 sur les deux scripts de test shell, tous les
  tests Go verts (build, vet, test, test -race, plus le filtre licence
  du module `engines/udp`).
- Le flux de la mission (bannière → vérifications → licence →
  installation → configuration → composants → services → démarrage →
  vérifications finales → OK) est intégralement implémenté et
  correspond aux 11 étapes numérotées demandées.

## 12. Fichiers modifiés

Dans cette session, uniquement :
- `labosurf-pro.sh` — 3 corrections ciblées (voir §8) :
  `cleanup()`, `step_ok()`, `step_fail()` (garde `IS_TTY_OUT` avant
  `tput cnorm`) et `read_license_token()` (initialisation de `t`,
  tolérance à l'absence de `/dev/tty`).
- `INSTALLER_PROFESSIONAL_UX_REPORT.md` — ce rapport (nouveau fichier).

**Non modifiés par cette session** (déjà présents, non commités, issus
d'un travail antérieur sur ce dépôt) :
- Le corps de `labosurf-pro.sh` réécrit pour la présentation
  professionnelle (bannière, écran de licence, spinner, 11 étapes,
  écran final) — déjà en place au début de cette session.
- `test_installer_ux.sh`, `test_install_license_gate.sh` — déjà présents,
  seulement **exécutés** dans cette session (jamais lancés avant).
- Tous les fichiers `AUDIT_*.md`, `engines/*`, `internal/*` liés à la
  mission d'audit de licence précédente — **non touchés**, considérés
  terminés comme demandé.
- `LABOSURF_LICENSE_MAKER` — **zéro changement** (`git status --short`
  vide dans ce dépôt frère, vérifié explicitement). Son menu (options 1
  à 9, couleurs ANSI, structure) est resté strictement identique.

## 13. Problèmes restants

- Aucun test réel sur un VPS root/systemd n'a pu être exécuté dans cet
  environnement de développement (pas de root réel, pas de systemd) —
  seuls les chemins de code ont été exercés via les scripts de test qui
  sourcent le script sans lancer `main()`. Une validation finale sur un
  vrai VPS Debian/Ubuntu avant diffusion publique reste recommandée.
  (déjà noté comme limitation connue dans `AUDIT_TECHNIQUE_COMPLET_2026-09-09.md`,
  non spécifique à cette mission).
- Le contenu des fichiers `AUDIT_*.md`/`RAPPORT_*.md` et les modifications
  non commitées dans `engines/*`/`internal/*` (mission de licence
  précédente) restent en attente d'un commit — hors périmètre de cette
  mission, signalé ici uniquement pour information, non traité.

## 14. État final

- LABOSURF_LICENSE_MAKER : **conservé** (menu, options, structure —
  zéro modification).
- Système de validation de licence : **conservé** (Ed25519, format de
  jeton, clé publique, fenêtre de 3h, 1 clé = 1 install — zéro
  changement cryptographique).
- Installation : **professionnalisée** (bannière, écran de licence dédié
  avec contact Telegram informatif, 11 étapes visibles, spinner honnête,
  fallback ASCII/sans-couleur/sans-TTY, écran final honnête) et
  **plus robuste** que l'état de départ de cette session (2 bugs de
  robustesse réels corrigés, tous deux découverts par exécution réelle
  des tests, pas par relecture).
- Verrouillage : **CLÉ VALIDE → installation continue**, **CLÉ INVALIDE
  / ABSENTE / EXPIRÉE / falsifiée / déjà utilisée → installation
  complètement bloquée** — vérifié par 13 scénarios réels contre le
  vrai binaire et le vrai License Maker.

---

INSTALLATEUR PROFESSIONNEL TERMINÉ

PROJET : LABOSURF_PRO
LICENSE MAKER : CONSERVÉ
VALIDATION : CONSERVÉE
INSTALLATION : PROFESSIONNALISÉE
RAPPORT : INSTALLER_PROFESSIONAL_UX_REPORT.md
