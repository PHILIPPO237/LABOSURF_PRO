// Chaînage générique de moteurs hybrides A -> B -> C -> ...
//
// Ce fichier formalise, SANS remplacer le mécanisme existant
// (CompositeEngine, pickTransportAndBackend, voir composite_engine.go), une
// représentation générique d'une composition ordonnée de moteurs :
// Components[0] est le composant le plus proche du client (l'entrée de la
// chaîne, "A"), Components[len-1] est le backend final ("le dernier
// composant"). C'est la même convention que celle déjà promise à
// l'utilisateur dans cmd/labosurf/menu.go (runHybridCreateMenu :
// "L'ordre de saisie définit l'ordre des composants").
//
// ComputeChainLinks/EvaluateChain répondent à la question "cette
// composition PEUT-ELLE être réellement relayée de bout en bout avec le
// mécanisme actuel ?" de façon purement déclarative (CanConnect, capacités
// statiques) — un pré-filtre consultatif, jamais une garantie d'exécution :
// seul CompositeEngine.Start() (via waitForEndpoint sur l'endpoint RÉEL)
// fait foi au moment de démarrer quoi que ce soit.
package engineutil

import (
	"labosurf/internal/srvcfg"
)

// ChainLink décrit une liaison ADJACENTE candidate entre deux composants
// consécutifs d'une chaîne A -> B -> C -> ..., dans l'ordre déclaré
// (Front = plus proche du client, Back = étape suivante). Wired indique si
// CanConnect() la juge structurellement possible ; Reason explique pourquoi,
// dans les deux cas (jamais vide si Wired est faux).
type ChainLink struct {
	Front  string
	Back   string
	Wired  bool
	Reason string
}

// ComputeChainLinks calcule, pour une composition ORDONNÉE (front -> back),
// chaque liaison adjacente candidate. Analyse purement statique (capacités
// déclarées) — voir le commentaire de CanConnect (compat.go) pour la limite
// de cette garantie.
func ComputeChainLinks(components []string) []ChainLink {
	if len(components) < 2 {
		return nil
	}
	links := make([]ChainLink, 0, len(components)-1)
	for i := 0; i+1 < len(components); i++ {
		front, back := components[i], components[i+1]
		ok, reason := CanConnect(front, back)
		links = append(links, ChainLink{Front: front, Back: back, Wired: ok, Reason: reason})
	}
	return links
}

// PortConflict signale que plusieurs composants d'une même composition
// écouteraient sur le même (réseau, port) d'après le profil serveur réel —
// jamais un port supposé ou codé en dur pour telle ou telle combinaison.
type PortConflict struct {
	Network    string
	Port       int
	Components []string
}

// DetectPortConflicts vérifie, à partir des ports RÉELLEMENT configurés
// dans le profil serveur (srvcfg.Profile — le même qui alimente
// engine.Configure() en production, via ApplyServerConfig), si plusieurs
// composants d'une composition écouteraient sur le même (réseau, port) du
// système. N'invente aucun port : lit le port effectif de chaque moteur
// (Profile.Port, qui retombe sur DefaultPorts() si non surchargé) et son
// réseau déclaré (EngineCapabilitiesMap.Network — qui reflète l'endpoint
// réel de chaque moteur, voir compat.go). Un moteur sans Network déclaré
// (aucun engine.Endpointer connu) est ignoré : on ne peut pas savoir avec
// quoi il entrerait en conflit.
func DetectPortConflicts(components []string, prof srvcfg.Profile) []PortConflict {
	type key struct {
		network string
		port    int
	}
	seen := map[key][]string{}
	var order []key

	for _, name := range components {
		cap, ok := GetEngineCapability(name)
		if !ok || cap.Network == "" {
			continue
		}
		port := prof.Port(name)
		if port <= 0 {
			continue
		}
		k := key{cap.Network, port}
		if _, exists := seen[k]; !exists {
			order = append(order, k)
		}
		seen[k] = append(seen[k], name)
	}

	var conflicts []PortConflict
	for _, k := range order {
		names := seen[k]
		if len(names) > 1 {
			conflicts = append(conflicts, PortConflict{Network: k.network, Port: k.port, Components: names})
		}
	}
	return conflicts
}

// ChainReport résume l'évaluation d'une composition candidate en chaîne
// A -> B -> C -> ...
type ChainReport struct {
	Components []string
	Links      []ChainLink

	// FullyChained est vrai si TOUTES les liaisons adjacentes sont câblables
	// avec le mécanisme actuel (chaîne entièrement relayée de bout en bout).
	// Faux ne signifie pas "composition interdite" : les composants peuvent
	// très bien démarrer en parallèle (comme aujourd'hui pour toute
	// composition sans transport), simplement sans relais réel entre eux —
	// voir CompatibilityCheck pour le guide d'avertissement existant, non
	// remplacé par ce rapport.
	FullyChained bool

	PortConflicts []PortConflict
}

// EvaluateChain évalue une composition ORDONNÉE candidate à devenir une
// chaîne A -> B -> C -> ... — sans rien démarrer, sans rien enregistrer, et
// sans modifier ValidateHybrid/CompatibilityCheck (qui restent le guide
// existant, utilisé tel quel par runHybridCreateMenu). C'est le point
// d'entrée du "moteur de compatibilité" étendu pour préparer l'évaluation
// des futures chaînes (TUIC+SSH+Xray, TUIC+DNSTT+Xray, Hysteria2+SSH+Xray,
// Hysteria2+DNSTT, UDP+Hysteria2, UDP+TUIC, UDP+Xray, ...) : il répond
// honnêtement à "cette composition peut-elle être réellement relayée de
// bout en bout avec le mécanisme actuel ?", jamais "ça a l'air de
// marcher" — aucune de ces compositions n'est déclarée fonctionnelle ici,
// seulement évaluée.
func EvaluateChain(components []string, prof srvcfg.Profile) ChainReport {
	links := ComputeChainLinks(components)
	full := len(links) > 0
	for _, l := range links {
		if !l.Wired {
			full = false
			break
		}
	}
	return ChainReport{
		Components:    components,
		Links:         links,
		FullyChained:  full,
		PortConflicts: DetectPortConflicts(components, prof),
	}
}
