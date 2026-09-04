// Package srvcfg gère le profil de connexion du serveur LABOSURF PRO :
// adresse IP publique, domaines et ports par moteur. Il alimente la
// génération des configurations serveur et des liens clients.
package srvcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultFilename est le nom du fichier de profil dans le répertoire de données.
const DefaultFilename = "server.json"

// Profile décrit la configuration réseau de la machine serveur.
type Profile struct {
	// IP (ou domaine) publique du serveur, utilisée dans les liens clients.
	Host string `json:"host"`

	// Ports par moteur/protocole.
	Ports map[string]int `json:"ports,omitempty"`

	// Domaines autorisés (ex. pour dnstt/slowdns qui utilisent le DNS).
	Domains []string `json:"domains,omitempty"`
}

// DefaultPorts associe un port par défaut à chaque moteur.
func DefaultPorts() map[string]int {
	return map[string]int{
		"udp":      5667,
		"xray":     443,
		"hysteria": 8443,
		"slowdns":  53,
		"dnstt":    53,
		"ssh":      22,
	}
}

// Default construit un profil par défaut (host vide, ports par défaut).
func Default() Profile {
	return Profile{
		Host:    "",
		Ports:   DefaultPorts(),
		Domains: nil,
	}
}

// Path retourne le chemin du profil dans le répertoire de données LABOSURF.
func Path() string {
	dataDir := os.Getenv("LABOSURF_DATA_DIR")
	if dataDir == "" {
		dataDir = "/etc/labosurf"
	}
	return filepath.Join(dataDir, DefaultFilename)
}

// Load charge le profil ; un fichier absent donne le profil par défaut.
func Load() (Profile, error) {
	p := Default()
	path := Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return p, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return p, fmt.Errorf("profil serveur invalide (%s) : %w", path, err)
		}
	}
	if p.Ports == nil {
		p.Ports = DefaultPorts()
	}
	return p, nil
}

// Save persiste le profil (atomique).
func (p *Profile) Save() error {
	path := Path()
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Port retourne le port associé à un moteur (port par défaut sinon).
func (p *Profile) Port(engineName string) int {
	if p.Ports != nil {
		if v, ok := p.Ports[engineName]; ok && v > 0 {
			return v
		}
	}
	for k, v := range DefaultPorts() {
		if k == engineName {
			return v
		}
	}
	return 0
}

// SetPort définit le port associé à un moteur.
func (p *Profile) SetPort(engineName string, port int) {
	if p.Ports == nil {
		p.Ports = DefaultPorts()
	}
	p.Ports[engineName] = port
}