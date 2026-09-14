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

// EnsureTUICCerts génère (si absents) une paire certificat/clé TLS
// auto-signée pour TUIC via openssl, sous le répertoire de données du
// moteur. TUIC exige TLS pour son handshake QUIC — il n'existe pas de mode
// "sans TLS" côté protocole. En mode "bypass" sans domaine/ACME, un
// certificat auto-signé est utilisé ; le client doit alors activer l'option
// d'insécurité correspondante (même contrainte que Hysteria).
func EnsureTUICCerts(dataDir string) (certPath, keyPath string, err error) {
	dir := filepath.Join(dataDir, "tuic")
	if err := EnsureDir(dir); err != nil {
		return "", "", err
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}

	cmd := exec.Command("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt",
		"ec_paramgen_curve:prime256v1", "-keyout", keyPath, "-out", certPath,
		"-days", "3650", "-nodes", "-subj", "/C=FR/ST=LaboSurf/O=LaboSURF PRO/CN=labosurf.local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("openssl (génération certificat TUIC) : %v\n%s", err, string(out))
	}
	return certPath, keyPath, nil
}

// EnsureHysteria2Certs génère (si absents) une paire certificat/clé TLS
// auto-signée pour le moteur Hysteria2 OFFICIEL, sous son propre
// répertoire ("hysteria2") — jamais celui du moteur "hysteria" maison
// (EnsureHysteriaCerts) : le mécanisme de génération (certificat
// auto-signé ECDSA P-256 via openssl) est identique et réutilisable tel
// quel (Hysteria2 n'a aucune exigence particulière sur le contenu du
// certificat au-delà d'un couple cert/clé PEM standard), mais partager le
// RÉPERTOIRE avec le moteur maison coupleraient artificiellement deux
// moteurs indépendants (un Uninstall/régénération de l'un affecterait
// l'autre). D'où cette fonction séparée plutôt qu'un appel direct à
// EnsureHysteriaCerts — même modèle que EnsureTUICCerts avant elle.
func EnsureHysteria2Certs(dataDir string) (certPath, keyPath string, err error) {
	dir := filepath.Join(dataDir, "hysteria2")
	if err := EnsureDir(dir); err != nil {
		return "", "", err
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}

	cmd := exec.Command("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt",
		"ec_paramgen_curve:prime256v1", "-keyout", keyPath, "-out", certPath,
		"-days", "3650", "-nodes", "-subj", "/C=FR/ST=LaboSurf/O=LaboSURF PRO/CN=labosurf.local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("openssl (génération certificat Hysteria2) : %v\n%s", err, string(out))
	}
	return certPath, keyPath, nil
}

// EnsureXrayCerts génère (si absents) une paire certificat/clé TLS
// auto-signée pour le moteur Xray via openssl, sous le répertoire de données
// du moteur — modèle identique à EnsureHysteriaCerts/EnsureTUICCerts. Utilisé
// quand l'opérateur choisit le mode TLS sans fournir ses propres chemins :
// le client devra alors activer allowInsecure (même contrainte que
// Hysteria/TUIC) ; un vrai certificat (ex. Let's Encrypt) reste nécessaire
// pour une chaîne de confiance publique.
func EnsureXrayCerts(dataDir string) (certPath, keyPath string, err error) {
	dir := filepath.Join(dataDir, "xray")
	if err := EnsureDir(dir); err != nil {
		return "", "", err
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}

	cmd := exec.Command("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt",
		"ec_paramgen_curve:prime256v1", "-keyout", keyPath, "-out", certPath,
		"-days", "3650", "-nodes", "-subj", "/C=FR/ST=LaboSurf/O=LaboSURF PRO/CN=labosurf.local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("openssl (génération certificat Xray) : %v\n%s", err, string(out))
	}
	return certPath, keyPath, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
