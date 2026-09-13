// Command labosurf-freewaygate : binaire autonome du moteur freeway-gate.
// Même principe que labosurf-tuic : télécharge/déploie/supervise le binaire
// freeway-gate (reverse proxy zero-rating multi-opérateur).
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/freewaygate"
)

func main() {
	e, err := engine.Get("freeway-gate")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}
