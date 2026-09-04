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
func runCentralMenu() {
	for {
		clearScreen()
		printCentralHeader()
		printSystemPanel()
		fmt.Println()
		fmt.Println("  ── MENU CENTRAL ──────────────────────────────────────")
		fmt.Println()
		fmt.Println("  " + green("1") + " 🔧 GESTION DES MOTEURS")
		fmt.Println("      Installez, démarrez, arrêtez et configurez chaque moteur VPN.")
		fmt.Println("  " + green("2") + " 👥 GESTION DES UTILISATEURS")
		fmt.Println("      Comptes, durées, blocages — rattachés à un ou plusieurs moteurs.")
		fmt.Println("  " + cyan("3") + " 🖥️  ÉTAT GLOBAL")
		fmt.Println("      Récapitulatif de tous les moteurs (installés / en cours).")
		fmt.Println("  " + cyan("4") + " ⚙️  PROFIL SERVEUR")
		fmt.Println("      IP publique, domaines et ports par moteur (configs serveur + client).")
		fmt.Println("  " + cyan("9") + " ℹ️ À PROPOS")
		fmt.Println("  " + dim("0") + " ❌ QUITTER")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Choisissez une option : ")
		switch choice {
		case "1":
			runEngineMenu()
		case "2":
			runUsersMenu()
		case "3":
			runGlobalStatus()
		case "4":
			runServerProfileMenu()
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
func printCentralHeader() {
	fmt.Println()
	fmt.Println(dim("  ════════════════════════════════════════════════════════════════════════════════"))
	fmt.Println()
	fmt.Println(green("  ██╗      █████╗ ██████╗  ██████╗ ███████╗██╗   ██╗██████╗ "))
	fmt.Println(green("  ██║     ██╔══██╗██╔══██╗██╔═══██╗██╔════╝██║   ██║██╔══██╗"))
	fmt.Println(green("  ██║     ███████║██████╔╝██║   ██║███████╗██║   ██║██████╔╝"))
	fmt.Println(green("  ██║     ██╔══██║██╔══██╗██║   ██║╚════██║██║   ██║██╔══██╗"))
	fmt.Println(green("  ███████╗██║  ██║██████╔╝╚██████╔╝███████║╚██████╔╝██║  ██║"))
	fmt.Println(green("  ╚══════╝╚═╝  ╚═╝╚═════╝  ╚═════╝ ╚══════╝ ╚═════╝ ╚═╝  ╚═╝"))
	fmt.Println()
	fmt.Println(dim("  ════════════════════════════════════════════════════════════════════════════════"))
	fmt.Println(dim("  LABORATOIRE DU FREESURF  •  CONÇU PAR PHILIPPO237  •  MULTI-MOTEURS"))
	fmt.Println(dim("  ════════════════════════════════════════════════════════════════════════════════"))
}

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
// des moteurs principaux (xray, hysteria, udp, slowdns, dnstt, ssh). L'ordre
// de saisie = ordre des composants. Le guide de compatibilité liste les
// avertissements (rôles) sans interdire.
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
			menuConfigure(e)
		case "3":
			fmt.Println("\n  ▶️ Démarrage du moteur " + name(e) + "...")
			if err := e.Start(nil); err != nil {
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
			if err := e.Restart(nil); err != nil {
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
	if err := e.Install(nil, defaultInstallConfig()); err != nil {
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
	if err := e.Configure(nil, engine.EngineConfig{JSON: data}); err != nil {
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
	fmt.Println()
	pauseMenu()
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
		if len(prof.Domains) > 0 {
			fmt.Println("  Domaines         : " + strings.Join(prof.Domains, ", "))
		} else {
			fmt.Println("  Domaines         : (aucun)")
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
	cReset = "\033[0m"  // reset
	cDim   = "\033[2m"  // gris
	cGreen = "\033[1;32m"
	cRed   = "\033[1;31m"
	cCyan  = "\033[1;36m"
	cYellow = "\033[1;33m"
)

func green(s string) string   { return cGreen + s + cReset }
func red(s string) string     { return cRed + s + cReset }
func cyan(s string) string    { return cCyan + s + cReset }
func dim(s string) string     { return cDim + s + cReset }
func yellow(s string) string  { return cYellow + s + cReset }
func name(e engine.Engine) string { return e.Name() }
func itoa(n int) string       { return fmt.Sprintf("%d", n) }

func defaultInstallConfig() engine.InstallConfig {
	return engine.InstallConfig{
		Arch:      engineutil.DetectArch(),
		DataDir:   engineutil.DefaultDataDir,
		BinaryDir: engineutil.DefaultBinaryDir,
	}
}