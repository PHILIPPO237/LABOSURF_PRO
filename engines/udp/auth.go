package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const challengeLifetime = 2 * time.Minute

type challengeEntry struct {
	nonce     []byte
	createdAt time.Time
}

type AuthUser struct {
	Username string
	Config   UserConfig
}

type AuthManager struct {
	users      map[string]AuthUser
	challenges map[string]challengeEntry
	mu         sync.Mutex
}

func NewAuthManager(users map[string]UserConfig) *AuthManager {
	m := &AuthManager{
		users:      make(map[string]AuthUser),
		challenges: make(map[string]challengeEntry),
	}

	for username, user := range users {
		if !user.Enabled {
			continue
		}

		if user.Password == "" {
			continue
		}

		if userConfigExpired(user, time.Now()) {
			continue
		}

		m.users[username] = AuthUser{
			Username: username,
			Config:   user,
		}
	}

	return m
}

func (a *AuthManager) NewChallenge(clientID string) (string, error) {
	nonce := make([]byte, 32)

	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf(
			"génération du challenge : %w",
			err,
		)
	}

	a.mu.Lock()

	a.challenges[clientID] = challengeEntry{
		nonce:     nonce,
		createdAt: time.Now(),
	}

	a.mu.Unlock()

	return hex.EncodeToString(nonce), nil
}

func (a *AuthManager) ReloadFromStore(path string) error {
	s, err := LoadStore(path)
	if err != nil {
		return err
	}
	users := make(map[string]AuthUser)
	for _, acc := range s.ListAccounts() {
		if !acc.Enabled || acc.Password == "" || userConfigExpired(UserConfig{ExpiresAt: acc.ExpiresAt}, time.Now()) {
			continue
		}
		users[acc.ID] = AuthUser{Username: acc.ID, Config: UserConfig{
			Password: acc.Password, ExpiresAt: acc.ExpiresAt, QuotaBytes: acc.QuotaBytes,
			MaxConnections: acc.MaxConnections, MaxIPs: acc.MaxIPs, Enabled: acc.Enabled,
		}}
	}
	a.mu.Lock()
	a.users = users
	a.mu.Unlock()
	return nil
}

func (a *AuthManager) Verify(
	clientID string,
	response string,
) (AuthUser, bool) {
	a.mu.Lock()

	entry, exists := a.challenges[clientID]

	if exists {
		// Le challenge est consommé immédiatement.
		// Cela empêche sa réutilisation.
		delete(a.challenges, clientID)
	}

	a.mu.Unlock()

	if !exists {
		return AuthUser{}, false
	}

	if time.Since(entry.createdAt) > challengeLifetime {
		return AuthUser{}, false
	}

	responseBytes, err := hex.DecodeString(response)
	if err != nil {
		return AuthUser{}, false
	}

	for _, user := range a.users {
		mac := hmac.New(
			sha256.New,
			[]byte(user.Config.Password),
		)

		_, _ = mac.Write(entry.nonce)

		expected := mac.Sum(nil)

		if hmac.Equal(expected, responseBytes) {
			// Re-vérification en direct : un compte peut avoir été
			// désactivé ou avoir expiré depuis le démarrage du serveur.
			if !user.Config.Enabled {
				return AuthUser{}, false
			}

			if userConfigExpired(user.Config, time.Now()) {
				return AuthUser{}, false
			}

			return user, true
		}
	}

	return AuthUser{}, false
}
