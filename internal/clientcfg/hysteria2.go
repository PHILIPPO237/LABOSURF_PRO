package clientcfg

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"labosurf/engines/hysteria2"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// Ce fichier génère la configuration client/serveur du moteur Hysteria2
// OFFICIEL (engines/hysteria2), au format réellement attendu par le binaire
// `hysteria` officiel — voir v2.hysteria.network/docs/advanced/Full-Server-Config/
// pour le schéma serveur, et v2.hysteria.network/docs/developers/URI-Scheme/
// pour le format du lien client. Volontairement INDÉPENDANT de
// internal/clientcfg/hysteria.go (moteur "hysteria" maison, protocole
// différent) : aucune fonction, aucun champ de grant, aucun chemin de
// certificat n'est partagé entre les deux.

// hysteria2CertPaths retourne les chemins déterministes du certificat TLS
// auto-signé du moteur (voir engineutil.EnsureHysteria2Certs — même
// formule, calculée indépendamment ici plutôt que couplée à l'exécution du
// moteur, exactement comme internal/clientcfg/hysteria.go le fait déjà pour
// le moteur maison via tlsCertPath()/tlsKeyPath()).
func hysteria2CertPaths() (certPath, keyPath string) {
	base := engineutil.DefaultDataDir
	return base + "/hysteria2/cert.pem", base + "/hysteria2/key.pem"
}

// hysteria2Password retourne le mot de passe d'authentification "userpass"
// du compte (champ du grant "hysteria2", ou mot de passe du compte par
// défaut) — jamais celui du grant "hysteria" (moteur maison).
func hysteria2Password(a store.Account) string {
	if pw := grantString(a, store.EngineHysteria2, "password"); pw != "" {
		return pw
	}
	return a.Password
}

// hysteria2Link compose un lien client hysteria2://, au format officiel du
// schéma URI Hysteria2 (userinfo user:password, query obfs/obfs-password/
// sni/insecure). insecure=1 est nécessaire tant que le certificat serveur
// est auto-signé (EnsureHysteria2Certs) plutôt qu'émis par une CA publique
// (ACME) pour un domaine réel — même contrainte assumée que pour TUIC.
func hysteria2Link(username, password, host string, port int) (string, error) {
	obfsPW, err := hysteria2.EnsureObfsPassword(engineutil.DefaultDataDir)
	if err != nil {
		return "", fmt.Errorf("secret d'obfuscation Hysteria2 indisponible (le moteur hysteria2 a-t-il été installé ?) : %w", err)
	}

	u := url.URL{
		Scheme: "hysteria2",
		User:   url.UserPassword(username, password),
		Host:   fmt.Sprintf("%s:%d", host, port),
	}
	q := u.Query()
	q.Set("obfs", "salamander")
	q.Set("obfs-password", obfsPW)
	q.Set("sni", host)
	q.Set("insecure", "1")
	u.RawQuery = q.Encode()
	u.Fragment = "LABOSURF"
	return u.String(), nil
}

// hysteria2ServerConfig produit un aperçu de config serveur pour UN compte
// (utilisé par Generate(), un aperçu — voir le commentaire de
// ClientResult.ServerConfig). La configuration réellement appliquée au
// moteur passe par buildGroupedConfig -> hysteria2GroupedConfig, avec TOUS
// les comptes autorisés.
func hysteria2ServerConfig(acc store.Account, prof srvcfg.Profile) []byte {
	return hysteria2GroupedConfig([]store.Account{acc}, prof)
}

// hysteria2GroupedConfig construit la configuration serveur Hysteria2
// (YAML, schéma officiel) avec tous les comptes autorisés. Assemblage de
// chaînes plutôt qu'un encodeur YAML structuré — voir le commentaire
// détaillé de Hysteria2Engine.Configure (engines/hysteria2/hysteria2_binary.go)
// pour la justification (aucune dépendance YAML dans ce dépôt). Le
// certificat/clé sont des chemins déterministes déjà connus (générés par
// Install(), voir engineutil.EnsureHysteria2Certs) — pas besoin d'un
// mécanisme d'injection au moment de Configure(), contrairement à TUIC/Xray.
func hysteria2GroupedConfig(accounts []store.Account, prof srvcfg.Profile) []byte {
	port := prof.Port(store.EngineHysteria2)
	certPath, keyPath := hysteria2CertPaths()
	obfsPW, _ := hysteria2.EnsureObfsPassword(engineutil.DefaultDataDir)

	var lines []string
	lines = append(lines, "listen: :"+strconv.Itoa(port))
	lines = append(lines, "")
	lines = append(lines, "tls:")
	lines = append(lines, "  cert: "+certPath)
	lines = append(lines, "  key: "+keyPath)
	lines = append(lines, "")
	lines = append(lines, "auth:")
	lines = append(lines, "  type: userpass")
	lines = append(lines, "  userpass:")
	for _, a := range accounts {
		pw := hysteria2Password(a)
		if pw == "" {
			continue
		}
		lines = append(lines, "    "+yamlKey(a.ID)+": "+pw)
	}
	lines = append(lines, "")
	lines = append(lines, "obfs:")
	lines = append(lines, "  type: salamander")
	lines = append(lines, "  salamander:")
	lines = append(lines, "    password: "+obfsPW)
	lines = append(lines, "")
	lines = append(lines, "masquerade:")
	lines = append(lines, "  type: proxy")
	lines = append(lines, "  proxy:")
	lines = append(lines, "    url: https://www.bing.com")
	lines = append(lines, "    rewriteHost: true")

	return utf8(strings.Join(lines, "\n"))
}
