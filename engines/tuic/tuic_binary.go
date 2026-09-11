package tuic

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
)

// tuicServerVersion est la version de la ligne de release "tuic-server-*"
// du dépôt officiel tuic-protocol/tuic (anciennement EAimTY/tuic) pinnée par
// ce moteur. Vérifiée via l'API GitHub réelle au moment de l'intégration :
// https://github.com/tuic-protocol/tuic/releases/tag/tuic-server-1.0.0
// (dernière release de cette ligne à cette date). Si cette version est mise
// à jour, expectedSHA256For DOIT être remis à jour avec les nouveaux hash —
// sinon Install() échouera systématiquement (comportement sûr par défaut :
// refus explicite plutôt qu'un binaire non vérifié), voir AUDIT_TUIC_INTEGRATION.md §2.
const tuicServerVersion = "1.0.0"

const tuicReleaseBaseURL = "https://github.com/tuic-protocol/tuic/releases/download"

// TUICEngine pilote le binaire officiel tuic-server en sous-processus,
// exactement comme engines/xray pilote Xray-core (voir
// AUDIT_TUIC_INTEGRATION.md §1.3 pour la justification de ce choix plutôt
// qu'une réimplémentation Go du protocole).
type TUICEngine struct {
	configPath string

	mu     sync.Mutex // protège cmd/cancel/done contre Start/Stop/Endpoint concurrents
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan error
}

// NewTUICEngine construit le moteur TUIC avec ses chemins par défaut,
// surchageables comme les autres moteurs via variables d'environnement.
func NewTUICEngine() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/tuic/config.json"
	if p := os.Getenv("LABOSURF_TUIC_CONFIG"); p != "" {
		cfgPath = p
	}
	return &TUICEngine{configPath: cfgPath}, nil
}

func (e *TUICEngine) Name() string        { return engineName }
func (e *TUICEngine) Version() string     { return tuicServerVersion }
func (e *TUICEngine) Description() string { return engineDesc }

// binarySpec décrit la source du binaire tiers officiel tuic-server, via
// l'abstraction générique engineutil.BinarySpec (téléchargement + SHA-256 +
// déploiement) — déjà présente dans le dépôt mais utilisée par aucun moteur
// avant cette intégration. Contrairement à Xray-core, les releases
// tuic-server publient un binaire brut par cible (pas d'archive zip).
func binarySpec() *engineutil.BinarySpec {
	return &engineutil.BinarySpec{
		Name:        engineName,
		BinName:     "tuic-server",
		InstallName: "tuic-server",
		URL: func(arch string) string {
			target := rustTarget(arch)
			return fmt.Sprintf("%s/tuic-server-%s/tuic-server-%s-%s",
				tuicReleaseBaseURL, tuicServerVersion, tuicServerVersion, target)
		},
		SHA256: func(arch string) string {
			return expectedSHA256For(rustTarget(arch))
		},
		IsArchive: false,
	}
}

// rustTarget associe l'architecture normalisée LABOSURF
// (engineutil.DetectArch : "amd64"/"arm64") à la cible Rust officielle
// publiée par tuic-protocol/tuic. Les variantes "musl" (liaison statique)
// sont choisies plutôt que "gnu" pour ne dépendre d'aucune version de glibc
// sur le VPS cible — même logique que les binaires statiques CGO_ENABLED=0
// déjà utilisés pour les autres moteurs Go de ce dépôt.
func rustTarget(arch string) string {
	switch arch {
	case "arm64":
		return "aarch64-unknown-linux-musl"
	default:
		return "x86_64-unknown-linux-musl"
	}
}

// expectedSHA256For retourne le SHA-256 attendu (hex) pour une cible Rust
// donnée, pinné à tuicServerVersion.
//
// Valeurs obtenues PENDANT cette intégration en téléchargeant réellement
// chacun des deux binaires depuis la release officielle et en recalculant
// leur SHA-256 localement (sha256sum), puis confirmées identiques aux
// fichiers .sha256sum publiés par le projet à côté de chaque asset — pas une
// valeur recopiée sans vérification. Voir AUDIT_TUIC_INTEGRATION.md §2.
func expectedSHA256For(target string) string {
	switch target {
	case "x86_64-unknown-linux-musl":
		return "adb808944ca829857bf408d2ee438a156e8171543dbbed3e94964b66b4135393"
	case "aarch64-unknown-linux-musl":
		return "6ac43186b5197afaf0bc707c1241f2b3931d61356fc8354807884b68861a4892"
	default:
		return ""
	}
}

// Install télécharge (si nécessaire) le binaire tuic-server officiel et
// prépare un certificat TLS auto-signé — TUIC exige TLS pour son handshake
// QUIC, il n'existe pas de mode "sans TLS".
func (e *TUICEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return fmt.Errorf("création répertoire config TUIC : %w", err)
	}

	if _, _, err := engineutil.EnsureTUICCerts(engineutil.DefaultDataDir); err != nil {
		return fmt.Errorf("certificat TLS TUIC : %w", err)
	}
	log.Printf("✔ TUIC : certificat TLS auto-signé prêt")

	spec := binarySpec()
	if spec.Installed() {
		log.Printf("✔ TUIC : tuic-server %s déjà installé", tuicServerVersion)
		return nil
	}

	arch := cfg.Arch
	if arch == "" {
		arch = engineutil.DetectArch()
	}

	log.Printf("⬇ Téléchargement tuic-server %s pour %s...", tuicServerVersion, arch)
	path, err := spec.Download(ctx, arch)
	if err != nil {
		return fmt.Errorf("installation tuic-server : %w", err)
	}
	log.Printf("✔ TUIC : tuic-server %s installé (%s)", tuicServerVersion, path)
	return nil
}

// Configure applique une configuration au format officiel tuic-server (voir
// AUDIT_TUIC_INTEGRATION.md §2 pour le schéma exact, récupéré à la source).
// Elle complète certificate/private_key si absents (jamais un chemin
// fictif : exige que Install() ait déjà généré le certificat) et impose une
// adresse d'écoute par défaut si absente.
func (e *TUICEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration TUIC vide")
	}

	var raw map[string]any
	if err := json.Unmarshal(cfg.JSON, &raw); err != nil {
		return fmt.Errorf("JSON invalide : %w", err)
	}

	if strVal(raw["certificate"]) == "" || strVal(raw["private_key"]) == "" {
		certPath, keyPath, err := engineutil.EnsureTUICCerts(engineutil.DefaultDataDir)
		if err != nil {
			return fmt.Errorf("certificat TLS TUIC introuvable (lancez 'install' avant 'configure') : %w", err)
		}
		raw["certificate"] = certPath
		raw["private_key"] = keyPath
	}

	if strVal(raw["server"]) == "" {
		raw["server"] = "[::]:443"
	}

	if users, ok := raw["users"].(map[string]any); !ok || len(users) == 0 {
		return fmt.Errorf("configuration TUIC : au moins un utilisateur (uuid -> mot de passe) est requis")
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("sérialisation config TUIC : %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, out, 0o600); err != nil {
		return fmt.Errorf("écriture config TUIC : %w", err)
	}
	log.Printf("✔ TUIC : configuration écrite (%d utilisateur(s))", len(raw["users"].(map[string]any)))
	return nil
}

// Start démarre tuic-server en sous-processus.
//
// cmd.Start() est appelé de façon SYNCHRONE (pas cmd.Run() dans une
// goroutine séparée, comme le fait engines/xray) : seul cmd.Start() garantit
// que cmd.Process est déjà écrit quand Start() le lit (même goroutine, pas
// de dépendance à un délai de sleep pour "espérer" que le champ soit posé).
// Lire cmd.Process après un simple time.Sleep depuis une autre goroutine
// (le pattern xray) n'offre aucune relation happens-before réelle au sens
// du memory model Go — `go test -race` l'a confirmé pendant cette
// intégration sur ce moteur. cmd.Wait() est ensuite délégué à une goroutine
// dédiée (nécessaire : il bloque jusqu'à la fin du process).
func (e *TUICEngine) Start(ctx context.Context) error {
	spec := binarySpec()
	if !spec.Installed() {
		return fmt.Errorf("binaire tuic-server non trouvé (lancer 'install' d'abord)")
	}
	if _, err := os.Stat(e.configPath); err != nil {
		return fmt.Errorf("config TUIC non trouvée : %s (lancer 'configure' d'abord)", e.configPath)
	}

	runCtx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(runCtx, spec.DeployedPath(), "-c", e.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("démarrage tuic-server : %w", err)
	}

	done := make(chan error, 1)
	e.mu.Lock()
	e.cmd = cmd
	e.cancel = cancel
	e.done = done
	e.mu.Unlock()

	go func() {
		done <- cmd.Wait()
	}()

	// Laisse le temps au process d'échouer immédiatement s'il le fait (port
	// déjà utilisé, certificat illisible, config invalide, etc.) avant de le
	// déclarer démarré.
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-done:
		e.mu.Lock()
		e.cmd, e.cancel, e.done = nil, nil, nil
		e.mu.Unlock()
		return fmt.Errorf("tuic-server s'est arrêté immédiatement : %w", err)
	default:
	}

	log.Printf("✔ TUIC : tuic-server démarré (PID %d)", cmd.Process.Pid)
	return nil
}

// RunForeground lance le moteur en avant-plan et bloque jusqu'à son arrêt
// (systemd Type=simple).
func (e *TUICEngine) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	return <-done
}

// Stop arrête tuic-server proprement (SIGKILL après 5s si nécessaire).
//
// Attend sur le CANAL e.done déjà alimenté par la goroutine de Start()
// (qui appelle cmd.Run(), lequel attend déjà le process en interne) plutôt
// que d'appeler cmd.Wait() une seconde fois ici : *exec.Cmd ne supporte pas
// deux appels concurrents à Wait() sur la même commande — un appel
// supplémentaire ici (comme le fait actuellement engines/xray, à l'identique)
// provoque une vraie data race détectée par `go test -race` sur ce moteur
// pendant cette intégration (accès concurrent à cmd.ProcessState). Corrigé
// ici en réutilisant le résultat déjà publié par Start(), jamais dupliqué.
func (e *TUICEngine) Stop() error {
	e.mu.Lock()
	cancel := e.cancel
	cmd := e.cmd
	done := e.done
	e.mu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()

	if cmd != nil && cmd.Process != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if err := cmd.Process.Kill(); err != nil {
				return fmt.Errorf("kill forcé tuic-server : %w", err)
			}
			<-done
		}
	}

	e.mu.Lock()
	e.cmd, e.cancel, e.done = nil, nil, nil
	e.mu.Unlock()
	return nil
}

// Restart redémarre le moteur.
func (e *TUICEngine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

// Status retourne l'état courant du moteur.
func (e *TUICEngine) Status() engine.EngineStatus {
	installed := binarySpec().Installed()

	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()

	running := false
	pid := 0
	if cmd != nil && cmd.Process != nil {
		running = true
		pid = cmd.Process.Pid
	}

	st := engine.EngineStatus{Installed: installed, Running: running, PID: pid}
	if running {
		if ep, ok := e.Endpoint(); ok {
			st.ListenAddr = ep.Addr
			if _, portStr, err := net.SplitHostPort(ep.Addr); err == nil {
				fmt.Sscanf(portStr, "%d", &st.Port)
			}
			st.Health = "healthy"
		} else {
			st.Health = "unknown"
		}
	}
	return st
}

// configuredAddr lit le champ "server" (ex: "[::]:443") depuis le fichier de
// configuration réellement écrit sur disque par Configure() — celui que le
// process tuic-server a réellement chargé au démarrage.
func (e *TUICEngine) configuredAddr() (string, bool) {
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		return "", false
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", false
	}
	addr := strVal(cfg["server"])
	if addr == "" {
		return "", false
	}
	return addr, true
}

// Endpoint implémente engine.Endpointer. Contrairement aux moteurs Go
// natifs (hysteria/dnstt/slowdns/ssh), tuic-server est un processus externe
// qui ouvre lui-même son socket UDP/QUIC — ce process Go n'a donc aucune
// visibilité directe dessus (pas de net.Listener/net.UDPConn à interroger).
// Contrairement à Xray-core (TCP, sondable par une vraie tentative de
// connexion), TUIC est un protocole applicatif au-dessus d'UDP : une
// "connexion" UDP côté client ne prouve rien (UDP est sans connexion), et
// réaliser un vrai handshake QUIC/TUIC nécessiterait un client TUIC complet.
// Le signal vérifiable retenu ici est un "bind-probe" : tenter de lier
// nous-mêmes le port UDP configuré. Un succès prouve que RIEN n'écoute
// dessus (donc pas prêt) ; un échec ("address already in use") prouve qu'une
// socket UDP y est réellement liée. Ce n'est pas une preuve qu'un handshake
// TUIC y répondrait correctement — seulement qu'un processus a réellement
// pris ce port, jamais une déduction optimiste basée sur le seul délai de
// démarrage. Documenté aussi dans AUDIT_TUIC_INTEGRATION.md §4.
func (e *TUICEngine) Endpoint() (engine.Endpoint, bool) {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return engine.Endpoint{}, false
	}

	addr, ok := e.configuredAddr()
	if !ok {
		return engine.Endpoint{}, false
	}
	if !udpAddrBound(addr) {
		return engine.Endpoint{}, false
	}
	return engine.Endpoint{Network: "udp", Addr: addr}, true
}

// udpAddrBound indique si une socket UDP est déjà liée sur addr, en tentant
// de la lier nous-mêmes (voir le commentaire d'Endpoint ci-dessus pour le
// raisonnement complet).
func udpAddrBound(addr string) bool {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return false
	}
	l, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return true
	}
	l.Close()
	return false
}

// HealthCheck vérifie que le moteur est démarré ET que son port UDP/QUIC
// configuré est réellement occupé (voir Endpoint).
func (e *TUICEngine) HealthCheck() error {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("tuic-server non démarré")
	}
	if _, ok := e.Endpoint(); !ok {
		return fmt.Errorf("port QUIC configuré non occupé par le process (échec probable au démarrage)")
	}
	return nil
}

// Logs retourne un indicateur ; les journaux réels transitent par
// stdout/stderr du service systemd (même convention que les autres moteurs
// pilotant un binaire externe).
func (e *TUICEngine) Logs(lines int) ([]string, error) {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return []string{"tuic-server non démarré"}, nil
	}
	return []string{fmt.Sprintf("tuic-server (PID %d) : logs via stdout/stderr systemd", cmd.Process.Pid)}, nil
}

// Update réinstalle le binaire tuic-server (même version pinnée tant que
// tuicServerVersion n'est pas changé dans le code).
func (e *TUICEngine) Update() error {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		if err := e.Stop(); err != nil {
			return err
		}
	}

	spec := binarySpec()
	if err := os.Remove(spec.DeployedPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("suppression ancien binaire tuic-server : %w", err)
	}
	if _, err := spec.Download(context.Background(), engineutil.DetectArch()); err != nil {
		return fmt.Errorf("réinstallation tuic-server : %w", err)
	}
	return nil
}

// Uninstall arrête le moteur et supprime binaire + configuration.
func (e *TUICEngine) Uninstall() error {
	_ = e.Stop()
	spec := binarySpec()
	_ = os.Remove(spec.DeployedPath())
	_ = os.Remove(e.configPath)
	return nil
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
