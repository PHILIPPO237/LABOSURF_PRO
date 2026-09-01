package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

const engineVersion = "1.1.0"

func printMenu() {
	printBanner()
	printSystemOverview()
	fmt.Println(colorDim + "── MENU PRINCIPAL ─────────────────────────────────────" + colorReset)
	fmt.Println()
	fmt.Printf("  %s[1]%s 👥 GESTION DES UTILISATEURS\n", colorGreen, colorReset)
	fmt.Println("      Gérez les comptes, accès, durées, quotas et blocages.")
	fmt.Printf("  %s[2]%s ➕ CRÉER UN UTILISATEUR\n", colorGreen, colorReset)
	fmt.Println("      Créez rapidement un nouvel accès client UDP.")
	fmt.Printf("  %s[3]%s 📊 STATISTIQUES\n", colorCyan, colorReset)
	fmt.Println("      Consultez les comptes et l'activité disponible.")
	fmt.Printf("  %s[4]%s 🌐 PORTAIL CLIENT\n", colorCyan, colorReset)
	fmt.Println("      Gérez les liens et informations du portail.")
	fmt.Printf("  %s[5]%s 🖥️ ÉTAT DU SERVEUR\n", colorCyan, colorReset)
	fmt.Println("      Vérifiez l'état de LABOSURF PRO et de l'UDP Engine.")
	fmt.Printf("  %s[6]%s 🔄 MISE À JOUR\n", colorCyan, colorReset)
	fmt.Println("      Vérifiez les nouvelles versions disponibles.")
	fmt.Printf("  %s[7]%s ⚙️ CONFIGURATION\n", colorCyan, colorReset)
	fmt.Println("      Consultez les paramètres du service.")
	fmt.Printf("  %s[8]%s 💾 SAUVEGARDE\n", colorCyan, colorReset)
	fmt.Println("      Gérez les sauvegardes des données du serveur.")
	fmt.Printf("  %s[9]%s ℹ️ À PROPOS\n", colorCyan, colorReset)
	fmt.Println("      Informations sur LABOSURF PRO et son concepteur.")
	fmt.Printf("  %s[0]%s ❌ QUITTER\n", colorDim, colorReset)
	fmt.Println()
}

func runUDPEngineModule() {
	configPath := "config.json"

	config, err := loadConfig(configPath)
	if err != nil {
		fmt.Printf(
			"\nErreur de configuration : %v\n",
			err,
		)

		fmt.Print("\nAppuyez sur Entrée pour revenir...")
		_, _ = fmt.Scanln()

		return
	}

	// Vérification de licence obligatoire (sauf mode développement explicite
	// via LABOSURF_DEV=1). Ferme le contournement par le menu interactif.
	if devModeEnabled(false) {
		fmt.Println("⚠ MODE DÉVELOPPEMENT : vérification de licence ignorée")
	} else if err := checkLicense(config); err != nil {
		fmt.Printf("\nDémarrage refusé : %v\n", err)
		fmt.Println()
		fmt.Println("Activez une licence avec :")
		fmt.Println("  labosurf license activate -token <jeton>")

		fmt.Print("\nAppuyez sur Entrée pour revenir...")
		_, _ = fmt.Scanln()

		return
	}

	server, err := NewServer(config)
	if err != nil {
		fmt.Printf(
			"\nImpossible de démarrer l'UDP Engine : %v\n",
			err,
		)

		fmt.Println()
		fmt.Println("Vérifiez notamment que le port UDP")
		fmt.Printf("%s est disponible.\n", config.Listen)

		fmt.Print("\nAppuyez sur Entrée pour revenir...")
		_, _ = fmt.Scanln()

		return
	}

	ctx, cancel := context.WithCancel(
		context.Background(),
	)

	done := make(chan error, 1)

	go func() {
		done <- server.Run(ctx)
	}()

	printModuleBanner("UDP Engine")
	fmt.Printf("  Serveur UDP  : %s\n", server.conn.LocalAddr())
	fmt.Printf("  Configuration: %s\n", configPath)

	if config.Portal.Enabled {
		fmt.Printf("  Portail HTTP : %s (intégré)\n", config.Portal.Listen)
	}

	fmt.Println()
	fmt.Printf("  %s✔ UDP Engine démarré.%s\n", colorGreen, colorReset)
	fmt.Println()
	fmt.Println("  Appuyez sur Entrée pour arrêter.")

	_, _ = fmt.Scanln()

	cancel()

	err = <-done

	closeErr := server.Close()

	if err != nil {
		fmt.Printf(
			"\nErreur du module UDP Engine : %v\n",
			err,
		)
	}

	if closeErr != nil {
		fmt.Printf(
			"\nErreur fermeture UDP : %v\n",
			closeErr,
		)
	}

	fmt.Println()
	fmt.Printf("%sLABOSURF PRO%s → UDP Engine arrêté.\n", colorGreen, colorReset)

	fmt.Print("\nAppuyez sur Entrée pour revenir...")
	_, _ = fmt.Scanln()
}

func runMenu() {
	for {
		fmt.Print("\033[2J\033[H")

		printMenu()

		var choice string

		fmt.Printf("%sLABOSURF PRO ►%s Choisissez une option : ", colorGreen, colorReset)

		if _, err := fmt.Scanln(&choice); err != nil {
			fmt.Println()
			return
		}

		choice = strings.TrimSpace(choice)

		switch choice {

		case "1":
			runUserMenu()

		case "2":
			interactiveCreateUser()

		case "3":
			printStatisticsMenu()

		case "4":
			printPortalMenu()

		case "5":
			printServerStatusMenu()

		case "6":
			printUpdateMenu()

		case "7":
			printConfigMenu()

		case "8":
			printBackupMenu()

		case "9":
			printAbout()
			fmt.Print("\nAppuyez sur Entrée pour revenir...")
			_, _ = fmt.Scanln()

		case "0":
			fmt.Println()
			fmt.Printf("%sLABOSURF PRO%s arrêté.\n", colorGreen, colorReset)
			return

		default:
			fmt.Println()
			fmt.Println("Option inconnue.")

			fmt.Print("\nAppuyez sur Entrée...")
			_, _ = fmt.Scanln()
		}
	}
}

func main() {
	if len(os.Args) == 1 {
		runMenu()
		return
	}

	switch os.Args[1] {

	case "udp":
		runUDPEngineCmd(os.Args[2:])

	case "admin":
		if err := runAdmin(os.Args[2:]); err != nil {
			log.Fatal(err)
		}

	case "license":
		if err := runLicense(os.Args[2:]); err != nil {
			log.Fatal(err)
		}

	case "portal":
		runPortalCmd(os.Args[2:])

	case "help", "-h", "--help":
		printUsage()

	default:
		fmt.Println("Module inconnu :", os.Args[1])
		fmt.Println()
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	printBanner()
	fmt.Println("Utilisation :")
	fmt.Println("  labosurf                           mode interactif")
	fmt.Println("  labosurf udp server -c config    démarrer le serveur UDP Engine")
	fmt.Println("  labosurf admin <cmd> [options]     administration des comptes")
	fmt.Println("  labosurf license <cmd> [options]   gestion des licences (Ed25519)")
	fmt.Println("  labosurf portal -addr :8080        portail client autonome")
	fmt.Println()
	fmt.Println("Options licence :")
	fmt.Println("  keygen   | create | revoke | list   (administrateur)")
	fmt.Println("  activate | status | deactivate      (utilisateur)")
}

func runUDPEngineCmd(args []string) {
	if len(args) == 0 {
		printBanner()
		fmt.Println("Utilisation : labosurf udp server -c config.json")
		fmt.Println()
		fmt.Println("Commandes :")
		fmt.Println("  server -c <config>  démarrer le serveur UDP Engine")
		return
	}

	if args[0] == "server" {
		serverFlags := flag.NewFlagSet("server", flag.ExitOnError)
		configPath := serverFlags.String("c", "config.json", "fichier de configuration")
		devMode := serverFlags.Bool("dev", false, "mode développement (ignore la vérification de licence)")
		_ = serverFlags.Parse(args[1:])

		if err := runServer(*configPath, devModeEnabled(*devMode)); err != nil {
			log.Fatal(err)
		}
		return
	}

	fmt.Println("Commande UDP Engine inconnue :", args[0])
	fmt.Println("Utilisation : labosurf udp server -c config.json")
	os.Exit(1)
}

// devModeEnabled indique si le mode développement est actif : soit via le
// drapeau -dev explicite, soit via la variable d'environnement
// LABOSURF_DEV=1. En dehors de ces cas, la vérification de licence est
// TOUJOURS appliquée (aucun contournement accidentel en production).
func devModeEnabled(flag bool) bool {
	if flag {
		return true
	}
	return os.Getenv("LABOSURF_DEV") == "1"
}

// checkLicense vérifie qu'une licence valide est activée avant de démarrer
// l'UDP Engine. Utilise les chemins définis dans la configuration (avec valeurs
// par défaut). Vérifie signature, expiration, révocation (si registre
// disponible) et liaison à l'identifiant d'installation (machine ID).
func checkLicense(cfg Config) error {
	as, err := LoadActivationStore(cfg.License.Activation, cfg.License.MachineID)
	if err != nil {
		return fmt.Errorf("impossible de charger l'état d'activation : %w", err)
	}

	// Le registre est optionnel (déploiement autonome sans révocation centralisée).
	reg, _ := LoadLicenseRegistry(cfg.License.Registry)

	res, err := as.Check(reg)
	if err != nil {
		return fmt.Errorf("licence invalide : %w", err)
	}

	if !res.Activated || res.Status != LicenseActive {
		return fmt.Errorf("licence non active : %s", res.Status)
	}

	expires := res.Data.ActivationUntil
	if expires == "" {
		expires = "illimité"
	}
	log.Printf("✔ Licence validée : %s (expire : %s)", res.Data.ID, expires)
	return nil
}

func runPortalCmd(args []string) {
	portalFlags := flag.NewFlagSet("portal", flag.ExitOnError)
	addr := portalFlags.String("addr", ":8080", "adresse d'écoute HTTP")
	store := portalFlags.String("store", "store.json", "chemin vers le store")
	_ = portalFlags.Parse(args)

	ps, err := NewPortalServer(*addr, *store)
	if err != nil {
		log.Fatalf("portail : %v", err)
	}

	if err := ps.ListenAndServe(context.Background()); err != nil {
		log.Fatalf("portail : %v", err)
	}
}
