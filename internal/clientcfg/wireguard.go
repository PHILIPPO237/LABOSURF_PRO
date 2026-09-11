package clientcfg

import (
	"fmt"
	"strings"

	"labosurf/engines/wireguard"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// Ce fichier génère la configuration client/serveur du moteur WireGuard, au
// format INI standard (wg-quick) — voir man wg-quick / documentation
// officielle WireGuard pour le schéma [Interface]/[Peer]. Volontairement
// indépendant de tout autre moteur : WireGuard n'a ni le format Xray
// (JSON, inbounds VLESS) ni celui de TUIC/Hysteria2 — aucun de leurs
// helpers n'est réutilisé ici.

// wireguardServerAddress est l'adresse de l'interface SERVEUR dans le
// sous-réseau VPN LABOSURF (voir internal/store/secrets.go :
// wireguardAddressFormat — .1 réservée au serveur, .2+ aux comptes).
const wireguardServerAddress = "10.66.0.1/24"

// wireguardServerKeys retourne la paire de clés du SERVEUR (générée et
// persistée une fois par engines/wireguard.EnsureServerKeys — même
// principe que xray.LoadRealityKeys / hysteria2.EnsureObfsPassword, relu
// depuis clientcfg plutôt que régénéré).
func wireguardServerKeys() (privB64, pubB64 string, err error) {
	return wireguard.EnsureServerKeys(engineutil.DefaultDataDir)
}

// wireguardPeer regroupe les champs d'un compte nécessaires à un bloc
// [Peer] serveur.
type wireguardPeer struct {
	accountID string
	publicKey string
	address   string // "10.66.0.5/32"
}

func wireguardPeerFor(a store.Account) (wireguardPeer, bool) {
	pub := grantString(a, store.EngineWireGuard, "public_key")
	addr := grantString(a, store.EngineWireGuard, "address")
	if pub == "" || addr == "" {
		return wireguardPeer{}, false
	}
	return wireguardPeer{accountID: a.ID, publicKey: pub, address: addr}, true
}

// wireguardClientConfig produit le fichier .conf CLIENT complet (format
// standard WireGuard) pour un compte : la clé privée du COMPTE (jamais
// celle du serveur), la clé publique du serveur, son endpoint public réel
// (srvcfg.Profile.Host — jamais une adresse inventée, voir mission P2
// Étape 8/9), et les réseaux autorisés.
//
// Placé dans ClientResult.ClientLink (un simple string, réutilisé tel
// quel) plutôt qu'un nouveau champ dédié à ClientResult : WireGuard n'a pas
// de schéma URI aussi établi que vless/tuic/hysteria2 (son vrai artefact
// client standard EST ce fichier .conf, importé directement dans
// l'application WireGuard) — étendre ClientResult pour ce seul moteur
// aurait été une extension d'architecture non nécessaire ici.
func wireguardClientConfig(acc store.Account, host string, port int) (string, error) {
	privKey := grantString(acc, store.EngineWireGuard, "private_key")
	addr := grantString(acc, store.EngineWireGuard, "address")
	if privKey == "" || addr == "" {
		return "", fmt.Errorf("clé/adresse WireGuard du compte %s indisponible (secrets non générés ?)", acc.ID)
	}
	_, serverPub, err := wireguardServerKeys()
	if err != nil {
		return "", fmt.Errorf("clé publique serveur WireGuard indisponible (le moteur wireguard a-t-il été installé ?) : %w", err)
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", privKey)
	fmt.Fprintf(&b, "Address = %s\n", addr)
	b.WriteString("DNS = 1.1.1.1\n")
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", serverPub)
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	fmt.Fprintf(&b, "Endpoint = %s:%d\n", host, port)
	b.WriteString("PersistentKeepalive = 25\n")
	return b.String(), nil
}

// wireguardServerConfig produit un aperçu de config serveur pour UN compte
// (utilisé par Generate(), un aperçu — voir le commentaire de
// ClientResult.ServerConfig). La configuration réellement appliquée au
// moteur passe par buildGroupedConfig -> wireguardGroupedConfig, avec TOUS
// les comptes autorisés.
func wireguardServerConfig(acc store.Account, prof srvcfg.Profile) []byte {
	return wireguardGroupedConfig([]store.Account{acc}, prof)
}

// wireguardGroupedConfig construit la configuration SERVEUR (format
// standard wg-quick : [Interface] + un [Peer] par compte autorisé). La clé
// privée du SERVEUR n'est jamais générée ici : engines/wireguard.
// EnsureServerKeys (appelée par le moteur à Install()) est la source de
// vérité, relue ici — même séparation de responsabilité que pour la clé
// REALITY d'xray ou le certificat TUIC.
//
// Le forwarding/NAT internet (PostUp/PostDown) est laissé en COMMENTAIRE,
// jamais activé automatiquement : LABOSURF PRO ne modifie jamais le
// routage global de la machine sans action explicite de l'opérateur (voir
// mission P2 Étape 5, et ETUDE_PROTOCOLes_COMPATIBLES.md) — contrairement à
// engines/udp (moteur historique) qui active lui-même forwarding/NAT via
// iptables/nftables dans son propre domaine dédié, ce nouveau moteur reste
// délibérément plus conservateur : décommenter est un choix conscient de
// l'opérateur, pas un effet de bord de "configure".
func wireguardGroupedConfig(accounts []store.Account, prof srvcfg.Profile) []byte {
	port := prof.Port(store.EngineWireGuard)
	privKey, _, err := wireguardServerKeys()

	var b strings.Builder
	b.WriteString(wireguard.LabosurfMarker + "\n")
	b.WriteString("[Interface]\n")
	if err == nil {
		fmt.Fprintf(&b, "PrivateKey = %s\n", privKey)
	} else {
		b.WriteString("# PrivateKey indisponible : lancez 'install' avant d'appliquer cette configuration.\n")
	}
	fmt.Fprintf(&b, "Address = %s\n", wireguardServerAddress)
	fmt.Fprintf(&b, "ListenPort = %d\n", port)
	b.WriteString("\n")
	b.WriteString("# Forwarding/NAT internet désactivés par défaut — LABOSURF PRO ne modifie\n")
	b.WriteString("# jamais le routage global de la machine sans action explicite (voir\n")
	b.WriteString("# ETUDE_PROTOCOLes_COMPATIBLES.md). Pour autoriser les clients WireGuard à\n")
	b.WriteString("# sortir vers Internet, décommentez et adaptez l'interface WAN (ex: eth0) :\n")
	b.WriteString("# PostUp = sysctl -w net.ipv4.ip_forward=1; iptables -t nat -A POSTROUTING -s 10.66.0.0/24 -o eth0 -j MASQUERADE\n")
	b.WriteString("# PostDown = iptables -t nat -D POSTROUTING -s 10.66.0.0/24 -o eth0 -j MASQUERADE\n")

	for _, a := range accounts {
		peer, ok := wireguardPeerFor(a)
		if !ok {
			continue
		}
		b.WriteString("\n[Peer]\n")
		fmt.Fprintf(&b, "# compte: %s\n", peer.accountID)
		fmt.Fprintf(&b, "PublicKey = %s\n", peer.publicKey)
		fmt.Fprintf(&b, "AllowedIPs = %s\n", peer.address)
	}

	return []byte(b.String())
}
