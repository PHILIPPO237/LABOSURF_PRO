package engineutil

import (
	"errors"
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
}

var EngineCapabilitiesMap = map[string]EngineCapability{
	"udp": {
		Provides: []string{"udp-transport", "vpn-service"},
		Requires: []string{},
		Protocol: "labosurf-udp",
		Port:     5667,
	},
	"xray": {
		Provides: []string{"vpn-service", "tcp-proxy"},
		Requires: []string{"tcp-transport"},
		Protocol: "vless",
		Port:     443,
	},
	"hysteria": {
		Provides: []string{"vpn-service", "udp-relay"},
		Requires: []string{"udp-transport"},
		Protocol: "hysteria2",
		Port:     8443,
	},
	"slowdns": {
		Provides: []string{"dns-tunnel", "tcp-transport"},
		Requires: []string{},
		Protocol: "dns-tunnel",
		Port:     53,
	},
	"dnstt": {
		Provides: []string{"dns-tunnel", "tcp-transport"},
		Requires: []string{},
		Protocol: "dns-tunnel",
		Port:     53,
	},
	"ssh": {
		Provides: []string{"shell-access", "tcp-transport"},
		Requires: []string{},
		Protocol: "ssh",
		Port:     22,
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
	case "xray", "hysteria", "udp":
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
	ErrNoTransport       = errors.New("aucun transport disponible pour acheminer le trafic VPN")
	ErrMultipleTransports = errors.New("plusieurs transports en conflit")
	ErrMultipleVPNs      = errors.New("plusieurs VPN en conflit")
	ErrAccountAsTransport = errors.New("un moteur compte (ssh) ne peut pas servir de transport")
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