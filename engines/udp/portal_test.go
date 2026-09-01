package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- helpers ---

func newTestPortalServer(t *testing.T) *portalServer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "portal.json")
	s, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore : %v", err)
	}

	ps := &portalServer{store: s, addr: ":0"}
	ps.mux = http.NewServeMux()
	ps.mux.HandleFunc("/", ps.handleIndex)
	ps.mux.HandleFunc("/client/", ps.handleClient)
	return ps
}

// --- formatBytesPortal ---

func TestFormatBytesPortal(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
	}
	for _, tt := range tests {
		got := formatBytesPortal(tt.in)
		if got != tt.want {
			t.Errorf("formatBytesPortal(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- buildPortalData ---

func TestBuildPortalDataActive(t *testing.T) {
	acc := Account{
		ID:             "testuser",
		Username:       "testuser",
		Enabled:        true,
		ExpiresAt:      time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
		QuotaBytes:     1000000000,
		UsedBytes:      500000000,
		MaxConnections: 3,
		CurrentIPs:     []string{"10.0.0.1"},
		MaxIPs:         5,
		OfferID:        "premium",
	}
	pd := buildPortalData(acc)

	if pd.StateLabel != "Actif" || pd.StateClass != "ok" {
		t.Errorf("état actif attendu, obtenu label=%q class=%q", pd.StateLabel, pd.StateClass)
	}
	if pd.StateWarn {
		t.Error("StateWarn doit être false pour un compte actif")
	}
	if pd.Expired {
		t.Error("Expired doit être false pour un compte non expiré")
	}
	if pd.QuotaUsedPercent != 50 {
		t.Errorf("quota utilisé attendu 50%%, obtenu %d%%", pd.QuotaUsedPercent)
	}
	if pd.QuotaWarn {
		t.Error("QuotaWarn doit être false sous 80%%")
	}
	if pd.CurrentIPs != 1 {
		t.Errorf("CurrentIPs attendu 1, obtenu %d", pd.CurrentIPs)
	}
}

func TestBuildPortalDataDisabled(t *testing.T) {
	acc := Account{Enabled: false, ExpiresAt: time.Now().Add(24 * time.Hour).Format(time.RFC3339)}
	pd := buildPortalData(acc)
	if pd.StateLabel != "Inactif" || pd.StateClass != "warn" {
		t.Errorf("état inactif attendu, obtenu label=%q class=%q", pd.StateLabel, pd.StateClass)
	}
	if !pd.StateWarn {
		t.Error("StateWarn doit être true pour un compte inactif")
	}
}

func TestBuildPortalDataExpired(t *testing.T) {
	acc := Account{
		Enabled:   true,
		ExpiresAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	pd := buildPortalData(acc)
	if !pd.Expired {
		t.Error("Expired doit être true pour un compte expiré")
	}
	if pd.ExpiryLabel != "Expiré" {
		t.Errorf("ExpiryLabel attendu 'Expiré', obtenu %q", pd.ExpiryLabel)
	}
}

func TestBuildPortalDataNoExpiry(t *testing.T) {
	acc := Account{Enabled: true, ExpiresAt: ""}
	pd := buildPortalData(acc)
	if pd.ExpiryLabel != "Illimité" {
		t.Errorf("ExpiryLabel attendu 'Illimité', obtenu %q", pd.ExpiryLabel)
	}
}

func TestBuildPortalDataQuotaWarning(t *testing.T) {
	acc := Account{
		Enabled:    true,
		ExpiresAt:  time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		QuotaBytes: 1000,
		UsedBytes:  900,
	}
	pd := buildPortalData(acc)
	if !pd.QuotaWarn {
		t.Error("QuotaWarn doit être true au-delà de 80%%")
	}
	if pd.QuotaUsedPercent != 90 {
		t.Errorf("quota utilisé attendu 90%%, obtenu %d%%", pd.QuotaUsedPercent)
	}
}

func TestBuildPortalDataUnlimitedQuota(t *testing.T) {
	acc := Account{
		Enabled:    true,
		ExpiresAt:  time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		UsedBytes:  5000,
		QuotaBytes: 0,
	}
	pd := buildPortalData(acc)
	if pd.QuotaUsedPercent != 0 {
		t.Errorf("quotaPercent doit être 0 pour quota illimité, obtenu %d", pd.QuotaUsedPercent)
	}
	if !strings.Contains(pd.QuotaLabel, "illimité") {
		t.Errorf("QuotaLabel doit contenir 'illimité', obtenu %q", pd.QuotaLabel)
	}
}

func TestBuildPortalDataQuotaExceeds100(t *testing.T) {
	acc := Account{
		Enabled:    true,
		ExpiresAt:  time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		QuotaBytes: 100,
		UsedBytes:  200,
	}
	pd := buildPortalData(acc)
	if pd.QuotaUsedPercent != 100 {
		t.Errorf("quotaPercent doit être plafonné à 100, obtenu %d", pd.QuotaUsedPercent)
	}
}

// --- HTTP handler tests ---

func TestPortalIndexPage(t *testing.T) {
	ps := newTestPortalServer(t)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ps.handleIndex(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET / : code %d, attendu 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "LABOSURF PRO") {
		t.Error("la page d'accueil doit contenir 'LABOSURF PRO'")
	}
}

func TestPortalIndexNotFound(t *testing.T) {
	ps := newTestPortalServer(t)

	req := httptest.NewRequest("GET", "/inexistant", nil)
	rec := httptest.NewRecorder()
	ps.handleIndex(rec, req)

	if rec.Code != 404 {
		t.Errorf("GET /inexistant : code %d, attendu 404", rec.Code)
	}
}

func TestPortalClientValidToken(t *testing.T) {
	ps := newTestPortalServer(t)
	acc, _ := ps.store.CreateAccount(Account{
		ID:         "alice",
		Enabled:    true,
		ExpiresAt:  time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
		QuotaBytes: 1000000,
	})
	token := acc.Token

	req := httptest.NewRequest("GET", "/client/"+token, nil)
	rec := httptest.NewRecorder()
	ps.handleClient(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /client/<token> : code %d, attendu 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice") {
		t.Error("le portail doit afficher le nom du compte")
	}
	if !strings.Contains(body, "Actif") {
		t.Error("le portail doit afficher l'état Actif")
	}
}

func TestPortalClientInvalidToken(t *testing.T) {
	ps := newTestPortalServer(t)

	req := httptest.NewRequest("GET", "/client/nonceexistenttoken1234567890abcdef", nil)
	rec := httptest.NewRecorder()
	ps.handleClient(rec, req)

	if rec.Code != 404 {
		t.Errorf("token inexistant : code %d, attendu 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Lien invalide") {
		t.Error("la page 404 doit afficher 'Lien invalide'")
	}
}

func TestPortalClientEmptyToken(t *testing.T) {
	ps := newTestPortalServer(t)

	req := httptest.NewRequest("GET", "/client/", nil)
	rec := httptest.NewRecorder()
	ps.handleClient(rec, req)

	if rec.Code != 404 {
		t.Errorf("token vide : code %d, attendu 404", rec.Code)
	}
}

func TestPortalClientIsolation(t *testing.T) {
	ps := newTestPortalServer(t)

	alice, _ := ps.store.CreateAccount(Account{
		ID:        "alice",
		Enabled:   true,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
	})
	bob, _ := ps.store.CreateAccount(Account{
		ID:        "bob",
		Enabled:   true,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
	})

	// Le token de alice ne doit PAS afficher les données de bob.
	req := httptest.NewRequest("GET", "/client/"+alice.Token, nil)
	rec := httptest.NewRecorder()
	ps.handleClient(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "bob") {
		t.Fatal("ISOLATION VIOLÉE : le token de alice affiche les données de bob")
	}
	if !strings.Contains(body, "alice") {
		t.Fatal("le token de alice doit afficher les données de alice")
	}

	// Et vice versa.
	req2 := httptest.NewRequest("GET", "/client/"+bob.Token, nil)
	rec2 := httptest.NewRecorder()
	ps.handleClient(rec2, req2)

	body2 := rec2.Body.String()
	if strings.Contains(body2, "alice") {
		t.Fatal("ISOLATION VIOLÉE : le token de bob affiche les données de alice")
	}
	if !strings.Contains(body2, "bob") {
		t.Fatal("le token de bob doit afficher les données de bob")
	}
}

func TestPortalClientRevokedToken(t *testing.T) {
	ps := newTestPortalServer(t)
	acc, _ := ps.store.CreateAccount(Account{
		ID:        "alice",
		Enabled:   true,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
	})
	token := acc.Token

	// Révoquer le token.
	ps.store.RevokeToken("alice")

	req := httptest.NewRequest("GET", "/client/"+token, nil)
	rec := httptest.NewRecorder()
	ps.handleClient(rec, req)

	if rec.Code != 404 {
		t.Errorf("token révoqué : code %d, attendu 404", rec.Code)
	}
}

func TestPortalClientExpiredAccount(t *testing.T) {
	ps := newTestPortalServer(t)
	acc, _ := ps.store.CreateAccount(Account{
		ID:        "expired",
		Enabled:   true,
		ExpiresAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	})

	req := httptest.NewRequest("GET", "/client/"+acc.Token, nil)
	rec := httptest.NewRecorder()
	ps.handleClient(rec, req)

	if rec.Code != 200 {
		t.Errorf("compte expiré doit quand même afficher la page : code %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Expiré") {
		t.Error("le portail doit afficher 'Expiré'")
	}
}

func TestPortalClientExpiryWarningLessThan7Days(t *testing.T) {
	ps := newTestPortalServer(t)
	acc, _ := ps.store.CreateAccount(Account{
		ID:        "bientot",
		Enabled:   true,
		ExpiresAt: time.Now().Add(5 * 24 * time.Hour).Format(time.RFC3339),
	})

	req := httptest.NewRequest("GET", "/client/"+acc.Token, nil)
	rec := httptest.NewRecorder()
	ps.handleClient(rec, req)

	if rec.Code != 200 {
		t.Errorf("code %d, attendu 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "warn") {
		t.Error("le portail doit afficher un avertissement (warn) pour < 7 jours")
	}
}

// --- Sécurité API : /api/usage ne doit JAMAIS exposer de secret ---

func TestPortalAPIUsageNeverLeaksSecrets(t *testing.T) {
	ps := newTestPortalServer(t)
	acc, _ := ps.store.CreateAccount(Account{
		ID:         "secretclient",
		Password:   "SUPER_SECRET_PASSWORD_123",
		Enabled:    true,
		ExpiresAt:  time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
		QuotaBytes: 1000000,
	})

	req := httptest.NewRequest("GET", "/api/usage?token="+acc.Token, nil)
	rec := httptest.NewRecorder()
	ps.handleAPIUsage(rec, req)

	if rec.Code != 200 {
		t.Fatalf("GET /api/usage : code %d, attendu 200", rec.Code)
	}

	body := rec.Body.String()

	// Le mot de passe ne doit JAMAIS apparaître dans la réponse JSON.
	if strings.Contains(body, "SUPER_SECRET_PASSWORD_123") {
		t.Fatal("FUITE DE SÉCURITÉ : le mot de passe apparaît dans /api/usage")
	}
	if strings.Contains(strings.ToLower(body), "password") {
		t.Fatal("FUITE DE SÉCURITÉ : le champ password apparaît dans /api/usage")
	}
	// Le jeton d'accès (lien client) ne doit pas être renvoyé dans le corps.
	if strings.Contains(body, acc.Token) {
		t.Fatal("FUITE DE SÉCURITÉ : le token client apparaît dans /api/usage")
	}
}

func TestPortalAPIUsageInvalidToken(t *testing.T) {
	ps := newTestPortalServer(t)

	req := httptest.NewRequest("GET", "/api/usage?token=inexistant", nil)
	rec := httptest.NewRecorder()
	ps.handleAPIUsage(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("token invalide : code %d, attendu 404", rec.Code)
	}
}

func TestPortalAPIUsageMissingToken(t *testing.T) {
	ps := newTestPortalServer(t)

	req := httptest.NewRequest("GET", "/api/usage", nil)
	rec := httptest.NewRecorder()
	ps.handleAPIUsage(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("token manquant : code %d, attendu 400", rec.Code)
	}
}
