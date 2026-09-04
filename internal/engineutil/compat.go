package engineutil

import "strings"

// EGRole décrit le rôle d'un moteur dans une composition hybride.
type EGRole int

const (
	RoleNone EGRole = iota
	// RoleTransport : tunnel sous-jacent (dnstt, slowdns) qui achemine le trafic.
	RoleTransport
	// RoleVPN : service VPN/proxy au-dessus du transport (xray, hysteria, udp).
	RoleVPN
	// RoleAccount : accès compte/shell (ssh), qui ne transporte pas d'autrui.
	RoleAccount
)

// Role retourne le rôle d'un moteur principal.
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

// RoleLabel est une description lisible d'un rôle.
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

// CompatibilityOk indique si une combinaison de moteurs est acceptable.
// Elle retourne true si aucun avertissement bloquant n'est détecté.
func CompatibilityOk(components []string) bool {
	return len(CompatibilityCheck(components)) == 0
}

// CompatibilityCheck renvoie une liste d'avertissements compréhensibles pour
// une combinaison libre de moteurs. L'utilisateur est libre de composer N
// moteurs ; ces messages l'aident à comprendre le rôle de chacun. Retourne
// une liste vide quand la combinaison est recommandée.
func CompatibilityCheck(components []string) []string {
	var warnings []string
	if len(components) < 2 {
		warnings = append(warnings, "un moteur hybride requiert au moins 2 moteurs.")
		return warnings
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
		case RoleNone:
			warnings = append(warnings, "'"+c+"' n'a pas de rôle connu pour la composition.")
		}
	}

	if transports > 1 {
		warnings = append(warnings,
			"plusieurs transports détectés ("+countList(components, RoleTransport)+") : indécis sur le tunnel à utiliser.")
	}
	if vpns > 1 {
		warnings = append(warnings,
			"plusieurs VPN détectés ("+countList(components, RoleVPN)+") : seul l'un d'eux guide la connexion.")
	}
	if vpns > 0 && transports == 0 {
		warnings = append(warnings, "aucun transport détecté : le trafic VPN ne sera pas acheminé dans un tunnel.")
	}
	if accounts > 0 {
		warnings = append(warnings,
			"'"+countList(components, RoleAccount)+"' est un accès compte (ssh) et ne sert pas de transport pour les autres moteurs.")
	}
	return warnings
}

// countList énumère les moteurs d'un rôle donné.
func countList(components []string, role EGRole) string {
	var names []string
	for _, c := range components {
		if Role(c) == role {
			names = append(names, c)
		}
	}
	return strings.Join(names, ", ")
}

// SuggestPrimaryRecommends propose un ordre de démarrage : le VPN au-dessus du
// transport. Retourne les composants réordonnés (VPN en tête, puis transport).
// Ne modifie pas l'ordre si indéterminable.
func SuggestPrimaryRecommends(components []string) []string {
	if len(components) < 2 || Role(components[0]) != RoleTransport {
		return components
	}
	// Le premier composant guide Le cycle de vie ; on veut le VPN en tête.
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