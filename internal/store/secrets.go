package store

import (
	"fmt"

	"labosurf/internal/secret"
)

// EngineSecrets génère et persiste les secrets manquants d'un grant de compte
// pour le moteur donné. Cette fonction est idempotente : elle ne change que
// les champs encore vides du grant Config, et stocke dans users_db.json.
//
// Secrets créés par moteur :
//
//	xray / xray-*      -> {"uuid": "<uuid v4>"}
//	hysteria           -> {"password": "<secret>"}
//	dnstt / slowdns    -> {"public_key":"<hex>", "private_key":"<hex>"}
//	ssh                -> {"public_key":"<hex>", "private_key":"<hex>"}
//	tuic               -> {"uuid": "<uuid v4>", "password": "<secret>"}
//	wireguard          -> {"private_key":"<base64>", "public_key":"<base64>", "address":"10.66.0.N/32"}
func (s *Store) EnsureEngineSecrets(accountID, engine string) (Account, error) {
	acc, ok := s.GetAccount(accountID)
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	g := acc.Grants[engine]
	if g == nil {
		return Account{}, fmt.Errorf("le compte %s n'a pas accès au moteur %s", accountID, engine)
	}
	cfg := map[string]any{}
	if g.Config != nil {
		for k, v := range g.Config {
			cfg[k] = v
		}
	}

	changed := false
	switch engine {
	case EngineXray, "xray-slowdns", "xray-dnstt":
		if strVal(cfg["uuid"]) == "" {
			u, err := secret.UUID()
			if err != nil {
				return Account{}, err
			}
			cfg["uuid"] = u
			changed = true
		}
	case EngineHysteria:
		if strVal(cfg["password"]) == "" {
			tk, err := secret.RandToken(12)
			if err != nil {
				return Account{}, err
			}
			cfg["password"] = tk
			changed = true
		}
	case EngineTUIC:
		// TUIC authentifie par la paire UUID + mot de passe (voir schéma
		// officiel tuic-server dans AUDIT_TUIC_INTEGRATION.md §2) : les deux
		// secrets sont nécessaires, contrairement à xray (uuid seul) ou
		// hysteria (mot de passe seul).
		if strVal(cfg["uuid"]) == "" {
			u, err := secret.UUID()
			if err != nil {
				return Account{}, err
			}
			cfg["uuid"] = u
			changed = true
		}
		if strVal(cfg["password"]) == "" {
			tk, err := secret.RandToken(12)
			if err != nil {
				return Account{}, err
			}
			cfg["password"] = tk
			changed = true
		}
	case EngineDNSTT, EngineSlowDNS:
		if strVal(cfg["public_key"]) == "" || strVal(cfg["private_key"]) == "" {
			pub, priv, err := secret.Ed25519Keypair()
			if err != nil {
				return Account{}, err
			}
			cfg["public_key"] = pub
			cfg["private_key"] = priv
			changed = true
		}
	case EngineSSH:
		if strVal(cfg["public_key"]) == "" || strVal(cfg["private_key"]) == "" {
			pub, priv, err := secret.Ed25519Keypair()
			if err != nil {
				return Account{}, err
			}
			cfg["public_key"] = pub
			cfg["private_key"] = priv
			changed = true
		}
	case EngineWireGuard:
		// Modification volontaire et minimale d'EnsureEngineSecrets (mission
		// P2) : contrairement aux autres secrets ci-dessus (indépendants par
		// compte), l'adresse VPN WireGuard d'un compte DOIT être unique à
		// l'échelle de TOUS les comptes — deux peers avec la même adresse
		// casseraient le routage des deux. C'est un problème directement
		// bloquant pour que WireGuard soit fonctionnel (pas cosmétique),
		// justifiant cet ajout ciblé plutôt qu'un contournement fragile
		// ailleurs (ex: générée à la volée dans clientcfg sans persistance,
		// ce qui produirait une adresse DIFFÉRENTE à chaque régénération de
		// la config serveur).
		if strVal(cfg["private_key"]) == "" || strVal(cfg["public_key"]) == "" {
			priv, pub, err := secret.X25519Keypair()
			if err != nil {
				return Account{}, err
			}
			cfg["private_key"] = priv
			cfg["public_key"] = pub
			changed = true
		}
		if strVal(cfg["address"]) == "" {
			addr, err := s.nextWireGuardAddress()
			if err != nil {
				return Account{}, err
			}
			cfg["address"] = addr
			changed = true
		}
	}

	if !changed {
		return acc, nil
	}
	return s.SetGrantConfig(accountID, engine, cfg)
}

// wireguardAddressFormat est le patron d'adresse VPN allouée par
// nextWireGuardAddress ci-dessous : sous-réseau LABOSURF fixe 10.66.0.0/24,
// .1 réservée à l'interface serveur (voir internal/clientcfg/wireguard.go),
// .2 à .254 allouées aux comptes.
const wireguardAddressFormat = "10.66.0.%d/32"

// nextWireGuardAddress alloue la première adresse libre du pool VPN
// WireGuard LABOSURF, en inspectant les adresses déjà attribuées à TOUS les
// comptes (pas seulement celui en cours) pour garantir l'unicité — voir le
// commentaire du cas EngineWireGuard ci-dessus pour la justification de cet
// ajout à EnsureEngineSecrets.
func (s *Store) nextWireGuardAddress() (string, error) {
	used := map[int]bool{}
	for _, a := range s.ListAccounts() {
		g := a.Grants[EngineWireGuard]
		if g == nil || g.Config == nil {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strVal(g.Config["address"]), wireguardAddressFormat, &n); err == nil {
			used[n] = true
		}
	}
	for n := 2; n < 255; n++ {
		if !used[n] {
			return fmt.Sprintf(wireguardAddressFormat, n), nil
		}
	}
	return "", fmt.Errorf("pool d'adresses WireGuard épuisé (10.66.0.0/24, 253 comptes max)")
}

// grantSecret lit une chaîne dans le Config d'un grant.
func grantSecret(g *EngineGrant, key string) string {
	if g == nil || g.Config == nil {
		return ""
	}
	return strVal(g.Config[key])
}

// StrVal retourne v s'il s'agit d'une chaîne non vide, sinon "".
func StrVal(v any) string { return strVal(v) }

func strVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}