package freewaygate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	releaseVersion = "v0.1.0"
	releaseBaseURL = "https://github.com/PHILIPPO237/freeway-gate/releases/download"
	binaryName     = "freeway-gate"
)

// assetNameFor retourne le nom d'asset de la release freeway-gate pour
// l'architecture courante. Les assets publiés :
//   - freeway-gate-linux-amd64 (VPS, WSL, Termux x86_64)
//   - freeway-gate-linux-arm64 (Termux ARM64, Raspberry Pi)
//   - freeway-gate-linux-386
//   - freeway-gate-windows-amd64.exe (non installé par ce moteur)
func assetNameFor(goos, goarch string) string {
	if goos != "linux" {
		return ""
	}
	switch goarch {
	case "amd64":
		return "freeway-gate-linux-amd64"
	case "arm64":
		return "freeway-gate-linux-arm64"
	case "386":
		return "freeway-gate-linux-386"
	default:
		return ""
	}
}

// expectedSHA256For retourne le SHA-256 attendu de l'asset de la release
// courante, ou "" si aucun hash n'est épinglé. Surchargé par la variable
// d'environnement LABOSURF_FREEWAY_SHA256 (hash de l'asset courant).
//
// Les hashes ci-dessous correspondent aux assets v0.1.0 compilés depuis
// PHILIPPO237/freeway-gate @ main (renommage proxy-mtn/proxy-orange) +
// route /health montée, avec `-trimpath -ldflags='-s -w'` (Go 1.22.2, CGO
// désactivé). La release GitHub v0.1.0 DOIT publier EXACTEMENT ces fichiers,
// sinon l'installation échoue (refus explicite, jamais silencieux) : un
// mismatch ne laisse aucun doute sur une altération. Régénérer les hashes à
// chaque nouvelle release, puis ce tableau.
func expectedSHA256For(asset string) string {
	if v := strings.TrimSpace(os.Getenv("LABOSURF_FREEWAY_SHA256")); v != "" {
		return strings.ToLower(v)
	}
	switch asset {
	case "freeway-gate-linux-amd64":
		return "ae42ce326144813e53d56fea386912905dec3691f22d3dbb32f0d5c19e79c754"
	case "freeway-gate-linux-arm64":
		return "0553eaae3a0732dc1f0041a30672782678b28cbc4d6f5cc168e5e398e1c17ea5"
	case "freeway-gate-linux-386":
		return "3f986b671e03240468d7f58bf66ce34dea6e8fa5774a26273f67e21e6faa7d5f"
	default:
		return ""
	}
}

// installBinary télécharge le binaire freeway-gate depuis la release GitHub
// et le place dans binaryDir. Vérifie le SHA-256 quand un hash est connu
// (voir expectedSHA256For). Retourne le chemin complet du binaire.
func installBinary(ctx context.Context, binaryDir string) (string, error) {
	binaryPath := filepath.Join(binaryDir, binaryName)

	asset := assetNameFor(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		return "", fmt.Errorf("freeway-gate: pas d'asset pour %s/%s",
			runtime.GOOS, runtime.GOARCH)
	}
	url := releaseBaseURL + "/" + releaseVersion + "/" + asset

	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("freeway-gate: telechargement %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("freeway-gate: telechargement %s: %s", url, resp.Status)
	}

	tmp := binaryPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}

	if expected := expectedSHA256For(asset); expected != "" {
		if err := verifySHA256(tmp, expected); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("freeway-gate: verification SHA-256: %w", err)
		}
	} else {
		log.Printf("⚠ freeway-gate : aucun SHA-256 épingle pour la release %s — binaire téléchargé NON vérifié cryptographiquement (définir LABOSURF_FREEWAY_SHA256 pour épingler)", releaseVersion)
	}

	_ = os.Remove(binaryPath)
	if err := os.Rename(tmp, binaryPath); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	// Windows ignore la permission ; sur Unix on s'assure que le binaire
	// est exécutable même si le téléchargement a créé un fichier 0444.
	if !strings.EqualFold(runtime.GOOS, "windows") {
		_ = os.Chmod(binaryPath, 0o755)
	}
	return binaryPath, nil
}

// verifySHA256 vérifie que le fichier a bien l'empreinte SHA-256 attendue.
func verifySHA256(path, expected string) error {
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
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("mismatch: attendu %s, obtenu %s", expected, actual)
	}
	return nil
}
