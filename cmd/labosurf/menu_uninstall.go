// Désinstallation de LABOSURF PRO (menu [10]).
//
// Toute la logique métier (détection, plan, sauvegarde, arrêt de services,
// suppression sécurisée, auto-désinstallation) vit dans internal/uninstall,
// testée indépendamment avec des répertoires temporaires. Cet écran ne fait
// qu'afficher les informations et collecter les confirmations explicites —
// aucune suppression n'a lieu avant la confirmation finale (mission §4).
//
// Portée : comme les écrans SERVICES/ACCÈS (M4) et MISE À JOUR, cette
// fonctionnalité vit dans cmd/labosurf ("labosurf-mgr"), non installé par
// labosurf-pro.sh sur une installation VPS standard aujourd'hui.
package main

import (
	"fmt"
	"strings"
	"time"

	"labosurf/internal/uninstall"
)

func runUninstallMenu() {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── 🗑️  DÉSINSTALLATION LABOSURF_PRO ─────────────────────")
	fmt.Println()
	fmt.Println("  Cette opération va désinstaller LABOSURF PRO.")
	fmt.Println()

	paths := uninstall.DefaultPaths()
	inv := uninstall.Detect(paths)

	fmt.Println("  Éléments détectés :")
	fmt.Printf("    Binaires       : %d fichier(s)\n", len(inv.Binaries))
	for _, b := range inv.Binaries {
		fmt.Println("      - " + b)
	}
	fmt.Printf("    Services       : %d unité(s) systemd\n", len(inv.SystemdUnits))
	for _, u := range inv.SystemdUnits {
		fmt.Println("      - " + u)
	}
	if inv.ThirdPartyDir != "" {
		fmt.Println("    Binaires tiers : " + inv.ThirdPartyDir)
	} else {
		fmt.Println("    Binaires tiers : " + dim("aucun"))
	}
	fmt.Printf("    Configuration  : %d fichier(s)\n", len(inv.ConfigFiles))
	for _, c := range inv.ConfigFiles {
		fmt.Println("      - " + c)
	}
	if inv.DataDir != "" {
		fmt.Printf("    Données        : %s (%d élément(s))\n", inv.DataDir, len(inv.DataFiles))
	} else {
		fmt.Println("    Données        : " + dim("aucune"))
	}
	if inv.SystemUserPresent {
		fmt.Println("    Utilisateur système : labosurf (présent)")
	}
	if len(inv.Warnings) > 0 {
		fmt.Println()
		fmt.Println("  " + yellow("⚠ Configurations ignorées (jamais utilisées comme cible de suppression) :"))
		for _, w := range inv.Warnings {
			fmt.Println("    - " + yellow(w))
		}
	}

	if len(inv.Binaries) == 0 && len(inv.SystemdUnits) == 0 && inv.DataDir == "" && inv.ThirdPartyDir == "" {
		fmt.Println()
		fmt.Println("  " + dim("Aucune installation LABOSURF PRO détectée sur cette machine."))
		pauseMenu()
		return
	}

	fmt.Println()
	fmt.Println("  Que voulez-vous faire ?")
	fmt.Println()
	fmt.Println("  " + cyan("[1]") + " Désinstaller le programme uniquement")
	fmt.Println("  " + cyan("[2]") + " Désinstaller le programme + conserver les données")
	fmt.Println("  " + red("[3]") + " Désinstaller complètement + supprimer les données")
	fmt.Println("  " + dim("[0]") + " Annuler")
	fmt.Println()

	choice := promptLine(green("LABOSURF PRO ►") + " Option : ")
	var mode uninstall.Mode
	switch choice {
	case "1":
		mode = uninstall.ModeProgramOnly
	case "2":
		mode = uninstall.ModeProgramKeepData
	case "3":
		mode = uninstall.ModeFull
	default:
		fmt.Println("  Annulé.")
		pauseMenu()
		return
	}

	if mode.RequiresTypedConfirmation() {
		fmt.Println()
		fmt.Println("  " + red("ATTENTION !"))
		fmt.Println("  La suppression des données peut être irréversible.")
		fmt.Println()
		fmt.Printf("  Tapez exactement : %s\n", uninstall.ConfirmationPhrase)
		fmt.Printf("  pour continuer : ")
		if !uninstall.ValidatePhrase(promptLine("")) {
			fmt.Println("  Saisie incorrecte — désinstallation annulée.")
			pauseMenu()
			return
		}
	}

	plan := uninstall.BuildPlan(inv, mode)

	fmt.Println()
	fmt.Println("  ── RÉCAPITULATIF ────────────────────────────────────────")
	fmt.Println()
	fmt.Println("  Action : " + modeLabel(mode))
	fmt.Println()
	fmt.Println("  Éléments qui seront supprimés :")
	if len(plan.RemovePaths) == 0 {
		fmt.Println("    " + dim("(aucun)"))
	}
	for _, r := range plan.RemovePaths {
		fmt.Println("    - " + r)
	}
	fmt.Println()
	fmt.Println("  Éléments conservés :")
	if len(plan.KeepPaths) == 0 {
		fmt.Println("    " + dim("(aucun)"))
	}
	for _, k := range plan.KeepPaths {
		fmt.Println("    - " + k)
	}

	if licenseWillBeRemoved(inv, plan) {
		fmt.Println()
		fmt.Println("  " + yellow("⚠ La clé de vérification de licence locale (license_pub.key) et les"))
		fmt.Println("  " + yellow("  reçus d'activation seront supprimés. Une réinstallation nécessitera"))
		fmt.Println("  " + yellow("  une nouvelle clé d'activation. Le LICENSE MAKER et les clés du dépôt"))
		fmt.Println("  " + yellow("  source ne sont jamais concernés par cette opération."))
	}

	fmt.Println()
	fmt.Printf("  Confirmer définitivement ? (o/N) : ")
	if !uninstall.ShouldProceed(promptLine("")) {
		fmt.Println("  Désinstallation annulée.")
		pauseMenu()
		return
	}

	doBackup := false
	if mode == uninstall.ModeFull && inv.DataDir != "" {
		fmt.Println()
		fmt.Printf("  Voulez-vous créer une sauvegarde avant suppression ? (O/n) : ")
		answer := promptLine("")
		doBackup = answer == "" || strings.EqualFold(strings.TrimSpace(answer), "o")
	}

	executeUninstall(paths, inv, plan, doBackup)
}

func modeLabel(m uninstall.Mode) string {
	switch m {
	case uninstall.ModeProgramOnly:
		return "Désinstallation du programme uniquement"
	case uninstall.ModeProgramKeepData:
		return "Désinstallation du programme (données conservées)"
	case uninstall.ModeFull:
		return "Désinstallation complète (données supprimées)"
	default:
		return "?"
	}
}

// licenseWillBeRemoved indique si license_pub.key fait partie des chemins
// supprimés par ce plan — utilisé pour afficher l'avertissement mission §9.
func licenseWillBeRemoved(inv uninstall.Inventory, plan uninstall.Plan) bool {
	for _, c := range inv.ConfigFiles {
		if strings.HasSuffix(c, "license_pub.key") {
			for _, r := range plan.RemovePaths {
				if r == c || (inv.DataDir != "" && r == inv.DataDir) {
					return true
				}
			}
		}
	}
	return false
}

// executeUninstall arrête les services, effectue la sauvegarde éventuelle,
// puis supprime chaque chemin du plan — en s'arrêtant à la première erreur
// et en rapportant précisément ce qui a réellement été fait.
func executeUninstall(paths uninstall.Paths, inv uninstall.Inventory, plan uninstall.Plan, doBackup bool) {
	fmt.Println()
	fmt.Println("  ── EXÉCUTION ────────────────────────────────────────────")
	fmt.Println()

	if len(plan.StopUnits) > 0 {
		fmt.Println("  Arrêt des services...")
		stopErrs := uninstall.StopUnits(plan.StopUnits, uninstall.RealRunner)
		for _, u := range plan.StopUnits {
			if err, failed := stopErrs[u]; failed {
				fmt.Println("    " + yellow("⚠ "+u+" : "+err.Error()))
			} else {
				fmt.Println("    " + green("✔ "+u+" arrêté"))
			}
		}
		fmt.Println("  Désactivation des services...")
		disErrs := uninstall.DisableUnits(plan.DisableUnits, uninstall.RealRunner)
		for _, u := range plan.DisableUnits {
			if err, failed := disErrs[u]; failed {
				fmt.Println("    " + yellow("⚠ "+u+" : "+err.Error()))
			} else {
				fmt.Println("    " + green("✔ "+u+" désactivé"))
			}
		}
	}

	var backupPath string
	if doBackup {
		fmt.Println()
		fmt.Println("  Création de la sauvegarde...")
		backupPath = inv.DataDir + "-backup-" + timestampNow() + ".tar.gz"
		if err := uninstall.CreateBackup(inv.DataDir, backupPath); err != nil {
			fmt.Println("  " + red("✗ Sauvegarde échouée : "+err.Error()))
			fmt.Println("  " + red("  Désinstallation annulée — aucune donnée n'a été supprimée."))
			pauseMenu()
			return
		}
		if err := uninstall.VerifyBackup(backupPath); err != nil {
			fmt.Println("  " + red("✗ Sauvegarde invalide : "+err.Error()))
			fmt.Println("  " + red("  Désinstallation annulée — aucune donnée n'a été supprimée."))
			pauseMenu()
			return
		}
		fmt.Println("  " + green("✔ Sauvegarde créée : "+backupPath))
	}

	fmt.Println()
	fmt.Println("  Suppression...")

	removed := 0
	for _, p := range plan.RemovePaths {
		if uninstall.IsPathCurrentExecutable(p) {
			if err := uninstall.SpawnSelfDeleteHelper(p); err != nil {
				fmt.Println("    " + yellow("⚠ "+p+" (binaire en cours d'exécution) : "+err.Error()))
			} else {
				fmt.Println("    " + green("✔ "+p+" sera supprimé après la fermeture du programme"))
				removed++
			}
			continue
		}
		if err := uninstall.RemovePath(p, paths); err != nil {
			fmt.Println("    " + red("✗ "+p+" : "+err.Error()))
			fmt.Println()
			fmt.Println("  " + red(fmt.Sprintf("Désinstallation interrompue après %d élément(s) supprimé(s).", removed)))
			if backupPath != "" {
				fmt.Println("  Sauvegarde conservée : " + backupPath)
			}
			pauseMenu()
			return
		}
		fmt.Println("    " + green("✔ "+p+" supprimé"))
		removed++
	}

	fmt.Println()
	fmt.Println("  " + green(fmt.Sprintf("✔ Désinstallation terminée (%d élément(s) supprimé(s)).", removed)))
	if backupPath != "" {
		fmt.Println("  Sauvegarde disponible : " + backupPath)
	}
	pauseMenu()
}

func timestampNow() string {
	return time.Now().UTC().Format("20060102T150405Z")
}
