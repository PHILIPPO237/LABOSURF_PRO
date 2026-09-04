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
	}

	if !changed {
		return acc, nil
	}
	return s.SetGrantConfig(accountID, engine, cfg)
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