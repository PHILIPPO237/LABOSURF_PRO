// Package enginecli fournit le CLI partagé des binaires moteurs autonomes
// (labosurf-xray, labosurf-slowdns, ...) et du gestionnaire (labosurf engine).
package enginecli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
)

// Run exécute une sous-commande du CLI d'un moteur. Retourne le code de sortie.
// Sous-commandes : list, install, configure, start, stop, restart, status,
// health, logs, update, uninstall.
func Run(e engine.Engine, args []string) int {
	if len(args) == 0 {
		usage(e)
		return 0
	}

	switch args[0] {
	case "info":
		fmt.Printf("Nom     : %s\nVersion : %s\n%s\n", e.Name(), e.Version(), e.Description())
		return 0

	case "install":
		if err := e.Install(context.Background(), engine.InstallConfig{
			Arch:      engineutil.DetectArch(),
			DataDir:   engineutil.DefaultDataDir,
			BinaryDir: engineutil.DefaultBinaryDir,
		}); err != nil {
			return fail(e, "install", err)
		}
		fmt.Printf("Moteur %s installé.\n", e.Name())
		return 0

	case "configure":
		if len(args) < 2 {
			fmt.Println("usage : labosurf-" + e.Name() + " configure <fichier.json>")
			return 2
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return fail(e, "configure", err)
		}
		if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
			return fail(e, "configure", err)
		}
		fmt.Printf("Moteur %s configuré depuis %s.\n", e.Name(), args[1])
		return 0

	case "start":
		if err := e.Start(context.Background()); err != nil {
			return fail(e, "start", err)
		}
		fmt.Printf("Moteur %s démarré.\n", e.Name())
		return 0

	case "run":
		if err := e.RunForeground(context.Background()); err != nil {
			return fail(e, "run", err)
		}
		return 0

	case "stop":
		if err := e.Stop(); err != nil {
			return fail(e, "stop", err)
		}
		fmt.Printf("Moteur %s arrêté.\n", e.Name())
		return 0

	case "restart":
		if err := e.Restart(context.Background()); err != nil {
			return fail(e, "restart", err)
		}
		fmt.Printf("Moteur %s redémarré.\n", e.Name())
		return 0

	case "status":
		st := e.Status()
		fmt.Printf("Moteur %s : installé=%v running=%v pid=%d\n",
			e.Name(), st.Installed, st.Running, st.PID)
		if st.Error != "" {
			fmt.Printf("  erreur : %s\n", st.Error)
		}
		return 0

	case "health":
		if err := e.HealthCheck(); err != nil {
			return fail(e, "health", err)
		}
		fmt.Printf("Moteur %s : opérationnel.\n", e.Name())
		return 0

	case "logs":
		lines := 30
		if len(args) >= 2 {
			_, _ = fmt.Sscanf(args[1], "%d", &lines)
		}
		logs, err := e.Logs(lines)
		if err != nil {
			return fail(e, "logs", err)
		}
		for _, l := range logs {
			fmt.Println(l)
		}
		return 0

	case "update":
		if err := e.Update(); err != nil {
			return fail(e, "update", err)
		}
		fmt.Printf("Moteur %s mis à jour.\n", e.Name())
		return 0

	case "uninstall":
		if err := e.Uninstall(); err != nil {
			return fail(e, "uninstall", err)
		}
		fmt.Printf("Moteur %s désinstallé.\n", e.Name())
		return 0

	case "help", "-h", "--help":
		usage(e)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "sous-commande inconnue : %s\n", args[0])
		usage(e)
		return 1
	}
}

func usage(e engine.Engine) {
	fmt.Printf("LABOSURF PRO — moteur %s (v%s)\n\n", e.Name(), e.Version())
	fmt.Println("Usage : labosurf-" + e.Name() + " <commande> [options]")
	fmt.Println()
	fmt.Println("  info                      informations du moteur")
	fmt.Println("  install                   installer le binaire du moteur (téléchargement + SHA-256)")
	fmt.Println("  configure <file.json>     appliquer une configuration")
	fmt.Println("  run                       lancer le moteur en avant-plan (systemd Type=simple)")
	fmt.Println("  start                     démarrer le moteur")
	fmt.Println("  stop                      arrêter le moteur")
	fmt.Println("  restart                   redémarrer le moteur")
	fmt.Println("  status                    état du moteur")
	fmt.Println("  health                    vérification de bon fonctionnement")
	fmt.Println("  logs [n]                  dernières n lignes de journal")
	fmt.Println("  update                    mettre à jour le moteur")
	fmt.Println("  uninstall                 désinstaller le moteur")
	fmt.Println()
	fmt.Println(strings.TrimSpace(e.Description()))
}

func fail(e engine.Engine, cmd string, err error) int {
	fmt.Fprintf(os.Stderr, "moteur %s : %s : %v\n", e.Name(), cmd, err)
	return 1
}