package engineutil

import (
	"errors"
	"fmt"
	"strings"
)

// EGRole décrit le rôle d'un moteur dans une composition hybride.
type EGRole int

const (
	RoleNone EGRole = iota
	RoleTransport
	RoleVPN
	RoleAccount
)

// EngineCapability décrit ce qu'un moteur fournit et ce qu'il requiert.
type EngineCapability struct {
	Provides []string
	Requires []string
	Protocol string
	Port     int

	// Network est le type de socket RÉELLEMENT lié par ce moteur une fois
	// démarré : "tcp" ou "udp". Ce n'est pas une aspiration ni une valeur
	// choisie ici — c'est la retranscription exacte de ce que chaque moteur
	// fait déjà dans son propre engines/<nom>/*.go (net.Listen("tcp", ...)
	// ou net.ListenUDP("udp", ...)) et rapporte via engine.Endpointer.
	// CompositeEngine revérifie TOUJOURS cette valeur contre l'endpoint réel
	// (waitForEndpoint) avant de câbler quoi que ce soit — cette déclaration
	// n'est qu'un pré-filtre statique, jamais une preuve suffisante à elle
	// seule. Vide pour un moteur qui n'implémente pas engine.Endpointer
	// (aujourd'hui : "udp", voir internal/engineudp — son wrapper
	// engineutil.SystemEngine ne l'implémente pas).
	Network string

	// RelaysTo déclare, si non vide, le type réseau ("tcp" aujourd'hui) vers
	// lequel ce moteur sait réellement relayer du trafic reçu lorsqu'il est
	// configuré avec le champ JSON générique "backend" (voir
	// injectBackend/pickTransportAndBackend dans composite_engine.go) —
	// mécanisme aujourd'hui implémenté UNIQUEMENT par dnstt et slowdns
	// (net.Dial("tcp", cfg.Backend), voir engines/dnstt/server.go et
	// engines/slowdns/server.go). Vide == moteur terminal : il consomme du
	// trafic mais n'en relaie jamais vers un composant suivant de la chaîne.
	RelaysTo string
}

var EngineCapabilitiesMap = map[string]EngineCapability{
	"udp": {
		Provides: []string{"udp-transport", "vpn-service"},
		Requires: []string{},
		Protocol: "labosurf-udp",
		Port:     5667,
		// Network "udp" décrit le protocole réel du serveur historique
		// (engines/udp/server.go: net.ListenUDP) — mais le moteur
		// RÉELLEMENT enregistré dans ce binaire (internal/engineudp,
		// wrapper engineutil.SystemEngine qui supervise ce serveur en
		// sous-processus) n'implémente PAS engine.Endpointer aujourd'hui :
		// aucun endpoint réel n'est donc actuellement exposable pour le
		// chaînage, quelle que soit cette déclaration statique — voir
		// ARCHITECTURE_HYBRIDES.md. RelaysTo vide : "udp" ne relaie rien.
		Network:  "udp",
		RelaysTo: "",
	},
	"xray": {
		Provides: []string{"vpn-service", "tcp-proxy"},
		Requires: []string{"tcp-transport"},
		Protocol: "vless",
		Port:     443,
		Network:  "tcp", // engines/xray/xray_binary.go: Endpoint{Network: "tcp", ...}
		RelaysTo: "",    // terminal : ne relaie jamais vers un composant suivant
	},
	"hysteria": {
		Provides: []string{"vpn-service", "udp-relay"},
		Requires: []string{"udp-transport"},
		Protocol: "hysteria2",
		Port:     8443,
		Network:  "udp", // engines/hysteria/engine.go: Endpoint{Network: "udp", ...}
		RelaysTo: "",
	},
	"tuic": {
		Provides: []string{"vpn-service", "quic-proxy"},
		// Même déclaration que "hysteria" et pour la même raison : tuic est
		// un VPN purement UDP/QUIC, il déclare avoir besoin d'un transport
		// UDP sous-jacent. Aucun moteur actuellement enregistré ne fournit
		// "udp-transport" (seul "udp" le fait, et son wrapper
		// n'implémente pas engine.Endpointer — voir le commentaire de
		// l'entrée "udp" ci-dessus) : ce n'est donc pas encore câblable en
		// hybride avec le mécanisme actuel, ce que CompatibilityCheck
		// signale honnêtement plutôt que de prétendre une compatibilité qui
		// n'existe pas. Voir AUDIT_TUIC_INTEGRATION.md §1.4/§4 — cette
		// entrée prépare seulement les informations pour un futur système
		// hybride, sans le construire (hors périmètre ici).
		Requires: []string{"udp-transport"},
		Protocol: "tuic",
		Port:     443,
		Network:  "udp", // engines/tuic/tuic_binary.go: Endpoint{Network: "udp", ...}
		RelaysTo: "",
	},
	"hysteria2": {
		Provides: []string{"vpn-service", "quic-proxy"},
		// Même déclaration que "tuic" et pour la même raison : hysteria2 est
		// un VPN purement UDP/QUIC. Distinct du moteur "hysteria" maison
		// (protocole non-officiel, voir engines/hysteria2/engine.go) — les
		// deux entrées coexistent volontairement dans cette carte, jamais
		// fusionnées.
		Requires: []string{"udp-transport"},
		Protocol: "hysteria2",
		// Port officiel recommandé : 443/UDP (tous les exemples de
		// configuration officiels Hysteria2 l'utilisent). Collision connue
		// et assumée avec "tuic" (également udp/443 dans ce dépôt) : les
		// deux ne peuvent pas être actifs simultanément sur le même hôte
		// sans reconfigurer l'un des deux ports via srvcfg.Profile.SetPort
		// — mécanisme déjà existant (DetectPortConflicts, chain.go), aucune
		// nouvelle logique nécessaire. Pas de collision avec "hysteria"
		// (moteur maison, port 8443 par défaut, resté inchangé).
		Port:     443,
		Network:  "udp", // engines/hysteria2 : bind-probe UDP réel, comme tuic
		RelaysTo: "",    // terminal : ne relaie jamais vers un composant suivant
	},
	"wireguard": {
		Provides: []string{"vpn-service"},
		Requires: []string{"udp-transport"},
		Protocol: "wireguard",
		// Port UDP conventionnel WireGuard (utilisé par la quasi-totalité
		// des tutoriels/outils officiels et tiers, y compris les scripts de
		// génération wg-quick) — pas de collision avec les autres moteurs
		// de ce dépôt (udp:5667, xray:tcp/443, hysteria:8443, tuic/hysteria2:
		// udp/443).
		Port:     51820,
		Network:  "udp", // engines/wireguard : bind-probe UDP réel après `wg-quick up`
		RelaysTo: "",    // terminal : ne relaie jamais vers un composant suivant — voir engines/wireguard/engine.go
	},
	"slowdns": {
		Provides: []string{"dns-tunnel", "tcp-transport"},
		Requires: []string{},
		Protocol: "dns-tunnel",
		Port:     53,
		// Network "udp" : c'est le protocole DNS lui-même que slowdns
		// écoute côté client (engines/slowdns/engine.go: Endpoint{Network:
		// "udp", ...}) — à ne pas confondre avec RelaysTo, le type de
		// backend qu'il sait joindre EN SORTIE (voir le champ RelaysTo).
		Network:  "udp",
		RelaysTo: "tcp", // net.Dial("tcp", cfg.Backend) — engines/slowdns/server.go
	},
	"dnstt": {
		Provides: []string{"dns-tunnel", "tcp-transport"},
		Requires: []string{},
		Protocol: "dns-tunnel",
		Port:     53,
		Network:  "udp", // engines/dnstt/engine.go: Endpoint{Network: "udp", ...}
		RelaysTo: "tcp", // net.Dial("tcp", cfg.Backend) — engines/dnstt/server.go
	},
	"ssh": {
		Provides: []string{"shell-access", "tcp-transport"},
		Requires: []string{},
		Protocol: "ssh",
		Port:     22,
		Network:  "tcp", // engines/ssh/engine.go: Endpoint{Network: "tcp", ...}
		RelaysTo: "",
	},
}

func GetEngineCapability(name string) (EngineCapability, bool) {
	cap, ok := EngineCapabilitiesMap[name]
	return cap, ok
}

func Role(engineName string) EGRole {
	switch engineName {
	case "slowdns", "dnstt":
		return RoleTransport
	case "xray", "hysteria", "udp", "tuic", "hysteria2", "wireguard":
		return RoleVPN
	case "ssh":
		return RoleAccount
	default:
		return RoleNone
	}
}

func (r EGRole) RoleLabel() string {
	switch r {
	case RoleTransport:
		return "transport (tunnel en dessous)"
	case RoleVPN:
		return "VPN/proxy (au-dessus du transport)"
	case RoleAccount:
		return "accès compte (ne transporte pas autrui)"
	default:
		return "rôle inconnu"
	}
}

var (
	ErrNoTransport         = errors.New("aucun transport disponible pour acheminer le trafic VPN")
	ErrMultipleTransports  = errors.New("plusieurs transports en conflit")
	ErrMultipleVPNs        = errors.New("plusieurs VPN en conflit")
	ErrAccountAsTransport  = errors.New("un moteur compte (ssh) ne peut pas servir de transport")
	ErrVPNWithoutTransport = errors.New("VPN sans transport sous-jacent")
)

func ValidateHybrid(components []string) error {
	if len(components) < 2 {
		return errors.New("un moteur hybride requiert au moins 2 moteurs")
	}

	var transports, vpns, accounts int
	hasUnknown := false

	for _, c := range components {
		switch Role(c) {
		case RoleTransport:
			transports++
		case RoleVPN:
			vpns++
		case RoleAccount:
			accounts++
		case RoleNone:
			hasUnknown = true
		}
	}

	if hasUnknown {
		return errors.New("moteur inconnu dans la composition")
	}

	if transports > 1 {
		return ErrMultipleTransports
	}
	if vpns > 1 {
		return ErrMultipleVPNs
	}

	return nil
}

func CompatibilityOk(components []string) bool {
	return ValidateHybrid(components) == nil
}

func CompatibilityCheck(components []string) []string {
	var warnings []string
	if err := ValidateHybrid(components); err != nil {
		warnings = append(warnings, err.Error())
	}

	var transports, vpns, accounts int
	for _, c := range components {
		switch Role(c) {
		case RoleTransport:
			transports++
		case RoleVPN:
			vpns++
		case RoleAccount:
			accounts++
		}
	}

	if vpns > 0 && transports == 0 {
		warnings = append(warnings, "aucun transport détecté : le trafic VPN ne sera pas acheminé dans un tunnel.")
	}
	if accounts > 0 && transports == 0 && vpns == 0 {
		warnings = append(warnings,
			"'"+countList(components, RoleAccount)+"' est un accès compte (ssh) et ne sert pas de transport pour les autres moteurs.")
	}
	if vpns > 0 && transports == 0 && accounts > 0 {
		warnings = append(warnings, "compte ssh présent mais aucun transport : le trafic SSH ne sera pas tunnelé.")
	}
	if accounts > 0 && vpns > 0 && transports == 0 {
		warnings = append(warnings, "'"+countList(components, RoleAccount)+"' ne sert pas de transport pour les autres moteurs.")
	}

	for _, name := range components {
		cap, ok := GetEngineCapability(name)
		if !ok {
			continue
		}
		for _, req := range cap.Requires {
			found := false
			for _, other := range components {
				if other == name {
					continue
				}
				otherCap, ok := GetEngineCapability(other)
				if !ok {
					continue
				}
				for _, prov := range otherCap.Provides {
					if prov == req {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				warnings = append(warnings, "moteur '"+name+"' requiert '"+req+"' non fourni par les autres composants")
			}
		}
	}

	return warnings
}

func countList(components []string, role EGRole) string {
	var names []string
	for _, c := range components {
		if Role(c) == role {
			names = append(names, c)
		}
	}
	return strings.Join(names, ", ")
}

func SuggestPrimaryRecommends(components []string) []string {
	if len(components) < 2 || Role(components[0]) != RoleTransport {
		return components
	}
	shuffled := make([]string, 0, len(components))
	for _, c := range components {
		if Role(c) == RoleVPN {
			shuffled = append(shuffled, c)
		}
	}
	for _, c := range components {
		if Role(c) != RoleVPN {
			shuffled = append(shuffled, c)
		}
	}
	return shuffled
}

// CanConnect indique, à partir des SEULES capacités déclarées (analyse
// statique, avant tout démarrage réel), si "front" peut structurellement
// relayer du trafic vers "back" en le configurant comme son backend
// (mécanisme générique "backend" JSON — voir CompositeEngine). C'est une
// condition NÉCESSAIRE mais jamais SUFFISANTE à elle seule : au démarrage
// réel, le chaînage revérifie toujours l'endpoint RÉELLEMENT exposé par
// "back" (engine.Endpointer) avant de câbler quoi que ce soit — jamais une
// adresse fictive, jamais une confiance aveugle en cette déclaration
// statique. CanConnect sert de pré-filtre : guide de compatibilité,
// évaluation de chaîne (EvaluateChain, chain.go), menu — jamais de
// condition dispersée du type `if name == "xray"`.
func CanConnect(front, back string) (bool, string) {
	fc, ok := GetEngineCapability(front)
	if !ok {
		return false, fmt.Sprintf("moteur inconnu : %s", front)
	}
	bc, ok := GetEngineCapability(back)
	if !ok {
		return false, fmt.Sprintf("moteur inconnu : %s", back)
	}
	if fc.RelaysTo == "" {
		return false, fmt.Sprintf("%s est un composant terminal : il ne relaie jamais de trafic vers un composant suivant", front)
	}
	if bc.Network == "" {
		return false, fmt.Sprintf("%s ne déclare pas le type réseau de son endpoint (aucun engine.Endpointer connu)", back)
	}
	if fc.RelaysTo != bc.Network {
		return false, fmt.Sprintf("%s ne relaie que vers un backend %s, mais %s expose un endpoint %s", front, fc.RelaysTo, back, bc.Network)
	}
	return true, ""
}
