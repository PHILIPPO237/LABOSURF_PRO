package engineutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"labosurf/internal/engine"
)

// ErrNotInstalled signale qu'un moteur n'est pas encore installé sur le système.
type ErrNotInstalled struct{ Engine string }

func (e *ErrNotInstalled) Error() string {
	return "le moteur " + e.Engine + " n'est pas installé : exécutez `labosurf engine " + e.Engine + " install`"
}

// SystemEngine est une base réutilisable qui implémente l'interface
// engine.Engine pour les moteurs qui exécutent un binaire externe
// (xray, slowdns, dnstt, hysteria, ...).
//
// La gestion du processus repose sur un fichier PID (répertoire de données
// du moteur), ce qui rend le cycle de vie robuste y compris lorsque chaque
// sous-commande CLI (start/stop/status) est exécutée dans un processus séparé.
type SystemEngine struct {
	// Spec décrit l'identité du moteur.
	Spec struct {
		Name        string
		Version     string
		Description string
	}

	// Command est le nom du binaire à lancer (cherché dans le PATH),
	// utilisé uniquement si BinaryPath est vide.
	Command string

	// BinaryPath est le chemin absolu du binaire à lancer (binaire tierce
	// déployé par le superviseur). S'il est défini, il prime sur Command.
	BinaryPath string

	// Binary est la spec de déploiement du binaire tierce (optionnel, pour
	// le cas supervisé). Utilisé par InstallFromSpec.
	Binary *BinarySpec

	// Args sont les arguments fixes passés au binaire au démarrage.
	Args []string

	// Init est appelé au démarrage pour calculer d'éventuels arguments
	// supplémentaires. Peut être nil.
	Init func(ctx context.Context, cfg engine.EngineConfig) ([]string, error)

	// ConfigPath est le chemin du fichier de configuration à écrire lors
	// d'un Configure (optionnel).
	ConfigPath string

	// InstallName est le nom sous lequel déployer le binaire tierce
	// (utilisé par le superviseur). Optionnel.
	InstallName string

	// envPrefix est le préfixe des variables d'environnement de la source
	// binaire (voir NewSupervisedEngine).
	envPrefix string
}

// NewSupervisedEngine construit un SystemEngine autonome qui supervise un
// binaire tierce. C'est le "même principe qu'UDP" : le binaire LABOSURF du
// moteur télécharge, déploie (SHA-256 vérifié) puis supervise le vrai moteur
// tierce (xray, hysteria, ...).
//
// La source du binaire est décrite par des variables d'environnement
// préfixées par envPrefix :
//   - <PREFIX>_BINARY_URL        : URL de téléchargement (obligatoire)
//   - <PREFIX>_BINARY_URL_<ARCH> : URL spécifique à une arch (amd64/arm64)
//   - <PREFIX>_BINARY_SHA256     : SHA-256 attendu (hex, optionnel)
//   - <PREFIX>_BINARY_ARCHIVE    : "1" si le téléchargé est une archive
//   - <PREFIX>_BINARY_NAME       : nom du binaire à extraire de l'archive
func NewSupervisedEngine(name, version, desc, binName, envPrefix string) *SystemEngine {
	e := &SystemEngine{}
	e.Spec.Name = name
	e.Spec.Version = version
	e.Spec.Description = desc
	e.InstallName = binName
	e.envPrefix = envPrefix
	e.ConfigPath = fmt.Sprintf("%s/config.json", EngineDataDir(name))

	e.Binary = &BinarySpec{
		Name:        name,
		BinName:     EnvOr(envPrefix+"_BINARY_NAME", binName),
		InstallName: binName,
		IsArchive:   EnvOr(envPrefix+"_BINARY_ARCHIVE", "") == "1",
		URL: func(arch string) string {
			if u := EnvOr(envPrefix+"_BINARY_URL_"+arch, ""); u != "" {
				return u
			}
			return EnvOr(envPrefix+"_BINARY_URL", "")
		},
		SHA256: func(arch string) string {
			if s := EnvOr(envPrefix+"_BINARY_SHA256_"+arch, ""); s != "" {
				return s
			}
			return EnvOr(envPrefix+"_BINARY_SHA256", "")
		},
	}
	return e
}

// Name retourne l'identifiant unique du moteur.
func (e *SystemEngine) Name() string { return e.Spec.Name }

// Version retourne la version du moteur.
func (e *SystemEngine) Version() string { return e.Spec.Version }

// Description fournit une courte description du moteur.
func (e *SystemEngine) Description() string { return e.Spec.Description }

// EnsureConfig définit un ConfigPath par défaut si absent.
func (e *SystemEngine) EnsureConfig() {
	if e.ConfigPath == "" {
		e.ConfigPath = fmt.Sprintf("%s/config.json", EngineDataDir(e.Name()))
	}
}

// Configure écrit la configuration brute dans ConfigPath.
func (e *SystemEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	e.EnsureConfig()
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration JSON vide pour %s", e.Name())
	}
	if err := EnsureDir(dirOf(e.ConfigPath)); err != nil {
		return err
	}
	return os.WriteFile(e.ConfigPath, cfg.JSON, 0o600)
}

// InstallFromSpec télécharge (via le superviseur) le binaire tierce du moteur
// et configure BinaryPath pour le lancement. C'est l'équivalent de l'étape
// "téléchargement + SHA-256 + déploiement" de labosurf-pro.sh.
func (e *SystemEngine) InstallFromSpec(ctx context.Context, spec *BinarySpec, arch string) error {
	p, err := spec.Download(ctx, arch)
	if err != nil {
		return err
	}
	e.BinaryPath = p
	e.Binary = spec
	e.Args = nil
	return nil
}

// resolveBinary détermine le chemin du binaire à lancer.
func (e *SystemEngine) resolveBinary() (string, error) {
	binary := e.BinaryPath
	if binary == "" && e.Binary != nil {
		binary = e.Binary.DeployedPath()
	}
	if binary == "" && e.Command != "" {
		bin, err := exec.LookPath(e.Command)
		if err != nil {
			return "", &ErrNotInstalled{Engine: e.Name()}
		}
		return bin, nil
	}
	if binary == "" {
		return "", fmt.Errorf("moteur %s : aucune commande configurée", e.Name())
	}
	if !Exists(binary) {
		return "", &ErrNotInstalled{Engine: e.Name()}
	}
	return binary, nil
}

// buildArgs calcule les arguments de lancement.
func (e *SystemEngine) buildArgs(ctx context.Context) ([]string, error) {
	args := append([]string{}, e.Args...)
	if e.Init != nil {
		extra, err := e.Init(ctx, engine.EngineConfig{})
		if err != nil {
			return nil, err
		}
		args = append(args, extra...)
	}
	return args, nil
}

// Start lance le binaire du moteur avec ses arguments et enregistre son PID.
func (e *SystemEngine) Start(ctx context.Context) error {
	binary, err := e.resolveBinary()
	if err != nil {
		return err
	}

	if pid, ok := ReadPID(e.Name()); ok && ProcessAlive(pid) {
		return nil // déjà démarré
	}

	args, err := e.buildArgs(ctx)
	if err != nil {
		return err
	}

	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("démarrage %s : %w", e.Name(), err)
	}

	_ = WritePID(e.Name(), cmd.Process.Pid)

	// Libère immédiatement : le PID-fichier est la source de vérité.
	go func() { _ = cmd.Wait() }()

	return nil
}

// RunForeground lance le binaire tierce en avant-plan et bloque jusqu'à son
// arrêt. Conçu pour être exécuté directement par systemd (Type=simple) qui
// devient alors le superviseur de processus.
func (e *SystemEngine) RunForeground(ctx context.Context) error {
	binary, err := e.resolveBinary()
	if err != nil {
		return err
	}

	args, err := e.buildArgs(ctx)
	if err != nil {
		return err
	}

	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("lancement %s : %w", e.Name(), err)
	}
	_ = WritePID(e.Name(), cmd.Process.Pid)

	if err := cmd.Wait(); err != nil {
		_ = os.Remove(PIDFile(e.Name()))
		return fmt.Errorf("moteur %s terminé : %w", e.Name(), err)
	}
	_ = os.Remove(PIDFile(e.Name()))
	return nil
}

// Stop arrête le processus du moteur identifié par son fichier PID.
func (e *SystemEngine) Stop() error {
	pid, ok := ReadPID(e.Name())
	if !ok {
		return nil
	}

	if ProcessAlive(pid) {
		proc, err := os.FindProcess(pid)
		if err == nil {
			_ = proc.Kill()
		}
	}
	_ = os.Remove(PIDFile(e.Name()))
	return nil
}

// Restart redémarre le moteur.
func (e *SystemEngine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

// Status retourne l'état courant du moteur.
func (e *SystemEngine) Status() engine.EngineStatus {
	st := engine.EngineStatus{}

	if e.Binary != nil {
		st.Installed = e.Binary.Installed()
	} else if e.BinaryPath != "" {
		st.Installed = Exists(e.BinaryPath)
	} else if e.Command != "" {
		_, st.Installed = FindBinary(e.Command)
	}

	if pid, ok := ReadPID(e.Name()); ok && ProcessAlive(pid) {
		st.Running = true
		st.PID = pid
	}
	return st
}

// HealthCheck vérifie que le moteur est opérationnel.
func (e *SystemEngine) HealthCheck() error {
	st := e.Status()
	if !st.Installed {
		return &ErrNotInstalled{Engine: e.Name()}
	}
	if !st.Running {
		return fmt.Errorf("moteur %s non démarré", e.Name())
	}
	return nil
}

// Logs indique que les journaux se lisent via systemd.
func (e *SystemEngine) Logs(lines int) ([]string, error) {
	if lines <= 0 {
		lines = 50
	}
	return nil, fmt.Errorf("journal de %s : lisez via `journalctl -u labosurf-%s`", e.Name(), e.Name())
}

// Uninstall supprime les données et le binaire déployé du moteur.
func (e *SystemEngine) Uninstall() error {
	_ = e.Stop()
	err := os.RemoveAll(EngineDataDir(e.Name()))
	if e.Binary != nil {
		err = os.RemoveAll(filepath.Join(BinaryDir(), e.Name()))
	}
	return err
}

func dirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "."
	}
	return path[:i]
}

// ParsePID est un utilitaire exporté (lecture de PID).
func ParsePID(data []byte) int {
	v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return v
}
