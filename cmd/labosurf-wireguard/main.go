// Command labosurf-wireguard : binaire autonome du moteur WireGuard.
// Contrairement à labosurf-tuic/labosurf-xray/labosurf-hysteria2, ce moteur
// ne supervise aucun binaire tiers téléchargé : il pilote les outils
// système wg/wg-quick (module noyau) — voir engines/wireguard/engine.go
// pour la justification complète de ce choix.
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/wireguard"
)

func main() {
	e, err := engine.Get("wireguard")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}
