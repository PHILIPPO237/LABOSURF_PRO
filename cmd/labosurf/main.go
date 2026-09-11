// Command labosurf est le point d'entrée unique de la plateforme
// LABOSURF PRO. Il expose la gestion multi-moteurs via internal/engine.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/license"

	// Imports d'enregistrement (init) des moteurs dans le registre.
	_ "labosurf/engines/dnstt"
	_ "labosurf/engines/hysteria"
	_ "labosurf/engines/hysteria2"
	_ "labosurf/engines/slowdns"
	_ "labosurf/engines/ssh"
	_ "labosurf/engines/tuic"
	_ "labosurf/engines/wireguard"
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
	case "license":
		// Alias de premier niveau : c'est EXACTEMENT la forme que
		// labosurf-pro.sh invoque ("$BIN_PATH" license verify -token ...
		// -print-id) — voir activate_license() dans ce script. Avant ce
		// correctif, "license" n'était routé qu'en tant que sous-commande
		// de "engine" (cf. printRootUsage : "labosurf engine license
		// <cmd>"), donc `labosurf license verify ...` tombait dans le cas
		// `default` ci-dessous et échouait systématiquement (usage +
		// exit 1) sans jamais atteindre la vérification de licence —
		// cassant tout le flux d'activation réel de l'installateur.
		if err := runLicenseCmd(os.Args[2:]); err != nil {
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
	// NOTE : aucune vérification de licence au démarrage des moteurs.
	// La licence LABOSURF PRO ouvre l'accès au script d'installation,
	// pas au serveur : une fois installé, tout démarre librement.
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
	fmt.Println("  labosurf license activate -token ... activer une licence (1 installation)")
	fmt.Println("  labosurf license verify   -token ... vérifier une licence (option -print-id)")
	fmt.Println("  labosurf license status              installations déjà activées sur cette machine")
	fmt.Println()
	fmt.Println("Moteurs historiques (binaire engines/udp) :")
	fmt.Println("  labosurf udp server -c config.json   serveur UDP Engine")
}

func runLicenseCmd(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage : labosurf license <activate|status|verify> ...")
		fmt.Println("  labosurf license activate -token <jeton>")
		fmt.Println("  labosurf license verify   -token <jeton> [-print-id]")
		fmt.Println("  labosurf license status")
		return nil
	}
	switch args[0] {
	case "activate":
		return runLicenseActivate(args[1:])
	case "status":
		return license.Status()
	case "verify":
		return runLicenseVerify(args[1:])
	default:
		return fmt.Errorf("commande licence inconnue : %s", args[0])
	}
}

// tokenFromArgs accepte le jeton soit via le flag -token (forme utilisée par
// labosurf-pro.sh : "license verify -token \"$token\" -print-id"), soit en
// argument positionnel (forme historique : "license verify <token>") —
// les deux restent supportées pour ne rien casser côté appelants existants.
func tokenFromArgs(fs *flag.FlagSet, tokenFlag *string) (string, error) {
	if *tokenFlag != "" {
		return *tokenFlag, nil
	}
	if fs.NArg() > 0 {
		return fs.Arg(0), nil
	}
	return "", fmt.Errorf("jeton manquant (utilisez -token <jeton> ou un argument positionnel)")
}

// runLicenseActivate gère `license activate [-token <jeton>] [<jeton>]`.
func runLicenseActivate(args []string) error {
	fs := flag.NewFlagSet("license activate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	token := fs.String("token", "", "jeton de licence à activer")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("usage : license activate -token <jeton>")
	}
	t, err := tokenFromArgs(fs, token)
	if err != nil {
		return fmt.Errorf("usage : license activate -token <jeton> (%w)", err)
	}
	return license.Activate(t)
}

// runLicenseVerify gère `license verify [-token <jeton>] [<jeton>] [-print-id]`.
//
// -print-id : n'imprime QUE l'ID de licence sur stdout, sans aucune autre
// décoration — c'est la forme consommée par labosurf-pro.sh
// (id="$("$BIN_PATH" license verify -token "$token" -print-id)"), qui
// capture stdout tel quel comme identifiant. Toute sortie supplémentaire
// sur stdout dans ce mode corromprait cette capture.
func runLicenseVerify(args []string) error {
	fs := flag.NewFlagSet("license verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	token := fs.String("token", "", "jeton de licence à vérifier")
	printID := fs.Bool("print-id", false, "n'imprimer que l'ID de licence sur stdout (pour scripts)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("usage : license verify -token <jeton> [-print-id]")
	}
	t, err := tokenFromArgs(fs, token)
	if err != nil {
		return fmt.Errorf("usage : license verify -token <jeton> [-print-id] (%w)", err)
	}

	data, err := license.VerifyToken(t)
	if err != nil {
		return err
	}
	if *printID {
		fmt.Println(data.ID)
		return nil
	}
	fmt.Printf("✔ Signature valide : licence %s utilisable pour UNE installation.\n", data.ID)
	return nil
}
