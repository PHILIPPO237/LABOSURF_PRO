package wireguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"labosurf/internal/secret"
)

// serverKeyPath retourne le chemin déterministe de la clé privée du
// SERVEUR WireGuard (secret PAR SERVEUR, pas par compte — comme la clé
// privée REALITY d'xray ou le secret d'obfuscation Salamander d'hysteria2),
// sous le répertoire de données du moteur. Jamais commité, jamais loggée.
func serverKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "wireguard", "server_private.key")
}

// EnsureServerKeys génère (si absente) puis retourne la paire de clés du
// SERVEUR WireGuard — générée une seule fois et persistée, jamais
// régénérée silencieusement (une régénération invaliderait tous les
// fichiers clients déjà distribués, dont la clé "PublicKey" du bloc [Peer]
// désigne précisément cette clé publique serveur). Même principe que
// xray.EnsureRealityKeys / hysteria2.EnsureObfsPassword.
//
// Exportée : internal/clientcfg/wireguard.go l'appelle pour construire à la
// fois la configuration serveur ET les fichiers clients avec la même paire.
func EnsureServerKeys(dataDir string) (privBase64, pubBase64 string, err error) {
	path := serverKeyPath(dataDir)
	if data, err := os.ReadFile(path); err == nil {
		if priv := strings.TrimSpace(string(data)); priv != "" {
			pub, derr := secret.X25519PublicFromPrivate(priv)
			if derr != nil {
				return "", "", fmt.Errorf("clé privée serveur WireGuard corrompue (%s) : %w", path, derr)
			}
			return priv, pub, nil
		}
	}

	priv, pub, err := secret.X25519Keypair()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	// 0600 : ce fichier contient une clé privée — jamais lisible par
	// d'autres utilisateurs du système (voir mission P2, Étape 15).
	if err := os.WriteFile(path, []byte(priv), 0o600); err != nil {
		return "", "", err
	}
	return priv, pub, nil
}
