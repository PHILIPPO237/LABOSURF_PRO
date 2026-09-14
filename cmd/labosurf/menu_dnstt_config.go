// Assistant interactif de configuration du moteur dnstt. Réutilise
// menuDNSTunnelConfig (défini dans menu_slowdns_config.go) avec le label
// et l'engineName propres à dnstt.
package main

import "labosurf/internal/engine"

// menuDNSTTConfig est atteint par l'option [2] CONFIGURER du sous-menu dnstt.
func menuDNSTTConfig(e engine.Engine) {
	menuDNSTunnelConfig(e, "dnstt", "DNSTT")
}
