// Gestion des Access LABOSURF PRO (menu M4).
//
// Un Access représente le droit d'un ABONNÉ sur un SERVICE précis. Toute la
// logique métier (secrets, génération client, config serveur, migration)
// vit dans internal/service et internal/clientcfg (M1-M3) — cet écran ne fait
// qu'appeler ces API existantes, dans le style visuel déjà en place
// (voir la section « Gestion des utilisateurs » de menu.go pour le modèle).
package main

import (
	"fmt"
	"sort"
	"strings"

	"labosurf/internal/clientcfg"
	"labosurf/internal/service"
	"labosurf/internal/store"
)

// accessRow associe un Access au nom lisible de son service, pour l'affichage.
type accessRow struct {
	access  service.Access
	svcName string
}

// runAccessMenu est le point d'entrée du menu de gestion des Access.
func runAccessMenu() {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Println("  ── 🔑 ACCÈS ──────────────────────────────────────────")
		fmt.Println()

		rows, err := listAllAccessSorted()
		if err != nil {
			fmt.Println("  " + red("✗ Lecture des accès : "+err.Error()))
			pauseMenu()
			return
		}
		if len(rows) == 0 {
			fmt.Println("  " + dim("  Aucun accès. Créez-en un avec [N]."))
		} else {
			printAccessList(rows)
		}

		fmt.Println()
		fmt.Println("  " + cyan("[N]") + " Nouvel accès (abonné → service)")
		fmt.Println("  " + cyan("[M]") + " Migrer les Grants existants vers Access")
		fmt.Println("  " + cyan("[n°]") + " Gérer un accès de la liste")
		fmt.Println("  " + dim("[0]") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Accès : ")
		switch strings.ToLower(choice) {
		case "0":
			return
		case "n":
			accessCreateMenu()
		case "m":
			accessMigrateMenu()
		default:
			var num int
			if _, err := fmt.Sscanf(choice, "%d", &num); err == nil && num >= 1 && num <= len(rows) {
				accessDetailMenu(&rows[num-1])
			} else {
				fmt.Println("\n  ❌ Option inconnue.")
				pauseMenu()
			}
		}
	}
}

// listAllAccessSorted charge tous les Access et résout le nom de leur
// service, triés par abonné puis par service (affichage stable).
func listAllAccessSorted() ([]accessRow, error) {
	accesses, err := service.ListAllAccess()
	if err != nil {
		return nil, err
	}
	svcCache := map[string]string{}
	rows := make([]accessRow, 0, len(accesses))
	for _, a := range accesses {
		name, ok := svcCache[a.ServiceID]
		if !ok {
			if s, err := service.GetService(a.ServiceID); err == nil {
				name = s.Name
			}
			svcCache[a.ServiceID] = name
		}
		rows = append(rows, accessRow{access: a, svcName: name})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].access.AccountID != rows[j].access.AccountID {
			return rows[i].access.AccountID < rows[j].access.AccountID
		}
		return rows[i].svcName < rows[j].svcName
	})
	return rows, nil
}

func printAccessList(rows []accessRow) {
	for i, r := range rows {
		a := r.access
		bullet, label := accessStatusColored(&a)
		svcLabel := r.svcName
		if svcLabel == "" {
			svcLabel = dim("(service introuvable)")
		}
		fmt.Printf("  %s [%d] %-14s → %-20s (%-12s) %s\n",
			bullet, i+1, a.AccountID, svcLabel, a.Engine, label)
	}
}

func accessStatusColored(a *service.Access) (bullet, label string) {
	if a.Enabled {
		return green(a.StatusBullet()), green("actif")
	}
	return dim(a.StatusBullet()), dim("désactivé")
}

// accessCreateMenu implémente le workflow de création d'un Access :
// choisir l'abonné → choisir le service → configurer la politique (quota,
// limites, expiration) → activer ou non → générer les secrets manquants.
func accessCreateMenu() {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ➕ NOUVEL ACCÈS ───────────────────────────────────────")
	fmt.Println()

	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	accounts := s.ListAccounts()
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun abonné. Créez-en un via GESTION DES UTILISATEURS."))
		pauseMenu()
		return
	}
	fmt.Println("  Abonnés :")
	for i, a := range accounts {
		fmt.Printf("    [%d] %-14s %s\n", i+1, a.ID, dim(a.Username))
	}
	fmt.Printf("\n  Abonné (n°) : ")
	var accNum int
	if _, err := fmt.Sscanf(promptLine(""), "%d", &accNum); err != nil || accNum < 1 || accNum > len(accounts) {
		fmt.Println("  " + red("✗ Choix invalide."))
		pauseMenu()
		return
	}
	account := accounts[accNum-1]

	services, err := service.ListServices()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	var enabledServices []service.Service
	for _, svc := range services {
		if svc.Enabled {
			enabledServices = append(enabledServices, svc)
		}
	}
	if len(enabledServices) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun service actif. Créez/activez-en un via GESTION DES SERVICES."))
		pauseMenu()
		return
	}
	fmt.Println()
	fmt.Println("  Services actifs :")
	for i, svc := range enabledServices {
		eng := svc.Engine
		if svc.IsHybrid() {
			eng = strings.Join(svc.Components, "→")
		}
		fmt.Printf("    [%d] %-20s (%s)\n", i+1, svc.Name, eng)
	}
	fmt.Printf("\n  Service (n°) : ")
	var svcNum int
	if _, err := fmt.Sscanf(promptLine(""), "%d", &svcNum); err != nil || svcNum < 1 || svcNum > len(enabledServices) {
		fmt.Println("  " + red("✗ Choix invalide."))
		pauseMenu()
		return
	}
	svc := enabledServices[svcNum-1]

	if exists, _, _ := service.AccessExists(account.ID, svc.ID); exists {
		fmt.Println("  " + red("✗ Un accès existe déjà pour cet abonné sur ce service."))
		pauseMenu()
		return
	}

	a := service.NewAccess(account.ID, svc.ID, svc.Engine)

	fmt.Println()
	fmt.Println("  Quota :")
	fmt.Println("    [1] Illimité")
	fmt.Println("    [2] Limité")
	fmt.Printf("  Choix (défaut illimité) : ")
	qc := strings.TrimSpace(promptLine(""))
	if qc == "" {
		qc = "1"
	}
	gbInput := ""
	if qc == "2" {
		fmt.Printf("  Nombre de Go : ")
		gbInput = promptLine("")
	}
	if err := applyQuotaChoice(&a, qc, gbInput); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	fmt.Printf("\n  MaxDevices (appareils autorisés, 0 = illimité, vide = 0) : ")
	if v := strings.TrimSpace(promptLine("")); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &a.MaxDevices)
	}
	fmt.Printf("  MaxConnections (connexions simultanées, 0 = illimité, vide = 0) : ")
	if v := strings.TrimSpace(promptLine("")); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &a.MaxConnections)
	}
	fmt.Printf("  MaxSourceIPs (IP sources simultanées, 0 = illimité, vide = 0) : ")
	if v := strings.TrimSpace(promptLine("")); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &a.MaxSourceIPs)
	}

	fmt.Printf("\n  Expiration (jours à partir de maintenant, vide = aucune) : ")
	if v := strings.TrimSpace(promptLine("")); v != "" {
		var days int
		if _, err := fmt.Sscanf(v, "%d", &days); err == nil && days > 0 {
			a.ExpiresAt = store.ExpiryFromDays(days)
		}
	}

	fmt.Printf("\n  Activer maintenant ? (O/n) : ")
	activate := promptLine("")
	a.Enabled = activate == "" || strings.EqualFold(activate, "o")

	if err := service.EnsureAccessSecrets(&a, nil); err != nil {
		fmt.Println("  " + red("✗ Génération des secrets : "+err.Error()))
		pauseMenu()
		return
	}
	if err := service.SaveAccess(&a); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	fmt.Println("  " + green("✔ Accès créé : ") + a.ID)
	pauseMenu()
}

// accessDetailMenu affiche le sous-menu d'un accès sélectionné.
func accessDetailMenu(row *accessRow) {
	for {
		a := &row.access
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Printf("  ── ACCÈS : %s → %s ─────────────────────────────\n", a.AccountID, orDim(row.svcName, "service introuvable"))
		fmt.Println()
		fmt.Printf("  ID          : %s\n", dim(a.ID))
		fmt.Printf("  Abonné      : %s\n", a.AccountID)
		fmt.Printf("  Service     : %s (%s)\n", orDim(row.svcName, "introuvable"), a.Engine)
		bullet, label := accessStatusColored(a)
		fmt.Printf("  Statut      : %s %s\n", bullet, label)
		if a.QuotaUnlimited {
			fmt.Printf("  Quota       : illimité (utilisé : %s)\n", formatQuotaBytes(uint64(a.UsedBytes)))
		} else {
			fmt.Printf("  Quota       : %s / %s\n", formatQuotaBytes(uint64(a.UsedBytes)), formatQuotaBytes(a.QuotaLimitBytes))
		}
		fmt.Printf("  MaxDevices     : %s\n", limitOrUnlimited(a.MaxDevices))
		fmt.Printf("  MaxConnections : %s\n", limitOrUnlimited(a.MaxConnections))
		fmt.Printf("  MaxSourceIPs   : %s\n", limitOrUnlimited(a.MaxSourceIPs))
		fmt.Printf("  Expiration  : %s\n", orDim(a.ExpiresAt, "aucune"))
		fmt.Printf("  Créé le     : %s\n", a.CreatedAt.Format("2006-01-02 15:04"))

		fmt.Println()
		fmt.Println("  " + cyan("[A]") + " Activer")
		fmt.Println("  " + cyan("[D]") + " Désactiver (révoquer)")
		fmt.Println("  " + cyan("[R]") + " Renouveler (expiration)")
		fmt.Println("  " + cyan("[Q]") + " Modifier le quota")
		fmt.Println("  " + cyan("[L]") + " Modifier les limites (devices/connexions/IPs)")
		fmt.Println("  " + cyan("[C]") + " Générer la configuration client")
		fmt.Println("  " + red("[X]") + " Supprimer cet accès")
		fmt.Println("  " + dim("[0]") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Action : ")
		switch strings.ToLower(choice) {
		case "0":
			return
		case "a":
			a.Enabled = true
			saveAccessFeedback(a, "Accès activé.")
		case "d":
			a.Enabled = false
			saveAccessFeedback(a, "Accès désactivé (révoqué).")
		case "r":
			accessRenew(a)
		case "q":
			accessEditQuota(a)
		case "l":
			accessEditLimits(a)
		case "c":
			accessGenerateClientConfig(a, row.svcName)
		case "x":
			if accessDeleteConfirm(a) {
				return
			}
		default:
			fmt.Println("\n  ❌ Option inconnue.")
			pauseMenu()
		}
	}
}

func saveAccessFeedback(a *service.Access, okMsg string) {
	if err := service.SaveAccess(a); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ "+okMsg))
	}
	pauseMenu()
}

// accessRenew modifie uniquement l'expiration — jamais les secrets, l'UUID,
// le mot de passe ou le service (mission M4 §15 : un renouvellement ne doit
// pas générer de nouveaux credentials).
func accessRenew(a *service.Access) {
	fmt.Printf("\n  Nouvelle expiration en jours à partir de maintenant (vide = illimité) : ")
	v := strings.TrimSpace(promptLine(""))
	if v == "" {
		a.ExpiresAt = ""
	} else {
		var days int
		if _, err := fmt.Sscanf(v, "%d", &days); err != nil || days <= 0 {
			fmt.Println("  " + red("✗ Nombre de jours invalide."))
			pauseMenu()
			return
		}
		a.ExpiresAt = store.ExpiryFromDays(days)
	}
	saveAccessFeedback(a, "Expiration mise à jour.")
}

// accessEditQuota délègue à applyQuotaChoice (logique testée séparément).
func accessEditQuota(a *service.Access) {
	fmt.Println("\n  Quota :")
	fmt.Println("    [1] Illimité")
	fmt.Println("    [2] Limité")
	fmt.Printf("  Choix : ")
	choice := strings.TrimSpace(promptLine(""))
	gbInput := ""
	if choice == "2" {
		fmt.Printf("  Nombre de Go : ")
		gbInput = promptLine("")
	}
	if err := applyQuotaChoice(a, choice, gbInput); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	saveAccessFeedback(a, "Quota mis à jour.")
}

// accessEditLimits modifie MaxDevices / MaxConnections / MaxSourceIPs
// indépendamment — ces trois notions ne doivent jamais être confondues.
func accessEditLimits(a *service.Access) {
	fmt.Printf("\n  MaxDevices (appareils autorisés, actuel %s, vide = inchangé) : ", limitOrUnlimited(a.MaxDevices))
	if v := strings.TrimSpace(promptLine("")); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &a.MaxDevices)
	}
	fmt.Printf("  MaxConnections (connexions simultanées, actuel %s, vide = inchangé) : ", limitOrUnlimited(a.MaxConnections))
	if v := strings.TrimSpace(promptLine("")); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &a.MaxConnections)
	}
	fmt.Printf("  MaxSourceIPs (IP sources simultanées, actuel %s, vide = inchangé) : ", limitOrUnlimited(a.MaxSourceIPs))
	if v := strings.TrimSpace(promptLine("")); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &a.MaxSourceIPs)
	}
	saveAccessFeedback(a, "Limites mises à jour.")
}

// accessGenerateClientConfig utilise directement clientcfg.GenerateFromAccess
// — aucune logique de génération n'est dupliquée dans l'écran.
func accessGenerateClientConfig(a *service.Access, svcName string) {
	svc, err := service.GetService(a.ServiceID)
	if err != nil {
		fmt.Println("  " + red("✗ Service introuvable : "+err.Error()))
		pauseMenu()
		return
	}

	username := a.AccountID
	if st, err := openStore(); err == nil {
		if acc, ok := st.GetAccount(a.AccountID); ok && acc.Username != "" {
			username = acc.Username
		}
	}

	res, err := clientcfg.GenerateFromAccess(*a, svc, username)
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── 📤 CONFIG CLIENT — " + svc.Name + " ─────────────────────")
	fmt.Println()
	fmt.Println("  Abonné  : " + a.AccountID)
	fmt.Println("  Service : " + svc.Name + " (" + res.Engine + ")")
	fmt.Println("  Hôte    : " + svc.Host)
	fmt.Println()
	fmt.Println("  " + green("▸ CONFIGURATION CLIENT :"))
	fmt.Println("    " + wrapLine(res.ClientLink, 4))
	fmt.Println()
	pauseMenu()
}

// accessDeleteConfirm supprime l'accès après confirmation. Ne touche jamais
// au Service ni à un autre Access (la suppression est strictement locale à
// cet enregistrement).
func accessDeleteConfirm(a *service.Access) bool {
	fmt.Printf("\n  Confirmer la suppression de l'accès %s (%s) ? (o/N) : ", a.ID, a.AccountID)
	if !strings.EqualFold(promptLine(""), "o") {
		return false
	}
	if err := service.DeleteAccess(a.ID); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return false
	}
	fmt.Println("  " + green("✔ Accès supprimé."))
	pauseMenu()
	return true
}

// accessMigrateMenu lance la migration Grants → Access (M3), toujours après
// confirmation explicite — jamais automatique au démarrage. Idempotente :
// peut être relancée sans effet de bord sur les Access déjà migrés.
func accessMigrateMenu() {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── 🔁 MIGRATION GRANTS → ACCÈS ───────────────────────────")
	fmt.Println()
	fmt.Println("  Cette opération crée un Access pour chaque Grant existant qui n'a")
	fmt.Println("  pas encore d'Access correspondant. Elle est idempotente et ne")
	fmt.Println("  supprime, ni ne modifie, aucun Grant existant.")
	fmt.Println()
	fmt.Printf("  Lancer la migration ? (o/N) : ")
	if !strings.EqualFold(promptLine(""), "o") {
		return
	}

	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	results := service.MigrateGrantsToAccess(s.ListAccounts())
	created, skipped, failed := service.MigrateStats(results)

	fmt.Println()
	fmt.Println("  " + green(fmt.Sprintf("✔ Créés : %d", created)) +
		"   " + dim(fmt.Sprintf("Ignorés (déjà migrés) : %d", skipped)) +
		"   " + red(fmt.Sprintf("Échecs : %d", failed)))
	if failed > 0 {
		fmt.Println()
		fmt.Println("  Détail des échecs :")
		for _, r := range results {
			if r.Status == service.MigrateFailed {
				fmt.Printf("    - %s / %s : %s\n", r.AccountID, r.Engine, r.Err)
			}
		}
	}
	pauseMenu()
}
