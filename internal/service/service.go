// Package service gère les instances nommées de moteurs (Service) et les
// droits d'accès des abonnés à ces instances (Access).
//
// Un Service représente une configuration en cours d'exécution d'un moteur :
// deux services peuvent utiliser le même moteur sur des ports différents.
// Un hybride (ex: DNSTT→Xray) est UN SEUL Service (Engine="dnstt-xray",
// Components=["dnstt","xray"]).
//
// Un Access représente les droits d'un abonné sur un Service précis.
// Révoquer un Access ne supprime pas l'abonné.
//
// Stockage :
//
//	$LABOSURF_DATA_DIR/services/<id>.json — un fichier par service
//	$LABOSURF_DATA_DIR/access/<id>.json   — un fichier par accès
package service

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// newID génère un identifiant unique opaque (8 octets hex = 16 caractères),
// identique à la convention de profile.NewID().
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Service représente une instance nommée et configurée d'un moteur.
//
// Exemples :
//
//	"Xray-Production" — moteur xray, port 443
//	"Xray-Test"       — moteur xray, port 8443 (service distinct)
//	"SSH-Prod"        — moteur ssh, port 22
//	"DNSTT-Xray-Prod" — hybride dnstt→xray, un seul Service
type Service struct {
	// ID est un identifiant unique opaque (16 caractères hex).
	ID string `json:"id"`

	// Name est le nom lisible choisi par l'administrateur.
	// Ex: "Xray-Production", "SSH-Backup".
	Name string `json:"name"`

	// Engine est le nom du moteur simple ou du moteur hybride composé.
	// Ex: "xray", "ssh", "wireguard", "dnstt-xray", "slowdns-ssh".
	Engine string `json:"engine"`

	// ProfileID référence le profile.Profile V2 utilisé lors de la création.
	// Changer le profil actif du moteur ne modifie PAS silencieusement ce champ :
	// une mise à jour explicite est requise pour changer le profil d'un Service.
	ProfileID string `json:"profile_id,omitempty"`

	// Components liste les noms de moteurs composant un service hybride, dans
	// l'ordre du chaînage (Components[0]=transport, Components[last]=backend).
	// Vide pour un service simple.
	// Ex: ["dnstt", "xray"] pour un hybride DNSTT→Xray.
	Components []string `json:"components,omitempty"`

	// Host est l'adresse IP ou le domaine public du serveur.
	Host string `json:"host"`

	// Ports associe un rôle à un numéro de port.
	// Convention habituelle : {"listen": <port d'écoute>, "public": <port annoncé>}.
	// Pour les moteurs simples, "listen" suffit généralement.
	Ports map[string]int `json:"ports,omitempty"`

	// Domains liste les noms de domaine associés à ce service.
	// Requis pour les moteurs de type DNS tunnel (dnstt, slowdns).
	Domains []string `json:"domains,omitempty"`

	// TLSMode décrit le mode de terminaison TLS.
	// Valeurs possibles : "reality", "tls", "none".
	TLSMode string `json:"tls_mode,omitempty"`

	// Enabled indique si le service est actif.
	Enabled bool `json:"enabled"`

	// CreatedAt et UpdatedAt sont les horodatages de création et de modification.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewService crée un nouveau Service avec un ID et des timestamps initialisés.
func NewService(name, engine, host string, ports map[string]int) Service {
	now := time.Now()
	svc := Service{
		ID:        newID(),
		Name:      name,
		Engine:    engine,
		Host:      host,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if len(ports) > 0 {
		svc.Ports = make(map[string]int, len(ports))
		for k, v := range ports {
			svc.Ports[k] = v
		}
	}
	return svc
}

// IsHybrid indique si le service est composé de plusieurs moteurs.
func (s *Service) IsHybrid() bool {
	return len(s.Components) > 0
}

// StatusBullet retourne le bullet d'affichage selon l'état du service.
func (s *Service) StatusBullet() string {
	if s.Enabled {
		return "●"
	}
	return "○"
}

// StatusLabel retourne le libellé d'état pour les menus.
func (s *Service) StatusLabel() string {
	if s.Enabled {
		return "ACTIF"
	}
	return "inactif"
}

// ListenPort retourne le port d'écoute principal du service.
// Cherche d'abord "listen", puis "public", puis le premier port disponible.
func (s *Service) ListenPort() int {
	if p, ok := s.Ports["listen"]; ok && p > 0 {
		return p
	}
	if p, ok := s.Ports["public"]; ok && p > 0 {
		return p
	}
	for _, p := range s.Ports {
		if p > 0 {
			return p
		}
	}
	return 0
}

// Access représente le droit d'un abonné à utiliser un Service.
//
// Un abonné peut posséder plusieurs Access (un par service auquel il est rattaché).
// Révoquer un Access ne supprime jamais le compte de l'abonné.
//
// Pour un service hybride, un seul Access contient TOUS les secrets nécessaires
// aux composants (ex: uuid pour xray + public_key pour dnstt dans le même map).
type Access struct {
	// ID est un identifiant unique opaque.
	ID string `json:"id"`

	// AccountID référence le compte abonné (store.Account.ID).
	AccountID string `json:"account_id"`

	// ServiceID référence le service (service.Service.ID).
	ServiceID string `json:"service_id"`

	// Engine est le nom du moteur, dénormalisé depuis Service pour éviter
	// une lecture en cascade lors de la génération de config.
	Engine string `json:"engine"`

	// Secrets contient les identifiants spécifiques au protocole.
	// Le contenu varie selon Engine — exemples :
	//   xray        → {"uuid": "..."}
	//   ssh         → {"public_key": "...", "private_key": "..."}
	//   dnstt       → {"public_key": "...", "private_key": "..."}
	//   wireguard   → {"private_key": "...", "public_key": "...", "address": "..."}
	//   tuic        → {"uuid": "...", "password": "..."}
	//   hysteria(2) → {"password": "..."}
	//   hybrides    → union des secrets de chaque composant
	// Les secrets sont générés par EnsureAccessSecrets (étape M2).
	Secrets map[string]any `json:"secrets,omitempty"`

	// --- Quota ---
	// QuotaUnlimited=true  : aucune limite de bande passante.
	//   QuotaLimitBytes est ignoré.
	// QuotaUnlimited=false : QuotaLimitBytes est la limite réelle en octets.
	//   Une limite explicitement à 0 signifie 0 octet autorisé (refus complet),
	//   et non "illimité" — utiliser QuotaUnlimited=true pour l'illimité.
	QuotaUnlimited  bool   `json:"quota_unlimited"`
	QuotaLimitBytes uint64 `json:"quota_limit_bytes,omitempty"`

	// UsedBytes est le trafic consommé sur cet accès, persisté.
	UsedBytes int64 `json:"used_bytes"`

	// --- Appareils / Sessions ---
	// MaxDevices est le nombre maximum d'appareils distincts autorisés (IPs sources).
	// 0 = illimité.
	MaxDevices int `json:"max_devices"`

	// MaxConnections est le nombre maximum de connexions simultanées autorisées,
	// toutes connexions confondues. 0 = illimité.
	MaxConnections int `json:"max_connections"`

	// --- Expiration ---
	// ExpiresAt est la date d'expiration de l'accès au format RFC3339.
	// Chaîne vide = aucune expiration.
	// Ne doit pas dépasser Account.ExpiresAt si ce dernier est défini.
	ExpiresAt string `json:"expires_at,omitempty"`

	// Enabled indique si l'accès est actif.
	Enabled bool `json:"enabled"`

	// CreatedAt et UpdatedAt sont les horodatages de création et de modification.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewAccess crée un nouvel Access avec un ID et des timestamps initialisés.
func NewAccess(accountID, serviceID, engine string) Access {
	now := time.Now()
	return Access{
		ID:             newID(),
		AccountID:      accountID,
		ServiceID:      serviceID,
		Engine:         engine,
		Secrets:        make(map[string]any),
		QuotaUnlimited: true, // illimité par défaut — l'opérateur ajuste si besoin
		Enabled:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// IsExpired indique si l'accès a expiré à l'instant présent.
// Retourne false si ExpiresAt est vide (pas d'expiration).
func (a *Access) IsExpired() bool {
	if a.ExpiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, a.ExpiresAt)
	if err != nil {
		return false // date invalide → on ne bloque pas par prudence
	}
	return time.Now().After(t)
}

// QuotaExceeded indique si le quota de bande passante est dépassé.
// Retourne toujours false si QuotaUnlimited=true.
func (a *Access) QuotaExceeded() bool {
	if a.QuotaUnlimited {
		return false
	}
	return a.UsedBytes >= 0 && uint64(a.UsedBytes) >= a.QuotaLimitBytes
}

// StatusBullet retourne le bullet d'affichage selon l'état de l'accès.
func (a *Access) StatusBullet() string {
	if a.Enabled {
		return "●"
	}
	return "○"
}
