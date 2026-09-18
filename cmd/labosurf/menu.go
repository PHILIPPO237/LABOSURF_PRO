// Menu central interactif de LABOSURF PRO.
//
// Ce menu pilote l'ensemble de la plateforme multi-moteurs : chaque moteur
// (xray, slowdns, dnstt, hysteria, hybrides) dispose d'un sous-menu complet
// (install/start/stop/status/config/logs/update/uninstall/health).
//
// La gestion d'utilisateurs/portail du moteur UDP est déléguée au binaire UDP
// historique (sous-processus), car elle vit dans le module engines/udp.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"labosurf/internal/clientcfg"
	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

var menuReader = bufio.NewReader(os.Stdin)

func promptLine(label string) string {
	fmt.Printf("  %s", label)
	v, _ := menuReader.ReadString('\n')
	return strings.TrimSpace(v)
}

func pauseMenu() {
	fmt.Print("\n  ↩️  APPUYEZ SUR ENTRÉE POUR REVENIR...")
	_, _ = menuReader.ReadString('\n')
}

func clearScreen() { fmt.Print("\033[2J\033[H") }

// runCentralMenu est le point d'entrée du menu interactif central.
//
// Dashboard compact en une seule boîte (≤ dashW colonnes) : titre, puis
// système/licence, moteurs, compteurs services/accès/comptes, puis le
// menu lui-même en grille 2 colonnes — tout tient sur un écran de
// téléphone standard, sans scroller (voir maquette validée le
// 2026-09-18). Les 9 options et leur numéro/lettre sont strictement les
// mêmes qu'avant ; seule la présentation change — aucune description
// longue imprimée ici, elle reste dans chaque sous-menu.
func runCentralMenu() {
	for {
		clearScreen()
		printDashboard()

		choice := promptLine(magenta("LABOSURF PRO ►") + " Choix : ")
		switch choice {
		case "1":
			runEngineMenu()
		case "2":
			runUsersMenu()
		case "3":
			runGlobalStatus()
		case "4":
			runServerProfileMenu()
		case "5":
			runProfileMenu()
		case "6":
			runServiceMenu()
		case "7":
			runAccessMenu()
		case "9":
			printAbout()
		case "0":
			fmt.Println()
			fmt.Println(green("LABOSURF PRO") + " arrêté.")
			return
		default:
			fmt.Println("\n  ❌ OPTION INCONNUE.")
			pauseMenu()
		}
	}
}

// ── Header central ─────────────────────────────────────────
//
// printCentralHeader reste utilisé par tous les SOUS-menus (moteur,
// utilisateurs, services, accès, profil...) : bandeau compact, un seul
// cadre, jamais de lettres ASCII géantes (l'ancien bandeau de 6 lignes
// x ~68 colonnes cassait sur un terminal Termux portrait étroit).
// L'écran d'accueil, lui, utilise printDashboard() ci-dessous, qui
// intègre son propre titre dans la même boîte que le reste.
func printCentralHeader() {
	fmt.Println()
	fmt.Println(dashTop())
	fmt.Println(dashLine(magenta(bold("⚡ LABOSURF PRO")) + dim(" — FreeSurf")))
	fmt.Println(dashBottom())
}

// printDashboard affiche l'écran d'accueil complet dans une boîte
// unique : titre, système/licence, moteurs, compteurs, puis le menu en
// grille — voir la maquette validée (audit du 2026-09-18). Purement de
// la présentation : aucune donnée ici n'est recalculée différemment de
// ce que printSystemPanel lisait déjà.
func printDashboard() {
	fmt.Println()
	fmt.Println(dashTop())
	fmt.Println(dashLine(magenta(bold("⚡ LABOSURF PRO")) + dim(" — FreeSurf")))
	printSystemPanel()

	fmt.Println(dashMid())
	fmt.Println(dashLine(yellow(bold("MENU"))))
	fmt.Println(dashTwoCol(menuItem("1", "🔧", "Moteurs"), menuItem("2", "👥", "Comptes")))
	fmt.Println(dashTwoCol(menuItem("3", "🖥", "État global"), menuItem("4", "⚙", "Profil srv")))
	fmt.Println(dashTwoCol(menuItem("5", "📋", "Profils"), menuItem("6", "🧩", "Services")))
	fmt.Println(dashTwoCol(menuItem("7", "🔑", "Accès"), menuItem("9", "ℹ", "À propos")))
	fmt.Println(dashLine(menuItem("0", "❌", "Quitter")))
	fmt.Println(dashBottom())
}

// menuItem formate une entrée de menu "N icône libellé" avec un
// numéro/lettre en surbrillance — utilisé uniquement pour la grille du
// dashboard (dashTwoCol/dashLine) ; la logique de sélection elle-même
// (switch sur choice) est totalement inchangée.
func menuItem(key, icon, label string) string {
	return cWhite(key) + " " + icon + " " + label
}

const cWhiteCode = "\033[1;37m"

func cWhite(s string) string { return cWhiteCode + s + cReset }

// ── Gestion des moteurs ────────────────────────────────────
func runEngineMenu() {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Println("  ── 🔧 GESTION DES MOTEURS ────────────────────────────")
		fmt.Println()

		names := engine.Names()
		sort.Strings(names)
		idx := 1
		for _, n := range names {
			e, err := engine.Get(n)
			state := ""
			if err == nil {
				st := e.Status()
				switch {
				case st.Running:
					state = green("● en cours") + dim(" (pid "+itoa(st.PID)+")")
				case st.Installed:
					state = dim("○ installé")
				default:
					state = dim("· non installé")
				}
			}
			fmt.Printf("  %s %-12s %s\n", green(fmt.Sprintf("[%d]", idx)), n, state)
			idx++
		}
		fmt.Println("  " + cyan("[C]") + " 🧩 CRÉER UN MOTEUR HYBRIDE")
		fmt.Println("      Composer soi-même un hybride à partir des moteurs principaux.")
		fmt.Println("  " + cyan("[D]") + " 🗑️  RETIRER UN MOTEUR HYBRIDE")
		fmt.Println("      Supprimer un hybride précédemment composé.")
		fmt.Println("  " + cyan("[0]") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Moteur à gérer : ")
		if choice == "0" {
			return
		}
		if strings.EqualFold(choice, "C") {
			runHybridCreateMenu()
			continue
		}
		if strings.EqualFold(choice, "D") {
			runHybridRemoveMenu()
			continue
		}
		if choice == "" {
			continue
		}
		e, ok := engineByName(choice, names)
		if !ok {
			fmt.Println("\n  ❌ Moteur inconnu.")
			pauseMenu()
			continue
		}
		runSingleEngineMenu(e)
	}
}

// runHybridCreateMenu permet de composer librement un moteur hybride à partir
// des moteurs principaux (xray, hysteria, slowdns, dnstt, ssh). L'ordre de
// saisie = ordre des composants. Le guide de compatibilité liste les
// avertissements (rôles) sans interdire.
//
// "udp" n'apparaît jamais dans la liste ci-dessous : il vit dans
// engines/udp, un module Go séparé (binaire labosurf-udp autonome, jamais
// importé par ce binaire cmd/labosurf) — il n'est donc jamais présent dans
// engine.Names() ici, et ne peut structurellement pas participer à un
// hybride avec l'architecture actuelle (voir AUDIT_PHASE3_HYBRIDS.md).
func runHybridCreateMenu() {
	clearScreen()
	printCentralHeader()

	// Moteurs principaux : ceux qui ne sont pas des hybrides composés.
	var primaries []string
	for _, n := range engine.Names() {
		if !strings.Contains(n, "-") {
			primaries = append(primaries, n)
		}
	}
	if len(primaries) == 0 {
		fmt.Println("\n  Aucun moteur principal disponible.")
		pauseMenu()
		return
	}

	fmt.Println()
	fmt.Println("  ── 🧩 CRÉER UN MOTEUR HYBRIDE ─────────────────────────")
	fmt.Println()
	fmt.Println("  Moteurs disponibles :")
	for i, n := range primaries {
		fmt.Printf("    [%d] %-12s → %s\n", i+1, n, engineutil.Role(n).RoleLabel())
	}
	fmt.Println()
	fmt.Println("  Choisis plusieurs moteurs (n° séparés par des espaces).")
	fmt.Println("  L'ordre de saisie définit l'ordre des composants.")
	fmt.Printf("\n  Numéros : ")
	choice := promptLine("")
	if choice == "" {
		return
	}

	var chosen []string
	seen := map[string]bool{}
	for _, part := range strings.Fields(choice) {
		var num int
		if _, err := fmt.Sscanf(part, "%d", &num); err == nil && num >= 1 && num <= len(primaries) {
			name := primaries[num-1]
			if !seen[name] {
				seen[name] = true
				chosen = append(chosen, name)
			}
		}
	}

	if len(chosen) < 2 {
		fmt.Println("\n  " + red("✗ Un hybride requiert au moins 2 moteurs."))
		pauseMenu()
		return
	}

	fmt.Println()
	fmt.Println("  Composition choisie : " + cyan(strings.Join(chosen, " + ")))
	warnings := engineutil.CompatibilityCheck(chosen)
	if len(warnings) == 0 {
		fmt.Println("  " + green("✔ Compatibilité : aucune alerte."))
	} else {
		fmt.Println("  " + yellow("⚠ GUIDE DE COMPATIBILITÉ :"))
		for _, w := range warnings {
			fmt.Println("    - " + w)
		}
	}

	newName := engineutil.HybridName(chosen)
	fmt.Printf("\n  Nom proposé : %s\n", cyan(newName))
	fmt.Printf("  Créer ce moteur hybride ? (o/N) : ")
	if !strings.EqualFold(promptLine(""), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	if _, err := engineutil.RegisterHybridPersist(chosen); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Moteur hybride créé : ") + newName)
		fmt.Println("      Il apparaît maintenant dans la GESTION DES MOTEURS.")
	}
	pauseMenu()
}

// runHybridRemoveMenu liste les moteurs hybrides composés et permet d'en
// retirer un (session courante + persistance).
func runHybridRemoveMenu() {
	clearScreen()
	printCentralHeader()

	var hybrids []string
	for _, n := range engine.Names() {
		if strings.Contains(n, "-") {
			hybrids = append(hybrids, n)
		}
	}

	fmt.Println()
	fmt.Println("  ── 🗑️ RETIRER UN MOTEUR HYBRIDE ────────────────────────")
	fmt.Println()
	if len(hybrids) == 0 {
		fmt.Println("  Aucun moteur hybride composé.")
		pauseMenu()
		return
	}
	for i, h := range hybrids {
		fmt.Printf("    [%d] %s\n", i+1, h)
	}
	fmt.Println("    [0] 🔙 RETOUR")
	fmt.Printf("\n  Hybride à retirer (n°) : ")
	choice := promptLine("")
	var num int
	if _, err := fmt.Sscanf(choice, "%d", &num); err != nil || num < 1 || num > len(hybrids) {
		return
	}
	target := hybrids[num-1]
	fmt.Printf("\n  Retirer l'hybride %s ? (o/N) : ", cyan(target))
	if !strings.EqualFold(promptLine(""), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}
	if err := engineutil.RemoveHybrid(target); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Moteur hybride retiré : ") + target)
	}
	pauseMenu()
}

func engineByName(choice string, names []string) (engine.Engine, bool) {
	if n, err := fmt.Sscanf(choice, "%d", new(int)); err == nil && n == 1 {
		var num int
		_, _ = fmt.Sscanf(choice, "%d", &num)
		if num >= 1 && num <= len(names) {
			e, err := engine.Get(names[num-1])
			return e, err == nil
		}
		return nil, false
	}
	e, err := engine.Get(strings.ToLower(choice))
	return e, err == nil
}

func runSingleEngineMenu(e engine.Engine) {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Printf("  ── MOTEUR %s (v%s) ────────────────────────────────────\n",
			name(e), e.Version())
		fmt.Println()
		st := e.Status()
		fmt.Printf("  État : ")
		switch {
		case st.Running:
			fmt.Println(green("● EN COURS") + dim(" (pid "+itoa(st.PID)+")"))
		case st.Installed:
			fmt.Println(dim("○ installé"))
		default:
			fmt.Println(dim("· non installé"))
		}
		if st.Error != "" {
			fmt.Println("        " + red("⚠ "+st.Error))
		}
		fmt.Println()
		fmt.Println("  " + cyan("1") + " 📥 INSTALLER")
		fmt.Println("      Télécharger + déployer le moteur tierce (SHA-256 vérifié).")
		fmt.Println("  " + cyan("2") + " ⚙️  CONFIGURER")
		fmt.Println("      Appliquer un fichier de configuration au moteur.")
		fmt.Println("  " + green("3") + " ▶️  DÉMARRER")
		fmt.Println("  " + red("4") + " ⏹  ARRÊTER")
		fmt.Println("  " + cyan("5") + " 🔄 REDÉMARRER")
		fmt.Println("  " + cyan("6") + " 🖥️  ÉTAT DÉTAILLÉ")
		fmt.Println("  " + cyan("7") + " 💬 JOURNAUX")
		fmt.Println("  " + cyan("8") + " 🚀 MISE À JOUR")
		fmt.Println("  " + red("9") + " 🗑️  DÉSINSTALLER")
		fmt.Println("  " + dim("0") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Option : ")
		switch choice {
		case "1":
			menuInstall(e)
		case "2":
			switch e.Name() {
			case store.EngineXray:
				menuXrayConfig(e)
			case "freeway-gate":
				menuFreewayConfig(e)
			case store.EngineHysteria:
				menuHysteriaConfig(e)
			case store.EngineHysteria2:
				menuHysteria2Config(e)
			case store.EngineSlowDNS:
				menuSlowDNSConfig(e)
			case store.EngineDNSTT:
				menuDNSTTConfig(e)
			case store.EngineSSH:
				menuSSHConfig(e)
			case store.EngineTUIC:
				menuTUICConfig(e)
			case store.EngineWireGuard:
				menuWireGuardConfig(e)
			case store.EngineUDP:
				menuUDPConfig(e)
			default:
				menuConfigure(e)
			}
		case "3":
			fmt.Println("\n  ▶️ Démarrage du moteur " + name(e) + "...")
			if err := e.Start(context.Background()); err != nil {
				fmt.Println("  " + red("✗ "+err.Error()))
			} else {
				fmt.Println("  " + green("✔ Moteur démarré."))
			}
		case "4":
			fmt.Println("\n  ⏹ Arrêt du moteur " + name(e) + "...")
			if err := e.Stop(); err != nil {
				fmt.Println("  " + red("✗ "+err.Error()))
			} else {
				fmt.Println("  " + green("✔ Moteur arrêté."))
			}
		case "5":
			fmt.Println("\n  🔄 Redémarrage du moteur " + name(e) + "...")
			if err := e.Restart(context.Background()); err != nil {
				fmt.Println("  " + red("✗ "+err.Error()))
			} else {
				fmt.Println("  " + green("✔ Moteur redémarré."))
			}
		case "6":
			menuStatusDeep(e)
		case "7":
			menuLogs(e)
		case "8":
			fmt.Println("\n  🚀 Mise à jour du moteur " + name(e) + "...")
			if err := e.Update(); err != nil {
				fmt.Println("  " + red("✗ "+err.Error()))
			} else {
				fmt.Println("  " + green("✔ Moteur mis à jour."))
			}
		case "9":
			fmt.Printf("\n  Confirmer la désinstallation de %s ? (o/N) : ", name(e))
			if strings.EqualFold(promptLine(""), "o") {
				if err := e.Uninstall(); err != nil {
					fmt.Println("  " + red("✗ "+err.Error()))
				} else {
					fmt.Println("  " + green("✔ Moteur désinstallé."))
				}
			}
		case "0":
			return
		default:
			fmt.Println("\n  ❌ OPTION INCONNUE.")
		}
		if choice != "3" && choice != "6" {
			pauseMenu()
		}
	}
}

func menuInstall(e engine.Engine) {
	fmt.Println("\n  📥 Installation du moteur " + name(e) + "...")
	// Utilise les defaults orientés VPS ; l'arch est détectée automatiquement.
	if err := e.Install(context.Background(), defaultInstallConfig()); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Moteur installé. Utilisez [3] pour démarrer."))
	}
	pauseMenu()
}

func menuConfigure(e engine.Engine) {
	fmt.Printf("\n  Chemin du fichier de configuration (JSON) : ")
	path := promptLine("")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Configuration appliquée."))
	}
	pauseMenu()
}

func menuStatusDeep(e engine.Engine) {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Printf("  ── ÉTAT DU MOTEUR %s ────────────────────────────────────\n", name(e))
	fmt.Println()
	st := e.Status()
	fmt.Printf("  Installé : %v\n", st.Installed)
	fmt.Printf("  En cours : %v\n", st.Running)
	fmt.Printf("  PID      : %d\n", st.PID)
	if st.ListenAddr != "" {
		fmt.Printf("  Écoute   : %s\n", st.ListenAddr)
	}
	if st.Uptime != "" {
		fmt.Printf("  Uptime   : %s\n", st.Uptime)
	}
	if st.Error != "" {
		fmt.Printf("  Erreur   : %s\n", st.Error)
	}
	_ = e.HealthCheck() // ignore le code de retour ; affiche un verdict
	if err := e.HealthCheck(); err != nil {
		fmt.Printf("\n  Santé    : %s\n", red("✗ "+err.Error()))
	} else {
		fmt.Printf("\n  Santé    : %s\n", green("✔ opérationnel"))
	}
	printChainBreakdown(e)
	fmt.Println()
	pauseMenu()
}

// printChainBreakdown affiche, pour tout moteur hybride composé
// (*engineutil.CompositeEngine — détecté génériquement, jamais par un
// `if name == "..."` sur une combinaison précise), l'état RÉEL de chaque
// composant de la chaîne suivi de l'état agrégé de la chaîne entière, ex :
//
//	● ON  TUIC
//	● ON  SSH
//	● ON  XRAY
//	● ON  CHAIN
//
// N'affiche jamais les cinq futures chaînes candidates (TUIC+SSH+Xray,
// etc.) : ce bloc ne réagit qu'à un hybride RÉELLEMENT composé (menu
// "CRÉER UN MOTEUR HYBRIDE"), quel qu'il soit — préparation générique de la
// CLI, pas une déclaration de fonctionnalité pour ces combinaisons.
func printChainBreakdown(e engine.Engine) {
	ce, ok := e.(*engineutil.CompositeEngine)
	if !ok {
		return
	}
	fmt.Println()
	fmt.Println("  ── COMPOSANTS DE LA CHAÎNE ─────────────────────────────")
	allRunning := len(ce.Components) > 0
	for _, compName := range ce.Components {
		sub, err := ce.Component(compName)
		if err != nil {
			fmt.Printf("  %s %-10s %s\n", dim("●"), strings.ToUpper(compName), red("erreur : "+err.Error()))
			allRunning = false
			continue
		}
		running := sub.Status().Running
		if !running {
			allRunning = false
		}
		dot := dim("●") + " " + dim("OFF")
		if running {
			dot = green("●") + " " + green("ON ")
		}
		fmt.Printf("  %s  %-10s\n", dot, strings.ToUpper(compName))
	}
	chainDot := dim("●") + " " + dim("OFF")
	if allRunning {
		chainDot = green("●") + " " + green("ON ")
	}
	fmt.Printf("  %s  %-10s\n", chainDot, "CHAIN")
}

func menuLogs(e engine.Engine) {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Printf("  ── JOURNAUX DU MOTEUR %s ────────────────────────────────\n", name(e))
	fmt.Println()
	lines, err := e.Logs(30)
	if err != nil {
		fmt.Printf("  %s\n", red("✗ "+err.Error()))
	} else {
		for _, l := range lines {
			fmt.Println("  " + l)
		}
	}
	fmt.Println()
	pauseMenu()
}

// ── État global ────────────────────────────────────────────
func runGlobalStatus() {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── 🖥️ ÉTAT GLOBAL ──────────────────────────────────────")
	fmt.Println()
	for _, n := range engine.Names() {
		e, err := engine.Get(n)
		if err != nil {
			continue
		}
		st := e.Status()
		line := fmt.Sprintf("  %-12s ", n)
		switch {
		case st.Running:
			line += green("● en cours") + dim(" (pid "+itoa(st.PID)+")")
		case st.Installed:
			line += dim("○ installé")
		default:
			line += dim("· non installé")
		}
		fmt.Println(line)
	}
	fmt.Println()
	pauseMenu()
}

// ── Gestion des utilisateurs (centrale, multi-moteurs) ─────
//
// Un utilisateur est un compte unique du store central. Il peut être
// rattaché à UN OU PLUSIEURS moteurs simultanément (grants). Le moteur UDP
// n'est plus un cas particulier : il est géré comme les autres dans
// « GESTION DES MOTEURS ».
func runUsersMenu() {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Println("  ── 👥 GESTION DES UTILISATEURS ─────────────────────────")
		fmt.Println()
		fmt.Println("  " + cyan("1") + " ➕ CRÉER UN COMPTE")
		fmt.Println("      Crée un compte central, puis rattache-le à un ou plusieurs moteurs.")
		fmt.Println("  " + cyan("2") + " 📋 LISTER LES COMPTES")
		fmt.Println("      Affiche les comptes et leurs moteurs rattachés (grants).")
		fmt.Println("  " + cyan("3") + " 🔗 RATTACHER UN COMPTE À UN MOTEUR")
		fmt.Println("      Connecte un compte existant à un moteur supplémentaire.")
		fmt.Println("  " + cyan("4") + " 🕒 RENOUVELER / EXPIRATION")
		fmt.Println("  " + cyan("5") + " ✅ ACTIVER / ⛔ DÉSACTIVER")
		fmt.Println("  " + cyan("6") + " 🗑️  SUPPRIMER UN COMPTE")
		fmt.Println("  " + cyan("7") + " 📤 GÉNÉRER LA CONFIG CLIENT")
		fmt.Println("      Choisis un moteur auquel le compte a accès → lien client + config serveur.")
		fmt.Println("  " + dim("0") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Option : ")
		switch choice {
		case "1":
			menuUserCreate()
		case "2":
			menuUserList()
		case "3":
			menuUserGrantEngine()
		case "4":
			menuUserRenew()
		case "5":
			menuUserToggle()
		case "6":
			menuUserDelete()
		case "7":
			menuUserClientConfig()
		case "0":
			return
		default:
			fmt.Println("\n  ❌ OPTION INCONNUE.")
			pauseMenu()
		}
	}
}

// openStore ouvre le store central partagé par tous les moteurs.
func openStore() (*store.Store, error) {
	return store.LoadStore(store.StorePath())
}

func menuUserCreate() {
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	fmt.Printf("\n  Identifiant du compte : ")
	id := promptLine("")
	if id == "" {
		return
	}
	username := id
	fmt.Printf("  Nom d'utilisateur (vide = identifiant) : ")
	if uname := promptLine(""); uname != "" {
		username = uname
	}
	fmt.Printf("  Durée (jours, vide = illimité) : ")
	days := 0
	if d := promptLine(""); d != "" {
		_, _ = fmt.Sscanf(d, "%d", &days)
	}

	acc, err := s.CreateAccount(store.Account{
		ID:        id,
		Username:  username,
		ExpiresAt: store.ExpiryFromDays(days),
		Enabled:   true,
	})
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	fmt.Println("  " + green("✔ Compte créé :") + " " + acc.ID)
	fmt.Println("      Mot de passe : " + acc.Password)
	fmt.Println("      Lien client  : " + store.ClientLinkPath(acc.Token))

	if promptEngineAttach(s, acc.ID) {
		pauseMenu()
	}
}

// promptEngineAttach propose de rattacher le compte à un ou plusieurs moteurs
// du réseau central. Retourne true si au moins un moteur a été rattaché.
func promptEngineAttach(s *store.Store, accountID string) bool {
	names := engine.Names()
	if len(names) == 0 {
		return false
	}
	fmt.Println()
	fmt.Println("  Moteurs disponibles :")
	for i, n := range names {
		fmt.Printf("    [%d] %-12s\n", i+1, n)
	}
	fmt.Printf("\n  Rattacher à des moteurs (n°, ou 'r' pour terminer) : ")
	choice := promptLine("")
	fmt.Println()
	if choice == "" || strings.EqualFold(choice, "r") {
		return false
	}
	for _, part := range strings.Fields(choice) {
		var num int
		if _, err := fmt.Sscanf(part, "%d", &num); err == nil && num >= 1 && num <= len(names) {
			if _, err := s.AddGrant(accountID, names[num-1], currentGrantConfig(names[num-1])); err != nil {
				fmt.Println("  " + red("✗ "+err.Error()))
			} else {
				fmt.Println("  " + green("✔ Compte rattaché au moteur ") + names[num-1])
			}
		}
	}
	return true
}

// currentGrantConfig fournit une configuration initiale de grant selon le
// moteur. Elle est modifiable ensuite via chaque moteur.
func currentGrantConfig(engineName string) map[string]any {
	return map[string]any{"engine": engineName}
}

func menuUserList() {
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	accounts := s.ListAccounts()
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── 📋 COMPTES (store central) ──────────────────────────")
	fmt.Println()
	if len(accounts) == 0 {
		fmt.Println("  Aucun compte pour le moment.")
	} else {
		for _, a := range accounts {
			state := "⛔ désactivé"
			if a.Enabled {
				state = "✅ actif"
			}
			exp := a.ExpiresAt
			if exp == "" {
				exp = "illimité"
			}
			fmt.Printf("  • %-14s (%s) — expire : %s\n", a.ID, state, exp)
			if engs := a.LinkedEngines(); len(engs) > 0 {
				fmt.Printf("      Moteurs : %s\n", strings.Join(engs, ", "))
			} else {
				fmt.Println("      Moteurs : aucun")
			}
		}
	}
	fmt.Println()
	pauseMenu()
}

func menuUserGrantEngine() {
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	fmt.Printf("\n  Identifiant du compte : ")
	id := promptLine("")
	if _, ok := s.GetAccount(id); !ok {
		fmt.Println("  " + red("✗ Compte introuvable."))
		pauseMenu()
		return
	}
	promptEngineAttach(s, id)
	pauseMenu()
}

// menuUserClientConfig génère, pour un compte et un moteur choisi, le lien
// client et la configuration serveur. C'est la sélection du moteur à la
// connexion : seul un moteur présent dans les grants du compte est proposé.
func menuUserClientConfig() {
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	fmt.Printf("\n  Identifiant du compte : ")
	id := promptLine("")
	acc, ok := s.GetAccount(id)
	if !ok {
		fmt.Println("  " + red("✗ Compte introuvable."))
		pauseMenu()
		return
	}

	engines := acc.LinkedEngines()
	if len(engines) == 0 {
		fmt.Println("  " + red("✗ Ce compte n'a aucun moteur. Rattache-le d'abord (option 3)."))
		pauseMenu()
		return
	}

	sort.Strings(engines)
	fmt.Println()
	fmt.Println("  Moteurs auxquels le compte a accès :")
	for i, n := range engines {
		fmt.Printf("    [%d] %-16s\n", i+1, n)
	}
	fmt.Printf("\n  Moteur à configurer (n°) : ")
	choice := promptLine("")
	var num int
	if _, err := fmt.Sscanf(choice, "%d", &num); err != nil || num < 1 || num > len(engines) {
		fmt.Println("  " + red("✗ Choix invalide."))
		pauseMenu()
		return
	}

	engineName := engines[num-1]
	prof, err := srvcfg.Load()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	res, err := clientcfg.Generate(acc, engineName, prof)
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── 📤 CONFIG CLIENT — " + engineName + " ─────────────────────")
	fmt.Println()
	fmt.Println("  Compte : " + acc.ID)
	fmt.Println("  Moteur : " + engineName)
	fmt.Println()
	fmt.Println("  " + green("▸ LIEN CLIENT :"))
	fmt.Println("    " + wrapLine(res.ClientLink, 4))

	// Applique la config serveur groupée (tous les comptes autorisés sur ce
	// moteur) via engine.Configure, si le moteur est installé.
	if err := clientcfg.ApplyServerConfig(context.Background(), s, engineName, prof); err != nil {
		fmt.Println("\n  " + yellow("⚠ Config serveur non appliquée : "+err.Error()))
	} else {
		fmt.Println("\n  " + green("✔ Config serveur appliquée au moteur ") + engineName)
	}
	fmt.Println()
	pauseMenu()
}

// wrapLine découpe une longue chaîne pour l'affichage dans le terminal.
func wrapLine(s string, indent int) string {
	const width = 60
	padding := strings.Repeat(" ", indent)
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i += width {
		end := i + width
		if end > len(runes) {
			end = len(runes)
		}
		if i > 0 {
			b.WriteString("\n" + padding)
		}
		b.WriteString(string(runes[i:end]))
	}
	return b.String()
}

// runServerProfileMenu gère le profil serveur : hôte, domaines, ports.
func runServerProfileMenu() {
	for {
		clearScreen()
		printCentralHeader()
		prof, err := srvcfg.Load()
		if err != nil {
			fmt.Println("  " + red("✗ "+err.Error()))
			pauseMenu()
			return
		}
		fmt.Println()
		fmt.Println("  ── ⚙️ PROFIL SERVEUR ───────────────────────────────────")
		fmt.Println()
		fmt.Println("  Adresse publique : " + orDim(prof.Host, "(non définie)"))
		fmt.Println("  Domaines :")
		if len(prof.Domains) > 0 {
			for _, d := range prof.Domains {
				mark := dim("· DNS-only")
				if prof.IsProxied(d) {
					mark = yellow("☁ proxysé (Cloudflare)")
				}
				fmt.Printf("    - %-30s %s\n", strings.TrimSuffix(d, "."), mark)
			}
		} else {
			fmt.Println("    (aucun)")
		}
		fmt.Println()
		fmt.Println("  Ports par moteur :")
		for _, n := range engine.Names() {
			fmt.Printf("    %-16s : %d\n", n, prof.Port(n))
		}
		fmt.Println()
		fmt.Println("  " + cyan("1") + " ✏️  DÉFINIR L'ADRESSE PUBLIQUE")
		fmt.Println("  " + cyan("2") + " 🌐 AJOUTER UN DOMAINE")
		fmt.Println("  " + cyan("3") + " 🔢 MODIFIER UN PORT")
		fmt.Println("  " + cyan("4") + " ☁  MARQUER UN SOUS-DOMAINE CLOUDFLARE")
		fmt.Println("  " + dim("0") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Option : ")
		switch choice {
		case "1":
			fmt.Printf("  Adresse publique (IP ou domaine) : ")
			if h := strings.TrimSpace(promptLine("")); h != "" {
				prof.Host = h
				if err := prof.Save(); err != nil {
					fmt.Println("  " + red("✗ "+err.Error()))
				} else {
					fmt.Println("  " + green("✔ Profil enregistré."))
				}
			}
		case "2":
			fmt.Printf("  Domaine (ex. tunnel.example.com) : ")
			if d := strings.TrimSpace(promptLine("")); d != "" {
				prof.Domains = append(prof.Domains, d)
				if err := prof.Save(); err != nil {
					fmt.Println("  " + red("✗ "+err.Error()))
				} else {
					fmt.Println("  " + green("✔ Domaine ajouté."))
				}
			}
		case "3":
			fmt.Printf("  Nom du moteur : ")
			name := strings.TrimSpace(promptLine(""))
			fmt.Printf("  Nouveau port : ")
			var port int
			if _, err := fmt.Sscanf(promptLine(""), "%d", &port); err == nil && port > 0 && port <= 65535 {
				prof.SetPort(name, port)
				if err := prof.Save(); err != nil {
					fmt.Println("  " + red("✗ "+err.Error()))
				} else {
					fmt.Println("  " + green("✔ Port enregistré."))
				}
			} else {
				fmt.Println("  " + red("✗ Port invalide."))
			}
		case "4":
			// Bascule du statut Cloudflare d'un sous-domaine : DNS-only (gris,
			// requis pour REALITY/tcp) ou proxysé (orange, ws/xhttp derrière le CDN).
			if len(prof.Domains) == 0 {
				fmt.Println("  " + yellow("⚠ Aucun sous-domaine enregistré — ajoutez-en via [2]."))
				break
			}
			for i, d := range prof.Domains {
				mark := dim("· DNS-only")
				if prof.IsProxied(d) {
					mark = yellow("☁ proxysé")
				}
				fmt.Printf("    %d) %-30s %s\n", i+1, strings.TrimSuffix(d, "."), mark)
			}
			fmt.Printf("  Sous-domaine à basculer (1-%d, 0 = annuler) : ", len(prof.Domains))
			var idx int
			if _, err := fmt.Sscanf(promptLine(""), "%d", &idx); err == nil && idx >= 1 && idx <= len(prof.Domains) {
				prof.SetProxied(prof.Domains[idx-1], !prof.IsProxied(prof.Domains[idx-1]))
				if err := prof.Save(); err != nil {
					fmt.Println("  " + red("✗ "+err.Error()))
				} else {
					fmt.Println("  " + green("✔ Statut Cloudflare mis à jour."))
				}
			}
		case "0":
			return
		default:
			fmt.Println("\n  ❌ OPTION INCONNUE.")
		}
		pauseMenu()
	}
}

// orDim affiche une valeur ou un texte grisé si vide.
func orDim(v, fallback string) string {
	if v == "" {
		return dim(fallback)
	}
	return green(v)
}

func menuUserRenew() {
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	fmt.Printf("\n  Identifiant du compte : ")
	id := promptLine("")
	fmt.Printf("  Durée à ajouter (jours) : ")
	d := promptLine("")
	var days int
	if _, err := fmt.Sscanf(d, "%d", &days); err != nil || days <= 0 {
		fmt.Println("  " + red("✗ Nombre de jours invalide."))
		pauseMenu()
		return
	}
	if _, err := s.Renew(id, days); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Compte prolongé (") + fmt.Sprintf("%d", days) + " jours).")
	}
	pauseMenu()
}

func menuUserToggle() {
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	fmt.Printf("\n  Identifiant du compte : ")
	id := promptLine("")
	acc, ok := s.GetAccount(id)
	if !ok {
		fmt.Println("  " + red("✗ Compte introuvable."))
		pauseMenu()
		return
	}
	if _, err := s.SetEnabled(id, !acc.Enabled); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else if acc.Enabled {
		fmt.Println("  " + green("✔ Compte désactivé."))
	} else {
		fmt.Println("  " + green("✔ Compte activé."))
	}
	pauseMenu()
}

func menuUserDelete() {
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	fmt.Printf("\n  Identifiant du compte : ")
	id := promptLine("")
	fmt.Printf("  Confirmer la suppression de %s ? (o/N) : ", id)
	if !strings.EqualFold(promptLine(""), "o") {
		return
	}
	if err := s.DeleteAccount(id); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Compte supprimé."))
	}
	pauseMenu()
}

func printAbout() {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ℹ️ À PROPOS ──────────────────────────────────────────")
	fmt.Println()
	fmt.Println("  LABOSURF PRO — Plateforme VPN multi-moteurs")
	fmt.Println("  Laboratoire du FreeSurf  •  Conçu par PHILIPPO237")
	fmt.Println()
	fmt.Printf("  Moteurs enregistrés : %d\n", len(engine.Names()))
	for _, n := range engine.Names() {
		if e, err := engine.Get(n); err == nil {
			fmt.Printf("    → %-12s v%s\n", n, e.Version())
		}
	}
	fmt.Println()
	pauseMenu()
}

// ── Helpers d'affichage ────────────────────────────────────
const (
	cReset   = "\033[0m" // reset
	cDim     = "\033[2m" // gris
	cGreen   = "\033[1;32m"
	cRed     = "\033[1;31m"
	cCyan    = "\033[1;36m"
	cYellow  = "\033[1;33m"
	cMagenta = "\033[1;35m"
	cBlue    = "\033[1;34m"
	cBold    = "\033[1m"
)

func green(s string) string       { return cGreen + s + cReset }
func red(s string) string         { return cRed + s + cReset }
func cyan(s string) string        { return cCyan + s + cReset }
func dim(s string) string         { return cDim + s + cReset }
func yellow(s string) string      { return cYellow + s + cReset }
func magenta(s string) string     { return cMagenta + s + cReset }
func blue(s string) string        { return cBlue + s + cReset }
func bold(s string) string        { return cBold + s + cReset }
func name(e engine.Engine) string { return e.Name() }
func itoa(n int) string           { return fmt.Sprintf("%d", n) }

// ── Dashboard compact (boîte unique) ───────────────────────
//
// dashW est la largeur EXTÉRIEURE totale de la boîte (bordures incluses),
// choisie pour tenir dans un terminal Termux portrait étroit (~44-46
// colonnes visibles avec la police par défaut) tout en restant lisible
// en SSH/PC, où elle s'affiche simplement plus petite que la largeur du
// terminal — jamais plus grande.
const dashW = 44

func dashTop() string    { return cyan("┌" + strings.Repeat("─", dashW-2) + "┐") }
func dashMid() string    { return cyan("├" + strings.Repeat("─", dashW-2) + "┤") }
func dashBottom() string { return cyan("└" + strings.Repeat("─", dashW-2) + "┘") }

// dashLine encadre une ligne de contenu déjà mise en forme (couleurs
// incluses) dans la boîte, en la complétant à la largeur intérieure —
// le padding se base sur la longueur VISIBLE (visibleLen, dans
// sysinfo.go), pas le nombre d'octets, pour ne jamais désaligner le
// cadre à cause des séquences ANSI ou des émojis.
func dashLine(content string) string {
	inner := dashW - 4
	pad := inner - visibleLen(content)
	if pad < 0 {
		pad = 0
	}
	return cyan("│ ") + content + strings.Repeat(" ", pad) + cyan(" │")
}

// dashTwoCol assemble deux cellules sur une même ligne de la boîte,
// chacune complétée à la largeur (inner-1)/2 — utilisé pour les moteurs
// et le menu, afin que la 2e colonne démarre toujours à la même
// position quelle que soit la longueur du contenu de la 1ère.
func dashTwoCol(left, right string) string {
	inner := dashW - 4
	colW := (inner - 1) / 2
	l := left + strings.Repeat(" ", max0(colW-visibleLen(left)))
	return dashLine(l + " " + right)
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func defaultInstallConfig() engine.InstallConfig {
	return engine.InstallConfig{
		Arch:      engineutil.DetectArch(),
		DataDir:   engineutil.DefaultDataDir,
		BinaryDir: engineutil.DefaultBinaryDir,
	}
}
