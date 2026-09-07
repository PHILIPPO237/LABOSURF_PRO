package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// ============================================================
// REGISTRE LOCAL DES LICENCES — Historique administrateur
// ============================================================
//
// Le registre mémorise les licences vues sur cette machine :
// NEW (émise) / ACTIVE (a ouvert une installation) / REVOKED.
//
// Il ne bloque RIEN tout seul : la seule protection réelle est
// signature + fenêtre 3h + reçu d'installation (voir receipt.go).
// La révocation locale permet à l'administrateur de bloquer un ID
// à la main avant une (ré)installation.

// RegistryEntry est l'état d'une licence connue localement.
type RegistryEntry struct {
	ID              string        `json:"id"`
	IssuedAt        string        `json:"issued_at"`
	ActivationUntil string        `json:"activation_until"`
	Product         string        `json:"product"`
	Comment         string        `json:"comment,omitempty"`
	Status          LicenseStatus `json:"status"`
	Token           string        `json:"token"`
	ActivatedAt     string        `json:"activated_at,omitempty"`
	ActivatedBy     string        `json:"activated_by,omitempty"`
	RevokedAt       string        `json:"revoked_at,omitempty"`
}

type registryData struct {
	Licenses map[string]*RegistryEntry `json:"licenses"`
}

const defaultRegistryPath = "/etc/labosurf/licenses.json"

// LicenseRegistry persiste les licences connues et leur état
// (NEW / ACTIVE / REVOKED). Historique local administrateur.
type LicenseRegistry struct {
	path string
	mu   sync.RWMutex
	data registryData
}

// LoadLicenseRegistry charge (ou initialise) le registre des licences.
func LoadLicenseRegistry(path string) (*LicenseRegistry, error) {
	if path == "" {
		path = defaultRegistryPath
	}

	r := &LicenseRegistry{
		path: path,
		data: registryData{Licenses: make(map[string]*RegistryEntry)},
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("lecture du registre %s : %w", path, err)
	}

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &r.data); err != nil {
			return nil, fmt.Errorf("registre JSON invalide : %w", err)
		}
	}

	if r.data.Licenses == nil {
		r.data.Licenses = make(map[string]*RegistryEntry)
	}

	return r, nil
}

// saveLocked persiste le registre atomiquement (verrou détenu).
func (r *LicenseRegistry) saveLocked() error {
	raw, err := json.MarshalIndent(r.data, "", "  ")
	if err != nil {
		return fmt.Errorf("sérialisation du registre : %w", err)
	}
	return writeFileAtomic(r.path, raw, 0o600)
}

// Add enregistre une licence nouvellement émise à l'état NEW.
func (r *LicenseRegistry) Add(data LicenseData, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.data.Licenses[data.ID]; exists {
		return fmt.Errorf("licence %q déjà émise", data.ID)
	}

	entry := &RegistryEntry{
		ID:              data.ID,
		IssuedAt:        data.IssuedAt,
		ActivationUntil: data.ActivationUntil,
		Product:         data.Product,
		Comment:         data.Comment,
		Status:          LicenseNew,
		Token:           token,
	}

	r.data.Licenses[data.ID] = entry

	if err := r.saveLocked(); err != nil {
		delete(r.data.Licenses, data.ID)
		return err
	}

	return nil
}

// Get retourne une copie de l'entrée de registre.
func (r *LicenseRegistry) Get(id string) (RegistryEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.data.Licenses[id]
	if !ok {
		return RegistryEntry{}, false
	}
	return *e, true
}

// List retourne les licences connues, triées par identifiant.
func (r *LicenseRegistry) List() []RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]RegistryEntry, 0, len(r.data.Licenses))
	for _, e := range r.data.Licenses {
		out = append(out, *e)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out
}

// MarkUsed passe une licence à l'état ACTIVE : elle a ouvert une
// installation. Mémorise quand (pas de machine : le reçu local suffit).
func (r *LicenseRegistry) MarkUsed(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.data.Licenses[id]
	if !ok {
		return fmt.Errorf("licence %q inconnue du registre", id)
	}

	if e.Status == LicenseRevoked {
		return ErrLicenseRevoked
	}

	before := *e

	e.Status = LicenseActive
	e.ActivatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := r.saveLocked(); err != nil {
		*e = before
		return err
	}

	return nil
}

// Revoke révoque une licence de façon persistante.
func (r *LicenseRegistry) Revoke(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.data.Licenses[id]
	if !ok {
		return fmt.Errorf("licence %q inconnue du registre", id)
	}

	before := *e

	e.Status = LicenseRevoked
	e.RevokedAt = time.Now().UTC().Format(time.RFC3339)

	if err := r.saveLocked(); err != nil {
		*e = before
		return err
	}

	return nil
}

// IsRevoked indique si une licence est révoquée.
func (r *LicenseRegistry) IsRevoked(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.data.Licenses[id]
	if !ok {
		return false
	}
	return e.Status == LicenseRevoked
}

// IsUsed indique si une licence a déjà ouvert une installation
// (usage unique, d'après le registre local).
func (r *LicenseRegistry) IsUsed(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.data.Licenses[id]
	if !ok {
		return false
	}
	return e.Status == LicenseActive
}

// Count retourne le nombre de licences connues.
func (r *LicenseRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.data.Licenses)
}
