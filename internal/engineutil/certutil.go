package engineutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// EnsureHysteriaCerts génère (si absents) une paire certificat/clé TLS
// auto-signée pour Hysteria via openssl, sous le répertoire de données du
// moteur. En mode "bypass" sans domaine/ACME, Hysteria doit disposer d'un
// cert ; le client utilisera insecure:true.
func EnsureHysteriaCerts(dataDir string) (certPath, keyPath string, err error) {
	dir := filepath.Join(dataDir, "hysteria")
	if err := EnsureDir(dir); err != nil {
		return "", "", err
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}

	// Certificat auto-signé ECDSA P-256 (openssl : demande automatiquement
	// via config, requis sur certains build). On fournit une config -subj
	// non-interactive.
	cmd := exec.Command("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt",
		"ec_paramgen_curve:prime256v1", "-keyout", keyPath, "-out", certPath,
		"-days", "3650", "-nodes", "-subj", "/C=FR/ST=LaboSurf/O=LaboSURF PRO/CN=labosurf.local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("openssl (génération certificat Hysteria) : %v\n%s", err, string(out))
	}
	return certPath, keyPath, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
