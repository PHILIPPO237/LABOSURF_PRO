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

// EngineErrorCode représente un code d'erreur standardisé pour les moteurs.
type EngineErrorCode string

const (
	// Erreurs de configuration
	ErrCodeConfigInvalid      EngineErrorCode = "CONFIG_INVALID"
	ErrCodeConfigMissing      EngineErrorCode = "CONFIG_MISSING"
	ErrCodeConfigVersion      EngineErrorCode = "CONFIG_VERSION_MISMATCH"

	// Erreurs de démarrage
	ErrCodeBinaryNotFound     EngineErrorCode = "BINARY_NOT_FOUND"
	ErrCodeBinaryNotExec      EngineErrorCode = "BINARY_NOT_EXECUTABLE"
	ErrCodeBuildFailed        EngineErrorCode = "BUILD_FAILED"
	ErrCodeStartFailed        EngineErrorCode = "START_FAILED"
	ErrCodeProcessExited      EngineErrorCode = "PROCESS_EXITED"
	ErrCodePortNotListening   EngineErrorCode = "PORT_NOT_LISTENING"

	// Erreurs d'authentification
	ErrCodeAuthFailed         EngineErrorCode = "AUTH_FAILED"
	ErrCodeLicenseInvalid     EngineErrorCode = "LICENSE_INVALID"
	ErrCodeLicenseExpired     EngineErrorCode = "LICENSE_EXPIRED"
	ErrCodeLicenseMissing     EngineErrorCode = "LICENSE_MISSING"

	// Erreurs réseau
	ErrCodePortInUse          EngineErrorCode = "PORT_IN_USE"
	ErrCodeBindFailed         EngineErrorCode = "BIND_FAILED"
	ErrCodeNetworkUnreachable EngineErrorCode = "NETWORK_UNREACHABLE"
	ErrCodeTimeout            EngineErrorCode = "TIMEOUT"

	// Erreurs de santé
	ErrCodeHealthCheckFailed  EngineErrorCode = "HEALTH_CHECK_FAILED"
	ErrCodeEngineUnreachable  EngineErrorCode = "ENGINE_UNREACHABLE"
	ErrCodeTrafficTestFailed  EngineErrorCode = "TRAFFIC_TEST_FAILED"

	// Erreurs de permission
	ErrCodePermissionDenied   EngineErrorCode = "PERMISSION_DENIED"
	ErrCodeRootRequired       EngineErrorCode = "ROOT_REQUIRED"

	// Erreurs génériques
	ErrCodeInternalError      EngineErrorCode = "INTERNAL_ERROR"
	ErrCodeUnknown            EngineErrorCode = "UNKNOWN"
)

// EngineStatus décrit l'état courant d'un moteur.
type EngineStatus struct {
	// Installed indique si le moteur est installé sur le système.
	Installed bool

	// Running indique si le moteur est actuellement démarré.
	Running bool

	// PID est l'identifiant du processus du moteur (0 si non démarré).
	PID int

	// Port est le port d'écoute du moteur (0 si inconnu).
	Port int

	// ListenAddr est l'adresse d'écoute complète (ex: "0.0.0.0:443").
	ListenAddr string

	// Uptime est la durée de fonctionnement actuelle (vide si arrêté).
	Uptime string

	// Health indique l'état de santé : "healthy", "degraded", "unhealthy", "unknown".
	Health string

	// ErrorCode est le code d'erreur standardisé si le moteur est en erreur.
	ErrorCode EngineErrorCode

	// Error porte un éventuel message d'erreur de l'état.
	Error string

	// StartedAt est l'heure de démarrage du moteur (RFC3339).
	StartedAt string
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

// Endpoint décrit une adresse réseau réellement liée par un moteur démarré.
// Network vaut "tcp" ou "udp" selon le type de socket réellement ouvert.
type Endpoint struct {
	Network string
	Addr    string
}

// Endpointer est une interface optionnelle qu'un moteur peut implémenter en
// plus d'Engine pour exposer l'adresse réseau qu'il écoute réellement une
// fois démarré — nécessaire au chaînage de moteurs hybrides (un moteur
// transport doit savoir où relayer le trafic vers le moteur suivant).
//
// C'est volontairement une interface séparée d'Engine (à vérifier par
// assertion de type, `sub.(Endpointer)`), pas une méthode ajoutée à Engine :
// tous les moteurs n'ont pas nécessairement un endpoint réseau propre à
// exposer (ex : un futur moteur purement local), et ça évite d'imposer une
// implémentation à tout type Engine existant ou futur qui n'en a pas besoin.
type Endpointer interface {
	// Endpoint retourne l'adresse réellement liée et true si elle est
	// connue et prête à recevoir des connexions. Retourne (Endpoint{},
	// false) si le moteur n'est pas démarré, vient d'être arrêté, ou si
	// son endpoint n'est pas encore déterminable — JAMAIS une adresse
	// fictive ou un placeholder (ex : jamais "127.0.0.1:0").
	Endpoint() (Endpoint, bool)
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
