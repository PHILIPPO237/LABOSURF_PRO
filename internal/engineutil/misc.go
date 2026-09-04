// Package engineutil fournit des helpers partagés entre les moteurs
// LABOSURF PRO (chemins, lancement de processus externes, état).
package engineutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

var (
	// DefaultDataDir est le répertoire de données des moteurs.
	// Surchargeable via LABOSURF_DATA_DIR.
	DefaultDataDir = EnvOr("LABOSURF_DATA_DIR", "/etc/labosurf")

	// DefaultBinaryDir est le répertoire des binaires installés.
	// Surchargeable via LABOSURF_BIN_DIR.
	DefaultBinaryDir = EnvOr("LABOSURF_BIN_DIR", "/opt/labosurf")
)

// EngineDir retourne le répertoire de données d'un moteur donné.
func EngineDataDir(name string) string {
	return filepath.Join(DefaultDataDir, "engines", name)
}

// EngineBinaryName retourne le nom du binaire d'un moteur.
func EngineBinaryName(name string) string {
	return filepath.Join(DefaultBinaryDir, "labosurf-"+name)
}

// FindBinary cherche le binaire `name` dans le PATH.
// Retourne (chemin, true) s'il est trouvé.
func FindBinary(name string) (string, bool) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return p, true
}

// DetectArch retourne l'architecture normalisée ("amd64" ou "arm64").
func DetectArch() string {
	switch runtime.GOARCH {
	case "arm64", "aarch64":
		return "arm64"
	default:
		return "amd64"
	}
}

// EnvOr retourne la valeur de la variable d'environnement key, ou fallback
// si elle est vide/absente.
func EnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Exists vérifie qu'un chemin existe.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// EnsureDir crée un répertoire (avec parents) s'il n'existe pas.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// PIDFile retourne le chemin du fichier PID d'un moteur.
func PIDFile(name string) string {
	return filepath.Join(EngineDataDir(name), "engine.pid")
}

// ReadPID lit un PID depuis un fichier ; -1 et false si absent/invalide.
func ReadPID(name string) (int, bool) {
	data, err := os.ReadFile(PIDFile(name))
	if err != nil {
		return -1, false
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil || pid <= 0 {
		return -1, false
	}
	return pid, true
}

// WritePID écrit un PID dans le fichier PID d'un moteur.
func WritePID(name string, pid int) error {
	if err := EnsureDir(EngineDataDir(name)); err != nil {
		return err
	}
	return os.WriteFile(PIDFile(name), []byte(fmt.Sprintf("%d\n", pid)), 0o644)
}

// ProcessAlive indique si un processus avec ce PID est vivant.
// Fonctionne de façon fiable sur Linux (environnement cible des moteurs).
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 = test d'existence sans tuer (prise en charge Linux).
	if err := proc.Signal(os.Signal(syscall.Signal(0))); err != nil {
		return false
	}
	return true
}
