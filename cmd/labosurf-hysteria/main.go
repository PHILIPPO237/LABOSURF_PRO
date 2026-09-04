// Command labosurf-hysteria : binaire autonome du moteur Hysteria.
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/hysteria"
)

func main() {
	e, err := engine.Get("hysteria")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}