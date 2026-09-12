package freewaygate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// installBinary télécharge le binaire freeway-gate depuis la release GitHub
// et le place dans binaryDir. Retourne le chemin complet du binaire.
func installBinary(ctx context.Context, binaryDir string) (string, error) {
	binaryPath := filepath.Join(binaryDir, binaryName)

	asset := assetNameFor(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		return "", fmt.Errorf("freeway-gate: pas d'asset pour %s/%s",
			runtime.GOOS, runtime.GOARCH)
	}
	url := releaseBaseURL + "/" + releaseVersion + "/" + asset

	tmp := binaryPath + ".tmp"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("freeway-gate: telechargement %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("freeway-gate: telechargement %s: %s", url, resp.Status)
	}

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
	_ = os.Chmod(binaryPath, 0o755)
	_ = os.Remove(binaryPath)
	if err := os.Rename(tmp, binaryPath); err != nil {
		return "", err
	}
	// Windows ignore la permission ; sur Unix on s'assure que le binaire
	// est exécutable même si le téléchargement a créé un fichier 0444.
	if !strings.EqualFold(runtime.GOOS, "windows") {
		_ = os.Chmod(binaryPath, 0o755)
	}
	return binaryPath, nil
}
