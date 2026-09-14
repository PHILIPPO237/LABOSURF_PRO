// Gestion des Services LABOSURF PRO (menu M4).
//
// Un Service est une instance nommée et configurée d'un moteur (simple) ou
// d'une composition de moteurs déjà créée via GESTION DES MOTEURS (hybride).
// Toute la logique métier (CRUD, validation, application de config serveur)
// vit dans internal/service et internal/clientcfg (M1-M3) — cet écran ne fait
// qu'appeler ces API existantes, dans le style visuel déjà en place
// (voir menu_profiles.go pour le modèle de liste/détail repris ici).
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"labosurf/internal/clientcfg"
	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/profile"
	"labosurf/internal/service"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// runServiceMenu est le point d'entrée du menu de gestion des Services.
func runServiceMenu() {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Println("  ── 🧩 SERVICES ───────────────────────────────────────")
		fmt.Println()

		all, err := service.ListServices()
		if err != nil {
			fmt.Println("  " + red("✗ Lecture des services : "+err.Error()))
			pauseMenu()
			return
		}

		if len(all) == 0 {
			fmt.Println("  " + dim("  Aucun service. Créez-en un avec [N] ou [H]."))
		} else {
			printServiceList(all)
		}

		fmt.Println()
		fmt.Println("  " + cyan("[N]") + " Nouveau service SIMPLE   (un moteur)")
		fmt.Println("  " + cyan("[H]") + " Nouveau service HYBRIDE  (moteur composite déjà créé)")
		fmt.Println("  " + cyan("[n°]") + " Gérer un service de la liste")
		fmt.Println("  " + dim("[0]") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Services : ")
		switch strings.ToLower(choice) {
		case "0":
			return
		case "n":
			serviceCreateMenu(false)
		case "h":
			serviceCreateMenu(true)
		default:
			var num int
			if _, err := fmt.Sscanf(choice, "%d", &num); err == nil && num >= 1 && num <= len(all) {
				serviceDetailMenu(&all[num-1])
			} else {
				fmt.Println("\n  ❌ Option inconnue.")
				pauseMenu()
			}
		}
	}
}

// printServiceList affiche tous les services numérotés avec statut.
func printServiceList(all []service.Service) {
	for i, s := range all {
		kind := "simple"
		eng := s.Engine
		if s.IsHybrid() {
			kind = "hybride"
			eng = strings.Join(s.Components, "→")
		}
		bullet, label := serviceStatusColored(&s)
		fmt.Printf("  %s [%d] %-20s  %s  %s\n",
			bullet, i+1, s.Name, dim("("+kind+": "+eng+")"), label)
	}
}

// serviceStatusColored retourne le bullet et le libellé colorés selon Enabled.
func serviceStatusColored(s *service.Service) (bullet, label string) {
	if s.Enabled {
		return green(s.StatusBullet()), green(s.StatusLabel())
	}
	return dim(s.StatusBullet()), dim(s.StatusLabel())
}

// availableEnginesForService liste les moteurs disponibles pour créer un
// Service — au sens de la mission M4 : "moteur activé".
//
// Le projet ne possède AUCUNE notion "Enabled"/"Activated" propre aux moteurs
// (vérifié dans internal/engine, internal/engineutil, internal/engcfg —
// seuls Account, EngineGrant, Profile et License ont un état activé/désactivé
// explicite). engine.EngineStatus n'expose que deux états réels :
// Installed (persistant) et Running (processus démarré, transitoire).
//
// Best-effort documenté : on utilise Installed, pas Running, comme meilleure
// approximation existante d'"activé". Running exclurait à tort un moteur
// installé mais momentanément arrêté (maintenance, avant premier démarrage),
// alors que la mission demande explicitement qu'un tel moteur reste
// utilisable pour préparer un Service. Un moteur non installé est en
// revanche toujours exclu, sans exception.
//
// Limite assumée : sans champ Enabled dédié, "installé" et "activé" sont
// actuellement confondus. Introduire un vrai état Enabled par moteur serait
// une nouvelle fonctionnalité, explicitement hors périmètre de ce contrôle.
func availableEnginesForService() []string {
	var out []string
	for _, n := range engine.Names() {
		e, err := engine.Get(n)
		if err != nil {
			continue
		}
		if e.Status().Installed {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// needsDomain indique si le moteur (ou l'un des composants d'un hybride)
// nécessite un domaine (tunnels DNS).
func needsDomain(engineName string, components []string) bool {
	check := components
	if len(check) == 0 {
		check = []string{engineName}
	}
	for _, c := range check {
		if c == store.EngineDNSTT || c == store.EngineSlowDNS {
			return true
		}
	}
	return false
}

// serviceCreateMenu crée un nouveau service simple (hybrid=false) ou hybride
// (hybrid=true, nécessite qu'un moteur composite ait déjà été créé via
// GESTION DES MOTEURS → [C]).
func serviceCreateMenu(hybrid bool) {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	if hybrid {
		fmt.Println("  ── ➕ NOUVEAU SERVICE HYBRIDE ─────────────────────────")
	} else {
		fmt.Println("  ── ➕ NOUVEAU SERVICE SIMPLE ──────────────────────────")
	}
	fmt.Println()

	var candidates []string
	for _, n := range availableEnginesForService() {
		if strings.Contains(n, "-") == hybrid {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		if hybrid {
			fmt.Println("  " + yellow("⚠ Aucun moteur hybride installé. Créez-en un via GESTION DES MOTEURS → [C]."))
		} else {
			fmt.Println("  " + yellow("⚠ Aucun moteur simple installé. Installez-en un via GESTION DES MOTEURS."))
		}
		pauseMenu()
		return
	}

	fmt.Println("  Moteurs disponibles (installés) :")
	for i, n := range candidates {
		running := dim("○ arrêté")
		if e, err := engine.Get(n); err == nil && e.Status().Running {
			running = green("● en cours")
		}
		fmt.Printf("    [%d] %-16s %s\n", i+1, n, running)
	}
	fmt.Printf("\n  Moteur (n°) : ")
	var num int
	if _, err := fmt.Sscanf(promptLine(""), "%d", &num); err != nil || num < 1 || num > len(candidates) {
		fmt.Println("  " + red("✗ Choix invalide."))
		pauseMenu()
		return
	}
	engineName := candidates[num-1]

	e, err := engine.Get(engineName)
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	var components []string
	if ce, ok := e.(*engineutil.CompositeEngine); ok {
		components = ce.Components
	}

	fmt.Printf("  Nom du service : ")
	name := strings.TrimSpace(promptLine(""))
	if name == "" {
		fmt.Println("  Annulé.")
		pauseMenu()
		return
	}
	if _, found, _ := service.GetServiceByName(name); found {
		fmt.Println("  " + red("✗ Un service avec ce nom existe déjà."))
		pauseMenu()
		return
	}

	prof, _ := srvcfg.Load()
	host := prof.Host
	fmt.Printf("  Hôte (vide = %s) : ", orDim(host, "non défini"))
	if h := strings.TrimSpace(promptLine("")); h != "" {
		host = h
	}
	if host == "" {
		fmt.Println("  " + red("✗ Hôte obligatoire."))
		pauseMenu()
		return
	}

	defaultPort := prof.Port(engineName)
	fmt.Printf("  Port d'écoute (vide = %d) : ", defaultPort)
	port := defaultPort
	if p := strings.TrimSpace(promptLine("")); p != "" {
		if _, err := fmt.Sscanf(p, "%d", &port); err != nil || port <= 0 || port > 65535 {
			fmt.Println("  " + red("✗ Port invalide."))
			pauseMenu()
			return
		}
	}

	var domains []string
	if needsDomain(engineName, components) {
		def := ""
		if len(prof.Domains) > 0 {
			def = prof.Domains[0]
		}
		fmt.Printf("  Domaine (vide = %s) : ", orDim(def, "non défini"))
		d := strings.TrimSpace(promptLine(""))
		if d == "" {
			d = def
		}
		if d != "" {
			domains = []string{d}
		}
	}

	profileID := promptOptionalProfile(engineName, components)

	svc := service.NewService(name, engineName, host, map[string]int{"listen": port})
	svc.Components = components
	svc.Domains = domains
	svc.ProfileID = profileID

	if err := service.SaveService(&svc); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	fmt.Println("  " + green("✔ Service créé : "+svc.Name+" ("+svc.ID+")"))
	pauseMenu()
}

// promptOptionalProfile propose de lier le service à un profil nommé existant
// compatible (même moteur simple, ou mêmes composants hybrides) — réutilise
// le système de profils existant (internal/profile) sans dupliquer sa
// validation. Retourne "" si aucun profil n'est choisi.
//
// IMPORTANT — portée réelle de ce lien (limite connue, voir doc M4 §21) :
// ProfileID n'est qu'une RÉFÉRENCE stockée sur le Service. Les paramètres
// techniques du profil (network, security, domain, backend,
// congestion_control, etc. — internal/profile.Profile.Params) ne sont
// consultés que par profileActivate() (menu PROFILS NOMMÉS), via
// profile.BuildConfig + engine.Configure. Ni ApplyServerConfigFromAccess
// (menu SERVICES → [G]) ni GenerateFromAccess ne lisent Service.ProfileID
// ou ces Params : ils construisent la config uniquement depuis
// Access.Secrets + srvcfg.Profile. Lier un profil ici n'applique donc PAS
// automatiquement ses paramètres techniques — l'opérateur doit activer ce
// profil séparément (menu [5]) pour que Configure() en tienne compte, et
// [G] dans SERVICES écrasera ensuite la config transport avec son propre
// format (fixe pour xray : reality/tcp). Corriger cette divergence
// toucherait clientcfg/M3, hors périmètre de ce contrôle M4.
func promptOptionalProfile(engineName string, components []string) string {
	all, err := profile.ListAll()
	if err != nil || len(all) == 0 {
		return ""
	}
	var matches []profile.Profile
	for _, p := range all {
		if len(components) > 0 {
			if p.Kind == profile.KindHybrid && strings.Join(p.Components, "-") == strings.Join(components, "-") {
				matches = append(matches, p)
			}
		} else if p.Kind == profile.KindSimple && p.Engine == engineName {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	fmt.Println("  Profils nommés compatibles (optionnel — référence uniquement,")
	fmt.Println("  n'applique pas ses paramètres techniques : activez-le via [5]) :")
	for i, p := range matches {
		fmt.Printf("    [%d] %s\n", i+1, p.Name)
	}
	fmt.Printf("  Lier à un profil (n°, vide = aucun) : ")
	c := strings.TrimSpace(promptLine(""))
	if c == "" {
		return ""
	}
	var num int
	if _, err := fmt.Sscanf(c, "%d", &num); err == nil && num >= 1 && num <= len(matches) {
		return matches[num-1].ID
	}
	return ""
}

// serviceDetailMenu affiche le sous-menu d'un service sélectionné.
func serviceDetailMenu(s *service.Service) {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Printf("  ── SERVICE : %s ───────────────────────────────────\n", s.Name)
		fmt.Println()
		fmt.Printf("  ID          : %s\n", dim(s.ID))
		kind := "simple"
		if s.IsHybrid() {
			kind = "hybride"
		}
		fmt.Printf("  Type        : %s\n", kind)
		if s.IsHybrid() {
			fmt.Printf("  Composants  : %s\n", strings.Join(s.Components, " → "))
		} else {
			fmt.Printf("  Moteur      : %s\n", s.Engine)
		}
		if s.ProfileID != "" {
			fmt.Printf("  Profil      : %s %s\n", s.ProfileID, dim("(référence — activez-le via [5] pour appliquer ses paramètres)"))
		}
		fmt.Printf("  Hôte        : %s\n", orDim(s.Host, "non défini"))
		fmt.Printf("  Port        : %d\n", s.ListenPort())
		if len(s.Domains) > 0 {
			fmt.Printf("  Domaines    : %s\n", strings.Join(s.Domains, ", "))
		}
		bullet, label := serviceStatusColored(s)
		fmt.Printf("  Statut      : %s %s\n", bullet, label)
		fmt.Printf("  Créé le     : %s\n", s.CreatedAt.Format("2006-01-02 15:04"))

		accesses, _ := service.ListAccessByService(s.ID)
		fmt.Println()
		fmt.Println("  " + cyan(fmt.Sprintf("── ACCÈS ASSOCIÉS (%d) ──", len(accesses))))
		if len(accesses) == 0 {
			fmt.Println("  " + dim("  Aucun."))
		} else {
			for _, a := range accesses {
				st := "⛔"
				if a.Enabled {
					st = "✅"
				}
				exp := a.ExpiresAt
				if exp == "" {
					exp = "illimité"
				}
				q := "illimité"
				if !a.QuotaUnlimited {
					q = formatQuotaBytes(a.QuotaLimitBytes)
				}
				fmt.Printf("    %-14s %s  expire: %-17s  quota: %-10s  devices: %s  conns: %s\n",
					a.AccountID, st, exp, q, limitOrUnlimited(a.MaxDevices), limitOrUnlimited(a.MaxConnections))
			}
		}

		fmt.Println()
		fmt.Println("  " + cyan("[A]") + " Activer ce service")
		fmt.Println("  " + cyan("[D]") + " Désactiver ce service")
		fmt.Println("  " + cyan("[E]") + " Modifier (hôte / port / domaine)")
		fmt.Println("  " + cyan("[G]") + " Générer/appliquer la config serveur")
		fmt.Println("  " + red("[X]") + " Supprimer ce service")
		fmt.Println("  " + dim("[0]") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Action : ")
		switch strings.ToLower(choice) {
		case "0":
			return
		case "a":
			s.Enabled = true
			serviceSaveFeedback(s, "Service activé.")
		case "d":
			s.Enabled = false
			serviceSaveFeedback(s, "Service désactivé.")
		case "e":
			serviceEditMenu(s)
		case "g":
			serviceApplyServerConfig(s)
		case "x":
			if serviceDeleteConfirm(s) {
				return
			}
		default:
			fmt.Println("\n  ❌ Option inconnue.")
			pauseMenu()
		}
	}
}

func serviceSaveFeedback(s *service.Service, okMsg string) {
	if err := service.SaveService(s); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ "+okMsg))
	}
	pauseMenu()
}

// serviceEditMenu modifie hôte/port/domaine sans toucher au moteur, aux
// composants ni aux Access existants.
func serviceEditMenu(s *service.Service) {
	fmt.Printf("\n  Nouvel hôte (vide = inchangé, actuel : %s) : ", s.Host)
	if h := strings.TrimSpace(promptLine("")); h != "" {
		s.Host = h
	}
	fmt.Printf("  Nouveau port (vide = inchangé, actuel : %d) : ", s.ListenPort())
	if p := strings.TrimSpace(promptLine("")); p != "" {
		var port int
		if _, err := fmt.Sscanf(p, "%d", &port); err == nil && port > 0 && port <= 65535 {
			if s.Ports == nil {
				s.Ports = map[string]int{}
			}
			s.Ports["listen"] = port
		} else {
			fmt.Println("  " + red("✗ Port invalide, ignoré."))
		}
	}
	if needsDomain(s.Engine, s.Components) {
		cur := ""
		if len(s.Domains) > 0 {
			cur = s.Domains[0]
		}
		fmt.Printf("  Nouveau domaine (vide = inchangé, actuel : %s) : ", orDim(cur, "aucun"))
		if d := strings.TrimSpace(promptLine("")); d != "" {
			s.Domains = []string{d}
		}
	}
	serviceSaveFeedback(s, "Service mis à jour.")
}

// serviceApplyServerConfig applique la configuration serveur à partir des
// Access du service (nouvelle API M3 : ApplyServerConfigFromAccess).
func serviceApplyServerConfig(s *service.Service) {
	accesses, err := service.ListAccessByService(s.ID)
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	prof, err := srvcfg.Load()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	if err := clientcfg.ApplyServerConfigFromAccess(context.Background(), *s, accesses, prof); err != nil {
		fmt.Println("  " + yellow("⚠ Config serveur non appliquée : "+err.Error()))
	} else {
		fmt.Println("  " + green(fmt.Sprintf("✔ Config serveur appliquée (%d accès).", len(accesses))))
	}
	pauseMenu()
}

// serviceDeleteConfirm supprime le service après confirmation. Refuse la
// suppression si des Access en dépendent encore (garde déjà appliquée par
// service.DeleteService — ce contrôle préalable ne fait qu'afficher un
// message clair avant de tenter l'appel).
func serviceDeleteConfirm(s *service.Service) bool {
	accesses, _ := service.ListAccessByService(s.ID)
	if len(accesses) > 0 {
		fmt.Println("  " + red(fmt.Sprintf(
			"✗ Impossible : %d accès dépendent encore de ce service. Révoquez-les d'abord.", len(accesses))))
		pauseMenu()
		return false
	}
	fmt.Printf("  Confirmer la suppression de %s ? (o/N) : ", s.Name)
	if !strings.EqualFold(promptLine(""), "o") {
		return false
	}
	if err := service.DeleteService(s.ID); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return false
	}
	fmt.Println("  " + green("✔ Service supprimé."))
	pauseMenu()
	return true
}

// limitOrUnlimited affiche une limite entière ou "illimité" si <= 0.
func limitOrUnlimited(n int) string {
	if n <= 0 {
		return dim("illimité")
	}
	return fmt.Sprintf("%d", n)
}
