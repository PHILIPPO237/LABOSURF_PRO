package hysteria2

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/secret"
)

// hysteria2ServerVersion est la version de tag "app/v<version>" du dépôt
// officiel pinnée par ce moteur. Vérifiée via l'API GitHub réelle au moment
// de l'intégration : releases stables (non pre-release) de
// https://github.com/HyNetworks/hysteria/releases — dernière version de
// cette ligne à cette date. Si cette version est mise à jour,
// expectedSHA256For DOIT être remis à jour avec les nouveaux hash — sinon
// Install() échouera systématiquement (comportement sûr par défaut : refus
// explicite plutôt qu'un binaire non vérifié, même discipline que TUIC).
const hysteria2ServerVersion = "2.12.2"

// hysteria2ReleaseBaseURL pointe le dépôt CANONIQUE. Le dépôt historique
// "apernet/hysteria" (utilisé dans toute la documentation publique et les
// liens usuels) redirige aujourd'hui (HTTP 301, vérifié pendant cette
// intégration) vers "HyNetworks/hysteria" — même projet, même mainteneur,
// nouvelle organisation GitHub. On utilise directement l'URL canonique pour
// ne pas dépendre d'une redirection à l'exécution.
const hysteria2ReleaseBaseURL = "https://github.com/HyNetworks/hysteria/releases/download"

// Hysteria2Engine pilote le binaire officiel `hysteria` (sous-commande
// `server`) en sous-processus, exactement comme engines/tuic pilote
// tuic-server et engines/xray pilote Xray-core (même modèle "binaire tiers
// officiel supervisé", jamais une réimplémentation).
type Hysteria2Engine struct {
	configPath string

	mu     sync.Mutex // protège cmd/cancel/done contre Start/Stop/Endpoint concurrents
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan error
}

// NewHysteria2Engine construit le moteur avec ses chemins par défaut,
// surchageables comme les autres moteurs via variable d'environnement.
func NewHysteria2Engine() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/hysteria2/config.yaml"
	if p := os.Getenv("LABOSURF_HYSTERIA2_CONFIG"); p != "" {
		cfgPath = p
	}
	return &Hysteria2Engine{configPath: cfgPath}, nil
}

func (e *Hysteria2Engine) Name() string        { return engineName }
func (e *Hysteria2Engine) Version() string     { return hysteria2ServerVersion }
func (e *Hysteria2Engine) Description() string { return engineDesc }

// binarySpec décrit la source du binaire tiers officiel `hysteria`, via
// l'abstraction générique engineutil.BinarySpec (téléchargement + SHA-256 +
// déploiement) — même mécanisme déjà utilisé par TUIC et Xray. Contrairement
// à Xray-core (archive zip), les releases hysteria publient un binaire brut
// par cible (comme tuic-server) — pas d'archive à extraire.
func binarySpec() *engineutil.BinarySpec {
	return &engineutil.BinarySpec{
		Name:        engineName,
		BinName:     "hysteria",
		InstallName: "hysteria",
		URL: func(arch string) string {
			target := assetTarget(arch)
			return fmt.Sprintf("%s/app/v%s/hysteria-%s", hysteria2ReleaseBaseURL, hysteria2ServerVersion, target)
		},
		SHA256: func(arch string) string {
			return expectedSHA256For(assetTarget(arch))
		},
		IsArchive: false,
	}
}

// assetTarget associe l'architecture normalisée LABOSURF
// (engineutil.DetectArch : "amd64"/"arm64") au suffixe d'asset officiel
// publié par HyNetworks/hysteria (confirmé via l'API GitHub réelle :
// "hysteria-linux-amd64", "hysteria-linux-arm64" parmi les 27 assets de la
// release app/v2.12.2). Seules les cibles Linux (amd64/arm64) sont
// couvertes ici, même périmètre que TUIC — une cible "android-arm64"
// distincte existe aussi côté release officielle si un besoin Android natif
// (hors Termux/proot Linux) se confirme un jour, non traité ici.
func assetTarget(arch string) string {
	switch arch {
	case "arm64":
		return "linux-arm64"
	default:
		return "linux-amd64"
	}
}

// expectedSHA256For retourne le SHA-256 attendu (hex) pour une cible
// donnée, pinné à hysteria2ServerVersion.
//
// Valeurs obtenues PENDANT cette intégration en téléchargeant réellement le
// fichier officiel de checksums "hashes.txt" publié à côté des binaires
// (https://github.com/HyNetworks/hysteria/releases/download/app/v2.12.2/hashes.txt,
// format sha256sum standard) et en vérifiant indépendamment sa cohérence —
// pas une valeur recopiée sans vérification.
func expectedSHA256For(target string) string {
	switch target {
	case "linux-amd64":
		return "6493dfffd55b5883f64c76c63880ecc32988f0c568c9ca9014907877b4d55f94"
	case "linux-arm64":
		return "ebfacc1ec3a0edfd742cd68ce17f292a6092e606b9d11f99b035c1d888f3d709"
	default:
		return ""
	}
}

// Install télécharge (si nécessaire) le binaire hysteria officiel et
// prépare un certificat TLS auto-signé + un secret d'obfuscation Salamander
// — Hysteria2 exige TLS pour son handshake QUIC, il n'existe pas de mode
// "sans TLS" côté protocole officiel.
func (e *Hysteria2Engine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return fmt.Errorf("création répertoire config Hysteria2 : %w", err)
	}

	if _, _, err := engineutil.EnsureHysteria2Certs(engineutil.DefaultDataDir); err != nil {
		return fmt.Errorf("certificat TLS Hysteria2 : %w", err)
	}
	log.Printf("✔ Hysteria2 : certificat TLS auto-signé prêt")

	if _, err := EnsureObfsPassword(engineutil.DefaultDataDir); err != nil {
		return fmt.Errorf("secret d'obfuscation Salamander Hysteria2 : %w", err)
	}
	log.Printf("✔ Hysteria2 : secret d'obfuscation Salamander prêt")

	spec := binarySpec()
	if spec.Installed() {
		log.Printf("✔ Hysteria2 : hysteria %s déjà installé", hysteria2ServerVersion)
		return nil
	}

	arch := cfg.Arch
	if arch == "" {
		arch = engineutil.DetectArch()
	}

	log.Printf("⬇ Téléchargement hysteria %s pour %s...", hysteria2ServerVersion, arch)
	path, err := spec.Download(ctx, arch)
	if err != nil {
		return fmt.Errorf("installation hysteria : %w", err)
	}
	log.Printf("✔ Hysteria2 : hysteria %s installé (%s)", hysteria2ServerVersion, path)
	return nil
}

// Configure applique une configuration au format officiel Hysteria2 (YAML,
// voir v2.hysteria.network/docs/advanced/Full-Server-Config/).
//
// Contrairement à TUIC/Xray (JSON, structurellement parsé et re-validé par
// Configure()), ce dépôt n'a AUCUNE dépendance YAML (go.mod : une seule
// dépendance externe, golang.org/x/crypto) — le moteur "hysteria" maison
// déjà présent construit déjà sa propre configuration YAML par simple
// assemblage de chaînes (internal/clientcfg/hysteria.go), jamais par un
// encodeur structuré. Ce moteur fait le même choix délibéré plutôt que
// d'introduire la seule dépendance YAML du dépôt pour ce besoin : la
// configuration complète (y compris les chemins de certificat, déjà
// déterministes via EnsureHysteria2Certs) est construite par
// internal/clientcfg/hysteria2.go et transmise ici telle quelle. La
// validation ci-dessous est donc TEXTUELLE (recherche de marqueurs), pas
// structurelle — une limite assumée et documentée, PAS une équivalence
// prétendue avec la validation JSON de TUIC/Xray.
func (e *Hysteria2Engine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration Hysteria2 vide")
	}
	text := string(cfg.JSON)

	if !strings.Contains(text, "listen:") {
		return fmt.Errorf("configuration Hysteria2 : clé 'listen' absente")
	}
	if !strings.Contains(text, "auth:") {
		return fmt.Errorf("configuration Hysteria2 : bloc 'auth' absent")
	}
	if !hasAtLeastOneUser(text) {
		return fmt.Errorf("configuration Hysteria2 : au moins un utilisateur (auth.userpass) est requis")
	}

	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, []byte(text), 0o600); err != nil {
		return fmt.Errorf("écriture config Hysteria2 : %w", err)
	}
	log.Printf("✔ Hysteria2 : configuration écrite (%d octets)", len(text))
	return nil
}

// hasAtLeastOneUser vérifie, par inspection textuelle (pas un vrai parseur
// YAML — voir le commentaire de Configure), qu'au moins une ligne
// utilisateur indentée suit le marqueur "userpass:".
func hasAtLeastOneUser(text string) bool {
	idx := strings.Index(text, "userpass:")
	if idx == -1 {
		return false
	}
	rest := text[idx+len("userpass:"):]
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break // désindenté : fin du bloc userpass
		}
		if strings.Contains(trimmed, ":") {
			return true
		}
	}
	return false
}

// Start démarre `hysteria server -c <config>` en sous-processus.
//
// cmd.Start() synchrone (pas cmd.Run() dans une goroutine séparée) — même
// discipline que TUIC (voir son commentaire détaillé) : seul cmd.Start()
// garantit que cmd.Process est déjà écrit quand Start() le lit, vérifié par
// `go test -race` sur ce même patron pour TUIC.
func (e *Hysteria2Engine) Start(ctx context.Context) error {
	spec := binarySpec()
	if !spec.Installed() {
		return fmt.Errorf("binaire hysteria non trouvé (lancer 'install' d'abord)")
	}
	if _, err := os.Stat(e.configPath); err != nil {
		return fmt.Errorf("config Hysteria2 non trouvée : %s (lancer 'configure' d'abord)", e.configPath)
	}

	runCtx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(runCtx, spec.DeployedPath(), "server", "-c", e.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("démarrage hysteria : %w", err)
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
		return fmt.Errorf("hysteria s'est arrêté immédiatement : %w", err)
	default:
	}

	log.Printf("✔ Hysteria2 : hysteria démarré (PID %d)", cmd.Process.Pid)
	return nil
}

// RunForeground lance le moteur en avant-plan et bloque jusqu'à son arrêt
// (systemd Type=simple).
func (e *Hysteria2Engine) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	return <-done
}

// Stop arrête hysteria proprement (SIGKILL après 5s si nécessaire).
//
// Attend sur le canal e.done déjà alimenté par la goroutine de Start()
// plutôt que d'appeler cmd.Wait() une seconde fois — même correction déjà
// appliquée à TUIC (*exec.Cmd ne supporte pas deux Wait() concurrents).
func (e *Hysteria2Engine) Stop() error {
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
				return fmt.Errorf("kill forcé hysteria : %w", err)
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
func (e *Hysteria2Engine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

// Status retourne l'état courant du moteur.
func (e *Hysteria2Engine) Status() engine.EngineStatus {
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
			if _, portStr, err := net.SplitHostPort(normalizeListenAddr(ep.Addr)); err == nil {
				fmt.Sscanf(portStr, "%d", &st.Port)
			}
			st.Health = "healthy"
		} else {
			st.Health = "unknown"
		}
	}
	return st
}

// configuredAddr lit la valeur de "listen:" depuis le fichier de
// configuration réellement écrit sur disque par Configure() — celui que le
// process hysteria a réellement chargé au démarrage. Lecture textuelle
// simple (une ligne "listen: <valeur>"), cohérente avec le choix assumé de
// ne pas dépendre d'un parseur YAML (voir Configure).
func (e *Hysteria2Engine) configuredAddr() (string, bool) {
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "listen:") {
			continue
		}
		addr := strings.TrimSpace(strings.TrimPrefix(trimmed, "listen:"))
		addr = strings.Trim(addr, `"'`)
		if addr != "" {
			return addr, true
		}
	}
	return "", false
}

// normalizeListenAddr complète une adresse "listen" au format court YAML
// (ex: ":443", forme utilisée par tous les exemples officiels Hysteria2)
// en une adresse pleinement résolvable par net.SplitHostPort/ResolveUDPAddr.
func normalizeListenAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "0.0.0.0" + addr
	}
	return addr
}

// Endpoint implémente engine.Endpointer. Comme TUIC (voir son commentaire
// détaillé dans engines/tuic/tuic_binary.go), hysteria est un processus
// externe qui ouvre lui-même son socket UDP/QUIC — aucune visibilité
// directe depuis ce process Go, et un vrai handshake QUIC/Hysteria2
// nécessiterait un client complet. Le signal vérifiable retenu est le même
// "bind-probe" : tenter de lier nous-mêmes le port UDP configuré. Un succès
// prouve que RIEN n'écoute dessus (donc pas prêt) ; un échec ("address
// already in use") prouve qu'une socket UDP y est réellement liée — jamais
// une déduction optimiste basée sur le seul délai de démarrage.
func (e *Hysteria2Engine) Endpoint() (engine.Endpoint, bool) {
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
	full := normalizeListenAddr(addr)
	if !udpAddrBound(full) {
		return engine.Endpoint{}, false
	}
	return engine.Endpoint{Network: "udp", Addr: full}, true
}

// udpAddrBound indique si une socket UDP est déjà liée sur addr, en tentant
// de la lier nous-mêmes (voir le commentaire d'Endpoint ci-dessus).
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
func (e *Hysteria2Engine) HealthCheck() error {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("hysteria non démarré")
	}
	if _, ok := e.Endpoint(); !ok {
		return fmt.Errorf("port QUIC configuré non occupé par le process (échec probable au démarrage)")
	}
	return nil
}

// Logs retourne un indicateur ; les journaux réels transitent par
// stdout/stderr du service systemd (même convention que TUIC/Xray).
func (e *Hysteria2Engine) Logs(lines int) ([]string, error) {
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return []string{"hysteria non démarré"}, nil
	}
	return []string{fmt.Sprintf("hysteria (PID %d) : logs via stdout/stderr systemd", cmd.Process.Pid)}, nil
}

// Update réinstalle le binaire hysteria (même version pinnée tant que
// hysteria2ServerVersion n'est pas changé dans le code).
func (e *Hysteria2Engine) Update() error {
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
		return fmt.Errorf("suppression ancien binaire hysteria : %w", err)
	}
	if _, err := spec.Download(context.Background(), engineutil.DetectArch()); err != nil {
		return fmt.Errorf("réinstallation hysteria : %w", err)
	}
	return nil
}

// Uninstall arrête le moteur et supprime binaire + configuration. Le
// certificat TLS et le secret d'obfuscation Salamander (EnsureHysteria2Certs
// / EnsureObfsPassword) sont volontairement CONSERVÉS — même choix que TUIC
// pour son certificat : les régénérer à chaque réinstallation invaliderait
// silencieusement tous les liens clients déjà distribués.
func (e *Hysteria2Engine) Uninstall() error {
	_ = e.Stop()
	spec := binarySpec()
	_ = os.Remove(spec.DeployedPath())
	_ = os.Remove(e.configPath)
	return nil
}

// obfsPasswordPath retourne le chemin déterministe du secret d'obfuscation
// Salamander, sous le répertoire de données du moteur — même convention que
// engineutil.EnsureHysteria2Certs (répertoire "hysteria2" dédié, jamais
// partagé avec le moteur "hysteria" maison).
func obfsPasswordPath(dataDir string) string {
	return filepath.Join(dataDir, "hysteria2", "obfs_password.txt")
}

// EnsureObfsPassword génère (si absent) puis retourne le secret
// d'obfuscation Salamander du serveur — un secret PAR SERVEUR (pas par
// compte, cohérent avec le schéma officiel Hysteria2 où obfs.salamander.password
// est une valeur serveur unique partagée par tous les clients), généré
// aléatoirement et persisté une seule fois, jamais une valeur codée en dur
// (contrairement au moteur "hysteria" maison, dont le mot de passe
// d'obfuscation constant "labosurf-sal4mander-obfs-secret" est un choix
// existant de ce dépôt, non repris ici délibérément).
//
// Exportée : internal/clientcfg/hysteria2.go l'appelle pour construire à la
// fois la configuration serveur ET le lien client avec le MÊME secret —
// même principe que xray.LoadRealityKeys, lu depuis clientcfg.
func EnsureObfsPassword(dataDir string) (string, error) {
	path := obfsPasswordPath(dataDir)
	if data, err := os.ReadFile(path); err == nil {
		if pw := strings.TrimSpace(string(data)); pw != "" {
			return pw, nil
		}
	}

	pw, err := secret.RandToken(16)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(pw), 0o600); err != nil {
		return "", err
	}
	return pw, nil
}
