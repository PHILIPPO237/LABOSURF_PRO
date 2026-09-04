package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const defaultListen = ":5667"

// defaultPortalListen est l'adresse d'écoute par défaut du portail HTTP.
const defaultPortalListen = ":8080"

type Config struct {
	Listen string `json:"listen"`

	// Store est le chemin du fichier source de vérité des comptes.
	Store string `json:"store"`

	// Portal configure le portail HTTP client. Lorsqu'il est activé, il
	// est démarré DANS le processus UDP Engine et partage le SessionManager du
	// moteur : les données affichées sont donc les données réelles.
	Portal struct {
		Enabled bool   `json:"enabled"`
		Listen  string `json:"listen"`
	} `json:"portal"`

	// License configure les chemins des fichiers de licence utilisés lors
	// de la vérification au démarrage. Vides => valeurs par défaut.
	// La vérification est TOUJOURS active en production ; seul le drapeau
	// -dev (ou LABOSURF_DEV=1) permet de la contourner pour le développement.
	License struct {
		Activation string `json:"activation"`
		MachineID  string `json:"machine_id"`
		Registry   string `json:"registry"`
	} `json:"license"`

	TUN struct {
		Enabled bool   `json:"enabled"`
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"tun"`

	Auth struct {
		Mode  string                `json:"mode"`
		Users map[string]UserConfig `json:"users"`
	} `json:"auth"`
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var config Config

	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("JSON invalide : %w", err)
	}

	if config.Listen == "" {
		config.Listen = defaultListen
	}

	if config.Store == "" {
		config.Store = defaultStorePath
	}

	if config.Portal.Listen == "" {
		config.Portal.Listen = defaultPortalListen
	}

	if config.License.Activation == "" {
		config.License.Activation = defaultActivationPath
	}

	if config.License.MachineID == "" {
		config.License.MachineID = defaultMachineIDPath
	}

	if config.License.Registry == "" {
		config.License.Registry = defaultRegistryPath
	}

	if config.TUN.Name == "" {
		config.TUN.Name = "labosurf0"
	}

	if config.TUN.Address == "" {
		config.TUN.Address = "10.77.0.1/24"
	}

	if config.Auth.Mode == "" {
		config.Auth.Mode = "passwords"
	}

	if config.Auth.Users == nil {
		config.Auth.Users = make(map[string]UserConfig)
	}

	for name, user := range config.Auth.Users {
		if user.MaxConnections <= 0 {
			user.MaxConnections = 1
		}
		// 0 = illimité pour les IP ; une valeur positive impose une limite.
		if user.MaxIPs < 0 {
			user.MaxIPs = 1
		}
		config.Auth.Users[name] = user
	}

	return config, nil
}

func tunnelClientID(clientID string) uint64 {
	sum := sha256.Sum256([]byte(clientID))
	return binary.BigEndian.Uint64(sum[:8])
}

// userConfigExpired indique si un compte est expiré à l'instant now.
//
//   - ExpiresAt vide  => pas d'expiration (jamais expiré).
//   - ExpiresAt illisible => considéré expiré par prudence (sécurité).
func userConfigExpired(user UserConfig, now time.Time) bool {
	if user.ExpiresAt == "" {
		return false
	}

	expires, err := time.Parse(time.RFC3339, user.ExpiresAt)
	if err != nil {
		return true
	}

	return now.After(expires)
}
