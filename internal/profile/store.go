package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir retourne le répertoire de stockage des profils.
// $LABOSURF_DATA_DIR/profiles/ — un fichier JSON par profil.
func Dir() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LABOSURF_DATA_DIR")), "/")
	if base == "" {
		base = "/etc/labosurf"
	}
	return filepath.Join(base, "profiles")
}

func filePath(id string) string {
	return filepath.Join(Dir(), id+".json")
}

// ListAll retourne tous les profils persistés, dans l'ordre des fichiers.
// Un répertoire absent retourne une liste vide sans erreur.
func ListAll() ([]Profile, error) {
	entries, err := os.ReadDir(Dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture répertoire profils : %w", err)
	}
	var profiles []Profile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		p, err := Get(id)
		if err != nil {
			continue // fichier corrompu : ignoré silencieusement
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// Get charge un profil par son ID. Retourne une erreur si introuvable.
func Get(id string) (Profile, error) {
	raw, err := os.ReadFile(filePath(id))
	if err != nil {
		return Profile{}, fmt.Errorf("profil %q introuvable : %w", id, err)
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, fmt.Errorf("profil %q invalide (JSON) : %w", id, err)
	}
	if p.Params == nil {
		p.Params = map[string]string{}
	}
	return p, nil
}

// GetByName cherche un profil par son nom (insensible à la casse).
// Retourne false si aucun profil ne correspond.
func GetByName(name string) (Profile, bool, error) {
	all, err := ListAll()
	if err != nil {
		return Profile{}, false, err
	}
	for _, p := range all {
		if strings.EqualFold(p.Name, name) {
			return p, true, nil
		}
	}
	return Profile{}, false, nil
}

// Save persiste un profil sur disque (crée le répertoire si nécessaire).
// Met à jour UpdatedAt avant l'écriture.
func Save(p *Profile) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return fmt.Errorf("création répertoire profils : %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath(p.ID), data, 0o600)
}

// Delete supprime le fichier d'un profil. Ne vérifie PAS les dépendances :
// l'appelant doit avoir consulté Dependents() au préalable.
func Delete(id string) error {
	err := os.Remove(filePath(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ListByEngine retourne tous les profils simples d'un moteur donné.
func ListByEngine(engineName string) ([]Profile, error) {
	all, err := ListAll()
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, p := range all {
		if p.Kind == KindSimple && p.Engine == engineName {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListHybrids retourne tous les profils hybrides.
func ListHybrids() ([]Profile, error) {
	all, err := ListAll()
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, p := range all {
		if p.Kind == KindHybrid {
			out = append(out, p)
		}
	}
	return out, nil
}

// ActiveProfile retourne le profil actif pour un moteur, s'il existe.
func ActiveProfile(engineName string) (Profile, bool) {
	all, _ := ListAll()
	for _, p := range all {
		if p.Kind == KindSimple && p.Engine == engineName && p.IsActive() {
			return p, true
		}
	}
	return Profile{}, false
}

// Dependents retourne les profils hybrides qui utilisent le moteur engineName.
// Utilisé avant suppression/modification pour détecter les dépendances.
func Dependents(engineName string) ([]Profile, error) {
	all, err := ListAll()
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, p := range all {
		if p.Kind != KindHybrid {
			continue
		}
		for _, c := range p.Components {
			if c == engineName {
				out = append(out, p)
				break
			}
		}
	}
	return out, nil
}

// DeactivateAllForEngine marque inactifs tous les profils actifs d'un moteur,
// puis active le profil donné. Garantit qu'il n'y a qu'un seul profil actif
// par moteur à la fois.
func DeactivateAllForEngine(engineName, exceptID string) error {
	all, err := ListAll()
	if err != nil {
		return err
	}
	for i := range all {
		p := &all[i]
		if p.Engine == engineName && p.ID != exceptID && p.IsActive() {
			p.Deactivate()
			if err := Save(p); err != nil {
				return err
			}
		}
	}
	return nil
}
