package engineutil

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BinarySpec décrit une source de binaire tierce à déployer.
type BinarySpec struct {
	// Name est l'identifiant du moteur (détermine le répertoire cible).
	Name string

	// BinName est le nom du fichier exécutable à l'intérieur de l'archive
	// (ou le nom du fichier s'il est brut). C'est aussi le nom du binaire
	// déployé dans le répertoire cible si RenameAs est vide.
	BinName string

	// InstallName est le nom sous lequel déployer le binaire (ex : xray).
	// Par défaut = BinName.
	InstallName string

	// URL est le lien de téléchargement, par arch.
	URL func(arch string) string

	// SHA256 attendu, par arch (hex). Si non renseigné, la vérification
	// est sautée (déconseillé en production).
	SHA256 func(arch string) string

	// IsArchive indique que le téléchargé est une archive (.tar.gz/.zip)
	// contenant BinName à extraire.
	IsArchive bool

	// ClientHTTP optionnel pour tests.
	ClientHTTP *http.Client
}

// BinaryDir retourne le répertoire d'installation des binaires tierce.
func BinaryDir() string { return DefaultBinaryDir + "/lib" }

// deployedPath retourne le chemin du binaire une fois déployé.
func (s *BinarySpec) deployedPath() string {
	name := s.InstallName
	if name == "" {
		name = s.BinName
	}
	return filepath.Join(BinaryDir(), s.Name, name)
}

// Installed indique si le binaire tierce est déjà déployé.
func (s *BinarySpec) Installed() bool { return Exists(s.deployedPath()) }

// DeployedPath retourne le chemin absolu du binaire déployé.
func (s *BinarySpec) DeployedPath() string { return s.deployedPath() }

// Download récupère, vérifie et déploie le binaire tierce.
func (s *BinarySpec) Download(ctx context.Context, arch string) (string, error) {
	if s.Installed() {
		return s.deployedPath(), nil
	}

	if s.URL == nil {
		return "", fmt.Errorf("moteur %s : URL de téléchargement non définie", s.Name)
	}

	url := s.URL(arch)
	if url == "" {
		return "", fmt.Errorf("moteur %s : pas d'URL de téléchargement pour arch %s", s.Name, arch)
	}

	if err := EnsureDir(BinaryDir() + "/" + s.Name); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "labosurf-"+s.Name+"-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// Support des binaires locaux (file://) pour les tests et le dev.
	if strings.HasPrefix(url, "file://") {
		local := strings.TrimPrefix(url, "file://")
		if err := copyFile(local, tmpName); err != nil {
			return "", fmt.Errorf("copie binaire local %s : %w", local, err)
		}
	} else {
		client := s.ClientHTTP
		if client == nil {
			client = &http.Client{Timeout: 5 * time.Minute}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}

		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("téléchargement %s : %w", url, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("téléchargement %s : statut HTTP %d", url, resp.StatusCode)
		}

		if _, err := io.Copy(tmp, resp.Body); err != nil {
			return "", err
		}
	}
	_ = tmp.Close()

	// Vérification SHA-256.
	if s.SHA256 != nil {
		if want := s.SHA256(arch); want != "" {
			got, herr := fileSHA256(tmpName)
			if herr != nil {
				return "", herr
			}
			if !strings.EqualFold(got, want) {
				return "", fmt.Errorf("SHA-256 invalide pour %s (voulu %s, obtenu %s)", url, want, got)
			}
		}
	}

	// Extraction ou copie.
	dst := s.deployedPath()
	if s.IsArchive {
		if err := extractBin(tmpName, s.BinName, dst); err != nil {
			return "", err
		}
	} else {
		if err := copyFile(tmpName, dst); err != nil {
			return "", err
		}
	}
	_ = os.Chmod(dst, 0o755)

	return dst, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

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
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func extractBin(archive, wantName, dst string) error {
	if strings.HasSuffix(archive, ".zip") || isZip(archive) {
		return extractZip(archive, wantName, dst)
	}
	return extractTarGz(archive, wantName, dst)
}

func extractTarGz(archive, wantName, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == wantName || hdr.Name == wantName {
			out, err := os.Create(dst)
			if err != nil {
				return err
			}
			_, cerr := io.Copy(out, tr)
			out.Close()
			return cerr
		}
	}
	return fmt.Errorf("binaire %q introuvable dans l'archive", wantName)
}

func extractZip(archive, wantName, dst string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) == wantName || f.Name == wantName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			out, err := os.Create(dst)
			if err != nil {
				return err
			}
			_, cerr := io.Copy(out, rc)
			out.Close()
			return cerr
		}
	}
	return fmt.Errorf("binaire %q introuvable dans l'archive zip", wantName)
}

func isZip(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4)
	n, _ := io.ReadFull(f, buf)
	return n == 4 && buf[0] == 'P' && buf[1] == 'K'
}

// LocalBinary crée une spec pointant vers un binaire local (pour tests/dev),
// en le copiant directement.
func LocalBinary(name, installName, localPath string) *BinarySpec {
	return &BinarySpec{
		Name:        name,
		BinName:     localPath,
		InstallName: installName,
		URL: func(arch string) string {
			return "file://" + localPath
		},
		SHA256:   func(arch string) string { return "" },
		IsArchive: false,
	}
}
