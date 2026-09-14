// Conversion et application du quota (Go ↔ octets) pour les écrans d'Access.
//
// Isolé dans son propre fichier car c'est la seule logique réellement
// nouvelle introduite par M4 (le reste des écrans Services/Access délègue
// entièrement aux API déjà testées de internal/service et internal/clientcfg).
package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"labosurf/internal/service"
)

const bytesPerGB = 1 << 30 // 1 Go = 1 073 741 824 octets

// parseQuotaGB convertit une saisie utilisateur (nombre de Go, éventuellement
// décimal) en octets pour un quota Access LIMITÉ.
//
// Refuse explicitement :
//   - une valeur vide ;
//   - une valeur non numérique ;
//   - une valeur négative ou nulle (un quota limité à 0 Go n'a pas de sens :
//     utiliser le mode illimité — QuotaUnlimited=true — à la place) ;
//   - une valeur qui dépasserait la capacité d'un uint64 une fois convertie.
//
// Ne convertit jamais silencieusement une entrée invalide en 0.
func parseQuotaGB(input string) (uint64, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, errors.New("valeur vide : indiquez un nombre de Go, ou choisissez illimité")
	}
	gb, err := strconv.ParseFloat(input, 64)
	if err != nil {
		return 0, fmt.Errorf("valeur invalide : %q n'est pas un nombre", input)
	}
	if gb <= 0 {
		return 0, errors.New("un quota limité doit être supérieur à 0 Go — utilisez illimité pour aucune limite")
	}
	const maxGB = float64(math.MaxUint64) / bytesPerGB
	if gb > maxGB {
		return 0, fmt.Errorf("valeur trop grande (maximum ~%.0f Go)", maxGB)
	}
	return uint64(gb * bytesPerGB), nil
}

// formatQuotaBytes affiche un nombre d'octets sous forme lisible en Go.
func formatQuotaBytes(b uint64) string {
	gb := float64(b) / bytesPerGB
	return strconv.FormatFloat(gb, 'f', -1, 64) + " Go"
}

// applyQuotaChoice modifie le quota d'un Access selon le choix utilisateur :
//
//	choice "1" → illimité (QuotaLimitBytes remis à 0, ignoré tant qu'illimité).
//	choice "2" → limité, gbInput est converti via parseQuotaGB.
//
// Logique pure (aucune lecture stdin, aucun affichage) : c'est ce qui rend
// la transition limité→illimité et illimité→limité testable indépendamment
// de l'écran terminal qui l'appelle (accessCreateMenu / accessEditQuota).
func applyQuotaChoice(a *service.Access, choice, gbInput string) error {
	switch choice {
	case "1":
		a.QuotaUnlimited = true
		a.QuotaLimitBytes = 0
		return nil
	case "2":
		b, err := parseQuotaGB(gbInput)
		if err != nil {
			return err
		}
		a.QuotaUnlimited = false
		a.QuotaLimitBytes = b
		return nil
	default:
		return fmt.Errorf("choix de quota invalide : %q", choice)
	}
}
