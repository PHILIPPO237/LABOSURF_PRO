// Package selfupdate vérifie l'existence d'une nouvelle version de
// LABOSURF PRO (gestionnaire cmd/labosurf) sur les releases GitHub
// officielles du dépôt, et installe la mise à jour après confirmation
// explicite et vérification d'intégrité SHA-256.
//
// Réutilise la même source de vérité que le reste du projet : les tags git
// publiés via .github/workflows/release.yml (voir labosurf-pro.sh, qui
// télécharge déjà les binaires de moteurs depuis
// "releases/latest/download/<asset>" + SHA256SUMS selon la même logique).
// Aucun nouveau système de version n'est créé — la version courante est
// injectée au build via -ldflags (voir cmd/labosurf/main.go, variable
// `version`), depuis le même tag git que celui qui nomme la release.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo est le dépôt GitHub officiel de LABOSURF PRO.
const DefaultRepo = "PHILIPPO237/LABOSURF_PRO"

// Asset décrit un artefact attaché à une release GitHub.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Release décrit une release GitHub telle que renvoyée par l'API
// "/repos/<owner>/<repo>/releases/latest".
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// ErrNoCompatibleAsset indique qu'aucun artefact de la release ne correspond
// à l'architecture courante.
var ErrNoCompatibleAsset = errors.New("aucune mise à jour compatible avec cette architecture")

// ErrChecksumMismatch indique que le fichier téléchargé ne correspond pas au
// SHA-256 attendu — la mise à jour est annulée, jamais installée.
var ErrChecksumMismatch = errors.New("vérification SHA-256 échouée")

// apiBaseURL et downloadBaseURL sont substituables dans les tests (serveur
// httptest local) — en production, ce sont les vraies URL GitHub.
var (
	apiBaseURL = "https://api.github.com"
)

// FetchLatestRelease interroge l'API GitHub Releases (endpoint officiel,
// aucune URL arbitraire) pour connaître la dernière release publiée du
// dépôt. N'effectue aucun téléchargement de binaire.
func FetchLatestRelease(ctx context.Context, client *http.Client, repo string) (Release, error) {
	if client == nil {
		client = http.DefaultClient
	}
	url := apiBaseURL + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("contact de l'API GitHub : %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return Release{}, fmt.Errorf("aucune release publiée pour %s", repo)
	}
	if resp.StatusCode/100 != 2 {
		return Release{}, fmt.Errorf("API GitHub : %s", resp.Status)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Release{}, fmt.Errorf("réponse API GitHub illisible : %w", err)
	}
	if rel.TagName == "" {
		return Release{}, fmt.Errorf("réponse API GitHub sans tag_name")
	}
	return rel, nil
}

// AssetNameFor retourne le nom de l'artefact du gestionnaire multi-moteurs
// pour l'OS/architecture donnés — même convention que
// .github/workflows/release.yml ("labosurf-mgr-<goos>-<goarch>").
func AssetNameFor(goos, goarch string) string {
	return fmt.Sprintf("labosurf-mgr-%s-%s", goos, goarch)
}

// FindAsset cherche un artefact par nom exact dans une release.
func FindAsset(rel Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// ParseVersion décompose une version "vX.Y.Z" (le "v" est optionnel) en
// triplet numérique. Retourne une erreur explicite pour tout format non
// conforme — jamais une comparaison approximative.
func ParseVersion(v string) ([3]int, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("version %q : format attendu X.Y.Z", v)
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return [3]int{}, fmt.Errorf("version %q : composant %q invalide", v, p)
		}
		out[i] = n
	}
	return out, nil
}

// CompareVersions compare deux versions "vX.Y.Z". Retourne -1 si a < b,
// 0 si égales, 1 si a > b. Erreur si l'une des deux est mal formée.
func CompareVersions(a, b string) (int, error) {
	pa, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	pb, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

// UpdateCheck résume le résultat d'une vérification de mise à jour.
type UpdateCheck struct {
	CurrentVersion string
	Release        Release
	UpdateFound    bool
	Asset          Asset  // valide seulement si UpdateFound et AssetFound
	AssetFound     bool
}

// CheckForUpdate interroge la dernière release et détermine si elle est plus
// récente que currentVersion. N'installe rien — se contente de comparer et
// de repérer l'artefact compatible avec goos/goarch s'il existe.
//
// Si currentVersion n'est pas parseable (ex. "dev", build locale non taguée),
// une mise à jour est considérée disponible dès qu'une release existe — on
// ne bloque jamais silencieusement une mise à jour faute de savoir où l'on
// en est (cohérent avec la philosophie du projet : jamais de version
// fabriquée, mais jamais non plus un blocage arbitraire).
func CheckForUpdate(ctx context.Context, client *http.Client, repo, currentVersion, goos, goarch string) (UpdateCheck, error) {
	rel, err := FetchLatestRelease(ctx, client, repo)
	if err != nil {
		return UpdateCheck{}, err
	}

	updateFound := true
	if cmp, err := CompareVersions(currentVersion, rel.TagName); err == nil {
		updateFound = cmp < 0
	}
	// Si currentVersion est mal formée (ex. "dev"), on garde updateFound=true.

	check := UpdateCheck{
		CurrentVersion: currentVersion,
		Release:        rel,
		UpdateFound:    updateFound,
	}
	if updateFound {
		asset, ok := FindAsset(rel, AssetNameFor(goos, goarch))
		check.Asset = asset
		check.AssetFound = ok
	}
	return check, nil
}

// ShouldInstall interprète la réponse de l'administrateur au prompt de
// confirmation ("o/N"). Logique pure, testable indépendamment de la saisie
// stdin réelle de l'écran menu.
func ShouldInstall(answer string) bool {
	return strings.EqualFold(strings.TrimSpace(answer), "o")
}

// ParseSHA256Sums lit le contenu d'un fichier SHA256SUMS (format
// "sha256sum" standard : "<hash>  <nom-de-fichier>" par ligne) et retourne
// une map nom→hash en minuscules.
func ParseSHA256Sums(data []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := strings.ToLower(fields[0])
		name := fields[len(fields)-1]
		// Le format sha256sum préfixe parfois le nom d'un "*" (mode binaire).
		name = strings.TrimPrefix(name, "*")
		out[name] = hash
	}
	return out
}

// downloadToFile télécharge url vers destPath de façon sûre : écriture dans
// un fichier temporaire, jamais dans destPath directement (un téléchargement
// interrompu ne laisse donc jamais un fichier partiel au nom final).
func downloadToFile(ctx context.Context, client *http.Client, url, destPath string) error {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("téléchargement %s : %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("téléchargement %s : %s", url, resp.Status)
	}

	tmp := destPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("téléchargement interrompu (%s) : %w", url, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// verifySHA256 vérifie que le fichier a bien l'empreinte SHA-256 attendue.
func verifySHA256(path, expectedHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expectedHex) {
		return fmt.Errorf("%w : attendu %s, obtenu %s", ErrChecksumMismatch, expectedHex, actual)
	}
	return nil
}

// DownloadAndVerify télécharge le SHA256SUMS de la release puis l'artefact
// visé, et vérifie son intégrité avant de retourner son chemin temporaire.
// Le fichier retourné n'est PAS encore installé — c'est le rôle d'Install.
//
// Si le SHA256SUMS ne contient pas de hash pour cet artefact, ou si le
// SHA256SUMS lui-même est introuvable dans la release, retourne une erreur
// explicite : jamais d'installation non vérifiée.
func DownloadAndVerify(ctx context.Context, client *http.Client, rel Release, asset Asset, workDir string) (string, error) {
	sumsAsset, ok := FindAsset(rel, "SHA256SUMS")
	if !ok {
		return "", fmt.Errorf("SHA256SUMS introuvable dans la release %s", rel.TagName)
	}

	sumsPath := filepath.Join(workDir, "SHA256SUMS")
	if err := downloadToFile(ctx, client, sumsAsset.BrowserDownloadURL, sumsPath); err != nil {
		return "", fmt.Errorf("téléchargement SHA256SUMS : %w", err)
	}
	sumsData, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", err
	}
	sums := ParseSHA256Sums(sumsData)
	expected, ok := sums[asset.Name]
	if !ok {
		return "", fmt.Errorf("aucun SHA-256 pour %s dans SHA256SUMS de %s — release incohérente", asset.Name, rel.TagName)
	}

	binPath := filepath.Join(workDir, asset.Name)
	if err := downloadToFile(ctx, client, asset.BrowserDownloadURL, binPath); err != nil {
		return "", fmt.Errorf("téléchargement %s : %w", asset.Name, err)
	}

	if err := verifySHA256(binPath, expected); err != nil {
		_ = os.Remove(binPath)
		return "", err
	}
	return binPath, nil
}

// isExecutableFile vérifie que le fichier téléchargé a une taille non nulle
// et, sur Linux (cible réelle de ce binaire), qu'il porte bien l'en-tête ELF
// — un contrôle honnête que "le fichier est exploitable" avant tout
// remplacement, sans prétendre valider davantage qu'un format de binaire.
func isExecutableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("fichier téléchargé vide")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return fmt.Errorf("en-tête illisible : %w", err)
	}
	// En-tête ELF : 0x7F 'E' 'L' 'F'. Vérifié uniquement quand on s'attend à
	// un binaire Linux (cible réelle de LABOSURF PRO) ; ne bloque pas les
	// autres OS pour ne pas fabriquer une contrainte inexistante ailleurs.
	if strings.EqualFold(currentGOOS(), "linux") {
		if magic[0] != 0x7F || magic[1] != 'E' || magic[2] != 'L' || magic[3] != 'F' {
			return fmt.Errorf("fichier téléchargé : en-tête ELF absent (fichier corrompu ou incorrect)")
		}
	}
	return nil
}

// currentGOOS existe pour être substitué dans les tests (forcer le contrôle
// ELF sans dépendre du système qui exécute réellement les tests).
var currentGOOSFunc = func() string { return goosRuntime() }

func currentGOOS() string { return currentGOOSFunc() }

// InstallResult résume ce qui a été fait lors d'une installation.
type InstallResult struct {
	BackupPath string
	Installed  bool
}

// Install remplace atomiquement le binaire courant (currentBinaryPath) par
// downloadedPath, avec sauvegarde préalable et vérification post-remplacement
// via `<nouveau binaire> version`. En cas d'échec de cette vérification, le
// binaire d'origine est restauré (rollback) et une erreur est retournée.
//
// Ne supprime jamais currentBinaryPath avant que downloadedPath ait été
// vérifié à la fois par SHA-256 (en amont, DownloadAndVerify) et par ce
// contrôle d'exécutabilité + sanity-check. Ne touche à aucun autre fichier
// (données, configuration, licences, services) : Install ne connaît que les
// deux chemins de binaire qu'on lui passe.
//
// sanityCheck est injectable pour les tests (éviter d'exécuter un vrai
// binaire) ; nil utilise le comportement réel (`exec.Command(path, "version")`).
func Install(downloadedPath, currentBinaryPath string, sanityCheck func(path string) error) (InstallResult, error) {
	if err := isExecutableFile(downloadedPath); err != nil {
		return InstallResult{}, fmt.Errorf("fichier téléchargé inexploitable : %w", err)
	}

	backupPath := currentBinaryPath + ".backup-" + time.Now().UTC().Format("20060102T150405Z")
	if err := copyFile(currentBinaryPath, backupPath); err != nil {
		return InstallResult{}, fmt.Errorf("sauvegarde du binaire actuel : %w", err)
	}
	if err := os.Chmod(backupPath, 0o755); err != nil {
		_ = os.Remove(backupPath)
		return InstallResult{}, fmt.Errorf("permissions de sauvegarde : %w", err)
	}

	if err := os.Chmod(downloadedPath, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("permissions du nouveau binaire : %w", err)
	}

	// Remplacement atomique : rename dans le même répertoire que la cible
	// (même filesystem requis pour un rename atomique) plutôt qu'un simple
	// os.Rename(downloadedPath, currentBinaryPath) si downloadedPath vient
	// d'un autre filesystem (ex. /tmp sur un montage séparé) — on copie
	// d'abord dans un fichier voisin de la cible, puis on rename.
	stagedPath := currentBinaryPath + ".new"
	if err := copyFile(downloadedPath, stagedPath); err != nil {
		return InstallResult{}, fmt.Errorf("préparation du nouveau binaire : %w", err)
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		_ = os.Remove(stagedPath)
		return InstallResult{}, fmt.Errorf("permissions du binaire préparé : %w", err)
	}
	if err := os.Rename(stagedPath, currentBinaryPath); err != nil {
		_ = os.Remove(stagedPath)
		return InstallResult{}, fmt.Errorf("remplacement atomique : %w", err)
	}

	check := sanityCheck
	if check == nil {
		check = defaultSanityCheck
	}
	if err := check(currentBinaryPath); err != nil {
		// Rollback : le nouveau binaire ne fonctionne pas, on restaure l'ancien.
		if rbErr := copyFile(backupPath, currentBinaryPath); rbErr != nil {
			return InstallResult{BackupPath: backupPath}, fmt.Errorf(
				"nouveau binaire invalide (%v) ET rollback échoué (%v) — restaurer manuellement depuis %s",
				err, rbErr, backupPath)
		}
		_ = os.Chmod(currentBinaryPath, 0o755)
		return InstallResult{BackupPath: backupPath}, fmt.Errorf("nouveau binaire invalide, rollback effectué : %w", err)
	}

	return InstallResult{BackupPath: backupPath, Installed: true}, nil
}

// defaultSanityCheck exécute `<path> version` et vérifie que la commande
// réussit — confirmation minimale que le nouveau binaire s'exécute avant de
// considérer la mise à jour définitive.
func defaultSanityCheck(path string) error {
	cmd := exec.Command(path, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("`%s version` a échoué : %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyFile copie src vers dst (contenu complet), sans préserver dst s'il
// existe déjà (écrasé). Utilisé pour la sauvegarde et le remplacement — pas
// un rename, car src et dst peuvent être sur des filesystems différents.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
