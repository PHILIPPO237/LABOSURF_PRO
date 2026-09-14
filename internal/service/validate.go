package service

import (
	"fmt"
	"strings"
	"time"
)

// ValidationError rassemble les erreurs de validation d'un Service ou Access.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Errors, "; ")
}

func (e *ValidationError) add(msg string, args ...any) {
	e.Errors = append(e.Errors, fmt.Sprintf(msg, args...))
}

func (e *ValidationError) ok() bool { return len(e.Errors) == 0 }

// ValidateService vérifie la cohérence d'un Service avant persistance.
func ValidateService(s *Service) error {
	ve := &ValidationError{}

	if strings.TrimSpace(s.ID) == "" {
		ve.add("ID obligatoire")
	}
	if strings.TrimSpace(s.Name) == "" {
		ve.add("Name obligatoire")
	}
	if strings.TrimSpace(s.Engine) == "" {
		ve.add("Engine obligatoire")
	}

	for role, port := range s.Ports {
		if port < 0 {
			ve.add("port %q négatif (%d)", role, port)
		} else if port > 65535 {
			ve.add("port %q hors plage réseau (%d > 65535)", role, port)
		}
	}

	// Pour un hybride, Components doit contenir au moins 2 éléments et ne pas
	// inclure de nom vide.
	if s.IsHybrid() {
		if len(s.Components) < 2 {
			ve.add("un service hybride requiert au moins 2 composants (Components)")
		}
		for i, c := range s.Components {
			if strings.TrimSpace(c) == "" {
				ve.add("Components[%d] est vide", i)
			}
		}
	}

	if !ve.ok() {
		return ve
	}
	return nil
}

// ValidateAccess vérifie la cohérence d'un Access avant persistance.
func ValidateAccess(a *Access) error {
	ve := &ValidationError{}

	if strings.TrimSpace(a.ID) == "" {
		ve.add("ID obligatoire")
	}
	if strings.TrimSpace(a.AccountID) == "" {
		ve.add("AccountID obligatoire")
	}
	if strings.TrimSpace(a.ServiceID) == "" {
		ve.add("ServiceID obligatoire")
	}
	if strings.TrimSpace(a.Engine) == "" {
		ve.add("Engine obligatoire")
	}

	if a.MaxDevices < 0 {
		ve.add("MaxDevices ne peut pas être négatif (%d)", a.MaxDevices)
	}
	if a.MaxConnections < 0 {
		ve.add("MaxConnections ne peut pas être négatif (%d)", a.MaxConnections)
	}
	if a.MaxSourceIPs < 0 {
		ve.add("MaxSourceIPs ne peut pas être négatif (%d)", a.MaxSourceIPs)
	}

	// Quota : en mode limité, QuotaLimitBytes=0 est refusé explicitement.
	// Utiliser QuotaUnlimited=true pour un accès illimité.
	if !a.QuotaUnlimited && a.QuotaLimitBytes == 0 {
		ve.add("quota limité (QuotaUnlimited=false) avec QuotaLimitBytes=0 : " +
			"utiliser QuotaUnlimited=true pour un accès illimité, ou fixer une limite réelle")
	}

	// ExpiresAt doit être parseable si non vide.
	if a.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, a.ExpiresAt); err != nil {
			ve.add("ExpiresAt %q n'est pas une date RFC3339 valide : %v", a.ExpiresAt, err)
		}
	}

	if !ve.ok() {
		return ve
	}
	return nil
}
