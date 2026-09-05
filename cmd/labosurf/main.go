// Command labosurf est le point d'entrée unique de la plateforme
// LABOSURF PRO. Il expose la gestion multi-moteurs via internal/engine.
package main

import (
	"context"
	"fmt"
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/license"

	// Imports d'enregistrement (init) des moteurs dans le registre.
	_ "labosurf/engines/dnstt"
	_ "labosurf/engines/hysteria"
	_ "labosurf/engines/slowdns"
	_ "labosurf/engines/ssh"
	_ "labosurf/engines/xray"
	_ "labosurf/internal/engineudp"
)

func main() {
	// Charge les moteurs hybrides composés par l'utilisateur (s'ils existent).
	_ = engineutil.EnsureHybridsRegistered()

	if len(os.Args) == 1 {
		runCentralMenu()
		return
	}

	switch os.Args[1] {
	case "engines", "engine":
		if err := runEngineCmd(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "erreur :", err)
			os.Exit(1)
		}
	case "menu":
		runCentralMenu()
	case "help", "-h", "--help":
		printRootUsage()
	default:
		printRootUsage()
		os.Exit(1)
	}
}

func runEngineCmd(args []string) error {
	if len(args) == 0 {
		return listEngines()
	}

	switch args[0] {
	case "list":
		return listEngines()
	case "status":
		return engineStatus(args)
	case "start":
		return startEngine(args)
	case "stop":
		return stopEngine(args)
	case "restart":
		return restartEngine(args)
	case "license":
		return runLicenseCmd(args[1:])
	default:
		return fmt.Errorf("commande inconnue : %s (utilisez list, status, start, stop, restart, license)", args[0])
	}
}

func listEngines() error {
	names := engine.Names()
	if len(names) == 0 {
		fmt.Println("Aucun moteur enregistré.")
		return nil
	}
	fmt.Printf("Moteurs disponibles (%d) :\n", len(names))
	for _, name := range names {
		e, err := engine.Get(name)
		ver, desc := "-", "-"
		if err == nil {
			ver = e.Version()
			desc = e.Description()
		}
		fmt.Printf("  - %-14s v%-9s %s\n", name, ver, desc)
	}
	return nil
}

func engineStatus(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage : engine status <name>")
	}
	mgr := engine.NewManager()
	st, err := mgr.Status(args[1])
	if err != nil {
		return err
	}
	fmt.Printf("Moteur %s : installé=%v running=%v pid=%d\n",
		args[1], st.Installed, st.Running, st.PID)
	if st.Error != "" {
		fmt.Printf("  erreur : %s\n", st.Error)
	}
	return nil
}

func startEngine(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage : engine start <name>")
	}
	// Vérifier la licence avant de démarrer un moteur
	if err := license.VerifyPlatformLicense(); err != nil {
		return fmt.Errorf("licence invalide : %w", err)
	}
	e, err := engine.Get(args[1])
	if err != nil {
		return err
	}
	fmt.Printf("Démarrage du moteur %s...\n", args[1])
	return e.Start(context.Background())
}

func stopEngine(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage : engine stop <name>")
	}
	mgr := engine.NewManager()
	return mgr.Stop(args[1])
}

func restartEngine(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage : engine restart <name>")
	}
	mgr := engine.NewManager()
	return mgr.Restart(context.Background(), args[1])
}

func printRootUsage() {
	fmt.Println("LABOSURF PRO — Plateforme multi-moteurs")
	fmt.Println()
	fmt.Println("Usage :")
	fmt.Println("  labosurf engine list                 lister les moteurs")
	fmt.Println("  labosurf engine status <name>        état d'un moteur")
	fmt.Println("  labosurf engine start <name>         démarrer un moteur")
	fmt.Println("  labosurf engine stop <name>          arrêter un moteur")
	fmt.Println("  labosurf engine restart <name>       redémarrer un moteur")
	fmt.Println("  labosurf engine license <cmd>        gérer la licence")
	fmt.Println()
	fmt.Println("Moteurs historiques (binaire engines/udp) :")
	fmt.Println("  labosurf udp server -c config.json   serveur UDP Engine")
}

func runLicenseCmd(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage : labosurf engine license <activate|status|verify>")
		return nil
	}
	switch args[0] {
	case "activate":
		if len(args) < 2 {
			return fmt.Errorf("usage : license activate <token>")
		}
		return license.Activate(args[1])
	case "status":
		return license.Status()
	case "verify":
		return license.Verify()
	default:
		return fmt.Errorf("commande licence inconnue : %s", args[0])
	}
}
