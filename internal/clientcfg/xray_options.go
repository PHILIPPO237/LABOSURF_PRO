package clientcfg

import (
	"fmt"

	"labosurf/internal/store"
)

// XrayServerOptions regroupe les éléments de configuration serveur Xray saisis
// par l'opérateur. Ce sont les paramètres qui font réellement tourner
// l'inbound : adresse d'écoute, transport, sécurité, cible REALITY et
// comptes clients.
type XrayServerOptions struct {
	Port       int    // port d'écoute (défaut 443)
	Listen     string // adresse d'écoute : "0.0.0.0" (front public) ou "127.0.0.1" (derrière une chaîne/reverse proxy)
	Network    string // tcp | ws | grpc | kcp | xhttp
	Security   string // reality | tls | none
	ServerName string // SNI (REALITY/TLS) : domaine masqué / hébergé
	Dest       string // REALITY : cible de fallback (ex. www.microsoft.com:443)
	ShortID    string // REALITY shortId (hex ; vide = court conformément au schéma)
	Flow       string // XTLS flow (ex. xtls-rprx-vision)
	CertFile   string // TLS : chemin certificat
	KeyFile    string // TLS : chemin clé

	// XHTTP : paramètres du transport splithttp (mode auto|packet-up|stream-up|stream-one).
	XHTTPPath string
	XHTTPHost string
	XHTTPMode string

	// WS : chemin du transport WebSocket partagé avec le client.
	WSPath string
	// GRPC : nom de service du transport gRPC partagé avec le client.
	GRPCServiceName string
}

// validate vérifie la cohérence des options saisies.
func (o XrayServerOptions) validate() error {
	switch o.Port {
	case 0:
		return fmt.Errorf("port d'écoute invalide")
	}
	// Adresse d'écoute : front public (0.0.0.0) ou backend local
	// (127.0.0.1) quand l'inbound est derrière une chaîne (dnstt/slowdns)
	// ou un reverse proxy (freeway-gate) qui relaie vers lui.
	if o.Listen == "" {
		o.Listen = "0.0.0.0"
	}
	switch o.Listen {
	case "0.0.0.0", "::", "127.0.0.1", "localhost":
	default:
		return fmt.Errorf("adresse d'écoute invalide : %s (0.0.0.0, ::, 127.0.0.1, localhost)", o.Listen)
	}
	switch o.Network {
	case "tcp", "ws", "grpc", "kcp", "xhttp":
	default:
		return fmt.Errorf("type de transport inconnu : %s (tcp, ws, grpc, kcp, xhttp)", o.Network)
	}
	switch o.Security {
	case "reality", "tls", "none":
	default:
		return fmt.Errorf("sécurité inconnue : %s (reality, tls, none)", o.Security)
	}
	// REALITY n'est supporté que sur tcp, xhttp et grpc (schéma Xray-core).
	if o.Security == "reality" && (o.Network == "ws" || o.Network == "kcp") {
		return fmt.Errorf("REALITY non supporté sur %s (tcp, xhttp, grpc seulement)", o.Network)
	}
	// Flow XTLS : uniquement en REALITY + TCP (vision est basé sur TLS 1.3).
	if o.Flow != "" {
		switch o.Flow {
		case "xtls-rprx-vision", "xtls-rprx-direct":
		default:
			return fmt.Errorf("flow XTLS inconnu : %s (xtls-rprx-vision, xtls-rprx-direct, vide)", o.Flow)
		}
		if o.Security != "reality" || o.Network != "tcp" {
			return fmt.Errorf("flow XTLS réservé à REALITY + TCP (sécurité=%s, transport=%s)", o.Security, o.Network)
		}
	}
	if o.Network == "xhttp" {
		if o.XHTTPPath == "" {
			o.XHTTPPath = "/"
		}
		if o.XHTTPMode == "" {
			o.XHTTPMode = "auto"
		}
		switch o.XHTTPMode {
		case "auto", "packet-up", "stream-up", "stream-one":
		default:
			return fmt.Errorf("mode XHTTP inconnu : %s (auto, packet-up, stream-up, stream-one)", o.XHTTPMode)
		}
	}
	if o.Network == "ws" && o.WSPath == "" {
		o.WSPath = "/"
	}
	if o.Network == "grpc" && o.GRPCServiceName == "" {
		o.GRPCServiceName = "labosurf"
	}
	if o.Security == "reality" && o.ServerName == "" {
		return fmt.Errorf("SNI (serverNames) requis pour REALITY")
	}
	if o.Security == "tls" {
		if o.ServerName == "" {
			return fmt.Errorf("domaine (serverName) requis pour TLS")
		}
		if o.CertFile == "" || o.KeyFile == "" {
			return fmt.Errorf("certificat et clé requis pour TLS (certificateFile/keyFile)")
		}
	}
	return nil
}

// xrayNetworkSettings adapte le bloc réseau du stream selon le transport.
func xrayNetworkSettings(o XrayServerOptions) map[string]any {
	network := o.Network
	out := map[string]any{
		"network": network,
	}
	switch network {
	case "ws":
		path := o.WSPath
		if path == "" {
			path = "/"
		}
		out["wsSettings"] = map[string]any{"path": path, "headers": map[string]any{}}
	case "grpc":
		svc := o.GRPCServiceName
		if svc == "" {
			svc = "labosurf"
		}
		out["grpcSettings"] = map[string]any{"serviceName": svc}
	case "kcp":
		out["kcpSettings"] = map[string]any{
			"mtu":              1350,
			"tti":              50,
			"uplinkCapacity":   5,
			"downlinkCapacity": 20,
			"congestion":       false,
			"type":             "none",
		}
	case "xhttp":
		path := o.XHTTPPath
		if path == "" {
			path = "/"
		}
		mode := o.XHTTPMode
		if mode == "" {
			mode = "auto"
		}
		// XHTTP (splithttp) : mode auto accepte aussi bien packet-up que
		// stream-up côté serveur ; stream-one = flux bidirectionnel unique.
		out["xhttpSettings"] = map[string]any{
			"path": path,
			"host": o.XHTTPHost,
			"mode": mode,
		}
	}
	return out
}

// xraySecuritySettings adapte le bloc sécurité du stream selon la sécurité.
// REALITY injecte "privateKey": "" ici ; le moteur xray (Configure) la
// remplace par la vraie clé privée réellement générée à l'installation.
func xraySecuritySettings(security string, o XrayServerOptions) map[string]any {
	out := map[string]any{"security": security}
	switch security {
	case "reality":
		out["realitySettings"] = map[string]any{
			"show":        false,
			"dest":        o.Dest,
			"xver":        0,
			"serverNames": []string{o.ServerName},
			"privateKey":  "",
			"shortIds":    []string{o.ShortID},
		}
	case "tls":
		out["tlsSettings"] = map[string]any{
			"serverName": o.ServerName,
			"certificates": []any{
				map[string]any{
					"certificateFile": o.CertFile,
					"keyFile":         o.KeyFile,
				},
			},
		}
	}
	return out
}

// xrayClient décrit un client autorisé dans l'inbound Xray.
func xrayClient(a store.Account, engineName, flow string) map[string]any {
	uuid := grantString(a, engineName, "uuid")
	if uuid == "" {
		uuid = "uuid-" + a.ID
	}
	if flow == "" {
		flow = "xtls-rprx-vision"
	}
	return map[string]any{
		"id":      uuid,
		"email":   a.ID + "@labosurf",
		"flow":    flow,
		"enabled": a.Enabled,
	}
}

// BuildXrayServerConfig génère un config.json serveur Xray-core conforme au
// schéma officiel, paramétré par les options de l'opérateur et contenant tous
// les comptes autorisés vers le moteur (leurs UUID sont ceux du store central,
// rendus cohérents par l'appelant via EnsureEngineSecrets).
func BuildXrayServerConfig(o XrayServerOptions, accounts []store.Account) ([]byte, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if o.Listen == "" {
		o.Listen = "0.0.0.0"
	}

	// Flow XTLS : celui saisi par l'opérateur ; par défaut vision si
	// réaliste (REALITY + TCP), vide sinon.
	flow := o.Flow
	if flow == "" && o.Security == "reality" && o.Network == "tcp" {
		flow = "xtls-rprx-vision"
	}

	clients := make([]any, 0, len(accounts))
	for _, a := range accounts {
		clients = append(clients, xrayClient(a, store.EngineXray, flow))
	}

	inbound := map[string]any{
		"port":     o.Port,
		"listen":   o.Listen,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    clients,
			"decryption": "none",
			"fallbacks":  []any{},
		},
		"streamSettings": map[string]any{},
		"sniffing": map[string]any{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
		},
	}

	// Fusionne network puis security dans streamSettings.
	net := xrayNetworkSettings(o)
	for k, v := range net {
		inbound["streamSettings"].(map[string]any)[k] = v
	}
	sec := xraySecuritySettings(o.Security, o)
	for k, v := range sec {
		inbound["streamSettings"].(map[string]any)[k] = v
	}

	s := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			inbound,
		},
		"outbounds": []any{
			map[string]any{"protocol": "freedom", "tag": "direct"},
			map[string]any{"protocol": "blackhole", "tag": "block"},
		},
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules": []any{
				map[string]any{
					"type":        "field",
					"outboundTag": "block",
					"protocol":    []string{"bittorrent"},
				},
			},
		},
		"dns": map[string]any{
			"servers": []any{
				"https+local://8.8.8.8/dns-query",
				"https+local://1.1.1.1/dns-query",
				"localhost",
			},
		},
	}

	return marshal(s), nil
}

// XrayDefaultOptions fournit un jeu d'options par défaut cohérent.
func XrayDefaultOptions() XrayServerOptions {
	return XrayServerOptions{
		Port:            443,
		Listen:          "0.0.0.0",
		Network:         "tcp",
		Security:        "reality",
		ServerName:      "www.microsoft.com",
		Dest:            "www.microsoft.com:443",
		ShortID:         "",
		Flow:            "xtls-rprx-vision",
		XHTTPPath:       "/",
		XHTTPMode:       "auto",
		WSPath:          "/",
		GRPCServiceName: "labosurf",
	}
}

// XrayAccountUUID retourne l'UUID de compte pour xray, en le générant au
// besoin (repo idempotente EnsureEngineSecrets). Vide si le compte n'a pas
// de grant xray.
func XrayAccountUUID(s *store.Store, accountID string) string {
	acc, err := s.EnsureEngineSecrets(accountID, store.EngineXray)
	if err != nil {
		return ""
	}
	return grantString(acc, store.EngineXray, "uuid")
}
