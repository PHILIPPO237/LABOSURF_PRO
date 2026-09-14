package clientcfg

import (
	"fmt"
	"strings"

	"labosurf/internal/engineutil"
	"labosurf/internal/service"
	"labosurf/internal/store"
)

// GenerateFromAccess produit le lien/config CLIENT à partir d'un Access M2
// (service.Access + service.Service) sans toucher au système Grant existant.
//
// Préserve intégralement l'API Generate(acc store.Account, ...) — les deux
// fonctions coexistent le temps de la transition (M3/M4).
//
// username est utilisé dans les liens qui incorporent une identité client
// (ssh, dnstt, slowdns, hysteria2). Passez le nom de l'abonné ou son ID.
func GenerateFromAccess(acc service.Access, svc service.Service, username string) (ClientResult, error) {
	host := firstAccessDomain(svc)
	port := svc.ListenPort()
	if port == 0 {
		// Fallback vers le port par défaut du moteur si le service n'a pas
		// de port configuré (cohérent avec engineutil.EngineCapabilitiesMap).
		if caps, ok := engineutil.EngineCapabilitiesMap[svc.Engine]; ok {
			port = caps.Port
		}
	}
	if host == "" {
		return ClientResult{}, fmt.Errorf("hôte du service %q non défini", svc.ID)
	}

	if svc.IsHybrid() && len(svc.Components) > 0 {
		return accessHybridClientConfig(acc, svc.Components, username, host, port)
	}
	return accessSimpleClientConfig(acc, acc.Engine, username, host, port)
}

// accessSimpleClientConfig génère la config client pour un moteur simple.
func accessSimpleClientConfig(acc service.Access, eng, username, host string, port int) (ClientResult, error) {
	res := ClientResult{Engine: eng}

	switch eng {
	case store.EngineXray:
		uuid := accessStr(acc.Secrets, "uuid")
		link, err := vlessLink(uuid, host, port)
		if err != nil {
			return ClientResult{}, err
		}
		res.ClientLink = link

	case store.EngineHysteria:
		pw := accessStr(acc.Secrets, "password")
		if pw == "" {
			return ClientResult{}, fmt.Errorf("secret 'password' manquant pour hysteria (access %s)", acc.ID)
		}
		res.ClientLink = fmt.Sprintf("hysteria://%s@%s:%d", pw, host, port)

	case store.EngineHysteria2:
		pw := accessStr(acc.Secrets, "password")
		if pw == "" {
			return ClientResult{}, fmt.Errorf("secret 'password' manquant pour hysteria2 (access %s)", acc.ID)
		}
		link, err := hysteria2Link(username, pw, host, port)
		if err != nil {
			return ClientResult{}, err
		}
		res.ClientLink = link

	case store.EngineTUIC:
		uuid := accessStr(acc.Secrets, "uuid")
		pw := accessStr(acc.Secrets, "password")
		if uuid == "" {
			return ClientResult{}, fmt.Errorf("secret 'uuid' manquant pour tuic (access %s)", acc.ID)
		}
		if pw == "" {
			return ClientResult{}, fmt.Errorf("secret 'password' manquant pour tuic (access %s)", acc.ID)
		}
		res.ClientLink = tuicLink(uuid, pw, host, port)

	case store.EngineWireGuard:
		cfgText, err := wireguardClientConfigFromSecrets(acc.Secrets, host, port)
		if err != nil {
			return ClientResult{}, err
		}
		res.ClientLink = cfgText

	case store.EngineSSH:
		pubKey := accessStr(acc.Secrets, "public_key")
		if pubKey == "" {
			return ClientResult{}, fmt.Errorf("secret 'public_key' manquant pour ssh (access %s)", acc.ID)
		}
		// Le lien client SSH est la commande de connexion ; la clé publique
		// est fournie dans ServerConfig pour alimentation des authorized_keys.
		res.ClientLink = fmt.Sprintf("ssh %s@%s -p %d", username, host, port)
		res.ServerConfig = marshal(map[string]any{
			"mode":       "authorized_keys",
			"username":   username,
			"public_key": pubKey,
		})

	case store.EngineSlowDNS, store.EngineDNSTT:
		pubKey := accessStr(acc.Secrets, "public_key")
		if pubKey == "" {
			return ClientResult{}, fmt.Errorf("secret 'public_key' manquant pour %s (access %s)", eng, acc.ID)
		}
		res.ClientLink = fmt.Sprintf("%s://%s@%s?key=%s", eng, username, host, pubKey)

	case store.EngineUDP:
		pw := accessStr(acc.Secrets, "password")
		if pw == "" {
			return ClientResult{}, fmt.Errorf("secret 'password' manquant pour udp (access %s)", acc.ID)
		}
		res.ClientLink = fmt.Sprintf("udp://%s@%s:%d?pass=%s", username, host, port, pw)

	default:
		return ClientResult{}, fmt.Errorf("moteur %q : génération de config non supportée via Access", eng)
	}

	return res, nil
}

// accessHybridClientConfig génère le lien client d'un service hybride à
// partir de ses composants. Utilise la même règle que hybridClientLink :
// le lien est celui du composant VPN principal (RoleVPN).
func accessHybridClientConfig(acc service.Access, components []string, username, host string, port int) (ClientResult, error) {
	primary := accessPrimaryVPN(components)
	if primary == "" {
		return ClientResult{}, fmt.Errorf("aucun composant VPN dans %v", components)
	}
	res, err := accessSimpleClientConfig(acc, primary, username, host, port)
	if err != nil {
		return ClientResult{}, err
	}
	// Le nom du moteur dans le résultat est le nom hybride complet.
	res.Engine = strings.Join(components, "-")
	return res, nil
}

// accessPrimaryVPN retourne le premier composant de rôle VPN, ou "".
func accessPrimaryVPN(components []string) string {
	for _, c := range components {
		if engineutil.Role(c) == engineutil.RoleVPN {
			return c
		}
	}
	return ""
}

// firstAccessDomain retourne le premier domaine du service, ou son hôte IP.
func firstAccessDomain(svc service.Service) string {
	if len(svc.Domains) > 0 {
		return svc.Domains[0]
	}
	return svc.Host
}

// accessStr lit une valeur string depuis les secrets d'un Access.
func accessStr(secrets map[string]any, key string) string {
	if secrets == nil {
		return ""
	}
	if v, ok := secrets[key].(string); ok {
		return v
	}
	return ""
}

// wireguardClientConfigFromSecrets produit le fichier .conf CLIENT WireGuard
// (format wg-quick) depuis les secrets d'un Access — même format que
// wireguardClientConfig mais sans passer par store.Account.
func wireguardClientConfigFromSecrets(secrets map[string]any, host string, port int) (string, error) {
	privKey := accessStr(secrets, "private_key")
	addr := accessStr(secrets, "address")
	if privKey == "" || addr == "" {
		return "", fmt.Errorf("secrets WireGuard incomplets (private_key ou address manquant)")
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
