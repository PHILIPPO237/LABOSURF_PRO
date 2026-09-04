// Command labosurf-dnstt : binaire autonome du moteur dnstt.
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/dnstt"
)

func main() {
	e, err := engine.Get("dnstt")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}