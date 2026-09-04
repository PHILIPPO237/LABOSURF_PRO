package engineutil

import (
	"encoding/json"
	"os"
	"path/filepath"

	"labosurf/internal/engine"
)

// hybridFile est le nom du fichier de composition des moteurs hybrides.
const hybridFile = "hybrids.json"

// HybridsPath retourne le chemin du fichier des hybrides composés.
func HybridsPath() string {
	dataDir := os.Getenv("LABOSURF_DATA_DIR")
	if dataDir == "" {
		dataDir = "/etc/labosurf"
	}
	return filepath.Join(dataDir, hybridFile)
}

// LoadHybrids charge la liste des compositions d'hybrides enregistrées.
// Un fichier absent renvoie une liste vide.
func LoadHybrids() ([][]string, error) {
	raw, err := os.ReadFile(HybridsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var comps [][]string
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &comps); err != nil {
		return nil, err
	}
	return comps, nil
}

// saveHybrids persiste la liste des compositions.
func saveHybrids(comps [][]string) error {
	path := HybridsPath()
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(comps, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// RegisterHybrid persiste ET enregistre un moteur hybride composé des moteurs
// donnés. Inutile de rappeler engine.Register : la persistance suffit puisque
// EnsureHybridsRegistered enregistre au démarrage.
func RegisterHybridPersist(components []string) (string, error) {
	comps, _ := LoadHybrids()
	name := HybridName(components)
	for _, existing := range comps {
		if HybridName(existing) == name {
			return name, nil
		}
	}
	if _, err := RegisterHybrid(components); err != nil {
		return "", err
	}
	comps = append(comps, append([]string(nil), components...))
	if err := saveHybrids(comps); err != nil {
		return "", err
	}
	return name, nil
}

// RemoveHybridPersist retire une composition hybride (fichier + registre).
// Le registre ne permet pas de retirer une entrée directement ; on passe par
// un enregistrement neutre si besoin. Le retrait du fichier suffit pour que
// l'hybride disparaisse au prochain démarrage.
func RemoveHybridPersist(name string) error {
	comps, err := LoadHybrids()
	if err != nil {
		return err
	}
	var out [][]string
	for _, c := range comps {
		if HybridName(c) != name {
			out = append(out, c)
		}
	}
	return saveHybrids(out)
}

// RemoveHybrid supprime un hybride composé : retire son enregistrement du
// registre en mémoire (session courante) ET de la liste persistée.
func RemoveHybrid(name string) error {
	engine.Remove(name)
	return RemoveHybridPersist(name)
}

// EnsureHybridsRegistered enregistre dans le registre tous les hybrides
// persistés. À appeler au démarrage du menu central.
func EnsureHybridsRegistered() error {
	comps, err := LoadHybrids()
	if err != nil {
		return err
	}
	for _, c := range comps {
		if _, err := RegisterHybrid(c); err != nil {
			// Un hybride dont un composant manque est ignoré.
			continue
		}
	}
	return nil
}