package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

// authResponse reproduit le calcul effectué par un client légitime :
// HMAC-SHA256(password, nonce) encodé en hexadécimal.
// Il correspond exactement à la vérification de auth.go et aux
// clients Python (auth_test.py, etc.).
func authResponse(t *testing.T, password, challengeHex string) string {
	t.Helper()

	nonce, err := hex.DecodeString(challengeHex)
	if err != nil {
		t.Fatalf("challenge hexadécimal invalide : %v", err)
	}

	mac := hmac.New(sha256.New, []byte(password))
	_, _ = mac.Write(nonce)

	return hex.EncodeToString(mac.Sum(nil))
}

func validUsers() map[string]UserConfig {
	return map[string]UserConfig{
		"client1": {
			Password:       "CHANGE_ME",
			ExpiresAt:      "",
			QuotaBytes:     0,
			MaxConnections: 1,
			MaxIPs:         1,
			Enabled:        true,
		},
	}
}

// TestAuthValidPassword : bon mot de passe -> AUTH_OK.
func TestAuthValidPassword(t *testing.T) {
	am := NewAuthManager(validUsers())

	challenge, err := am.NewChallenge("clientA")
	if err != nil {
		t.Fatalf("NewChallenge : %v", err)
	}

	resp := authResponse(t, "CHANGE_ME", challenge)

	user, ok := am.Verify("clientA", resp)
	if !ok {
		t.Fatal("un bon mot de passe doit être accepté")
	}

	if user.Username != "client1" {
		t.Fatalf("username attendu client1, obtenu %q", user.Username)
	}

	if user.Config.Password != "CHANGE_ME" {
		t.Fatalf("la config du compte doit accompagner l'utilisateur authentifié")
	}
}

// TestAuthWrongPassword : mauvais mot de passe -> AUTH_FAIL.
func TestAuthWrongPassword(t *testing.T) {
	am := NewAuthManager(validUsers())

	challenge, err := am.NewChallenge("clientA")
	if err != nil {
		t.Fatalf("NewChallenge : %v", err)
	}

	resp := authResponse(t, "MAUVAIS_MOT_DE_PASSE", challenge)

	if _, ok := am.Verify("clientA", resp); ok {
		t.Fatal("un mauvais mot de passe doit être refusé")
	}
}

// TestAuthReplayRejected : rejouer le même AUTH -> AUTH_FAIL.
// Le challenge est consommé lors de la première vérification.
func TestAuthReplayRejected(t *testing.T) {
	am := NewAuthManager(validUsers())

	challenge, err := am.NewChallenge("clientA")
	if err != nil {
		t.Fatalf("NewChallenge : %v", err)
	}

	resp := authResponse(t, "CHANGE_ME", challenge)

	if _, ok := am.Verify("clientA", resp); !ok {
		t.Fatal("la première authentification doit réussir")
	}

	if _, ok := am.Verify("clientA", resp); ok {
		t.Fatal("le rejeu du même AUTH doit être refusé (anti-replay)")
	}
}

// TestAuthNoChallenge : un AUTH sans challenge préalable est refusé.
func TestAuthNoChallenge(t *testing.T) {
	am := NewAuthManager(validUsers())

	if _, ok := am.Verify("clientInconnu", "deadbeef"); ok {
		t.Fatal("un AUTH sans challenge doit être refusé")
	}
}

// TestAuthExpiredChallenge : un challenge trop vieux est refusé.
func TestAuthExpiredChallenge(t *testing.T) {
	am := NewAuthManager(validUsers())

	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i)
	}

	// Insertion manuelle d'un challenge périmé.
	am.mu.Lock()
	am.challenges["clientA"] = challengeEntry{
		nonce:     nonce,
		createdAt: time.Now().Add(-challengeLifetime - time.Minute),
	}
	am.mu.Unlock()

	resp := authResponse(t, "CHANGE_ME", hex.EncodeToString(nonce))

	if _, ok := am.Verify("clientA", resp); ok {
		t.Fatal("un challenge périmé doit être refusé")
	}
}

// TestAuthManagerFiltersUsers : seuls les comptes actifs, avec mot de
// passe et non expirés sont chargés.
func TestAuthManagerFiltersUsers(t *testing.T) {
	users := map[string]UserConfig{
		"actif": {
			Password: "p1",
			Enabled:  true,
		},
		"desactive": {
			Password: "p2",
			Enabled:  false,
		},
		"sans_mdp": {
			Password: "",
			Enabled:  true,
		},
		"expire": {
			Password:  "p3",
			ExpiresAt: "2000-01-01T00:00:00Z",
			Enabled:   true,
		},
	}

	am := NewAuthManager(users)

	if _, ok := am.users["actif"]; !ok {
		t.Fatal("le compte actif doit être chargé")
	}

	for _, name := range []string{"desactive", "sans_mdp", "expire"} {
		if _, ok := am.users[name]; ok {
			t.Fatalf("le compte %q ne doit pas être chargé", name)
		}
	}

	if len(am.users) != 1 {
		t.Fatalf("un seul compte doit être chargé, obtenu %d", len(am.users))
	}
}

// TestAuthDisabledAccountCannotAuthenticate : compte désactivé -> refus.
func TestAuthDisabledAccountCannotAuthenticate(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {
			Password: "CHANGE_ME",
			Enabled:  false,
		},
	}

	am := NewAuthManager(users)

	challenge, err := am.NewChallenge("clientA")
	if err != nil {
		t.Fatalf("NewChallenge : %v", err)
	}

	resp := authResponse(t, "CHANGE_ME", challenge)

	if _, ok := am.Verify("clientA", resp); ok {
		t.Fatal("un compte désactivé ne doit pas pouvoir s'authentifier")
	}
}

// TestAuthExpiredAccountCannotAuthenticate : compte expiré -> refus.
func TestAuthExpiredAccountCannotAuthenticate(t *testing.T) {
	users := map[string]UserConfig{
		"client1": {
			Password:  "CHANGE_ME",
			ExpiresAt: "2000-01-01T00:00:00Z",
			Enabled:   true,
		},
	}

	am := NewAuthManager(users)

	challenge, err := am.NewChallenge("clientA")
	if err != nil {
		t.Fatalf("NewChallenge : %v", err)
	}

	resp := authResponse(t, "CHANGE_ME", challenge)

	if _, ok := am.Verify("clientA", resp); ok {
		t.Fatal("un compte expiré ne doit pas pouvoir s'authentifier")
	}
}
