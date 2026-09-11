// Command labosurf-hysteria2 : binaire autonome du moteur Hysteria2 OFFICIEL.
// Même principe que labosurf-tuic/labosurf-xray : télécharge/déploie/
// supervise le binaire officiel `hysteria` (apernet/hysteria). Distinct de
// labosurf-hysteria (moteur maison, protocole non-officiel).
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/hysteria2"
)

func main() {
	e, err := engine.Get("hysteria2")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}
