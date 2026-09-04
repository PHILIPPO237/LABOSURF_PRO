// Package engine définit le contrat commun que chaque moteur VPN
// (UDP, Xray, DNS-over-HTTPS, etc.) doit implémenter pour être piloté
// par LABOSURF PRO.
//
// L'objectif est de rendre LABOSURF PRO une plateforme multi-moteurs :
// le binaire unique `labosurf` découvre les moteurs disponibles via un
// registre, et pilote leur cycle de vie (install, configure, start,
// stop, status, logs) à travers l'interface Engine.
package engine

import "context"

// EngineStatus décrit l'état courant d'un moteur.
type EngineStatus struct {
	// Installed indique si le moteur est installé sur le système.
	Installed bool

	// Running indique si le moteur est actuellement démarré.
	Running bool

	// PID est l'identifiant du processus du moteur (0 si non démarré).
	PID int

	// Uptime est la durée de fonctionnement actuelle (vide si arrêté).
	Uptime string

	// Error porte un éventuel message d'erreur de l'état.
	Error string
}

// InstallConfig regroupe les paramètres nécessaires à l'installation
// d'un moteur sur une machine (typiquement un VPS Linux).
type InstallConfig struct {
	// Arch est l'architecture cible ("amd64", "arm64").
	Arch string

	// ListenPort est le port réseau principal du moteur.
	ListenPort int

	// DataDir est le répertoire de données du moteur
	// (par défaut /etc/labosurf/engines/<name>/).
	DataDir string

	// BinaryDir est le répertoire d'installation du binaire
	// (par défaut /opt/labosurf/).
	BinaryDir string
}

// EngineConfig transporte la configuration spécifique d'un moteur
// (par exemple le contenu de config.json pour le moteur UDP).
type EngineConfig struct {
	// JSON contient la configuration brute au format JSON.
	JSON []byte
}

// Engine est le contrat commun de tous les moteurs VPN.
type Engine interface {
	// Name retourne l'identifiant unique du moteur ("udp", "xray", ...).
	Name() string

	// Version retourne la version du moteur.
	Version() string

	// Description fournit une courte description du moteur.
	Description() string

	// Install déploie le moteur sur le système.
	Install(ctx context.Context, cfg InstallConfig) error

	// Configure applique la configuration au moteur.
	Configure(ctx context.Context, cfg EngineConfig) error

	// Start démarre le moteur (bloquant jusqu'à l'arrêt).
	Start(ctx context.Context) error

	// RunForeground lance le moteur en avant-plan et bloque jusqu'à son
	// arrêt. Conçu pour être exécuté directement par systemd (Type=simple).
	RunForeground(ctx context.Context) error

	// Stop arrête le moteur.
	Stop() error

	// Restart redémarre le moteur.
	Restart(ctx context.Context) error

	// Status retourne l'état courant du moteur.
	Status() EngineStatus

	// HealthCheck vérifie que le moteur est opérationnel.
	HealthCheck() error

	// Logs retourne les dernières lignes de journal du moteur.
	Logs(lines int) ([]string, error)

	// Update met à jour le moteur vers la dernière version.
	Update() error

	// Uninstall désinstalle le moteur du système.
	Uninstall() error
}
