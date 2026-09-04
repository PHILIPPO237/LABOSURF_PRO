//go:build !linux || android

package main

import (
	"fmt"
	"os"
)

func runVPNClient(args []string) {
	fmt.Fprintln(os.Stderr, "Le client VPN n'est disponible que sur Linux.")
	fmt.Fprintln(os.Stderr, "Utilisez un VPS Linux pour tester le tunnel.")
	os.Exit(1)
}
