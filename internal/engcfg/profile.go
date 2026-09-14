// Package engcfg gère la persistance des profils de configuration par moteur.
// Chaque moteur dispose d'un fichier profile.json qui retient les choix de
// l'opérateur entre les sessions, évitant de ressaisir les paramètres à chaque
// relancement de l'assistant de configuration.
//
// Chemin : $LABOSURF_DATA_DIR/engines/<engineName>/profile.json
package engcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// EngineProfile conserve les paramètres de configuration saisis par l'opérateur
// pour un moteur donné. Les valeurs sont toujours des chaînes ; les helpers
// Get/GetInt/GetBool font la conversion à la lecture.
type EngineProfile struct {
	Engine string            `json:"engine"`
	Values map[string]string `json:"values"`
}

// New retourne un profil vide pour le moteur donné.
func New(engineName string) EngineProfile {
	return EngineProfile{Engine: engineName, Values: map[string]string{}}
}

// Load charge le profil persisté pour un moteur. Si le fichier n'existe pas,
// retourne un profil vide sans erreur (premier lancement).
func Load(engineName string) (EngineProfile, error) {
	path := profilePath(engineName)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return New(engineName), nil
	}
	if err != nil {
		return New(engineName), fmt.Errorf("lecture profil %s : %w", engineName, err)
	}
	var p EngineProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		return New(engineName), fmt.Errorf("profil %s invalide : %w", engineName, err)
	}
	if p.Values == nil {
		p.Values = map[string]string{}
	}
	return p, nil
}

// Save écrit le profil sur disque (crée le répertoire si nécessaire).
func Save(p EngineProfile) error {
	path := profilePath(p.Engine)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("création répertoire profil %s : %w", p.Engine, err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Get retourne la valeur d'une clé, ou fallback si absente.
func (p EngineProfile) Get(key, fallback string) string {
	if v, ok := p.Values[key]; ok && v != "" {
		return v
	}
	return fallback
}

// GetInt retourne la valeur entière d'une clé, ou fallback si absente/invalide.
func (p EngineProfile) GetInt(key string, fallback int) int {
	v := strings.TrimSpace(p.Values[key])
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// GetBool retourne la valeur booléenne d'une clé, ou fallback si absente.
func (p EngineProfile) GetBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(p.Values[key]))
	switch v {
	case "true", "1", "oui", "yes":
		return true
	case "false", "0", "non", "no":
		return false
	}
	return fallback
}

// Set écrit une valeur dans le profil (sans sauvegarder sur disque).
func (p *EngineProfile) Set(key, value string) {
	if p.Values == nil {
		p.Values = map[string]string{}
	}
	p.Values[key] = value
}

// SetInt écrit une valeur entière dans le profil.
func (p *EngineProfile) SetInt(key string, value int) {
	p.Set(key, strconv.Itoa(value))
}

// SetBool écrit une valeur booléenne dans le profil.
func (p *EngineProfile) SetBool(key string, value bool) {
	if value {
		p.Set(key, "true")
	} else {
		p.Set(key, "false")
	}
}

// profilePath retourne le chemin du fichier de profil pour un moteur.
func profilePath(engineName string) string {
	base := strings.TrimSpace(os.Getenv("LABOSURF_DATA_DIR"))
	if base == "" {
		base = "/etc/labosurf"
	}
	base = strings.TrimRight(base, "/")
	return filepath.Join(base, "engines", engineName, "profile.json")
}
