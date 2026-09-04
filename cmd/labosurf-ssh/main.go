// Command labosurf-ssh : binaire autonome du moteur SSH.
// Un compte peut disposer d'un accès SSH ; il est supervisé comme les autres
// moteurs via le CLI partagé (install/configure/run/...).
package main

import (
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/enginecli"

	_ "labosurf/engines/ssh"
)

func main() {
	e, err := engine.Get("ssh")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(enginecli.Run(e, os.Args[1:]))
}