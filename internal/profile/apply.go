package profile

import (
	"encoding/json"
	"fmt"
	"strconv"

	"labosurf/internal/store"
)

// BuildConfig construit le JSON de configuration à passer à engine.Configure()
// à partir des paramètres stockés dans un profil simple. La structure JSON
// retournée est identique à celle produite par les assistants de menu
// (menu_xray_config.go, menu_slowdns_config.go, etc.) — même format, même
// clés, afin que Configure() les traite sans modification.
//
// Les comptes actifs sont lus depuis le store uniquement pour les moteurs qui
// embarquent leurs secrets dans le JSON de config (xray, slowdns, dnstt,
// hysteria, ssh). Les moteurs sans utilisateurs embarqués (wireguard, tuic,
// udp) lisent eux-mêmes le store au démarrage.
//
// Un profil hybride ne passe JAMAIS par BuildConfig : son activation utilise
// engineutil.RegisterHybridPersist directement (voir menu_profiles.go).
func BuildConfig(p Profile, s *store.Store) ([]byte, error) {
	if p.Kind != KindSimple {
		return nil, fmt.Errorf("BuildConfig : profil %q n'est pas un profil simple", p.Name)
	}
	switch p.Engine {
	case store.EngineSlowDNS, store.EngineDNSTT:
		return buildDNSTunnelConfig(p, s)
	case store.EngineSSH:
		return buildSSHConfig(p, s)
	case store.EngineXray:
		return buildXrayConfig(p, s)
	case store.EngineHysteria:
		return buildHysteriaConfig(p, s)
	case store.EngineHysteria2:
		return buildHysteria2Config(p)
	case store.EngineTUIC:
		return buildTUICConfig(p, s)
	case store.EngineWireGuard:
		return buildWireguardConfig(p)
	case store.EngineUDP:
		return buildUDPConfig(p)
	default:
		return nil, fmt.Errorf("moteur %q : pas de BuildConfig implémenté", p.Engine)
	}
}

// helpers

func intParam(p Profile, key string, fallback int) int {
	if v := p.Param(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func boolParam(p Profile, key string, fallback bool) bool {
	if v := p.Param(key, ""); v != "" {
		switch v {
		case "true", "1", "yes", "o":
			return true
		case "false", "0", "no", "n":
			return false
		}
	}
	return fallback
}

func marshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// grantedAccountsFromStore retourne les comptes ayant un grant pour engineName.
func grantedAccountsFromStore(s *store.Store, engineName string) []store.Account {
	if s == nil {
		return nil
	}
	all := s.ListAccounts()
	var out []store.Account
	for _, a := range all {
		if a.HasEngine(engineName) {
			out = append(out, a)
		}
	}
	return out
}

// buildDNSTunnelConfig : slowdns / dnstt
func buildDNSTunnelConfig(p Profile, s *store.Store) ([]byte, error) {
	domain := p.Param("domain", "tunnel.example.com")
	port := intParam(p, "port", 53)
	backend := p.Param("backend", "127.0.0.1:22")
	jitter := intParam(p, "jitter_ms", 40)

	type dnsUser struct {
		User       string `json:"user"`
		PublicKey  string `json:"public_key"`
		PrivateKey string `json:"private_key"`
		Enabled    bool   `json:"enabled"`
	}
	var users []dnsUser
	for _, a := range grantedAccountsFromStore(s, p.Engine) {
		acc, err := s.EnsureEngineSecrets(a.ID, p.Engine)
		if err != nil {
			continue
		}
		pubKey, privKey := "", ""
		if acc.Grants != nil {
			if g := acc.Grants[p.Engine]; g != nil {
				pubKey, _ = g.Config["public_key"].(string)
				privKey, _ = g.Config["private_key"].(string)
			}
		}
		users = append(users, dnsUser{
			User: acc.ID, PublicKey: pubKey, PrivateKey: privKey, Enabled: true,
		})
	}
	cfg := map[string]any{
		"domain":    domain,
		"port":      port,
		"backend":   backend,
		"jitter_ms": jitter,
		"users":     users,
	}
	return marshalIndent(cfg)
}

// buildSSHConfig : ssh
func buildSSHConfig(p Profile, s *store.Store) ([]byte, error) {
	port := intParam(p, "port", 22)
	runAsUser := p.Param("run_as_user", "labosurf")

	type sshUser struct {
		Username  string `json:"username"`
		PublicKey string `json:"public_key"`
		Enabled   bool   `json:"enabled"`
	}
	var users []sshUser
	for _, a := range grantedAccountsFromStore(s, store.EngineSSH) {
		acc, err := s.EnsureEngineSecrets(a.ID, store.EngineSSH)
		if err != nil {
			continue
		}
		pubKey := ""
		if acc.Grants != nil {
			if g := acc.Grants[store.EngineSSH]; g != nil {
				pubKey, _ = g.Config["public_key"].(string)
			}
		}
		users = append(users, sshUser{Username: acc.ID, PublicKey: pubKey, Enabled: true})
	}
	cfg := map[string]any{
		"port":        port,
		"dir":         "/etc/labosurf/ssh",
		"run_as_user": runAsUser,
		"users":       users,
	}
	return marshalIndent(cfg)
}

// buildXrayConfig : xray
func buildXrayConfig(p Profile, s *store.Store) ([]byte, error) {
	port := intParam(p, "port", 443)
	network := p.Param("network", "tcp")
	security := p.Param("security", "tls")

	type xrayUser struct {
		ID   string `json:"id"`
		Flow string `json:"flow,omitempty"`
	}
	var users []xrayUser
	for _, a := range grantedAccountsFromStore(s, store.EngineXray) {
		acc, err := s.EnsureEngineSecrets(a.ID, store.EngineXray)
		if err != nil {
			continue
		}
		uuid := ""
		if acc.Grants != nil {
			if g := acc.Grants[store.EngineXray]; g != nil {
				uuid, _ = g.Config["uuid"].(string)
			}
		}
		if uuid != "" {
			users = append(users, xrayUser{ID: uuid})
		}
	}
	cfg := map[string]any{
		"port":     port,
		"network":  network,
		"security": security,
		"users":    users,
	}
	return marshalIndent(cfg)
}

// buildHysteriaConfig : hysteria (moteur maison, obfs XOR)
func buildHysteriaConfig(p Profile, s *store.Store) ([]byte, error) {
	port := intParam(p, "port", 8443)
	obfs := p.Param("obfs", "labosurf-sal4mander-obfs-secret")
	backend := p.Param("backend", "127.0.0.1:22")

	type hyUser struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Enabled  bool   `json:"enabled"`
	}
	var users []hyUser
	for _, a := range grantedAccountsFromStore(s, store.EngineHysteria) {
		acc, err := s.EnsureEngineSecrets(a.ID, store.EngineHysteria)
		if err != nil {
			continue
		}
		pw := ""
		if acc.Grants != nil {
			if g := acc.Grants[store.EngineHysteria]; g != nil {
				pw, _ = g.Config["password"].(string)
			}
		}
		if pw == "" {
			pw = acc.Password
		}
		users = append(users, hyUser{Name: acc.ID, Password: pw, Enabled: true})
	}
	cfg := map[string]any{
		"port":    port,
		"obfs":    obfs,
		"backend": backend,
		"users":   users,
	}
	return marshalIndent(cfg)
}

// buildHysteria2Config : hysteria2 (binaire officiel apernet/hysteria)
func buildHysteria2Config(p Profile) ([]byte, error) {
	port := intParam(p, "port", 443)
	cfg := map[string]any{
		"listen": fmt.Sprintf(":%d", port),
	}
	return marshalIndent(cfg)
}

// buildTUICConfig : tuic
func buildTUICConfig(p Profile, s *store.Store) ([]byte, error) {
	port := intParam(p, "port", 443)
	congestion := p.Param("congestion_control", "cubic")

	users := map[string]any{}
	for _, a := range grantedAccountsFromStore(s, store.EngineTUIC) {
		acc, err := s.EnsureEngineSecrets(a.ID, store.EngineTUIC)
		if err != nil {
			continue
		}
		uuid, pw := "", ""
		if acc.Grants != nil {
			if g := acc.Grants[store.EngineTUIC]; g != nil {
				uuid, _ = g.Config["uuid"].(string)
				pw, _ = g.Config["password"].(string)
			}
		}
		if pw == "" {
			pw = acc.Password
		}
		if uuid != "" {
			users[uuid] = pw
		}
	}
	cfg := map[string]any{
		"server":             fmt.Sprintf("[::]:%d", port),
		"users":              users,
		"congestion_control": congestion,
		"alpn":               []string{"h3"},
		"zero_rtt_handshake": false,
		"udp_relay_ipv6":     true,
	}
	return marshalIndent(cfg)
}

// buildWireguardConfig : wireguard
func buildWireguardConfig(p Profile) ([]byte, error) {
	port := intParam(p, "port", 51820)
	cfg := map[string]any{
		"listen_port": port,
	}
	return marshalIndent(cfg)
}

// buildUDPConfig : udp natif
func buildUDPConfig(p Profile) ([]byte, error) {
	port := intParam(p, "port", 5667)
	portalEnabled := boolParam(p, "portal_enabled", false)
	portalListen := p.Param("portal_listen", "127.0.0.1:8080")
	authMode := p.Param("auth_mode", "store")

	cfg := map[string]any{
		"listen": fmt.Sprintf(":%d", port),
		"store":  store.StorePath(),
		"portal": map[string]any{
			"enabled": portalEnabled,
			"listen":  portalListen,
		},
		"auth": map[string]any{
			"mode":  authMode,
			"users": map[string]any{},
		},
	}
	return marshalIndent(cfg)
}
