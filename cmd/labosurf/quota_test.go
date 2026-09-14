package main

import (
	"testing"

	"labosurf/internal/service"
)

// ============================================================
// parseQuotaGB
// ============================================================

func TestParseQuotaGBOneGB(t *testing.T) {
	b, err := parseQuotaGB("1")
	if err != nil {
		t.Fatalf("1 Go : %v", err)
	}
	if b != uint64(bytesPerGB) {
		t.Fatalf("attendu %d octets, obtenu %d", uint64(bytesPerGB), b)
	}
}

func TestParseQuotaGBFractional(t *testing.T) {
	b, err := parseQuotaGB("1.5")
	if err != nil {
		t.Fatalf("1.5 Go : %v", err)
	}
	want := uint64(1.5 * bytesPerGB)
	if b != want {
		t.Fatalf("attendu %d, obtenu %d", want, b)
	}
}

func TestParseQuotaGBInvalidValue(t *testing.T) {
	if _, err := parseQuotaGB("abc"); err == nil {
		t.Fatal("attendu une erreur pour une valeur non numérique")
	}
}

func TestParseQuotaGBEmpty(t *testing.T) {
	if _, err := parseQuotaGB(""); err == nil {
		t.Fatal("attendu une erreur pour une valeur vide")
	}
	if _, err := parseQuotaGB("   "); err == nil {
		t.Fatal("attendu une erreur pour une valeur composée uniquement d'espaces")
	}
}

func TestParseQuotaGBZero(t *testing.T) {
	if _, err := parseQuotaGB("0"); err == nil {
		t.Fatal("attendu une erreur pour 0 Go (utiliser illimité à la place)")
	}
}

func TestParseQuotaGBNegative(t *testing.T) {
	if _, err := parseQuotaGB("-5"); err == nil {
		t.Fatal("attendu une erreur pour une valeur négative")
	}
}

func TestParseQuotaGBTooLarge(t *testing.T) {
	if _, err := parseQuotaGB("99999999999999999999999"); err == nil {
		t.Fatal("attendu une erreur pour une valeur dépassant la capacité d'un uint64")
	}
}

// ============================================================
// applyQuotaChoice — transition de quota (logique pure, sans stdin)
// ============================================================

func TestApplyQuotaChoiceUnlimited(t *testing.T) {
	a := &service.Access{QuotaUnlimited: false, QuotaLimitBytes: 5 * uint64(bytesPerGB)}
	if err := applyQuotaChoice(a, "1", ""); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !a.QuotaUnlimited || a.QuotaLimitBytes != 0 {
		t.Fatalf("attendu illimité (0 octet), obtenu QuotaUnlimited=%v QuotaLimitBytes=%d",
			a.QuotaUnlimited, a.QuotaLimitBytes)
	}
}

func TestApplyQuotaChoiceLimited(t *testing.T) {
	a := &service.Access{QuotaUnlimited: true}
	if err := applyQuotaChoice(a, "2", "10"); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	want := uint64(10 * bytesPerGB)
	if a.QuotaUnlimited || a.QuotaLimitBytes != want {
		t.Fatalf("attendu limité à %d octets, obtenu QuotaUnlimited=%v QuotaLimitBytes=%d",
			want, a.QuotaUnlimited, a.QuotaLimitBytes)
	}
}

// TestApplyQuotaChoiceTransitionLimitedToUnlimited vérifie explicitement le
// passage limité → illimité (mission M4 §16).
func TestApplyQuotaChoiceTransitionLimitedToUnlimited(t *testing.T) {
	a := &service.Access{}
	if err := applyQuotaChoice(a, "2", "20"); err != nil {
		t.Fatalf("mise en place du quota limité : %v", err)
	}
	if a.QuotaUnlimited {
		t.Fatal("précondition : le quota devrait être limité avant la transition")
	}
	if err := applyQuotaChoice(a, "1", ""); err != nil {
		t.Fatalf("transition vers illimité : %v", err)
	}
	if !a.QuotaUnlimited || a.QuotaLimitBytes != 0 {
		t.Fatalf("transition limité→illimité incorrecte : %+v", a)
	}
}

// TestApplyQuotaChoiceTransitionUnlimitedToLimited vérifie explicitement le
// passage illimité → limité (mission M4 §16).
func TestApplyQuotaChoiceTransitionUnlimitedToLimited(t *testing.T) {
	a := &service.Access{QuotaUnlimited: true}
	if err := applyQuotaChoice(a, "2", "3"); err != nil {
		t.Fatalf("transition vers limité : %v", err)
	}
	want := uint64(3 * bytesPerGB)
	if a.QuotaUnlimited || a.QuotaLimitBytes != want {
		t.Fatalf("transition illimité→limité incorrecte : %+v (attendu %d octets)", a, want)
	}
}

func TestApplyQuotaChoiceLimitedInvalidValue(t *testing.T) {
	a := &service.Access{}
	if err := applyQuotaChoice(a, "2", "abc"); err == nil {
		t.Fatal("attendu une erreur pour une valeur de Go invalide")
	}
}

func TestApplyQuotaChoiceInvalidChoice(t *testing.T) {
	a := &service.Access{}
	if err := applyQuotaChoice(a, "9", ""); err == nil {
		t.Fatal("attendu une erreur pour un choix de quota invalide")
	}
}

// ============================================================
// formatQuotaBytes
// ============================================================

func TestFormatQuotaBytes(t *testing.T) {
	got := formatQuotaBytes(uint64(bytesPerGB))
	if got != "1 Go" {
		t.Fatalf("attendu %q, obtenu %q", "1 Go", got)
	}
}

func TestFormatQuotaBytesZero(t *testing.T) {
	got := formatQuotaBytes(0)
	if got != "0 Go" {
		t.Fatalf("attendu %q, obtenu %q", "0 Go", got)
	}
}
