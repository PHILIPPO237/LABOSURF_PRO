// Command labosurf-xray : binaire autonome du moteur Xray.
// Même principe que le binaire labosurf (UDP) : télécharge/déploie/supervise
// le binaire Xray-core officiel.
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/xray"
)

func main() {
	e, err := engine.Get("xray")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}