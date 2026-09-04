package clientcfg

import (
	"os"
	"strconv"
	"strings"

	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// hysteriaV2Config génère la configuration serveur Hysteria v2 (YAML) groupée
// pour tous les comptes autorisés. La config utilise :
//	- listen sur le port hysteria du profil serveur ;
//	- authentification par mots de passe (un par compte) ;
//	- obfuscation salamander (recommandée en bypass) ;
//	- masquerade HTTP (le serveur se fait passer pour un site HTTPS) ;
//	- TLS auto-signé (cert/key) — donc le client doit utiliser insecure.
func hysteriaV2Config(accounts []store.Account, prof srvcfg.Profile) []byte {
	port := prof.Port(store.EngineHysteria)
	var lines []string

	lines = append(lines, "listen: " + strconv.Itoa(port))
	lines = append(lines, "")
	lines = append(lines, "tls:")
	lines = append(lines, "  cert: " + tlsCertPath())
	lines = append(lines, "  key: " + tlsKeyPath())
	lines = append(lines, "")
	lines = append(lines, "auth:")
	lines = append(lines, "  type: userpass")
	lines = append(lines, "  userpass:")
	for _, a := range accounts {
		lines = append(lines, "    " + yamlKey(a.ID) + ": " + hysteriaPassword(a, "password"))
	}
	lines = append(lines, "")
	lines = append(lines, "obfs:")
	lines = append(lines, "  type: salamander")
	lines = append(lines, "  salamander:")
	lines = append(lines, "    password: " + obfsPassword())
	lines = append(lines, "")
	lines = append(lines, "masquerade:")
	lines = append(lines, "  type: proxy")
	lines = append(lines, "  proxy:")
	lines = append(lines, "    url: https://www.bing.com")
	lines = append(lines, "    rewriteHost: true")

	return utf8(strings.Join(lines, "\n"))
}

// utf8 convertit une chaîne en sa représentation octets UTF-8.
func utf8(s string) []byte {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		b[i] = s[i]
	}
	return b
}

// hysteriaPassword retourne le mot de passe du compte (champ du grant ou
// mot de passe du compte par défaut).
func hysteriaPassword(a store.Account, key string) string {
	pw := grantString(a, store.EngineHysteria, key)
	if pw == "" {
		pw = grantString(a, store.EngineHysteria, "password")
	}
	if pw == "" {
		pw = a.Password
	}
	return pw
}

// yamlKey quotote une clé YAML si nécessaire.
func yamlKey(k string) string {
	if strings.ContainsAny(k, ".:{}[],&*!|>'\"%@`") || strings.HasPrefix(k, "- ") {
		return "'" + k + "'"
	}
	return k
}

// tlsCertPath / tlsKeyPath : chemins des certificats TLS auto-signés du moteur
// hysteria, sous le répertoire de données LABOSURF.
func tlsCertPath() string { return dataDirPath("hysteria/cert.pem") }
func tlsKeyPath() string  { return dataDirPath("hysteria/key.pem") }

// obfsPassword est un secret d'obfuscation salamander stable du serveur.
func obfsPassword() string { return "labosurf-sal4mander-obfs-secret" }

// dataDirPath combine le répertoire de données (LABOSURF_DATA_DIR ou
// /etc/labosurf) avec rel.
func dataDirPath(rel string) string {
	base := strings.TrimSpace(os.Getenv("LABOSURF_DATA_DIR"))
	if base == "" {
		base = "/etc/labosurf"
	}
	if strings.HasSuffix(base, "/") {
		return base + rel
	}
	return base + "/" + rel
}