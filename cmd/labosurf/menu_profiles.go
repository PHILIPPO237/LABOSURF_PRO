// Gestion des profils nommés de LABOSURF PRO.
//
// Un profil est une configuration nommée (ex: Xray-Production, SlowDNS→SSH)
// pour un seul moteur (simple) ou une composition ordonnée de moteurs (hybride).
// Ce menu offre la liste, la création, la modification, la duplication,
// l'activation, la désactivation, le test (dry-run) et la suppression.
//
// Règles de sécurité appliquées :
//   - Pas de suppression d'un profil hybride actif avant désactivation.
//   - Pas de suppression d'un profil dont dépend un hybride actif.
//   - Une activation désactive automatiquement le profil précédemment actif
//     du même moteur (un seul profil actif par moteur).
//   - Validation complète (ValidateHybrid, EvaluateChain, CanConnect,
//     DetectPortConflicts) avant toute activation — aucune validation fictive.
package main

import (
	"context"
	"fmt"
	"strings"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/profile"
	"labosurf/internal/store"
)

// runProfileMenu est le point d'entrée du menu de gestion des profils.
func runProfileMenu() {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Println("  ── 📋 PROFILS NOMMÉS ─────────────────────────────────")
		fmt.Println()

		all, err := profile.ListAll()
		if err != nil {
			fmt.Println("  " + red("✗ Lecture des profils : "+err.Error()))
			pauseMenu()
			return
		}

		if len(all) == 0 {
			fmt.Println("  " + dim("  Aucun profil. Créez-en un avec [N] ou [H]."))
		} else {
			printProfileList(all)
		}

		fmt.Println()
		fmt.Println("  " + cyan("[N]") + " Nouveau profil SIMPLE     (un moteur)")
		fmt.Println("  " + cyan("[H]") + " Nouveau profil HYBRIDE    (chaîne de moteurs)")
		fmt.Println("  " + cyan("[n°]") + " Gérer un profil de la liste")
		fmt.Println("  " + dim("[0]") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Profils : ")
		switch strings.ToLower(choice) {
		case "0":
			return
		case "n":
			profileCreateSimpleMenu()
		case "h":
			profileCreateHybridMenu()
		default:
			var num int
			if _, err := fmt.Sscanf(choice, "%d", &num); err == nil && num >= 1 && num <= len(all) {
				profileDetailMenu(&all[num-1])
			} else {
				fmt.Println("\n  ❌ Option inconnue.")
				pauseMenu()
			}
		}
	}
}

// printProfileList affiche tous les profils numérotés avec statut.
func printProfileList(all []profile.Profile) {
	for i, p := range all {
		bullet := p.StatusBullet()
		label := p.StatusLabel()
		kind := "simple"
		engine := p.Engine
		if p.Kind == profile.KindHybrid {
			kind = "hybride"
			engine = strings.Join(p.Components, "→")
		}
		fmt.Printf("  %s [%d] %-20s  %s  %s %s\n",
			colorByStatus(p), i+1, p.Name, dim("("+kind+": "+engine+")"),
			bullet, colorStatusLabel(p, label))
		if p.Description != "" {
			fmt.Printf("        %s\n", dim(p.Description))
		}
	}
}

func colorByStatus(p profile.Profile) string {
	switch p.Status {
	case profile.StatusActive:
		return green("●")
	default:
		return dim("○")
	}
}

func colorStatusLabel(p profile.Profile, label string) string {
	switch p.Status {
	case profile.StatusActive:
		return green(label)
	case profile.StatusInactive:
		return dim(label)
	default:
		return yellow(label)
	}
}

// profileDetailMenu affiche le sous-menu d'un profil sélectionné.
func profileDetailMenu(p *profile.Profile) {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Printf("  ── PROFIL : %s ──────────────────────────────────────\n", p.Name)
		fmt.Println()
		fmt.Printf("  ID          : %s\n", dim(p.ID))
		fmt.Printf("  Type        : %s\n", string(p.Kind))
		if p.Kind == profile.KindSimple {
			fmt.Printf("  Moteur      : %s\n", p.Engine)
		} else {
			fmt.Printf("  Composants  : %s\n", strings.Join(p.Components, " → "))
		}
		fmt.Printf("  Statut      : %s %s\n", p.StatusBullet(), colorStatusLabel(*p, p.StatusLabel()))
		if p.Description != "" {
			fmt.Printf("  Description : %s\n", p.Description)
		}
		if len(p.Params) > 0 {
			fmt.Println()
			fmt.Println("  " + cyan("Paramètres :"))
			for k, v := range p.Params {
				fmt.Printf("    %-20s %s\n", k, v)
			}
		}
		fmt.Println()
		fmt.Println("  " + cyan("[A]") + " Activer ce profil")
		fmt.Println("  " + cyan("[D]") + " Désactiver ce profil")
		fmt.Println("  " + cyan("[T]") + " Tester (dry-run validation)")
		fmt.Println("  " + cyan("[C]") + " Dupliquer (copier vers un nouveau profil)")
		fmt.Println("  " + cyan("[E]") + " Éditer les paramètres")
		fmt.Println("  " + red("[X]") + " Supprimer ce profil")
		fmt.Println("  " + dim("[0]") + " 🔙 RETOUR")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Action : ")
		switch strings.ToLower(choice) {
		case "0":
			return
		case "a":
			profileActivate(p)
		case "d":
			profileDeactivate(p)
		case "t":
			profileDryRun(p)
		case "c":
			profileDuplicate(p)
			return // le duplicate est indépendant ; retour à la liste
		case "e":
			profileEditParams(p)
		case "x":
			if profileDelete(p) {
				return
			}
		default:
			fmt.Println("\n  ❌ Option inconnue.")
			pauseMenu()
		}
	}
}

// profileCreateSimpleMenu crée un nouveau profil simple.
func profileCreateSimpleMenu() {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ➕ NOUVEAU PROFIL SIMPLE ──────────────────────────────")
	fmt.Println()
	fmt.Println("  Moteurs disponibles :")
	engines := []string{
		store.EngineXray, store.EngineHysteria, store.EngineHysteria2,
		store.EngineSlowDNS, store.EngineDNSTT, store.EngineSSH,
		store.EngineTUIC, store.EngineWireGuard, store.EngineUDP,
	}
	for i, e := range engines {
		fmt.Printf("    [%d] %s\n", i+1, e)
	}
	fmt.Println()

	fmt.Printf("  Moteur (n° ou nom) : ")
	mc := strings.TrimSpace(promptLine(""))
	if mc == "" {
		return
	}
	engineName := mc
	var num int
	if _, err := fmt.Sscanf(mc, "%d", &num); err == nil && num >= 1 && num <= len(engines) {
		engineName = engines[num-1]
	}

	if _, ok := engineutil.GetEngineCapability(engineName); !ok {
		fmt.Println("  " + red("✗ Moteur inconnu : "+engineName))
		pauseMenu()
		return
	}

	fmt.Printf("  Nom du profil : ")
	name := strings.TrimSpace(promptLine(""))
	if name == "" {
		fmt.Println("  Annulé.")
		pauseMenu()
		return
	}
	if _, found, _ := profile.GetByName(name); found {
		fmt.Println("  " + red("✗ Un profil avec ce nom existe déjà."))
		pauseMenu()
		return
	}

	fmt.Printf("  Description (optionnel) : ")
	desc := strings.TrimSpace(promptLine(""))

	p := profile.NewSimple(name, desc, engineName, nil)
	if err := profile.Save(&p); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Profil créé : "+p.Name+" ("+p.ID+")"))
		fmt.Println("      Éditez ses paramètres avec [E] dans le détail du profil.")
	}
	pauseMenu()
}

// profileCreateHybridMenu crée un nouveau profil hybride.
func profileCreateHybridMenu() {
	clearScreen()
	printCentralHeader()

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
	fmt.Println("  ── ➕ NOUVEAU PROFIL HYBRIDE ─────────────────────────────")
	fmt.Println()
	fmt.Println("  Moteurs disponibles :")
	for i, n := range primaries {
		fmt.Printf("    [%d] %-12s → %s\n", i+1, n, engineutil.Role(n).RoleLabel())
	}
	fmt.Println()
	fmt.Println("  Choisis les composants (n° séparés par des espaces, dans l'ordre de la chaîne).")
	fmt.Printf("\n  Numéros : ")
	choices := strings.Fields(promptLine(""))
	if len(choices) < 2 {
		fmt.Println("  " + red("✗ Au moins 2 composants requis."))
		pauseMenu()
		return
	}

	var components []string
	seen := map[string]bool{}
	for _, c := range choices {
		var idx int
		if _, err := fmt.Sscanf(c, "%d", &idx); err == nil && idx >= 1 && idx <= len(primaries) {
			n := primaries[idx-1]
			if !seen[n] {
				seen[n] = true
				components = append(components, n)
			}
		}
	}
	if len(components) < 2 {
		fmt.Println("  " + red("✗ Sélection invalide."))
		pauseMenu()
		return
	}

	fmt.Println()
	fmt.Println("  Composition : " + cyan(strings.Join(components, " → ")))

	// Validation préliminaire : CompatibilityCheck (avertissements seulement)
	warns := engineutil.CompatibilityCheck(components)
	if len(warns) == 0 {
		fmt.Println("  " + green("✔ Compatibilité : aucune alerte."))
	} else {
		fmt.Println("  " + yellow("⚠ Compatibilité :"))
		for _, w := range warns {
			fmt.Println("    - " + w)
		}
	}
	fmt.Println()

	fmt.Printf("  Nom du profil : ")
	name := strings.TrimSpace(promptLine(""))
	if name == "" {
		fmt.Println("  Annulé.")
		pauseMenu()
		return
	}
	if _, found, _ := profile.GetByName(name); found {
		fmt.Println("  " + red("✗ Un profil avec ce nom existe déjà."))
		pauseMenu()
		return
	}

	fmt.Printf("  Description (optionnel) : ")
	desc := strings.TrimSpace(promptLine(""))

	p := profile.NewHybrid(name, desc, components)
	if err := profile.Save(&p); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Profil hybride créé : "+p.Name+" ("+p.ID+")"))
	}
	pauseMenu()
}

// profileDryRun lance une validation complète sans rien appliquer.
func profileDryRun(p *profile.Profile) {
	fmt.Println()
	fmt.Println("  ── 🧪 TEST (DRY-RUN) ────────────────────────────────────")
	res := profile.Validate(*p)
	if len(res.Errors) == 0 && len(res.Warnings) == 0 {
		fmt.Println("  " + green("✔ Profil valide — aucune alerte."))
	}
	for _, e := range res.Errors {
		fmt.Println("  " + red("✗ "+e))
	}
	for _, w := range res.Warnings {
		fmt.Println("  " + yellow("⚠ "+w))
	}
	if res.Report != nil {
		fmt.Println()
		if res.Report.FullyChained {
			fmt.Println("  " + green("● Chaîne entièrement câblée."))
		} else {
			fmt.Println("  " + yellow("○ Chaîne partiellement câblée (liaisons ci-dessus non relayées)."))
		}
		for _, l := range res.Report.Links {
			icon := green("✔")
			if !l.Wired {
				icon = yellow("○")
			}
			fmt.Printf("    %s %s → %s\n", icon, l.Front, l.Back)
		}
	}
	fmt.Println()
	pauseMenu()
}

// profileActivate active un profil après validation complète.
// Pour un profil simple : reconstruit le JSON et appelle e.Configure().
// Pour un profil hybride : enregistre la composition via RegisterHybridPersist.
func profileActivate(p *profile.Profile) {
	fmt.Println()
	res := profile.Validate(*p)
	if !res.OK {
		fmt.Println("  " + red("✗ Validation échouée — activation annulée :"))
		for _, e := range res.Errors {
			fmt.Println("    " + red("• "+e))
		}
		pauseMenu()
		return
	}
	for _, w := range res.Warnings {
		fmt.Println("  " + yellow("⚠ "+w))
	}

	switch p.Kind {
	case profile.KindSimple:
		s, err := openStore()
		if err != nil {
			fmt.Println("  " + red("✗ Store : "+err.Error()))
			pauseMenu()
			return
		}
		data, err := profile.BuildConfig(*p, s)
		if err != nil {
			fmt.Println("  " + red("✗ Construction config : "+err.Error()))
			pauseMenu()
			return
		}
		e, err := engine.Get(p.Engine)
		if err != nil {
			fmt.Println("  " + red("✗ Moteur introuvable : "+err.Error()))
			pauseMenu()
			return
		}
		if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
			fmt.Println("  " + red("✗ Configure : "+err.Error()))
			pauseMenu()
			return
		}
		// Désactive les autres profils du même moteur, active celui-ci.
		if err := profile.DeactivateAllForEngine(p.Engine, p.ID); err != nil {
			fmt.Println("  " + yellow("⚠ Désactivation partielle : "+err.Error()))
		}
		p.Activate()
		if err := profile.Save(p); err != nil {
			fmt.Println("  " + yellow("⚠ Persistance : "+err.Error()))
		}
		fmt.Println("  " + green("✔ Profil activé et configuration appliquée au moteur "+p.Engine+"."))

	case profile.KindHybrid:
		// Vérifie l'absence de conflit avec un hybride déjà actif sur ces moteurs.
		if _, err := engineutil.RegisterHybridPersist(p.Components); err != nil {
			fmt.Println("  " + red("✗ Enregistrement hybride : "+err.Error()))
			pauseMenu()
			return
		}
		p.Activate()
		if err := profile.Save(p); err != nil {
			fmt.Println("  " + yellow("⚠ Persistance : "+err.Error()))
		}
		fmt.Println("  " + green("✔ Profil hybride activé : "+strings.Join(p.Components, "→")+"."))
	}
	pauseMenu()
}

// profileDeactivate marque le profil inactif.
// Pour un hybride actif : appelle aussi RemoveHybrid pour retirer le moteur composé.
func profileDeactivate(p *profile.Profile) {
	if !p.IsActive() {
		fmt.Println("\n  Profil déjà inactif.")
		pauseMenu()
		return
	}
	if p.Kind == profile.KindHybrid {
		hybridName := engineutil.HybridName(p.Components)
		if err := engineutil.RemoveHybrid(hybridName); err != nil {
			fmt.Println("  " + yellow("⚠ Suppression moteur hybride : "+err.Error()))
		}
	}
	p.Deactivate()
	if err := profile.Save(p); err != nil {
		fmt.Println("  " + red("✗ Persistance : "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Profil désactivé."))
	}
	pauseMenu()
}

// profileDuplicate crée une copie du profil sous un nouveau nom.
func profileDuplicate(p *profile.Profile) {
	fmt.Printf("\n  Nom de la copie : ")
	name := strings.TrimSpace(promptLine(""))
	if name == "" {
		fmt.Println("  Annulé.")
		pauseMenu()
		return
	}
	if _, found, _ := profile.GetByName(name); found {
		fmt.Println("  " + red("✗ Un profil avec ce nom existe déjà."))
		pauseMenu()
		return
	}
	dup := p.Duplicate(name)
	if err := profile.Save(&dup); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Copie créée : "+dup.Name+" ("+dup.ID+")"))
	}
	pauseMenu()
}

// profileEditParams permet de modifier les paramètres clé/valeur du profil.
func profileEditParams(p *profile.Profile) {
	for {
		clearScreen()
		printCentralHeader()
		fmt.Println()
		fmt.Printf("  ── ÉDITION DES PARAMÈTRES : %s ──────────────────────\n", p.Name)
		fmt.Println()
		if len(p.Params) == 0 {
			fmt.Println("  " + dim("  (aucun paramètre)"))
		} else {
			i := 1
			keys := sortedKeys(p.Params)
			for _, k := range keys {
				fmt.Printf("    [%d] %-20s = %s\n", i, k, p.Params[k])
				i++
			}
		}
		fmt.Println()
		fmt.Println("  " + cyan("[+]") + " Ajouter / modifier un paramètre")
		fmt.Println("  " + red("[-]") + " Supprimer un paramètre")
		fmt.Println("  " + dim("[0]") + " Sauvegarder et retour")
		fmt.Println()

		choice := promptLine(green("LABOSURF PRO ►") + " Action : ")
		switch choice {
		case "0":
			if err := profile.Save(p); err != nil {
				fmt.Println("  " + red("✗ Sauvegarde : "+err.Error()))
				pauseMenu()
			}
			return
		case "+":
			fmt.Printf("  Clé   : ")
			k := strings.TrimSpace(promptLine(""))
			if k == "" {
				continue
			}
			fmt.Printf("  Valeur [%s] : ", p.Param(k, ""))
			v := strings.TrimSpace(promptLine(""))
			p.SetParam(k, v)
		case "-":
			fmt.Printf("  Clé à supprimer : ")
			k := strings.TrimSpace(promptLine(""))
			delete(p.Params, k)
		default:
			fmt.Println("  ❌ Option inconnue.")
			pauseMenu()
		}
	}
}

// profileDelete supprime le profil après vérification des dépendances.
// Retourne true si supprimé (le caller doit fermer le détail menu).
func profileDelete(p *profile.Profile) bool {
	if p.IsActive() {
		fmt.Println("\n  " + red("✗ Impossible de supprimer un profil actif. Désactivez-le d'abord."))
		pauseMenu()
		return false
	}

	// Vérifier les dépendances hybrides
	deps, err := profile.Dependents(p.Engine)
	if err != nil {
		fmt.Println("  " + red("✗ Vérification dépendances : "+err.Error()))
		pauseMenu()
		return false
	}
	activeDeps := filterActive(deps)
	if len(activeDeps) > 0 {
		fmt.Println("\n  " + red("✗ Ce moteur est utilisé par un ou plusieurs profils hybrides actifs :"))
		for _, d := range activeDeps {
			fmt.Printf("    • %s (%s)\n", d.Name, strings.Join(d.Components, "→"))
		}
		fmt.Println("      Désactivez ces profils hybrides avant de supprimer.")
		pauseMenu()
		return false
	}

	fmt.Printf("\n  Confirmer la suppression de %q ? (o/N) : ", p.Name)
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("  Annulé.")
		pauseMenu()
		return false
	}

	if err := profile.Delete(p.ID); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return false
	}
	fmt.Println("  " + green("✔ Profil supprimé."))
	pauseMenu()
	return true
}

// filterActive retourne les profils dont le statut est actif.
func filterActive(profiles []profile.Profile) []profile.Profile {
	var out []profile.Profile
	for _, p := range profiles {
		if p.IsActive() {
			out = append(out, p)
		}
	}
	return out
}

// sortedKeys retourne les clés d'une map string→string triées.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Tri simple par insertion (les params sont toujours peu nombreux).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
