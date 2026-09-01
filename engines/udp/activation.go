package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ============================================================
// ACTIVATION DE LICENCE — Côté CLIENT (LABOSURF PRO)
// ============================================================
//
// FLUX COMPLET :
//
//	ADMIN                                CLIENT
//	  │                                    │
//	  │ license create ──► jeton ──────────►│
//	  │                                    │ license activate <jeton>
//	  │                                    ▼
//	  │                            vérification Ed25519
//	  │                                    ▼
//	  │                            contrôle expiration
//	  │                                    ▼
//	  │                            contrôle révocation
//	  │                                    ▼
//	  │                            contrôle usage unique
//	  │                                    ▼
//	  │                            enregistrement (disque)
//	  │                                    ▼
//	  │                            LABOSURF PRO autorisé
//
// PERSISTANCE : l'activation survit au redémarrage (fichier activation.json).
//
// USAGE UNIQUE : une licence déjà activée ne peut pas être ré-activée.
// L'activation est liée à un identifiant technique d'installation
// (machine ID) : copier le fichier d'activation sur une autre machine
// l'invalide.
//
// LIMITATION HONNÊTE : en mode hors-ligne, le client ne peut pas savoir
// qu'une licence a été activée sur une AUTRE machine. La liaison au
// machine ID empêche la copie du fichier d'activation, et le registre
// administrateur permet la révocation, mais un contrôle mondial d'unicité
// exigerait un serveur d'activation en ligne (non implémenté ici).

const (
	defaultMachineIDPath  = "/etc/labosurf/machine.id"
	defaultActivationPath = "/etc/labosurf/activation.json"
	defaultRegistryPath   = "/etc/labosurf/licenses.json"
)

// ---------- Identifiant technique d'installation ----------

// MachineID retourne un identifiant technique stable de l'installation.
//
// CONFIDENTIALITÉ : cet identifiant est une valeur ALÉATOIRE générée
// localement au premier lancement. Il ne contient AUCUNE donnée
// personnelle (pas d'adresse MAC, pas de nom d'hôte, pas de numéro de
// série). Il sert uniquement à empêcher la réutilisation abusive d'une
// licence par copie du fichier d'activation.
func MachineID(path string) (string, error) {
	if path == "" {
		path = defaultMachineIDPath
	}

	if raw, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(raw))
		if len(id) >= 32 {
			return id, nil
		}
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("génération de l'identifiant d'installation : %w", err)
	}
	id := hex.EncodeToString(b)

	if err := writeFileAtomic(path, []byte(id), 0o600); err != nil {
		return "", err
	}

	return id, nil
}

// writeFileAtomic écrit un fichier de façon atomique (temp + rename) avec
// les permissions demandées. Évite la corruption si le programme est
// interrompu pendant l'écriture.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("création du dossier %s : %w", dir, err)
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("écriture temporaire %s : %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("remplacement atomique %s : %w", path, err)
	}

	return nil
}

// ---------- Enregistrement d'activation (client) ----------

// ActivationRecord est la trace persistante d'une activation réussie.
type ActivationRecord struct {
	LicenseID   string `json:"license_id"`
	MachineID   string `json:"machine_id"`
	ActivatedAt string `json:"activated_at"`
	Token       string `json:"token"`
}

// ActivationStore gère la persistance de l'activation côté client.
type ActivationStore struct {
	path        string
	machinePath string

	mu     sync.RWMutex
	record *ActivationRecord
}

// LoadActivationStore charge (ou initialise) l'état d'activation local.
func LoadActivationStore(path, machinePath string) (*ActivationStore, error) {
	if path == "" {
		path = defaultActivationPath
	}
	if machinePath == "" {
		machinePath = defaultMachineIDPath
	}

	as := &ActivationStore{path: path, machinePath: machinePath}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return as, nil
		}
		return nil, fmt.Errorf("lecture de l'activation %s : %w", path, err)
	}

	if len(raw) == 0 {
		return as, nil
	}

	var rec ActivationRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("activation JSON invalide : %w", err)
	}

	as.record = &rec

	return as, nil
}

// Record retourne une copie de l'enregistrement d'activation courant.
func (as *ActivationStore) Record() (ActivationRecord, bool) {
	as.mu.RLock()
	defer as.mu.RUnlock()

	if as.record == nil {
		return ActivationRecord{}, false
	}
	return *as.record, true
}

// ActivationResult décrit l'issue d'une vérification d'activation.
type ActivationResult struct {
	Activated bool
	Status    LicenseStatus
	Data      LicenseData
	Record    ActivationRecord
}

// Activate active une licence à partir de son jeton.
//
// Contrôles effectués, dans l'ordre :
//  1. signature Ed25519 (clé publique) ;
//  2. expiration ;
//  3. révocation (si un registre est fourni) ;
//  4. usage unique (licence déjà activée quelque part) ;
//  5. liaison à l'identifiant d'installation.
//
// L'activation réussie est persistée atomiquement.
func (as *ActivationStore) Activate(token string, registry *LicenseRegistry) (ActivationResult, error) {
	data, status, err := VerifyLicenseToken(token)
	if err != nil {
		return ActivationResult{Status: status, Data: data}, err
	}

	// Fenêtre d'activation de 3 heures : elle ne s'applique
	// qu'au premier enregistrement de l'activation.
	deadline, err := time.Parse(time.RFC3339, data.ActivationUntil)
	if err != nil {
		return ActivationResult{Status: LicenseExpired, Data: data}, ErrLicenseExpired
	}
	if time.Now().UTC().After(deadline) {
		return ActivationResult{Status: LicenseExpired, Data: data}, ErrLicenseExpired
	}

	// Révocation : contrôlée si le registre administrateur est disponible.
	if registry != nil && registry.IsRevoked(data.ID) {
		return ActivationResult{Status: LicenseRevoked, Data: data}, ErrLicenseRevoked
	}

	// Usage unique GLOBAL : la licence ne doit pas avoir déjà été activée
	// sur une autre installation (vérifié via le registre).
	if registry != nil && registry.IsActivated(data.ID) {
		return ActivationResult{Status: LicenseActive, Data: data}, ErrAlreadyActivated
	}

	machineID, err := MachineID(as.machinePath)
	if err != nil {
		return ActivationResult{}, err
	}

	as.mu.Lock()
	defer as.mu.Unlock()

	// Usage unique LOCAL : la même licence ne peut pas être activée deux fois
	// sur cette installation.
	if as.record != nil && as.record.LicenseID == data.ID {
		return ActivationResult{
			Activated: true,
			Status:    LicenseActive,
			Data:      data,
			Record:    *as.record,
		}, ErrAlreadyActivated
	}

	rec := ActivationRecord{
		LicenseID:   data.ID,
		MachineID:   machineID,
		ActivatedAt: time.Now().UTC().Format(time.RFC3339),
		Token:       token,
	}

	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return ActivationResult{}, fmt.Errorf("sérialisation de l'activation : %w", err)
	}

	if err := writeFileAtomic(as.path, raw, 0o600); err != nil {
		return ActivationResult{}, err
	}

	as.record = &rec

	// Marque la licence comme activée dans le registre si disponible.
	if registry != nil {
		_ = registry.MarkActivated(data.ID, machineID)
	}

	return ActivationResult{
		Activated: true,
		Status:    LicenseActive,
		Data:      data,
		Record:    rec,
	}, nil
}

// Check vérifie l'état d'activation courant. Appelé au démarrage de
// LABOSURF PRO pour savoir si le logiciel est autorisé.
//
// Contrôles : présence d'un enregistrement, correspondance du machine ID,
// signature, expiration, révocation.
func (as *ActivationStore) Check(registry *LicenseRegistry) (ActivationResult, error) {
	as.mu.RLock()
	rec := as.record
	as.mu.RUnlock()

	if rec == nil {
		return ActivationResult{Status: LicenseUnknown}, ErrActivationMissing
	}

	machineID, err := MachineID(as.machinePath)
	if err != nil {
		return ActivationResult{}, err
	}

	// Liaison à l'installation : un fichier d'activation copié depuis une
	// autre machine est refusé.
	if rec.MachineID != machineID {
		return ActivationResult{
			Status: LicenseUnknown,
			Record: *rec,
		}, ErrWrongDevice
	}

	data, status, err := VerifyLicenseToken(rec.Token)
	if err != nil {
		return ActivationResult{Status: status, Data: data, Record: *rec}, err
	}

	if registry != nil && registry.IsRevoked(data.ID) {
		return ActivationResult{Status: LicenseRevoked, Data: data, Record: *rec}, ErrLicenseRevoked
	}

	return ActivationResult{
		Activated: true,
		Status:    LicenseActive,
		Data:      data,
		Record:    *rec,
	}, nil
}

// Deactivate supprime l'activation locale (le logiciel redevient non
// autorisé). N'annule pas la licence côté administrateur.
func (as *ActivationStore) Deactivate() error {
	as.mu.Lock()
	defer as.mu.Unlock()

	if as.record == nil {
		return ErrActivationMissing
	}

	if err := os.Remove(as.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("suppression de l'activation : %w", err)
	}

	as.record = nil

	return nil
}

// ---------- Registre administrateur ----------

// RegistryEntry est l'état d'une licence émise, côté administrateur.
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

// LicenseRegistry persiste les licences émises et leur état
// (NEW / ACTIVE / REVOKED). Source de vérité administrateur.
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
		if errors.Is(err, os.ErrNotExist) {
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

// List retourne les licences émises, triées par identifiant.
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

// MarkActivated passe une licence à l'état ACTIVE et mémorise
// l'identifiant d'installation qui l'a activée.
func (r *LicenseRegistry) MarkActivated(id, machineID string) error {
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
	e.ActivatedBy = machineID

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

// IsActivated indique si une licence a déjà été activée (usage unique).
func (r *LicenseRegistry) IsActivated(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.data.Licenses[id]
	if !ok {
		return false
	}
	return e.Status == LicenseActive
}

// Count retourne le nombre de licences émises.
func (r *LicenseRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.data.Licenses)
}
