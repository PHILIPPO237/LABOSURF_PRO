package clientcfg

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// SSHUser décrit un compte autorisé au SSH (username + clé publique).
type SSHUser struct {
	Username  string
	PublicKey string
}

// SSHAuthorizedKeys génère le contenu authorized_keys pour les utilisateurs
// donnés (accédant via l'utilisateur commun `labosurf`). Chaque ligne porte
// la clé publique Ed25519 (brute) + commentaire du compte.
func SSHAuthorizedKeys(users []SSHUser) string {
	var lines []string
	for _, u := range users {
		if u.PublicKey == "" {
			continue
		}
		lines = append(lines, "ssh-ed25519 "+hexKeyToB64(u.PublicKey)+" "+u.Username+"@labosurf")
	}
	return strings.Join(lines, "\n")
}

// hexKeyToB64 convertit la clé publique brute (hex) en base64 pour la ligne
// authorized_keys OpenSSH (ssh-ed25519 <base64 du blob>).
func hexKeyToB64(hexKey string) string {
	b, err := hex.DecodeString(hexKey)
	if err != nil {
		return hexKey
	}
	return base64.StdEncoding.EncodeToString(b)
}

// SSHDConfig génère une config sshd minimale et verrouillée pour la
// plateforme LABOSURF : clés Ed25519 uniquement, pas de mot de passe.
func SSHDConfig(port int) string {
	return "Port " + str(port) + "\n" +
		"PermitRootLogin no\n" +
		"PasswordAuthentication no\n" +
		"KbdInteractiveAuthentication no\n" +
		"ChallengeResponseAuthentication no\n" +
		"PubkeyAuthentication yes\n" +
		"AuthorizedKeysFile " + dataDirPath("ssh/authorized_keys") + "\n" +
		"PermitEmptyPasswords no\n" +
		"AllowUsers labosurf\n"
}

// SSHDefaultPort est le port SSH par défaut de la plateforme, aligné sur le
// profil serveur (srvcfg "ssh" = 22). Utilisé si la config ne précise pas de
// port (ou est invoquée hors profil).
func SSHDefaultPort() int { return 22 }

// sshdConfigPath est conservé pour compatibilité (chemin sshd_config).
func sshdConfigPath() string { return dataDirPath("ssh/sshd_config") }

func str(i int) string { return fmt.Sprintf("%d", i) }

// (garantie de compilation)
var _ = sshdConfigPath