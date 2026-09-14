package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Erreurs renvoyées par les opérations CRUD.
var (
	ErrServiceNotFound  = errors.New("service introuvable")
	ErrAccessNotFound   = errors.New("accès introuvable")
	ErrServiceHasAccess = errors.New("impossible de supprimer : des accès référencent encore ce service")
	ErrDuplicateAccess  = errors.New("un accès pour ce compte sur ce service existe déjà")
)

// ---------- Répertoires ----------

// ServicesDir retourne le répertoire de stockage des Services.
// $LABOSURF_DATA_DIR/services/ — un fichier JSON par service.
func ServicesDir() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LABOSURF_DATA_DIR")), "/")
	if base == "" {
		base = "/etc/labosurf"
	}
	return filepath.Join(base, "services")
}

// AccessDir retourne le répertoire de stockage des Access.
// $LABOSURF_DATA_DIR/access/ — un fichier JSON par accès.
func AccessDir() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LABOSURF_DATA_DIR")), "/")
	if base == "" {
		base = "/etc/labosurf"
	}
	return filepath.Join(base, "access")
}

func serviceFilePath(id string) string {
	return filepath.Join(ServicesDir(), id+".json")
}

func accessFilePath(id string) string {
	return filepath.Join(AccessDir(), id+".json")
}

// writeAtomic écrit des données JSON dans un fichier de façon atomique
// (écriture dans un fichier temporaire + renommage).
func writeAtomic(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("création répertoire %s : %w", dir, err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("sérialisation JSON : %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("écriture temporaire %s : %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("remplacement atomique %s : %w", path, err)
	}
	return nil
}

// ---------- CRUD Service ----------

// SaveService valide puis persiste un Service.
// Met à jour UpdatedAt avant l'écriture.
func SaveService(s *Service) error {
	if err := ValidateService(s); err != nil {
		return fmt.Errorf("service invalide : %w", err)
	}
	s.UpdatedAt = time.Now()
	return writeAtomic(serviceFilePath(s.ID), s)
}

// GetService charge un Service par son ID.
func GetService(id string) (Service, error) {
	raw, err := os.ReadFile(serviceFilePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Service{}, ErrServiceNotFound
		}
		return Service{}, fmt.Errorf("lecture service %q : %w", id, err)
	}
	var s Service
	if err := json.Unmarshal(raw, &s); err != nil {
		return Service{}, fmt.Errorf("service %q JSON invalide : %w", id, err)
	}
	return s, nil
}

// GetServiceByName cherche un Service par son nom (insensible à la casse).
// Retourne false comme second retour si aucun service ne correspond.
func GetServiceByName(name string) (Service, bool, error) {
	all, err := ListServices()
	if err != nil {
		return Service{}, false, err
	}
	for _, s := range all {
		if strings.EqualFold(s.Name, name) {
			return s, true, nil
		}
	}
	return Service{}, false, nil
}

// ListServices retourne tous les Services persistés.
// Un répertoire absent retourne une liste vide sans erreur.
func ListServices() ([]Service, error) {
	entries, err := os.ReadDir(ServicesDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture répertoire services : %w", err)
	}
	var out []Service
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		s, err := GetService(id)
		if err != nil {
			continue // fichier corrompu : ignoré
		}
		out = append(out, s)
	}
	return out, nil
}

// ListServicesByEngine retourne tous les Services utilisant un moteur donné.
// Inclut les services hybrides dont Engine correspond exactement.
func ListServicesByEngine(engineName string) ([]Service, error) {
	all, err := ListServices()
	if err != nil {
		return nil, err
	}
	var out []Service
	for _, s := range all {
		if s.Engine == engineName {
			out = append(out, s)
		}
	}
	return out, nil
}

// DeleteService supprime un Service. Refuse la suppression si des Access
// référencent encore ce service (ErrServiceHasAccess).
func DeleteService(id string) error {
	accesses, err := ListAccessByService(id)
	if err != nil {
		return fmt.Errorf("vérification des accès pour service %q : %w", id, err)
	}
	if len(accesses) > 0 {
		return fmt.Errorf("%w (%d accès actifs)", ErrServiceHasAccess, len(accesses))
	}
	err = os.Remove(serviceFilePath(id))
	if os.IsNotExist(err) {
		return ErrServiceNotFound
	}
	return err
}

// ---------- CRUD Access ----------

// SaveAccess valide puis persiste un Access.
// Met à jour UpdatedAt avant l'écriture.
func SaveAccess(a *Access) error {
	if err := ValidateAccess(a); err != nil {
		return fmt.Errorf("accès invalide : %w", err)
	}
	a.UpdatedAt = time.Now()
	return writeAtomic(accessFilePath(a.ID), a)
}

// GetAccess charge un Access par son ID.
func GetAccess(id string) (Access, error) {
	raw, err := os.ReadFile(accessFilePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Access{}, ErrAccessNotFound
		}
		return Access{}, fmt.Errorf("lecture accès %q : %w", id, err)
	}
	var a Access
	if err := json.Unmarshal(raw, &a); err != nil {
		return Access{}, fmt.Errorf("accès %q JSON invalide : %w", id, err)
	}
	return a, nil
}

// listAllAccess retourne tous les Access persistés (usage interne).
func listAllAccess() ([]Access, error) {
	entries, err := os.ReadDir(AccessDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture répertoire access : %w", err)
	}
	var out []Access
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		a, err := GetAccess(id)
		if err != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// ListAccessByAccount retourne tous les Access d'un abonné donné.
func ListAccessByAccount(accountID string) ([]Access, error) {
	all, err := listAllAccess()
	if err != nil {
		return nil, err
	}
	var out []Access
	for _, a := range all {
		if a.AccountID == accountID {
			out = append(out, a)
		}
	}
	return out, nil
}

// ListAccessByService retourne tous les Access d'un service donné.
func ListAccessByService(serviceID string) ([]Access, error) {
	all, err := listAllAccess()
	if err != nil {
		return nil, err
	}
	var out []Access
	for _, a := range all {
		if a.ServiceID == serviceID {
			out = append(out, a)
		}
	}
	return out, nil
}

// DeleteAccess supprime un Access par son ID.
func DeleteAccess(id string) error {
	err := os.Remove(accessFilePath(id))
	if os.IsNotExist(err) {
		return ErrAccessNotFound
	}
	return err
}

// AccessExists vérifie si un accès existe déjà pour la paire (accountID, serviceID).
// Retourne (true, id, nil) si trouvé, (false, "", nil) si absent.
// Permet d'éviter des doublons Account+Service.
func AccessExists(accountID, serviceID string) (bool, string, error) {
	all, err := listAllAccess()
	if err != nil {
		return false, "", err
	}
	for _, a := range all {
		if a.AccountID == accountID && a.ServiceID == serviceID {
			return true, a.ID, nil
		}
	}
	return false, "", nil
}
