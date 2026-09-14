package profile

import (
	"fmt"
	"strings"

	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
)

// ValidationResult résume la validation d'un profil avant activation.
// Toutes les vérifications sont documentées : aucun résultat "positif" fictif.
type ValidationResult struct {
	OK       bool
	Errors   []string
	Warnings []string
	Report   *engineutil.ChainReport // non nil uniquement pour les profils hybrides
}

// Validate vérifie qu'un profil peut être activé en toute sécurité.
// Pour les profils simples : vérifie que le moteur est connu.
// Pour les profils hybrides : utilise ValidateHybrid, CanConnect, EvaluateChain,
// DetectPortConflicts — logique existante, aucune duplication.
func Validate(p Profile) ValidationResult {
	switch p.Kind {
	case KindSimple:
		return validateSimple(p)
	case KindHybrid:
		return validateHybrid(p)
	default:
		return ValidationResult{
			OK:     false,
			Errors: []string{fmt.Sprintf("type de profil inconnu : %q", string(p.Kind))},
		}
	}
}

func validateSimple(p Profile) ValidationResult {
	var errs []string
	var warns []string
	if p.Name == "" {
		errs = append(errs, "le profil n'a pas de nom")
	}
	if p.Engine == "" {
		errs = append(errs, "le champ engine est vide")
	} else if _, ok := engineutil.GetEngineCapability(p.Engine); !ok {
		// Moteur hors de la matrice de compatibilité (ex: freeway-gate) : ce n'est
		// pas une erreur pour un profil simple — la validité réelle est vérifiée à
		// l'activation via engine.Get. On avertit simplement l'opérateur.
		warns = append(warns, fmt.Sprintf(
			"moteur %q absent de la matrice de compatibilité hybride (profil simple uniquement)", p.Engine))
	}
	return ValidationResult{
		OK:       len(errs) == 0,
		Errors:   errs,
		Warnings: warns,
	}
}

func validateHybrid(p Profile) ValidationResult {
	var errs, warns []string

	if p.Name == "" {
		errs = append(errs, "le profil n'a pas de nom")
	}
	if len(p.Components) < 2 {
		errs = append(errs, "un profil hybride requiert au moins 2 composants")
		return ValidationResult{OK: false, Errors: errs}
	}

	// ValidateHybrid : rôles, unicité transport/VPN
	if err := engineutil.ValidateHybrid(p.Components); err != nil {
		errs = append(errs, err.Error())
	}

	// CompatibilityCheck : avertissements sur les dépendances manquantes
	for _, w := range engineutil.CompatibilityCheck(p.Components) {
		warns = append(warns, w)
	}

	// Chaînage réel : EvaluateChain (CanConnect + DetectPortConflicts)
	prof, _ := srvcfg.Load()
	report := engineutil.EvaluateChain(p.Components, prof)

	for _, link := range report.Links {
		if !link.Wired {
			warns = append(warns,
				fmt.Sprintf("liaison %s→%s non câblable : %s", link.Front, link.Back, link.Reason))
		}
	}
	for _, c := range report.PortConflicts {
		errs = append(errs,
			fmt.Sprintf("conflit de port %s/%d entre %s",
				c.Network, c.Port, strings.Join(c.Components, " et ")))
	}

	return ValidationResult{
		OK:       len(errs) == 0,
		Errors:   errs,
		Warnings: warns,
		Report:   &report,
	}
}
