// Command labosurf-tuic : binaire autonome du moteur TUIC.
// Même principe que labosurf-xray : télécharge/déploie/supervise le binaire
// tuic-server officiel.
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/tuic"
)

func main() {
	e, err := engine.Get("tuic")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}
