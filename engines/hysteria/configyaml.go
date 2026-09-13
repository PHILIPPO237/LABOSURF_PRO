package hysteria

import (
	"fmt"
	"strconv"
	"strings"
)

// parseConfigYAML tolère la config YAML émise par la plateforme
// (internal/clientcfg → hysteriaV2Config : listen / tls / auth.userpass /
// obfs.salamander / masquerade). Le moteur maison ne lisait que du JSON à
// l'origine ; sans ce pas de secours, l'application de la configuration
// groupée (buildGroupedConfig → EngineHysteria) émettait du YAML que
// json.Unmarshal(Configure) refusait, et le moteur ne pouvait jamais
// démarrer depuis le menu. Seules les clés utiles au moteur (port, users,
// obfs) sont extraites ; tls/masquerade restent sans effet (protocole
// maison, pas de TLS).
//
// Le parseur ne couvre QUE la forme régulière émise par hysteriaV2Config
// (indentation 2 espaces), et renvoie une erreur explicite sur toute forme
// inattendue — jamais de lecture partielle silencieuse.
func parseConfigYAML(raw []byte) (HysteriaConfig, error) {
	var cfg HysteriaConfig
	inUserpass := false
	userpassIndent := -1
	salamanderIndent := -1

	lines := strings.Split(string(raw), "\n")
	for i, ln := range lines {
		trim := strings.TrimSpace(ln)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := len(ln) - len(strings.TrimLeft(ln, " "))
		idx := strings.Index(trim, ":")
		if idx < 0 {
			return HysteriaConfig{}, fmt.Errorf("ligne %d : nœud YAML sans ':' : %q", i+1, ln)
		}
		key := strings.TrimSpace(trim[:idx])
		val := strings.Trim(strings.TrimSpace(trim[idx+1:]), `"'`)

		switch {
		case indent == 0 && key == "listen":
			p := strings.TrimPrefix(val, ":")
			port, err := strconv.Atoi(p)
			if err != nil || port <= 0 || port > 65535 {
				return HysteriaConfig{}, fmt.Errorf("ligne %d : listen invalide : %q", i+1, val)
			}
			cfg.Port = port
		case key == "userpass":
			inUserpass = true
			userpassIndent = indent
		case key == "salamander":
			salamanderIndent = indent
		case key == "password" && salamanderIndent >= 0 && indent == salamanderIndent+2 && val != "":
			cfg.Obfs = val
		case inUserpass && indent == userpassIndent+2 && key != "type" && val != "":
			cfg.Users = append(cfg.Users, HysteriaUser{Name: key, Password: val, Enabled: true})
		case inUserpass && indent <= userpassIndent:
			inUserpass = false
		}
	}

	if cfg.Port == 0 {
		cfg.Port = defaultHysteriaPort
	}
	return cfg, nil
}