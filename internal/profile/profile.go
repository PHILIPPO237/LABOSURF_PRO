// Package profile implémente le système de profils nommés de LABOSURF PRO.
//
// Un profil est une configuration nommée et versionnée pour un moteur ou une
// composition de moteurs. Il permet d'avoir plusieurs configurations par moteur
// (ex: Xray-Production, Xray-Test, Xray-Backup), de les activer/désactiver
// indépendamment, et de composer des profils hybrides (SlowDNS→SSH,
// DNSTT→Xray) en réutilisant la logique existante de CompositeEngine.
//
// Stockage : $LABOSURF_DATA_DIR/profiles/<id>.json — un fichier par profil.
package profile

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// ProfileKind décrit le type d'un profil.
type ProfileKind string

const (
	// KindSimple : profil pour un seul moteur (ex : Xray-Production).
	KindSimple ProfileKind = "simple"
	// KindHybrid : profil composé de plusieurs moteurs chaînés (ex : DNSTT→Xray).
	KindHybrid ProfileKind = "hybrid"
)

// ProfileStatus décrit l'état d'un profil.
type ProfileStatus string

const (
	// StatusDraft : profil créé mais pas encore appliqué au moteur.
	StatusDraft ProfileStatus = "draft"
	// StatusActive : profil actuellement appliqué et en service.
	StatusActive ProfileStatus = "active"
	// StatusInactive : profil existant mais non actif (remplacé ou mis en veille).
	StatusInactive ProfileStatus = "inactive"
)

// Profile est une configuration nommée pour un ou plusieurs moteurs.
type Profile struct {
	// ID est un identifiant unique opaque (hex 8 chars), généré à la création.
	ID string `json:"id"`

	// Name est le nom lisible choisi par l'administrateur (ex: "Xray-Production").
	Name string `json:"name"`

	// Description optionnelle décrivant l'usage prévu du profil.
	Description string `json:"description,omitempty"`

	// Kind indique si le profil est simple (un moteur) ou hybride (chaîne).
	Kind ProfileKind `json:"kind"`

	// Engine est le nom du moteur pour les profils simples (vide pour hybrides).
	Engine string `json:"engine,omitempty"`

	// Components est la liste ORDONNÉE des moteurs pour les profils hybrides
	// (Components[0] = transport côté client, Components[last] = backend final).
	// Vide pour les profils simples.
	Components []string `json:"components,omitempty"`

	// Params conserve les paramètres de configuration sous forme de chaînes
	// lisibles (port, backend, jitter_ms, run_as_user, etc.). Ces valeurs sont
	// utilisées à l'affichage et à la reconstruction du JSON lors de l'activation.
	Params map[string]string `json:"params,omitempty"`

	// Status est l'état courant du profil.
	Status ProfileStatus `json:"status"`

	// CreatedAt est la date de création du profil.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt est la date de dernière modification.
	UpdatedAt time.Time `json:"updated_at"`
}

// NewID génère un identifiant de profil unique (8 octets hex = 16 caractères).
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewSimple crée un nouveau profil simple (brouillon, non activé).
func NewSimple(name, description, engineName string, params map[string]string) Profile {
	now := time.Now()
	p := Profile{
		ID:          NewID(),
		Name:        name,
		Description: description,
		Kind:        KindSimple,
		Engine:      engineName,
		Params:      map[string]string{},
		Status:      StatusDraft,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	for k, v := range params {
		p.Params[k] = v
	}
	return p
}

// NewHybrid crée un nouveau profil hybride (brouillon, non activé).
func NewHybrid(name, description string, components []string) Profile {
	now := time.Now()
	return Profile{
		ID:          NewID(),
		Name:        name,
		Description: description,
		Kind:        KindHybrid,
		Components:  append([]string(nil), components...),
		Params:      map[string]string{},
		Status:      StatusDraft,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// Duplicate crée une copie du profil avec un nouvel ID et le nom donné.
// Le statut du duplicate est toujours StatusDraft : une copie n'est pas
// automatiquement active même si l'original l'est.
func (p Profile) Duplicate(newName string) Profile {
	now := time.Now()
	dup := p
	dup.ID = NewID()
	dup.Name = newName
	dup.Status = StatusDraft
	dup.CreatedAt = now
	dup.UpdatedAt = now
	dup.Params = map[string]string{}
	for k, v := range p.Params {
		dup.Params[k] = v
	}
	dup.Components = append([]string(nil), p.Components...)
	return dup
}

// Activate marque le profil comme actif.
func (p *Profile) Activate() {
	p.Status = StatusActive
	p.UpdatedAt = time.Now()
}

// Deactivate marque le profil comme inactif.
func (p *Profile) Deactivate() {
	p.Status = StatusInactive
	p.UpdatedAt = time.Now()
}

// IsActive indique si le profil est actuellement actif.
func (p *Profile) IsActive() bool { return p.Status == StatusActive }

// StatusLabel retourne un label court pour l'affichage en menu.
func (p *Profile) StatusLabel() string {
	switch p.Status {
	case StatusActive:
		return "ACTIF"
	case StatusInactive:
		return "inactif"
	default:
		return "brouillon"
	}
}

// StatusBullet retourne le caractère bullet approprié au statut.
func (p *Profile) StatusBullet() string {
	switch p.Status {
	case StatusActive:
		return "●"
	default:
		return "○"
	}
}

// Param retourne la valeur d'un paramètre ou la valeur par défaut.
func (p *Profile) Param(key, fallback string) string {
	if v, ok := p.Params[key]; ok && v != "" {
		return v
	}
	return fallback
}

// SetParam écrit un paramètre dans le profil.
func (p *Profile) SetParam(key, value string) {
	if p.Params == nil {
		p.Params = map[string]string{}
	}
	p.Params[key] = value
}
