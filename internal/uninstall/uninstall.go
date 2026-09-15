// Package uninstall détecte l'installation LABOSURF PRO présente sur la
// machine (binaires, unités systemd, répertoire tiers, configuration et
// données) et exécute une désinstallation choisie par l'administrateur,
// après confirmation explicite — jamais automatique, jamais silencieuse.
//
// Toute la logique métier vit ici, indépendante de l'écran terminal
// (cmd/labosurf/menu_uninstall.go), pour rester testable avec des
// répertoires temporaires — aucun test de ce package ne touche à une vraie
// installation.
//
// # Modèle de sécurité des chemins
//
// Une variable d'environnement (LABOSURF_DATA_DIR, LABOSURF_BIN_DIR) est une
// entrée NON FIABLE : mal configurée (typo, valeur héritée d'un autre
// contexte), elle pourrait désigner un répertoire système partagé. Aucune
// suppression récursive n'est autorisée sans que les trois conditions
// suivantes soient TOUTES vraies :
//
//  1. CHEMIN SÛR      — absolu, nettoyé, ni vide ni relatif.
//  2. RACINE AUTORISÉE — ne correspond à aucun répertoire système protégé
//     (/, /etc, /var, /usr, /opt, /home, /root, /bin, /sbin, /lib, /lib64,
//     /boot, /dev, /proc, /sys, /run, /tmp) et reste sous l'un des
//     répertoires de Paths.
//  3. IDENTITÉ CONFIRMÉE — pour un RemoveAll de répertoire entier
//     (DataDir/ThirdPartyDir), le nom du répertoire ET son contenu doivent
//     concorder avec une installation LABOSURF PRO connue (voir
//     identityConfirmedForDataDir / identityConfirmedForBinDir). Un nom
//     seul ne suffit jamais : il est trivialement imitable.
//
// Si une seule de ces conditions échoue, le répertoire est ignoré (jamais
// utilisé comme racine de suppression) et un avertissement est ajouté à
// Inventory.Warnings — jamais une transformation silencieuse.
package uninstall

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"labosurf/internal/engineutil"
)

// Paths regroupe les répertoires réels d'une installation LABOSURF PRO.
// Injectable pour les tests (répertoires temporaires) — DefaultPaths()
// retourne les chemins réellement utilisés en production. Ces valeurs sont
// TOUJOURS revalidées (voir ValidatePathComponent) avant tout usage comme
// racine de suppression — jamais utilisées telles quelles.
type Paths struct {
	BinDir     string // /usr/local/bin — binaires superviseurs
	OptDir     string // /opt/labosurf (LABOSURF_BIN_DIR) — binaires tiers téléchargés
	DataDir    string // /etc/labosurf (LABOSURF_DATA_DIR) — config + données
	SystemdDir string // /etc/systemd/system — unités de service
}

// DefaultPaths retourne les chemins réels de l'installation, cohérents avec
// labosurf-pro.sh et internal/engineutil (mêmes constantes, même
// surcharge par variables d'environnement — une seule source de vérité).
// Ces valeurs peuvent provenir de variables d'environnement non fiables ;
// elles sont revalidées à chaque usage (Detect, RemovePath).
func DefaultPaths() Paths {
	return Paths{
		BinDir:     "/usr/local/bin",
		OptDir:     engineutil.DefaultBinaryDir,
		DataDir:    engineutil.DefaultDataDir,
		SystemdDir: "/etc/systemd/system",
	}
}

// KnownEngines liste les moteurs dont labosurf-pro.sh peut installer le
// binaire superviseur et l'unité systemd — utilisé pour la détection,
// jamais pour deviner un nom arbitraire.
var KnownEngines = []string{
	"xray", "hysteria", "hysteria2", "tuic", "wireguard",
	"slowdns", "dnstt", "ssh", "freewaygate", "udp",
}

// configFileNames sont les fichiers de configuration d'exécution — suivis
// séparément dans Inventory.ConfigFiles (supprimés en mode ModeProgramKeepData,
// conservés en ModeProgramOnly, inclus dans ModeFull via DataDir entier).
var configFileNames = []string{"config.json", "license_pub.key"}

// dataOnlyMarkers sont les fichiers/répertoires de DONNÉES réellement
// produits par LABOSURF PRO — voir internal/store (users_db.json),
// internal/service (services/, access/), internal/profile (profiles/),
// internal/srvcfg (server.json), et labosurf-pro.sh (engines/, créé
// inconditionnellement à l'installation). Suivis séparément dans
// Inventory.DataFiles (toujours conservés sauf en ModeFull) — cette liste
// exclut délibérément configFileNames pour ne jamais faire apparaître le
// même fichier à la fois comme "à supprimer" (ConfigFiles) et "à
// conserver" (DataFiles).
var dataOnlyMarkers = []string{
	"users_db.json", "services", "access", "profiles", "engines", "server.json",
}

// dataDirIdentityMarkers est l'union des deux listes ci-dessus : n'importe
// lequel de ces noms, présent à l'intérieur d'un répertoire dont le nom de
// base contient "labosurf", suffit à confirmer l'identité LABOSURF PRO
// (voir identityConfirmedForDataDir) — utilisé uniquement pour cette
// décision, jamais pour peupler Inventory directement.
var dataDirIdentityMarkers = append(append([]string{}, configFileNames...), dataOnlyMarkers...)

// ---------- Erreurs ----------

// ErrUnsafePath signale un chemin refusé comme cible de suppression, pour
// n'importe laquelle des raisons ci-dessous (chemin vide, relatif, système
// protégé, ou hors des répertoires connus de l'installation).
var ErrUnsafePath = errors.New("chemin refusé — suppression impossible")

// ErrRelativePath signale spécifiquement un chemin non absolu.
var ErrRelativePath = errors.New("chemin relatif refusé — un chemin absolu est requis")

// ErrDangerousRoot signale spécifiquement un répertoire système protégé.
var ErrDangerousRoot = errors.New("répertoire système protégé — refusé comme racine de suppression")

// ErrUnidentifiedInstallation signale qu'un répertoire existe mais ne peut
// pas être identifié avec certitude comme appartenant à LABOSURF PRO.
var ErrUnidentifiedInstallation = errors.New("identité LABOSURF PRO non confirmée pour ce répertoire")

// protectedSystemPaths — jamais une racine de suppression, quelle que soit
// la configuration. Comparaison sur le chemin nettoyé (filepath.Clean),
// donc "/etc/" et "/etc" sont tous deux couverts.
var protectedSystemPaths = map[string]bool{
	"/":       true,
	"/etc":    true,
	"/var":    true,
	"/usr":    true,
	"/opt":    true,
	"/home":   true,
	"/root":   true,
	"/bin":    true,
	"/sbin":   true,
	"/lib":    true,
	"/lib64":  true,
	"/boot":   true,
	"/dev":    true,
	"/proc":   true,
	"/sys":    true,
	"/run":    true,
	"/tmp":    true,
	"/media":  true,
	"/mnt":    true,
	"/srv":    true,
	"/snap":   true,
	"/System": true, // défense en profondeur si jamais exécuté hors Linux
	"/Users":  true,
}

func isDangerousSystemPath(clean string) bool {
	return protectedSystemPaths[clean]
}

// ValidatePathComponent nettoie et valide un chemin destiné à servir de
// racine de suppression (un composant de Paths, ou tout chemin candidat).
// Retourne le chemin nettoyé si — et seulement si — il est absolu et ne
// correspond à aucun répertoire système protégé. Ne transforme JAMAIS
// silencieusement une valeur dangereuse : une erreur explicite est
// retournée dans tous les autres cas.
func ValidatePathComponent(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w : chemin vide", ErrUnsafePath)
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w : %q", ErrRelativePath, path)
	}
	if isDangerousSystemPath(clean) {
		return "", fmt.Errorf("%w : %q", ErrDangerousRoot, clean)
	}
	return clean, nil
}

// identityConfirmedForDataDir exige DEUX signaux concordants avant de
// considérer path comme un authentique répertoire de données LABOSURF PRO
// éligible à un RemoveAll intégral : (1) son nom de base contient
// "labosurf" ET (2) il contient au moins un marqueur de fichier/répertoire
// réellement produit par ce projet (dataDirIdentityMarkers). Un nom seul est
// trivialement imitable ; un marqueur seul pourrait coïncider par hasard —
// les deux ensemble réduisent fortement ce risque.
func identityConfirmedForDataDir(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.Clean(path)))
	if !strings.Contains(base, "labosurf") {
		return false
	}
	for _, m := range dataDirIdentityMarkers {
		if exists(filepath.Join(path, m)) {
			return true
		}
	}
	return false
}

// identityConfirmedForBinDir confirme qu'un répertoire de binaires tiers
// appartient à LABOSURF PRO. Ce répertoire (LABOSURF_BIN_DIR, ex.
// /opt/labosurf) ne contient que des binaires tiers téléchargés par les
// moteurs (xray-core, tuic-server, etc.) — il n'a pas de marqueur de
// fichier propre à vérifier, donc le nom du répertoire est le seul signal
// disponible ; c'est pourquoi la valeur par défaut ("/opt/labosurf") est
// nommée explicitement pour porter "labosurf", et pourquoi une valeur
// dégradée comme "/opt" (dépourvue de "labosurf") est déjà rejetée en
// amont par ValidatePathComponent (répertoire système protégé) — cette
// fonction est une seconde barrière indépendante, pas la seule.
func identityConfirmedForBinDir(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.Clean(path)))
	return strings.Contains(base, "labosurf")
}

// Inventory décrit ce qui a été réellement détecté sur la machine — jamais
// une valeur supposée ou inventée : chaque champ ne contient que des
// chemins dont l'existence ET l'appartenance à LABOSURF PRO ont été
// vérifiées par Detect. Detect() n'a pas la prétention d'être exhaustif sur
// tous les fichiers annexes possibles (voir Warnings et le commentaire de
// Detect) — seuls les fichiers listés dans configFileNames/dataOnlyMarkers
// sont reconnus explicitement.
type Inventory struct {
	Binaries          []string // labosurf, labosurf-<moteur>, menu — seulement ceux présents
	SystemdUnits      []string // labosurf.service, labosurf-<moteur>.service — seulement présents
	ThirdPartyDir     string   // OptDir si présent ET identité confirmée, sinon ""
	ConfigFiles       []string // config.json, license_pub.key — seulement si DataDir confirmé
	DataDir           string   // DataDir si présent ET identité confirmée, sinon ""
	DataFiles         []string // users_db.json, services/, access/, profiles/, engines/, server.json, reçus
	SystemUserPresent bool     // utilisateur système "labosurf" (moteur SSH)
	Warnings          []string // configuration douteuse ignorée (jamais utilisée comme racine)
}

// Detect scanne les chemins réels (aucune suppression) et retourne
// l'inventaire de ce qui appartient effectivement à LABOSURF PRO.
//
// Limite assumée et documentée : seuls les fichiers listés dans
// configFileNames et dataOnlyMarkers (+ reçus .install_*.receipt) sont
// reconnus individuellement. Un fichier de données futur non ajouté à
// cette liste resterait invisible à l'inventaire affiché (mais ne serait
// jamais perdu par erreur : il resterait simplement à l'intérieur de
// DataDir tant que ce dernier n'est pas supprimé en bloc — mode complet
// uniquement).
func Detect(p Paths) Inventory {
	var inv Inventory

	if binRoot, err := ValidatePathComponent(p.BinDir); err == nil {
		for _, name := range []string{"labosurf", "menu"} {
			path := filepath.Join(binRoot, name)
			if exists(path) {
				inv.Binaries = append(inv.Binaries, path)
			}
		}
		for _, eng := range KnownEngines {
			bin := filepath.Join(binRoot, "labosurf-"+eng)
			if exists(bin) {
				inv.Binaries = append(inv.Binaries, bin)
			}
		}
	} else if strings.TrimSpace(p.BinDir) != "" {
		inv.Warnings = append(inv.Warnings, fmt.Sprintf("BinDir %q ignoré : %v", p.BinDir, err))
	}

	if sysRoot, err := ValidatePathComponent(p.SystemdDir); err == nil {
		for _, eng := range KnownEngines {
			unit := filepath.Join(sysRoot, "labosurf-"+eng+".service")
			if exists(unit) {
				inv.SystemdUnits = append(inv.SystemdUnits, unit)
			}
		}
		if unit := filepath.Join(sysRoot, "labosurf.service"); exists(unit) {
			inv.SystemdUnits = append(inv.SystemdUnits, unit)
		}
	} else if strings.TrimSpace(p.SystemdDir) != "" {
		inv.Warnings = append(inv.Warnings, fmt.Sprintf("SystemdDir %q ignoré : %v", p.SystemdDir, err))
	}

	if optRoot, err := ValidatePathComponent(p.OptDir); err == nil {
		if exists(optRoot) {
			if identityConfirmedForBinDir(optRoot) {
				inv.ThirdPartyDir = optRoot
			} else {
				inv.Warnings = append(inv.Warnings, fmt.Sprintf(
					"OptDir %q existe mais son nom ne confirme pas une installation LABOSURF PRO (%v) — ignoré",
					optRoot, ErrUnidentifiedInstallation))
			}
		}
	} else if strings.TrimSpace(p.OptDir) != "" {
		inv.Warnings = append(inv.Warnings, fmt.Sprintf("OptDir %q ignoré : %v", p.OptDir, err))
	}

	if dataRoot, err := ValidatePathComponent(p.DataDir); err == nil {
		if exists(dataRoot) {
			if identityConfirmedForDataDir(dataRoot) {
				inv.DataDir = dataRoot
				for _, name := range configFileNames {
					f := filepath.Join(dataRoot, name)
					if exists(f) {
						inv.ConfigFiles = append(inv.ConfigFiles, f)
					}
				}
				for _, name := range dataOnlyMarkers {
					f := filepath.Join(dataRoot, name)
					if exists(f) {
						inv.DataFiles = append(inv.DataFiles, f)
					}
				}
				if entries, err := os.ReadDir(dataRoot); err == nil {
					for _, e := range entries {
						if strings.HasPrefix(e.Name(), ".install_") && strings.HasSuffix(e.Name(), ".receipt") {
							inv.DataFiles = append(inv.DataFiles, filepath.Join(dataRoot, e.Name()))
						}
					}
				}
			} else {
				inv.Warnings = append(inv.Warnings, fmt.Sprintf(
					"DataDir %q existe mais ne peut pas être identifié comme une installation LABOSURF PRO (%v) — ignoré, rien n'y sera supprimé",
					dataRoot, ErrUnidentifiedInstallation))
			}
		}
	} else if strings.TrimSpace(p.DataDir) != "" {
		inv.Warnings = append(inv.Warnings, fmt.Sprintf("DataDir %q ignoré : %v", p.DataDir, err))
	}

	inv.SystemUserPresent = systemUserExists("labosurf")

	return inv
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// systemUserExists vérifie la présence d'un utilisateur système via
// /etc/passwd (Linux). Retourne false sans erreur sur toute autre
// plateforme ou si le fichier est illisible — jamais une supposition.
func systemUserExists(username string) bool {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return false
	}
	prefix := username + ":"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// Mode décrit l'étendue de la désinstallation choisie par l'administrateur.
type Mode int

const (
	// ModeProgramOnly supprime uniquement le programme (binaires, unités
	// systemd, binaires tiers) — conserve intégralement /etc/labosurf.
	ModeProgramOnly Mode = iota
	// ModeProgramKeepData supprime le programme et la configuration
	// d'exécution (config.json, license_pub.key), mais conserve les
	// données LABOSURF PRO (comptes, services, accès, profils, reçus).
	ModeProgramKeepData
	// ModeFull supprime tout, y compris /etc/labosurf en entier.
	ModeFull
)

// RequiresTypedConfirmation indique si ce mode exige la saisie exacte de la
// phrase de confirmation (mission §3 : jamais un simple Entrée).
func (m Mode) RequiresTypedConfirmation() bool {
	return m != ModeProgramOnly
}

// ConfirmationPhrase est la phrase exacte exigée pour les modes affectant
// les données (§3). Toute autre saisie annule l'opération.
const ConfirmationPhrase = "DESINSTALLER"

// ValidatePhrase vérifie une saisie de confirmation textuelle — comparaison
// stricte, sensible à la casse et aux espaces (aucune tolérance qui
// affaiblirait la protection).
func ValidatePhrase(input string) bool {
	return input == ConfirmationPhrase
}

// ShouldProceed interprète une réponse "o/N" — jamais une chaîne vide ou
// une touche Entrée ne valent confirmation.
func ShouldProceed(answer string) bool {
	return strings.EqualFold(strings.TrimSpace(answer), "o")
}

// Plan décrit précisément ce qui sera arrêté/désactivé/supprimé/conservé —
// construit sans effectuer la moindre action, pour permettre à l'écran de
// l'afficher intégralement avant toute confirmation finale (mission §4).
//
// Plan ne fait que recopier les chemins déjà validés d'un Inventory —
// Detect() est le SEUL endroit où l'identité LABOSURF PRO d'un répertoire
// entier est établie ; BuildPlan ne réévalue jamais cette décision et ne
// peut donc jamais faire apparaître un répertoire non confirmé.
type Plan struct {
	Mode         Mode
	StopUnits    []string
	DisableUnits []string
	RemovePaths  []string
	KeepPaths    []string
}

// BuildPlan construit le plan de désinstallation pour un inventaire et un
// mode donnés. Logique pure — aucun accès disque.
func BuildPlan(inv Inventory, mode Mode) Plan {
	p := Plan{Mode: mode}
	p.StopUnits = append(p.StopUnits, inv.SystemdUnits...)
	p.DisableUnits = append(p.DisableUnits, inv.SystemdUnits...)

	p.RemovePaths = append(p.RemovePaths, inv.Binaries...)
	p.RemovePaths = append(p.RemovePaths, inv.SystemdUnits...)
	if inv.ThirdPartyDir != "" {
		p.RemovePaths = append(p.RemovePaths, inv.ThirdPartyDir)
	}

	switch mode {
	case ModeProgramOnly:
		if inv.DataDir != "" {
			p.KeepPaths = append(p.KeepPaths, inv.DataDir)
		}
	case ModeProgramKeepData:
		p.RemovePaths = append(p.RemovePaths, inv.ConfigFiles...)
		p.KeepPaths = append(p.KeepPaths, inv.DataFiles...)
	case ModeFull:
		if inv.DataDir != "" {
			p.RemovePaths = append(p.RemovePaths, inv.DataDir)
		}
	}
	return p
}

// allowedRoots retourne les racines déjà validées (ValidatePathComponent)
// sous lesquelles une suppression est permise. Toute valeur vide, relative,
// ou correspondant à un répertoire système protégé est silencieusement
// absente de cette liste (donc jamais utilisable comme racine) — voir
// Detect pour la trace explicite de ces exclusions (Inventory.Warnings).
func allowedRoots(p Paths) []string {
	var out []string
	for _, root := range []string{p.BinDir, p.OptDir, p.DataDir, p.SystemdDir} {
		clean, err := ValidatePathComponent(root)
		if err != nil {
			continue
		}
		out = append(out, clean)
	}
	return out
}

func isUnder(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// RemovePath supprime path après avoir vérifié (1) que path lui-même est un
// chemin sûr (absolu, non protégé) et (2) qu'il est bien contenu dans l'un
// des répertoires connus et validés de l'installation (p). Refuse
// explicitement tout chemin en dehors — jamais un os.RemoveAll sur un
// chemin non vérifié (mission §7).
func RemovePath(path string, p Paths) error {
	clean, err := ValidatePathComponent(path)
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrUnsafePath, err)
	}
	for _, root := range allowedRoots(p) {
		if isUnder(clean, root) {
			return os.RemoveAll(clean)
		}
	}
	return fmt.Errorf("%w : %s", ErrUnsafePath, path)
}

// RemovalOutcome résume le résultat d'une exécution de suppressions.
type RemovalOutcome struct {
	Removed   []string
	Remaining []string // chemins prévus mais jamais tentés (arrêt après erreur)
	Err       error
	FailedAt  string
}

// removeFunc est le type de fonction de suppression, injectable pour les
// tests (jamais un vrai os.RemoveAll dans les tests de ce package).
type removeFunc func(path string) error

// ExecuteRemovals applique séquentiellement remove à chaque chemin de
// paths. S'arrête à la première erreur — ne continue jamais après un échec
// (mission §10, "rollback en cas d'erreur" : pour une suppression, il n'y a
// rien à restaurer, mais on ne doit jamais prétendre à un succès complet
// après un échec partiel — l'opérateur reçoit la liste exacte de ce qui a
// été supprimé et de ce qui ne l'a pas été).
func ExecuteRemovals(paths []string, remove removeFunc) RemovalOutcome {
	var out RemovalOutcome
	for i, p := range paths {
		if err := remove(p); err != nil {
			out.Err = err
			out.FailedAt = p
			out.Remaining = append(out.Remaining, paths[i+1:]...)
			return out
		}
		out.Removed = append(out.Removed, p)
	}
	return out
}

// backupFunc est injectable pour les tests.
type backupFunc func() error

// ExecuteWithOptionalBackup effectue la sauvegarde (si demandée) avant
// toute suppression. Si la sauvegarde échoue, AUCUNE suppression n'est
// tentée (mission §6 : "si la sauvegarde échoue, arrêter la désinstallation
// complète, ne supprimer aucune donnée").
func ExecuteWithOptionalBackup(paths []string, doBackup bool, backup backupFunc, remove removeFunc) (backedUp bool, outcome RemovalOutcome, err error) {
	if doBackup {
		if berr := backup(); berr != nil {
			return false, RemovalOutcome{}, fmt.Errorf("sauvegarde échouée, désinstallation annulée : %w", berr)
		}
		backedUp = true
	}
	return backedUp, ExecuteRemovals(paths, remove), nil
}

// ---------- Sauvegarde ----------

// CreateBackup archive dataDir (tar.gz) vers destPath. N'écrit jamais dans
// dataDir lui-même. Le succès n'est retourné QUE si la marche du répertoire
// a réussi ET que la fermeture de chaque flux (tar, gzip, fichier) a
// elle-même réussi — un flush qui échouerait en toute fin d'archive (ex.
// disque plein) ne doit jamais être annoncé comme un succès silencieux.
func CreateBackup(dataDir, destPath string) error {
	info, err := os.Stat(dataDir)
	if err != nil {
		return fmt.Errorf("répertoire de données introuvable : %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s n'est pas un répertoire", dataDir)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(dataDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})

	// Fermeture explicite, dans l'ordre requis (tar puis gzip puis fichier),
	// chaque erreur étant prise en compte — pas de defer silencieux ici : un
	// double Close() accidentel ou une erreur de flush ignorée sont
	// exactement le genre de bug qui ferait passer une archive tronquée pour
	// une sauvegarde réussie.
	var closeErr error
	if err := tw.Close(); closeErr == nil {
		closeErr = err
	}
	if err := gz.Close(); closeErr == nil {
		closeErr = err
	}
	if err := out.Close(); closeErr == nil {
		closeErr = err
	}

	if walkErr != nil {
		_ = os.Remove(destPath)
		return fmt.Errorf("création de l'archive : %w", walkErr)
	}
	if closeErr != nil {
		_ = os.Remove(destPath)
		return fmt.Errorf("finalisation de l'archive : %w", closeErr)
	}
	return nil
}

// VerifyBackup ouvre l'archive et la lit ENTIÈREMENT jusqu'à io.EOF — pas
// seulement le premier en-tête tar — pour détecter une troncature n'importe
// où dans l'archive (en-tête, contenu d'un fichier, ou fin de flux gzip
// avec CRC invalide). Un contrôle qui s'arrêterait à la première entrée
// laisserait passer une archive tronquée après le premier fichier.
func VerifyBackup(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("archive de sauvegarde vide")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("archive corrompue (gzip) : %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	entries := 0
	for {
		_, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("archive corrompue ou tronquée (tar) : %w", err)
		}
		// Lire le contenu complet de l'entrée : une troncature en plein
		// milieu d'un fichier fait échouer cette copie, alors qu'un simple
		// tr.Next() suivant ne l'aurait pas détectée.
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return fmt.Errorf("archive tronquée (contenu illisible) : %w", err)
		}
		entries++
	}
	if entries == 0 {
		return fmt.Errorf("archive vide (aucune entrée)")
	}
	// Consommer le reste du flux gzip jusqu'à EOF déclenche la vérification
	// du CRC32/ISIZE de fin de flux — une corruption après le dernier
	// en-tête tar logique (mais avant la fin réelle du fichier gzip) est
	// détectée ici, pas avant.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("archive corrompue (fin de flux gzip invalide) : %w", err)
	}
	return nil
}

// ---------- Services systemd ----------

// CommandRunner exécute une commande externe — injectable pour les tests
// (jamais un vrai systemctl dans les tests de ce package).
type CommandRunner func(name string, args ...string) ([]byte, error)

// RealRunner exécute réellement la commande (comportement de production).
func RealRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// StopUnits arrête chaque unité listée. Ne touche jamais à une unité qui
// n'est pas dans la liste fournie — Detect ne renvoie que des unités
// labosurf* réellement présentes, jamais un nom arbitraire (mission §5.5).
func StopUnits(units []string, run CommandRunner) map[string]error {
	errs := map[string]error{}
	for _, u := range units {
		name := filepath.Base(u)
		if _, err := run("systemctl", "stop", name); err != nil {
			errs[u] = err
		}
	}
	return errs
}

// DisableUnits désactive chaque unité listée (même garde que StopUnits).
func DisableUnits(units []string, run CommandRunner) map[string]error {
	errs := map[string]error{}
	for _, u := range units {
		name := filepath.Base(u)
		if _, err := run("systemctl", "disable", name); err != nil {
			errs[u] = err
		}
	}
	return errs
}

// IsUnitActive interroge systemctl pour savoir si l'unité tourne encore —
// utilisé pour vérifier l'arrêt effectif avant de continuer (mission §5.3).
func IsUnitActive(unit string, run CommandRunner) bool {
	out, err := run("systemctl", "is-active", filepath.Base(unit))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}

// ---------- Auto-désinstallation ----------

// IsPathCurrentExecutable indique si path désigne le binaire actuellement
// en cours d'exécution — condition qui interdit une suppression directe
// (mission §8).
func IsPathCurrentExecutable(path string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	a, errA := filepath.EvalSymlinks(exe)
	if errA != nil {
		a = exe
	}
	b, errB := filepath.EvalSymlinks(path)
	if errB != nil {
		b = path
	}
	ca, errCa := filepath.Abs(a)
	cb, errCb := filepath.Abs(b)
	if errCa != nil || errCb != nil || ca == "" {
		return false
	}
	return ca == cb
}

// SpawnSelfDeleteHelper lance un script shell détaché qui attend la fin du
// processus courant puis supprime targetPath, avant de se supprimer
// lui-même. Linux uniquement (cible VPS réelle du projet) — retourne une
// erreur explicite ailleurs plutôt qu'un mécanisme non fiable (mission §8,
// §11 : isolation propre des parties spécifiques à Linux).
func SpawnSelfDeleteHelper(targetPath string) error {
	return spawnSelfDeleteHelperForPID(os.Getpid(), targetPath)
}

// spawnSelfDeleteHelperForPID est la version testable de
// SpawnSelfDeleteHelper : surveille pid (qui n'a pas besoin d'être le
// processus courant), ce qui permet à un test de fournir un processus
// jetable dont il contrôle le cycle de vie plutôt que de dépendre du
// processus de test lui-même.
func spawnSelfDeleteHelperForPID(pid int, targetPath string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf(
			"auto-suppression non supportée sur %s : arrêtez le programme puis supprimez %s manuellement",
			runtime.GOOS, targetPath)
	}

	script := selfDeleteScript(pid, targetPath)

	helper, err := os.CreateTemp("", "labosurf-uninstall-helper-*.sh")
	if err != nil {
		return err
	}
	helperPath := helper.Name()
	if _, err := helper.WriteString(script); err != nil {
		_ = helper.Close()
		_ = os.Remove(helperPath)
		return err
	}
	if err := helper.Close(); err != nil {
		_ = os.Remove(helperPath)
		return err
	}
	if err := os.Chmod(helperPath, 0o755); err != nil {
		_ = os.Remove(helperPath)
		return err
	}

	cmd := exec.Command("/bin/sh", helperPath)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(helperPath)
		return fmt.Errorf("lancement de l'aide à l'auto-suppression : %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

// selfDeleteScript construit le script shell de l'aide à l'auto-suppression
// — isolé dans sa propre fonction pour rester lisible et pour que son
// contenu (pas seulement le déclenchement) puisse être exercé par un test
// réel (voir TestSpawnSelfDeleteHelperDeletesAfterWatchedProcessExits).
func selfDeleteScript(pid int, targetPath string) string {
	return fmt.Sprintf(`#!/bin/sh
# Aide à l'auto-désinstallation LABOSURF PRO : attend la fin du processus
# %d (le gestionnaire en cours d'exécution), supprime le binaire cible,
# puis se supprime lui-même.
while kill -0 %d 2>/dev/null; do
  sleep 1
done
rm -f %s
rm -f "$0"
`, pid, pid, shellQuote(targetPath))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
