// Package store centralise la source de vérité des comptes clients LABOSURF.
// Un compte logique peut être rattaché à PLUSIEURS moteurs VPN via des grants ;
// le store est partagé par le menu central et tous les moteurs.
package store

import (
	"errors"
	"sort"
)

// Type de moteur/protocole auquel un compte peut être rattaché.
const (
	EngineUDP      = "udp"
	EngineXray     = "xray"
	EngineHysteria = "hysteria"
	EngineDNSTT    = "dnstt"
	EngineSlowDNS  = "slowdns"
	// EngineSSH : un compte peut disposer d'un accès SSH (shell via clé
	// publique) comme d'un accès aux tunnels VPN.
	EngineSSH = "ssh"
)

var (
	ErrAccountExists   = errors.New("compte déjà existant")
	ErrAccountNotFound = errors.New("compte introuvable")
	ErrOfferExists     = errors.New("offre déjà existante")
	ErrOfferNotFound   = errors.New("offre introuvable")
	ErrInvalidID       = errors.New("identifiant invalide")
	ErrTokenNotFound   = errors.New("lien client introuvable")
	ErrGrantExists     = errors.New("accès moteur déjà présent")
	ErrGrantNotFound   = errors.New("accès moteur introuvable")
)

// UserConfig est la projection d'un compte en règles appliquées par un moteur.
// C'est la forme minimale que chaque moteur consomme pour autoriser un client.
type UserConfig struct {
	Password       string `json:"password"`
	ExpiresAt      string `json:"expires_at"`
	QuotaBytes     uint64 `json:"quota_bytes"`
	MaxConnections int    `json:"max_connections"`
	MaxIPs         int    `json:"max_ips"`
	Enabled        bool   `json:"enabled"`
}

// EngineGrant est la configuration spécifique d'un compte pour UN moteur.
// Les champs varient selon le moteur : identifiants, port, cipher, etc.
type EngineGrant struct {
	Engine string `json:"engine"`

	// Configuration spécifique au moteur. Interprétation par moteur :
	//
	//	udp      -> {"password": "…"} (Password est déjà dans UserConfig)
	//	xray     -> {"uuid": "…", "port": 443, "flow": "xtls-rprx-vision"}
	//	hysteria -> {"password": "…", "up": 100000000, "down": 300000000}
	//	dnstt/slowdns -> {"public_key": "…", "server": "…", "port": 53}
	Config map[string]any `json:"config,omitempty"`

	Enabled bool `json:"enabled"`
}

// Account représente un compte client complet. Il est central et porte les
// accès (grants) vers un ou plusieurs moteurs VPN.
type Account struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	ExpiresAt      string `json:"expires_at"`
	QuotaBytes     uint64 `json:"quota_bytes"`
	MaxConnections int    `json:"max_connections"`
	MaxIPs         int    `json:"max_ips"`
	Enabled        bool   `json:"enabled"`

	// Offre commerciale rattachée (facultatif).
	OfferID string `json:"offer_id,omitempty"`

	// Token opaque du portail (jamais le mot de passe).
	Token string `json:"token,omitempty"`

	// Accès multi-moteurs : moteur -> grant. Un même compte peut être
	// connecté à plusieurs moteurs simultanément.
	Grants map[string]*EngineGrant `json:"grants,omitempty"`

	// Données runtime (non persistées) — utilisées par le portail HTTP.
	UsedBytes    int64    `json:"-"`
	CurrentConns int      `json:"-"`
	CurrentIPs   []string `json:"-"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Offer définit un modèle d'abonnement réutilisable.
type Offer struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DurationDays   int    `json:"duration_days"`
	QuotaBytes     uint64 `json:"quota_bytes"`
	MaxConnections int    `json:"max_connections"`
	MaxIPs         int    `json:"max_ips"`
}

// EngineGrants retourne une copie triée des grants par moteur.
func (a *Account) EngineGrants() []EngineGrant {
	out := make([]EngineGrant, 0, len(a.Grants))
	for _, g := range a.Grants {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Engine < out[j].Engine })
	return out
}

// HasEngine indique si le compte a un accès (grant) vers le moteur donné.
func (a *Account) HasEngine(engine string) bool {
	if a.Grants == nil {
		return false
	}
	_, ok := a.Grants[engine]
	return ok
}

// LinkedEngines retourne la liste des moteurs auxquels le compte est rattaché.
func (a *Account) LinkedEngines() []string {
	engs := make([]string, 0, len(a.Grants))
	for e := range a.Grants {
		engs = append(engs, e)
	}
	sort.Strings(engs)
	return engs
}
