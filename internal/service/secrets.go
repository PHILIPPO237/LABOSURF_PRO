package service

import (
	"fmt"
	"strings"

	"labosurf/internal/secret"
)

// EnsureAccessSecrets génère idempotent les secrets manquants d'un Access.
// Ne remplace jamais un secret déjà valide.
//
// Pour les moteurs hybrides (ex: "dnstt-xray"), les secrets de CHAQUE composant
// sont générés dans le même Access.Secrets (map plate), comme dans les Grants.
//
// extraUsedWGOctets : octets (2–254) déjà attribués par les anciens Grants
// (transition M3). Passez nil si non applicable.
//
// Secrets générés par moteur :
//
//	xray          -> {"uuid": "<uuid v4>"}
//	hysteria      -> {"password": "<token>"}
//	hysteria2     -> {"password": "<token>"}
//	tuic          -> {"uuid": "<uuid v4>", "password": "<token>"}
//	dnstt/slowdns -> {"public_key": "<hex>", "private_key": "<hex>"}
//	ssh           -> {"public_key": "<hex>", "private_key": "<hex>"}
//	wireguard     -> {"private_key": "<base64>", "public_key": "<base64>", "address": "10.66.0.N/32"}
//	udp           -> {"password": "<token>"}
func EnsureAccessSecrets(a *Access, extraUsedWGOctets map[int]bool) error {
	if a.Secrets == nil {
		a.Secrets = make(map[string]any)
	}
	for _, comp := range splitEngineComponents(a.Engine) {
		if err := ensureComponentSecrets(comp, a.Secrets, a.ID, extraUsedWGOctets); err != nil {
			return fmt.Errorf("EnsureAccessSecrets [%s → %s] : %w", a.Engine, comp, err)
		}
	}
	return nil
}

// splitEngineComponents décompose un nom de moteur (potentiellement hybride)
// en ses composants. Aucun nom de moteur simple connu ne contient de tiret
// (udp, xray, ssh, dnstt, slowdns, tuic, wireguard, hysteria, hysteria2,
// freewaygate) : le split sur "-" est donc correct.
func splitEngineComponents(engineName string) []string {
	if !strings.Contains(engineName, "-") {
		return []string{engineName}
	}
	parts := strings.Split(engineName, "-")
	for _, p := range parts {
		if !isKnownSimpleEngine(p) {
			return []string{engineName}
		}
	}
	return parts
}

func isKnownSimpleEngine(name string) bool {
	switch name {
	case "udp", "xray", "ssh", "dnstt", "slowdns", "tuic",
		"wireguard", "hysteria", "hysteria2", "freewaygate":
		return true
	}
	return false
}

// ensureComponentSecrets remplit les secrets manquants pour UN composant.
func ensureComponentSecrets(eng string, secrets map[string]any, accessID string, extraUsedWG map[int]bool) error {
	switch eng {
	case "xray":
		if secStr(secrets["uuid"]) == "" {
			u, err := secret.UUID()
			if err != nil {
				return fmt.Errorf("génération UUID xray : %w", err)
			}
			secrets["uuid"] = u
		}

	case "hysteria", "hysteria2":
		if secStr(secrets["password"]) == "" {
			tk, err := secret.RandToken(12)
			if err != nil {
				return fmt.Errorf("génération mot de passe %s : %w", eng, err)
			}
			secrets["password"] = tk
		}

	case "tuic":
		if secStr(secrets["uuid"]) == "" {
			u, err := secret.UUID()
			if err != nil {
				return fmt.Errorf("génération UUID tuic : %w", err)
			}
			secrets["uuid"] = u
		}
		if secStr(secrets["password"]) == "" {
			tk, err := secret.RandToken(12)
			if err != nil {
				return fmt.Errorf("génération mot de passe tuic : %w", err)
			}
			secrets["password"] = tk
		}

	case "dnstt", "slowdns", "ssh":
		// Les trois utilisent des paires Ed25519 (mêmes noms de champs).
		// Pour un hybride dnstt-ssh, une seule paire est partagée (même
		// convention que les anciens Grants : aliasGrantForComponent).
		if secStr(secrets["public_key"]) == "" || secStr(secrets["private_key"]) == "" {
			pub, priv, err := secret.Ed25519Keypair()
			if err != nil {
				return fmt.Errorf("génération clés Ed25519 %s : %w", eng, err)
			}
			secrets["public_key"] = pub
			secrets["private_key"] = priv
		}

	case "wireguard":
		if secStr(secrets["private_key"]) == "" || secStr(secrets["public_key"]) == "" {
			priv, pub, err := secret.X25519Keypair()
			if err != nil {
				return fmt.Errorf("génération clés WireGuard : %w", err)
			}
			secrets["private_key"] = priv
			secrets["public_key"] = pub
		}
		if secStr(secrets["address"]) == "" {
			addr, err := nextAccessWireGuardAddress(accessID, extraUsedWG)
			if err != nil {
				return err
			}
			secrets["address"] = addr
		}

	case "udp":
		if secStr(secrets["password"]) == "" {
			tk, err := secret.RandToken(12)
			if err != nil {
				return fmt.Errorf("génération mot de passe UDP : %w", err)
			}
			secrets["password"] = tk
		}

	// "freewaygate" et moteurs inconnus : pas de secrets gérés ici.
	}
	return nil
}

const wireguardAddrFormat = "10.66.0.%d/32"

// nextAccessWireGuardAddress alloue la première adresse libre du pool VPN
// WireGuard LABOSURF (10.66.0.2–254) en examinant :
//  1. les adresses déjà attribuées dans les fichiers Access existants ;
//  2. les adresses des anciens Grants (extraUsed, pour la transition M3).
//
// currentAccessID est exclu du scan (idempotence : un Access en cours de
// provisionnement ne doit pas bloquer sur sa propre adresse).
func nextAccessWireGuardAddress(currentAccessID string, extraUsed map[int]bool) (string, error) {
	used := make(map[int]bool)
	for k := range extraUsed {
		used[k] = true
	}

	all, err := listAllAccess()
	if err != nil {
		return "", fmt.Errorf("lecture des accès WireGuard existants : %w", err)
	}
	for _, a := range all {
		if a.ID == currentAccessID {
			continue
		}
		var n int
		if _, scanErr := fmt.Sscanf(secStr(a.Secrets["address"]), wireguardAddrFormat, &n); scanErr == nil {
			used[n] = true
		}
	}

	for n := 2; n < 255; n++ {
		if !used[n] {
			return fmt.Sprintf(wireguardAddrFormat, n), nil
		}
	}
	return "", fmt.Errorf("pool d'adresses WireGuard épuisé (10.66.0.0/24 : 253 accès max)")
}

// secStr extrait une valeur string depuis un secret map.
func secStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
