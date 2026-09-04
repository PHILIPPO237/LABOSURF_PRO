// Command labosurf-slowdns : binaire autonome du moteur SlowDNS.
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/slowdns"
)

func main() {
	e, err := engine.Get("slowdns")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}