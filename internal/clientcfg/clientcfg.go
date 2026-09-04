// Package clientcfg génère, à partir d'un compte central et d'un moteur
// (parmi les grants du compte), la CONFIG SERVEUR et le LIEN CLIENT de
// connexion. C'est la traduction concrète de « un compte peut se connecter
// à plusieurs moteurs » : l'utilisateur choisit le moteur qu'il utilise, et
// reçoit la configuration de ce moteur.
package clientcfg

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// ClientResult regroupe le lien client et la config serveur générés.
type ClientResult struct {
	Engine     string
	ClientLink string

	// ServerConfig est la configuration serveur du moteur (JSON), à appliquer
	// via engine.Configure. Vide si non applicable.
	ServerConfig []byte
}

// GrantConfig retourne la config spécifique d'un grant (map) ou nil.
func GrantConfig(acc store.Account, engineName string) map[string]any {
	if acc.Grants == nil {
		return nil
	}
	if g := acc.Grants[engineName]; g != nil {
		return g.Config
	}
	return nil
}

// hostPort détermine l'hôte (IP/domaine) et le port d'un moteur.
func hostPort(engineName string, prof srvcfg.Profile) (host string, port int, err error) {
	if prof.Host == "" {
		return "", 0, fmt.Errorf("hôte du serveur non défini : configurez le profil serveur")
	}
	return prof.Host, prof.Port(engineName), nil
}

// Generate produit la config client + serveur pour un moteur choisi, en se
// basant sur le compte (store) et le profil serveur.
func Generate(acc store.Account, engineName string, prof srvcfg.Profile) (ClientResult, error) {
	if !acc.HasEngine(engineName) {
		return ClientResult{}, fmt.Errorf("le compte %s n'a pas accès au moteur %s", acc.ID, engineName)
	}

	host, port, err := hostPort(engineName, prof)
	if err != nil {
		return ClientResult{}, err
	}

	res := ClientResult{Engine: engineName}

	switch engineName {
	case store.EngineUDP:
		res.ClientLink = fmt.Sprintf("udp://%s@%s:%d?pass=%s",
			acc.Username, host, port, acc.Password)
		res.ServerConfig = udpServerConfig(acc, port)

	case store.EngineXray:
		uuid := grantString(acc, store.EngineXray, "uuid")
		res.ClientLink = vlessLink(uuid, host, port)
		res.ServerConfig = xrayServerConfig(acc, uuid)

	case store.EngineHysteria:
		pw := grantString(acc, store.EngineHysteria, "password")
		if pw == "" {
			pw = acc.Password
		}
		res.ClientLink = fmt.Sprintf("hysteria://%s@%s:%d", pw, host, port)
		res.ServerConfig = hysteriaServerConfig(acc, pw)

	case store.EngineSSH:
		res.ClientLink = fmt.Sprintf("ssh %s@%s -p %d", acc.Username, host, port)
		// La config serveur SSH est gérée via authorized_keys (hors JSON).

	case store.EngineSlowDNS, store.EngineDNSTT:
		key := grantString(acc, engineName, "public_key")
		domain := firstDomain(prof)
		res.ClientLink = fmt.Sprintf("%s://%s@%s?key=%s", engineName, acc.Username, domain, key)
		res.ServerConfig = dnsTunnelServerConfig(acc, engineName, domain)

	default:
		// Moteur hybride composé (nom contenant un tiret, ex: ssh-xray-slowdns).
		// Le lien client est généré à partir du VPN principal ; ssh reste une
		// brique supplémentaire dans la composition.
		if isHybridName(engineName) {
			if link, scooted := hybridClientLink(acc, engineName, host, port, prof); scooted {
				res.ClientLink = link
				res.ServerConfig = hybridServerConfig(acc, engineName, primaryVPN(engineName))
				return res, nil
			}
		}
		return ClientResult{}, fmt.Errorf("moteur %s : génération de config non supportée", engineName)
	}

	return res, nil
}

// grantString lit une valeur de chaîne dans le grant d'un compte.
func grantString(acc store.Account, engineName, key string) string {
	cfg := GrantConfig(acc, engineName)
	if cfg != nil {
		if v, ok := cfg[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// vlessLink compose une URI VLESS (Xray).
func vlessLink(uuid, host string, port int) string {
	if uuid == "" {
		uuid = "UUID-MANQUANT"
	}
	return fmt.Sprintf("vless://%s@%s:%d?encryption=none&type=tcp#LABOSURF", uuid, host, port)
}

func firstDomain(prof srvcfg.Profile) string {
	if len(prof.Domains) > 0 {
		return prof.Domains[0]
	}
	if prof.Host != "" {
		return prof.Host
	}
	return "domain-manquant"
}

// udpServerConfig produit le config.json du moteur UDP pour ce compte.
func udpServerConfig(acc store.Account, port int) []byte {
	s := map[string]any{
		"listen": ":" + strconv.Itoa(port),
		"auth": map[string]any{
			"mode":      "passwords",
			"storeFile": true,
		},
	}
	return marshal(s)
}

// xrayServerConfig produit la config serveur Xray (profil minimal).
func xrayServerConfig(acc store.Account, uuid string) []byte {
	s := map[string]any{
		"inbounds": []any{
			map[string]any{
				"port":     443,
				"protocol": "vless",
				"settings": map[string]any{
					"clients": []any{
						map[string]any{
							"id":      uuid,
							"email":   acc.ID + "@labosurf",
							"flow":    "xtls-rprx-vision",
							"enabled": acc.Enabled,
						},
					},
				},
			},
		},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	}
	return marshal(s)
}

// hysteriaServerConfig produit la config serveur Hysteria.
func hysteriaServerConfig(acc store.Account, password string) []byte {
	s := map[string]any{
		"listen": ":8443",
		"auth":   map[string]any{"type": "password", "password": password},
		"obfs":   "fuckGFW",
		"quic": map[string]any{
			"initStreamReceiveWindow": 8388608,
			"maxStreamReceiveWindow":  4194304,
		},
		"users": []any{map[string]any{"name": acc.ID}},
	}
	return marshal(s)
}

// dnsTunnelServerConfig produit une config minimale pour slowdns/dnstt.
func dnsTunnelServerConfig(acc store.Account, engineName, domain string) []byte {
	s := map[string]any{
		"domain": domain,
		"port":   53,
		"user":   acc.ID,
	}
	return marshal(s)
}

// hybridServerConfig combine la config des composants (transport tunnel + xray).
func hybridServerConfig(acc store.Account, engineName, uuid string) []byte {
	s := map[string]any{
		"name":      engineName,
		"transport": strings.TrimPrefix(engineName, "xray-"),
		"uuid":      uuid,
		"user":      acc.ID,
	}
	return marshal(s)
}

func marshal(v map[string]any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

// authorizedAccounts retourne tous les comptes ayant un grant actif vers le
// moteur donné. C'est ainsi qu'un moteur voit TOUS ses utilisateurs.
func authorizedAccounts(s *store.Store, engineName string) []store.Account {
	var out []store.Account
	for _, a := range s.ListAccounts() {
		if a.HasEngine(engineName) {
			out = append(out, a)
		}
	}
	return out
}

// isHybridName indique si le nom représente un moteur hybride composé
// (plusieurs composants séparés par des tirets).
func isHybridName(engineName string) bool {
	return strings.Contains(engineName, "-") && engine.Has(engineName)
}

// hybridComponents retourne la liste des moteurs composant un hybride.
func hybridComponents(engineName string) []string {
	e, err := engine.Get(engineName)
	if err != nil {
		return nil
	}
	if ce, ok := e.(*engineutil.CompositeEngine); ok {
		return ce.Components
	}
	return nil
}

// primaryVPN retourne le premier composant VPN d'un hybride, ou "".
func primaryVPN(engineName string) string {
	for _, c := range hybridComponents(engineName) {
		if engineutil.Role(c) == engineutil.RoleVPN {
			return c
		}
	}
	return ""
}

// hybridClientLink construit le lien client d'un hybride à partir de son VPN
// principal. Retourne (lien, true) si un VPN principal est trouvé.
func hybridClientLink(acc store.Account, engineName, host string, port int, prof srvcfg.Profile) (string, bool) {
	primary := primaryVPN(engineName)
	if primary == "" {
		return "", false
	}
	components := hybridComponents(engineName)

	// Le découpage du lien dépend du VPN principal (donne le format client).
	var link string
	switch primary {
	case store.EngineXray:
		uuid := grantString(acc, engineName, "uuid")
		link = vlessLink(uuid, host, port)
	case store.EngineHysteria:
		pw := grantString(acc, engineName, "password")
		if pw == "" {
			pw = acc.Password
		}
		link = fmt.Sprintf("hysteria://%s@%s:%d", pw, host, port)
	case store.EngineUDP:
		link = fmt.Sprintf("udp://%s@%s:%d?pass=%s", acc.Username, host, port, acc.Password)
	default:
		return "", false
	}
	_ = components
	return link, true
}

// ApplyServerConfig régénère et applique la configuration SERVEUR groupée d'un
// moteur : elle rassemble tous les comptes autorisés sur ce moteur (depuis le
// store central) et l'écrit via engine.Configure. Le moteur doit être installé.
func ApplyServerConfig(ctx context.Context, s *store.Store, engineName string, prof srvcfg.Profile) error {
	if prof.Host == "" {
		return fmt.Errorf("hôte du serveur non défini : configurez le profil serveur")
	}
	e, err := engine.Get(engineName)
	if err != nil {
		return err
	}
	// Garantit que chaque compte autorisé sur ce moteur porte ses secrets
	// (uuid, password, clés) — de manière idempotente dans users_db.json.
	for _, a := range s.ListAccounts() {
		if a.HasEngine(engineName) {
			_, _ = s.EnsureEngineSecrets(a.ID, engineName)
		}
	}
	accounts := authorizedAccounts(s, engineName)
	serverJSON := buildGroupedConfig(engineName, accounts, prof)
	return e.Configure(ctx, engine.EngineConfig{JSON: serverJSON})
}

// buildGroupedConfig construit la config serveur d'un moteur à partir de tous
// les comptes autorisés.
func buildGroupedConfig(engineName string, accounts []store.Account, prof srvcfg.Profile) []byte {
	switch engineName {
	case store.EngineXray:
		var clients []any
		for _, a := range accounts {
			uuid := grantString(a, engineName, "uuid")
			if uuid == "" {
				uuid = "uuid-" + a.ID
			}
			clients = append(clients, map[string]any{
				"id":      uuid,
				"email":   a.ID + "@labosurf",
				"flow":    "xtls-rprx-vision",
				"enabled": a.Enabled,
			})
		}
		return marshal(map[string]any{
			"inbounds": []any{
				map[string]any{
					"port":     443,
					"protocol": "vless",
					"settings": map[string]any{"clients": clients},
				},
			},
			"outbounds": []any{map[string]any{"protocol": "freedom"}},
		})

	case store.EngineHysteria:
		return hysteriaV2Config(accounts, prof)

	case store.EngineUDP:
		return marshal(map[string]any{
			"listen": ":" + strconv.Itoa(prof.Port(engineName)),
			"auth":   map[string]any{"mode": "passwords", "storeFile": true},
		})

	case store.EngineSlowDNS, store.EngineDNSTT:
		domain := firstDomain(prof)
		var users []any
		for _, a := range accounts {
			users = append(users, map[string]any{
				"user":        a.ID,
				"enabled":     a.Enabled,
				"public_key":  grantString(a, engineName, "public_key"),
				"private_key": grantString(a, engineName, "private_key"),
			})
		}
		return marshal(map[string]any{
			"engine": engineName,
			"domain": domain,
			"port":   53,
			"users":  users,
		})

	case store.EngineSSH:
		// Config serveur SSH regroupée : une clé publique par compte autorisé.
		var sshUsers []any
		for _, a := range accounts {
			key := grantString(a, engineName, "public_key")
			if key == "" {
				// Aucune clé déployée : on la génère et la stocke (idempotent).
				continue
			}
			sshUsers = append(sshUsers, map[string]any{
				"username":   a.Username,
				"public_key": key,
				"enabled":    a.Enabled,
			})
		}
		return marshal(map[string]any{
			"mode":  "authorized_keys",
			"port":  prof.Port(store.EngineSSH),
			"users": sshUsers,
		})

	default:
		// Moteur hybride composé : la config serveur suit le VPN principal.
		if isHybridName(engineName) {
			primary := primaryVPN(engineName)
			if primary != "" {
				return buildGroupedConfig(primary, accounts, prof)
			}
		}
		return marshal(map[string]any{"engine": engineName})
	}
}