// Package engineudp fournit le moteur UDP de la plateforme LABOSURF PRO,
// traité EXACTEMENT comme les autres moteurs (xray, hysteria, dnstt, ssh, ...).
//
// Le serveur UDP réel (auth, tunnels, store central, portail) vit dans
// engines/udp (sous-module historique) et est exposé par le binaire `labosurf`
// via la commande `udp server -c <config>`. Ce moteur racine supervise ce
// binaire (défaut : `labosurf`, surchargeable par LABOSURF_UDP_BIN) pour que
// UDP soit piloté par le menu central comme n'importe quel autre moteur
// (install/configure/start/stop/status/...), sans cycle de re-supervision.
package engineudp

import (
	"context"
	"os"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
)

const (
	name      = "udp"
	desc      = "Moteur VPN UDP natif LABOSURF PRO (transport UDP personnalisé, store central + portail)."
	binName   = "labosurf"
	envPrefix = "LABOSURF_UDP"
	version   = "1.1.0"
)

// UDPEngine est le moteur du serveur UDP natif.
type UDPEngine struct {
	*engineutil.SystemEngine
}

// udpBinary retourne le binaire à superviser : LABOSURF_UDP_BIN ou défaut.
func udpBinary() string {
	if p := os.Getenv("LABOSURF_UDP_BIN"); p != "" {
		return p
	}
	return binName
}

// New fabrique une instance du moteur udp.
func New() (engine.Engine, error) {
	e := engineutil.NewSupervisedEngine(name, version, desc, binName, envPrefix)
	// Le serveur UDP est le binaire historique `labosurf` commandé
	// `udp server -c <config>`. Aucun binaire tierce à télécharger.
	e.Command = udpBinary()
	e.Init = func(ctx context.Context, cfg engine.EngineConfig) ([]string, error) {
		return []string{"udp", "server", "-c", e.ConfigPath}, nil
	}
	return &UDPEngine{SystemEngine: e}, nil
}

// Install vérifie la présence du binaire serveur UDP. Contrairement aux
// moteurs tierce (qui téléchargent un binaire externe), le serveur UDP est
// fourni avec la plateforme (binaire `labosurf` déployé par l'installeur).
func (e *UDPEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	e.Command = udpBinary()
	if _, ok := engineutil.FindBinary(e.Command); !ok {
		return &engineutil.ErrNotInstalled{Engine: name}
	}
	e.EnsureConfig()
	return nil
}

// Update : le serveur UDP étant fourni avec la plateforme, il n'y a rien à
// télécharger. On vérifie simplement la présence du binaire.
func (e *UDPEngine) Update() error {
	e.Command = udpBinary()
	if _, ok := engineutil.FindBinary(e.Command); !ok {
		return &engineutil.ErrNotInstalled{Engine: name}
	}
	return nil
}

func init() {
	engine.Register(name, New)
}