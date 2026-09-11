package clientcfg

import (
	"fmt"

	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// tuicUser est une paire (uuid, mot de passe) TUIC — le schéma officiel
// tuic-server authentifie par cette paire (voir AUDIT_TUIC_INTEGRATION.md
// §2 pour le schéma de configuration relevé à la source).
type tuicUser struct {
	uuid     string
	password string
}

// tuicLink compose un lien client tuic://, au format déjà utilisé par les
// clients TUIC courants (ex: sing-box, NekoBox) : uuid:password en
// userinfo, puis les paramètres de connexion QUIC/TLS en query string.
// allow_insecure=1 est nécessaire tant que le certificat serveur est
// auto-signé (voir engineutil.EnsureTUICCerts) plutôt qu'émis par une CA
// publique (ACME) pour un domaine réel.
func tuicLink(uuid, password, host string, port int) string {
	return fmt.Sprintf(
		"tuic://%s:%s@%s:%d?congestion_control=bbr&alpn=h3&sni=%s&allow_insecure=1&udp_relay_mode=native#LABOSURF",
		uuid, password, host, port, host,
	)
}

// tuicServerConfig produit un aperçu de config serveur TUIC pour UN compte
// (utilisé par Generate(), qui ne représente qu'un aperçu — voir le
// commentaire de ClientResult.ServerConfig). La configuration réellement
// appliquée au moteur passe par buildGroupedConfig -> tuicGroupedConfig,
// avec TOUS les comptes autorisés.
func tuicServerConfig(uuid, password string) []byte {
	return tuicGroupedConfig([]tuicUser{{uuid: uuid, password: password}}, srvcfg.Default())
}

// tuicGroupedConfig construit la configuration serveur tuic-server (schéma
// JSON officiel) avec tous les utilisateurs donnés et le port du profil
// serveur. Le certificat/clé TLS ne sont PAS injectés ici : ce n'est pas
// leur emplacement final tant que le moteur TUIC n'a pas été installé —
// engines/tuic.TUICEngine.Configure() les complète via
// engineutil.EnsureTUICCerts au moment où la config est réellement
// appliquée au moteur (même séparation de responsabilité que pour la clé
// REALITY d'xray, injectée par le moteur, pas par clientcfg).
func tuicGroupedConfig(users []tuicUser, prof srvcfg.Profile) []byte {
	port := prof.Port(store.EngineTUIC)

	userMap := map[string]any{}
	for _, u := range users {
		if u.uuid == "" || u.password == "" {
			continue
		}
		userMap[u.uuid] = u.password
	}

	return marshal(map[string]any{
		"server":             fmt.Sprintf("[::]:%d", port),
		"users":              userMap,
		"congestion_control": "cubic",
		"alpn":               []string{"h3"},
		// Recommandation explicite de l'auteur du serveur officiel :
		// vulnérable au replay, laissé désactivé (voir AUDIT_TUIC_INTEGRATION.md §2/§4).
		"zero_rtt_handshake": false,
		"udp_relay_ipv6":     true,
	})
}
