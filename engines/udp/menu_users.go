package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const menuStorePath = defaultStorePath

func menuReader() *bufio.Reader { return bufio.NewReader(os.Stdin) }

func promptLine(r *bufio.Reader, label string) string {
	fmt.Printf("  %s", label)
	v, _ := r.ReadString('\n')
	return strings.TrimSpace(v)
}

func pauseMenu() {
	fmt.Print("\n  ↩️  APPUYEZ SUR ENTRÉE POUR REVENIR...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func runUserMenu() {
	for {
		fmt.Print("\033[2J\033[H")
		printBanner()
		fmt.Println("── 👥 GESTION DES UTILISATEURS ────────────────────────")
		fmt.Println()
		fmt.Println("  ➕ 1. CRÉER UN UTILISATEUR")
		fmt.Println("      Créez un nouvel accès client UDP.")
		fmt.Println("  📋 2. LISTE DES UTILISATEURS")
		fmt.Println("      Consultez tous les comptes du serveur.")
		fmt.Println("  🔎 3. RECHERCHER UN UTILISATEUR")
		fmt.Println("      Trouvez rapidement un compte.")
		fmt.Println("  👁️ 4. AFFICHER LES DÉTAILS")
		fmt.Println("      Consultez les informations et identifiants client.")
		fmt.Println("  ⏳ 5. AJOUTER DU TEMPS")
		fmt.Println("      Prolongez la validité d'un compte.")
		fmt.Println("  🔒 6. BLOQUER UN UTILISATEUR")
		fmt.Println("      Refusez temporairement l'accès sans supprimer le compte.")
		fmt.Println("  🔓 7. DÉBLOQUER UN UTILISATEUR")
		fmt.Println("      Réactivez un compte précédemment bloqué.")
		fmt.Println("  🗑️ 8. SUPPRIMER UN UTILISATEUR")
		fmt.Println("      Supprimez un compte du serveur.")
		fmt.Println("  ↩️ 0. RETOUR")
		fmt.Println()
		choice := promptLine(menuReader(), "LABOSURF PRO ► CHOISISSEZ UNE OPTION : ")
		switch choice {
		case "1":
			interactiveCreateUser()
		case "2":
			adminList(nil)
			pauseMenu()
		case "3":
			interactiveSearchUser()
		case "4":
			interactiveShowUser()
		case "5":
			interactiveRenewUser()
		case "6":
			interactiveDisableUser()
		case "7":
			interactiveEnableUser()
		case "8":
			interactiveDeleteUser()
		case "0":
			return
		default:
			fmt.Println("\n  ❌ OPTION INCONNUE.")
			pauseMenu()
		}
	}
}

func interactiveCreateUser() {
	fmt.Print("\033[2J\033[H")
	printBanner()
	fmt.Println("── ➕ CRÉER UN UTILISATEUR ─────────────────────────────")
	fmt.Println("  Créez un accès UDP et obtenez immédiatement la fiche client.")
	fmt.Println()
	r := menuReader()
	id := promptLine(r, "👤 IDENTIFIANT : ")
	if id == "" {
		fmt.Println("  ❌ IDENTIFIANT OBLIGATOIRE.")
		pauseMenu()
		return
	}
	password := promptLine(r, "🔐 MOT DE PASSE (VIDE = GÉNÉRÉ) : ")
	daysS := promptLine(r, "⏳ DURÉE EN JOURS (0 = ILLIMITÉ) : ")
	quotaS := promptLine(r, "📊 QUOTA EN GB (0 = ILLIMITÉ) : ")
	maxConnS := promptLine(r, "🔗 CONNEXIONS SIMULTANÉES MAX (1) : ")
	maxIPS := promptLine(r, "🌐 NOMBRE D'IP AUTORISÉES (1, 2, 5... / 0 = ILLIMITÉ) : ")

	days, _ := strconv.Atoi(daysS)
	quotaGB, _ := strconv.ParseUint(quotaS, 10, 64)
	maxConn, _ := strconv.Atoi(maxConnS)
	if maxConn <= 0 {
		maxConn = 1
	}
	maxIPs, _ := strconv.Atoi(maxIPS)
	if maxIPs < 0 {
		maxIPs = 1
	}
	quotaBytes := quotaGB * 1024 * 1024 * 1024

	s, err := LoadStore(menuStorePath)
	if err != nil {
		fmt.Printf("\n  ❌ %v\n", err)
		pauseMenu()
		return
	}
	acc, err := s.CreateAccount(Account{ID: id, Password: password, ExpiresAt: expiryFromDays(days), QuotaBytes: quotaBytes, MaxConnections: maxConn, MaxIPs: maxIPs, Enabled: true})
	if err != nil {
		fmt.Printf("\n  ❌ %v\n", err)
		pauseMenu()
		return
	}

	fmt.Println()
	fmt.Println("  ╭────────────────────────────────────────────────╮")
	fmt.Println("  │            ✅ UTILISATEUR CRÉÉ                 │")
	fmt.Println("  ╰────────────────────────────────────────────────╯")
	printClientCredentials(acc)
	pauseMenu()
}

func interactiveSearchUser() {
	r := menuReader()
	id := promptLine(r, "🔎 IDENTIFIANT : ")
	s, err := LoadStore(menuStorePath)
	if err != nil {
		fmt.Println("  ❌", err)
		pauseMenu()
		return
	}
	acc, ok := s.GetAccount(id)
	if !ok {
		fmt.Println("  ❌ UTILISATEUR INTROUVABLE.")
		pauseMenu()
		return
	}
	printAccount(acc)
	pauseMenu()
}

func interactiveShowUser() { interactiveSearchUser() }

func interactiveRenewUser() {
	r := menuReader()
	id := promptLine(r, "👤 IDENTIFIANT : ")
	days := promptLine(r, "⏳ JOURS À AJOUTER : ")
	n, err := strconv.Atoi(days)
	if err != nil || n <= 0 {
		fmt.Println("  ❌ DURÉE INVALIDE.")
		pauseMenu()
		return
	}
	if err := adminRenew([]string{"-id", id, "-days", strconv.Itoa(n), "-store", menuStorePath}); err != nil {
		fmt.Println("  ❌", err)
	}
	pauseMenu()
}

func interactiveDisableUser() {
	r := menuReader()
	id := promptLine(r, "🔒 IDENTIFIANT À BLOQUER : ")
	if err := adminSetEnabled([]string{"-id", id, "-store", menuStorePath}, false); err != nil {
		fmt.Println("  ❌", err)
	}
	pauseMenu()
}

func interactiveEnableUser() {
	r := menuReader()
	id := promptLine(r, "🔓 IDENTIFIANT À DÉBLOQUER : ")
	if err := adminSetEnabled([]string{"-id", id, "-store", menuStorePath}, true); err != nil {
		fmt.Println("  ❌", err)
	}
	pauseMenu()
}

func interactiveDeleteUser() {
	r := menuReader()
	id := promptLine(r, "🗑️ IDENTIFIANT À SUPPRIMER : ")
	confirm := promptLine(r, "TAPEZ SUPPRIMER POUR CONFIRMER : ")
	if strings.ToUpper(confirm) != "SUPPRIMER" {
		fmt.Println("  ↩️ ANNULÉ.")
		pauseMenu()
		return
	}
	if err := adminDelete([]string{"-id", id, "-store", menuStorePath}); err != nil {
		fmt.Println("  ❌", err)
	}
	pauseMenu()
}

func printClientCredentials(a Account) {
	exp := a.ExpiresAt
	if exp == "" {
		exp = "ILLIMITÉ"
	}
	quota := "ILLIMITÉ"
	if a.QuotaBytes > 0 {
		quota = FormatBytes(a.QuotaBytes)
	}
	srv := serverAddress()
	fmt.Println("  ── 📋 FICHE CLIENT ───────────────────────────────")
	fmt.Println()
	fmt.Println("  🔗 INFORMATIONS DE CONNEXION")
	fmt.Printf("  ├─ Serveur       : %s\n", srv)
	fmt.Printf("  ├─ Port UDP      : 5667\n")
	fmt.Printf("  ├─ Protocole     : UDP\n")
	fmt.Printf("  └─ Réseau VPN    : 10.77.0.0/24\n")
	fmt.Println()
	fmt.Println("  👤 IDENTIFIANTS")
	fmt.Printf("  ├─ Utilisateur   : %s\n", a.ID)
	fmt.Printf("  └─ Mot de passe  : %s\n", a.Password)
	fmt.Println()
	fmt.Println("  📊 LIMITES")
	fmt.Printf("  ├─ Expiration    : %s\n", exp)
	fmt.Printf("  ├─ Quota         : %s\n", quota)
	fmt.Printf("  ├─ Connexions    : %d simultanée(s)\n", a.MaxConnections)
	fmt.Printf("  └─ IPs max       : %d\n", a.MaxIPs)
	if a.Token != "" {
		fmt.Println()
		fmt.Println("  🌐 PORTAIL")
		fmt.Printf("  └─ Lien          : %s\n", formatLink("http://"+srv+":8080", a.Token))
	}
	fmt.Println()
	fmt.Println("  ℹ️ ENTREZ CES INFORMATIONS DANS VOTRE CLIENT VPN COMPATIBLE.")
}

func printStatisticsMenu() {
	fmt.Println("\n  📊 STATISTIQUES — la vue détaillée sera reliée aux métriques réelles de l'UDP Engine.")
	pauseMenu()
}
func printPortalMenu() {
	fmt.Println("\n  🌐 PORTAIL CLIENT — utilisez les commandes admin `token-new` et `link` pour gérer les liens.")
	pauseMenu()
}
func printServerStatusMenu() {
	srv := serverAddress()
	fmt.Println()
	fmt.Println("  ── 🖥️ ÉTAT DU SERVEUR ──────────────────────────────")
	fmt.Printf("  Adresse publique : %s\n", srv)
	fmt.Printf("  Port UDP         : 5667\n")
	fmt.Printf("  Port portail     : 8080\n")
	fmt.Printf("  Réseau VPN       : 10.77.0.0/24\n")
	fmt.Printf("  TUN              : labosurf0 (10.77.0.1)\n")
	fmt.Println()
	fmt.Println("  Commandes utiles :")
	fmt.Println("  • systemctl status labosurf   — état du service")
	fmt.Println("  • journalctl -u labosurf -f   — logs en direct")
	fmt.Println("  • ss -ulnp | grep 5667        — port UDP actif")
	pauseMenu()
}
func printUpdateMenu() {
	fmt.Println("\n  🔄 MISE À JOUR — la procédure de release doit être configurée avant toute mise à jour automatique.")
	pauseMenu()
}
func printConfigMenu() {
	fmt.Println("\n  ⚙️ CONFIGURATION — /etc/labosurf/config.json")
	pauseMenu()
}
func printBackupMenu() {
	fmt.Println("\n  💾 SAUVEGARDE — les données principales sont dans /etc/labosurf/users_db.json.")
	pauseMenu()
}

// serverAddress retourne l'adresse IPv4 d'écoute sortante du VPS.
// LABOSURF_PUBLIC_IP permet de définir explicitement l'IP publique si le VPS
// est derrière NAT ou possède plusieurs interfaces. Sinon on détermine
// l'adresse locale utilisée pour joindre une destination Internet.
func serverAddress() string {
	if v := strings.TrimSpace(os.Getenv("LABOSURF_PUBLIC_IP")); v != "" {
		return v
	}
	conn, err := net.Dial("udp", "1.1.1.1:53")
	if err == nil {
		defer conn.Close()
		if host, _, err := net.SplitHostPort(conn.LocalAddr().String()); err == nil && host != "" {
			return host
		}
	}
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
					return ipnet.IP.To4().String()
				}
			}
		}
	}
	return "NON DÉTECTÉE"
}
