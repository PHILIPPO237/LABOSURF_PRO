// Command labosurf-udp : binaire autonome du moteur UDP natif.
// Traité exactement comme les autres moteurs : supervise le serveur UDP
// (stocké dans engines/udp) via le CLI partagé (install/configure/run/...).
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/internal/engineudp"
)

func main() {
	e, err := engine.Get("udp")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}