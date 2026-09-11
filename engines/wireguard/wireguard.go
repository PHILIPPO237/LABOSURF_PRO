package wireguard

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
)

// WireGuardEngine pilote une interface WireGuard réelle via les outils
// système `wg`/`wg-quick` (module noyau) — voir le commentaire de tête
// d'engine.go pour la justification de ce choix plutôt qu'un binaire tiers
// téléchargé ou une bibliothèque Go embarquée.
//
// Différence structurelle importante avec TUIC/Xray/Hysteria2 : `wg-quick
// up` n'est PAS un processus longue durée à superviser (contrairement à
// tuic-server/xray-core/hysteria, qui restent en vie tout le temps où le
// service tourne) — c'est une commande qui configure l'interface puis
// SE TERMINE, laissant l'interface exister dans le noyau indépendamment de
// tout processus Go. Ce moteur ne garde donc PAS de *exec.Cmd vivant :
// Status()/Endpoint()/Stop() interrogent l'état RÉEL de l'interface dans le
// noyau à chaque appel (via `wg show <iface>` + un bind-probe UDP), jamais
// un état en mémoire qui pourrait diverger de la réalité entre deux
// invocations de ce binaire (cas normal : `configure` puis, plus tard,
// `start`, dans deux processus CLI séparés).
type WireGuardEngine struct {
	configPath string

	mu sync.Mutex
}

// NewWireGuardEngine construit le moteur avec ses chemins par défaut,
// surchageables comme les autres moteurs via variable d'environnement.
// Le nom d'interface est dérivé du nom de fichier de configuration (sans
// l'extension .conf) — exactement le comportement de `wg-quick` lui-même
// quand on lui passe un CHEMIN de fichier plutôt qu'un simple nom
// d'interface (voir man wg-quick). Nom par défaut délibérément distinctif
// ("wg-labosurf", pas "wg0") pour ne jamais entrer en collision avec une
// interface WireGuard qu'un opérateur aurait configurée manuellement par
// ailleurs (voir le commentaire d'ownedByLabosurf ci-dessous).
func NewWireGuardEngine() (engine.Engine, error) {
	cfgPath := "/etc/labosurf/engines/wireguard/wg-labosurf.conf"
	if p := os.Getenv("LABOSURF_WIREGUARD_CONFIG"); p != "" {
		cfgPath = p
	}
	return &WireGuardEngine{configPath: cfgPath}, nil
}

func (e *WireGuardEngine) Name() string        { return engineName }
func (e *WireGuardEngine) Version() string     { return engineVer }
func (e *WireGuardEngine) Description() string { return engineDesc }

// ifaceName dérive le nom d'interface du fichier de configuration (base du
// chemin, sans l'extension), comme `wg-quick up <chemin>` le fait lui-même.
func (e *WireGuardEngine) ifaceName() string {
	base := filepath.Base(e.configPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// LabosurfMarker est écrit en tête de chaque fichier de configuration
// généré par ce moteur — vérifié par ownedByLabosurf avant toute commande
// destructive (`wg-quick down`), voir le commentaire de Stop(). Exportée
// pour que internal/clientcfg/wireguard.go (qui génère effectivement le
// contenu du fichier) écrive exactement le même marqueur, sans dupliquer
// la chaîne littérale dans les deux paquets.
const LabosurfMarker = "# Généré par LABOSURF PRO — ne pas éditer à la main."

// ownedByLabosurf vérifie que le fichier de configuration de CE moteur
// porte bien le marqueur LABOSURF — garde-fou avant `wg-quick down` :
// cette commande n'agit QUE sur l'interface dérivée de ce fichier précis
// (jamais une interface arbitraire par son nom), mais si un opérateur (ou
// un autre outil) a remplacé son contenu par autre chose, on refuse
// d'agir plutôt que de supposer que l'interface nous appartient encore.
// Ceci NE PROUVE PAS cryptographiquement la propriété du côté noyau (aucun
// mécanisme du genre n'existe pour une interface réseau) — c'est une
// vérification de cohérence de CE côté (notre propre fichier), documentée
// comme telle plutôt que présentée comme une garantie plus forte qu'elle ne
// l'est.
func (e *WireGuardEngine) ownedByLabosurf() bool {
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), LabosurfMarker)
}

// toolsAvailable vérifie la présence des commandes système nécessaires
// (jamais supposée présente, voir mission P2 Étape 14) et retourne une
// erreur explicite et actionnable si l'une manque.
func toolsAvailable() error {
	if _, err := exec.LookPath("wg"); err != nil {
		return fmt.Errorf("WireGuard non disponible : commande 'wg' introuvable dans PATH — installez le paquet wireguard-tools (ex: apt install wireguard-tools)")
	}
	if _, err := exec.LookPath("wg-quick"); err != nil {
		return fmt.Errorf("WireGuard non disponible : commande 'wg-quick' introuvable dans PATH — installez le paquet wireguard-tools (ex: apt install wireguard-tools)")
	}
	return nil
}

// Install vérifie la présence des outils système et prépare la clé privée
// du serveur (générée une seule fois, jamais régénérée — voir
// EnsureServerKeys). N'installe AUCUN paquet système soi-même (ce dépôt ne
// lance aucune installation système, conformément à la consigne) : se
// contente de détecter et de rapporter une erreur claire et actionnable.
func (e *WireGuardEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	if err := toolsAvailable(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return fmt.Errorf("création répertoire config WireGuard : %w", err)
	}
	if _, _, err := EnsureServerKeys(engineutil.DefaultDataDir); err != nil {
		return fmt.Errorf("clé privée serveur WireGuard : %w", err)
	}
	log.Printf("✔ WireGuard : outils système détectés, clé serveur prête")
	return nil
}

// Configure applique une configuration au format standard wg-quick (INI :
// [Interface] + [Peer]...). Validation TEXTUELLE (mêmes raisons que
// hysteria2 : pas de dépendance INI/YAML dans ce dépôt, voir
// engines/hysteria2/hysteria2_binary.go) — pas structurelle comme TUIC/Xray
// (JSON). Le fichier est écrit en 0600 : il contient la clé privée du
// serveur (voir mission P2 Étape 15 — jamais de secret en permissions
// larges).
func (e *WireGuardEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if len(cfg.JSON) == 0 {
		return fmt.Errorf("configuration WireGuard vide")
	}
	text := string(cfg.JSON)

	if !strings.Contains(text, "[Interface]") {
		return fmt.Errorf("configuration WireGuard : bloc [Interface] absent")
	}
	if !strings.Contains(text, "PrivateKey") {
		return fmt.Errorf("configuration WireGuard : PrivateKey absente du bloc [Interface]")
	}
	if !strings.Contains(text, "ListenPort") {
		return fmt.Errorf("configuration WireGuard : ListenPort absent du bloc [Interface]")
	}
	if !strings.Contains(text, "[Peer]") {
		return fmt.Errorf("configuration WireGuard : au moins un [Peer] est requis")
	}

	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath, []byte(text), 0o600); err != nil {
		return fmt.Errorf("écriture config WireGuard : %w", err)
	}
	log.Printf("✔ WireGuard : configuration écrite (%d octets, interface %s)", len(text), e.ifaceName())
	return nil
}

// Start exécute `wg-quick up <config>`. Contrairement à TUIC/Xray/Hysteria2,
// il ne s'agit PAS d'un sous-processus longue durée : wg-quick configure
// l'interface puis se termine — voir le commentaire du type WireGuardEngine.
func (e *WireGuardEngine) Start(ctx context.Context) error {
	if err := toolsAvailable(); err != nil {
		return err
	}
	if _, err := os.Stat(e.configPath); err != nil {
		return fmt.Errorf("config WireGuard non trouvée : %s (lancer 'configure' d'abord)", e.configPath)
	}

	cmd := exec.CommandContext(ctx, "wg-quick", "up", e.configPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg-quick up : %w (sortie : %s)", err, strings.TrimSpace(string(out)))
	}
	log.Printf("✔ WireGuard : interface %s active", e.ifaceName())
	return nil
}

// RunForeground démarre l'interface puis bloque jusqu'à l'annulation du
// contexte (Type=simple systemd), avant de l'arrêter proprement — adapté
// au fait que WireGuard-via-kernel n'a pas de processus à attendre
// directement (contrairement à TUIC/Xray, dont RunForeground attend la fin
// du VRAI sous-processus).
func (e *WireGuardEngine) RunForeground(ctx context.Context) error {
	if err := e.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return e.Stop()
}

// Stop exécute `wg-quick down <config>` — uniquement si l'interface existe
// encore (voir ifaceExists : "gérer proprement le cas où l'interface n'existe
// plus", mission P2 Étape 4) ET si le fichier de configuration nous
// appartient encore (voir ownedByLabosurf) : ne jamais détruire une
// interface qui n'a pas été créée/possédée par ce moteur.
func (e *WireGuardEngine) Stop() error {
	if !e.ifaceExists() {
		return nil // rien à faire : déjà arrêté, ou jamais démarré.
	}
	if !e.ownedByLabosurf() {
		return fmt.Errorf(
			"refus d'arrêter l'interface %s : le fichier de configuration %s ne porte plus le marqueur LABOSURF (remplacé/altéré ?) — vérification manuelle requise",
			e.ifaceName(), e.configPath,
		)
	}

	cmd := exec.Command("wg-quick", "down", e.configPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg-quick down : %w (sortie : %s)", err, strings.TrimSpace(string(out)))
	}
	log.Printf("✔ WireGuard : interface %s arrêtée", e.ifaceName())
	return nil
}

// Restart redémarre le moteur.
func (e *WireGuardEngine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

// ifaceExists interroge le noyau via `wg show <iface>` — exit 0 signifie
// qu'une interface WireGuard de ce nom existe réellement (jamais déduit
// d'un état en mémoire, voir le commentaire du type).
func (e *WireGuardEngine) ifaceExists() bool {
	if _, err := exec.LookPath("wg"); err != nil {
		return false
	}
	return exec.Command("wg", "show", e.ifaceName()).Run() == nil
}

// Status retourne l'état courant du moteur, entièrement dérivé de l'état
// réel du système (outils présents, clé serveur générée, interface
// existante) — jamais d'état en mémoire.
func (e *WireGuardEngine) Status() engine.EngineStatus {
	installed := toolsAvailable() == nil
	if installed {
		if _, _, err := EnsureServerKeys(engineutil.DefaultDataDir); err != nil {
			installed = false
		}
	}

	running := e.ifaceExists()
	st := engine.EngineStatus{Installed: installed, Running: running}
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

// configuredListenPort lit la valeur de "ListenPort" depuis le fichier de
// configuration écrit sur disque par Configure() — lecture textuelle simple
// (mêmes raisons que hysteria2, voir Configure ci-dessus).
func (e *WireGuardEngine) configuredListenPort() (int, bool) {
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "ListenPort") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if port, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			return port, true
		}
	}
	return 0, false
}

func normalizeListenAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "0.0.0.0" + addr
	}
	return addr
}

// Endpoint implémente engine.Endpointer. Deux vérifications indépendantes,
// jamais une seule : (1) `wg show <iface>` confirme qu'une interface
// WireGuard de ce nom existe RÉELLEMENT dans le noyau ; (2) un bind-probe
// UDP sur le port configuré (même technique que TUIC/Hysteria2 — tenter de
// lier nous-mêmes le port : un échec "address already in use" prouve qu'une
// socket y est réellement liée) confirme que ce port précis est occupé.
// Jamais 127.0.0.1:0, jamais un placeholder.
func (e *WireGuardEngine) Endpoint() (engine.Endpoint, bool) {
	if !e.ifaceExists() {
		return engine.Endpoint{}, false
	}
	port, ok := e.configuredListenPort()
	if !ok {
		return engine.Endpoint{}, false
	}
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	if !udpAddrBound(addr) {
		return engine.Endpoint{}, false
	}
	return engine.Endpoint{Network: "udp", Addr: addr}, true
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

// HealthCheck vérifie que l'interface existe ET que son port UDP configuré
// est réellement occupé (voir Endpoint).
func (e *WireGuardEngine) HealthCheck() error {
	if !e.ifaceExists() {
		return fmt.Errorf("interface WireGuard %s non active", e.ifaceName())
	}
	if _, ok := e.Endpoint(); !ok {
		return fmt.Errorf("port UDP configuré non occupé (incohérence interface/port)")
	}
	return nil
}

// Logs retourne la sortie réelle de `wg show <iface>` — contrairement à
// TUIC/Xray (simple indicateur "logs via stdout/stderr systemd"), WireGuard
// via le noyau n'a pas de flux de logs de processus propre, mais `wg show`
// fournit un état interrogeable réel et utile (peers, handshakes, trafic).
// Ne contient JAMAIS de clé privée (wg show n'affiche que les clés
// publiques par défaut, voir man wg).
func (e *WireGuardEngine) Logs(lines int) ([]string, error) {
	if !e.ifaceExists() {
		return []string{"interface WireGuard non active"}, nil
	}
	out, err := exec.Command("wg", "show", e.ifaceName()).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("wg show : %w", err)
	}
	all := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	return all, nil
}

// Update n'a rien à mettre à jour : ce moteur ne gère aucun binaire
// versionné (voir le commentaire de tête d'engine.go) — les outils
// wg/wg-quick relèvent du gestionnaire de paquets du système, hors du
// périmètre de ce moteur (aucune installation système n'est effectuée ici).
func (e *WireGuardEngine) Update() error {
	log.Printf("ℹ WireGuard : aucune mise à jour applicable (outils système wg/wg-quick, gérés par le gestionnaire de paquets du système — voir le commentaire de tête d'engine.go)")
	return nil
}

// Uninstall arrête l'interface si active et supprime le fichier de
// configuration. La clé privée du serveur (EnsureServerKeys) est
// volontairement CONSERVÉE — même choix que TUIC pour son certificat : la
// régénérer invaliderait silencieusement tous les fichiers clients déjà
// distribués (dont la clé publique serveur est figée).
func (e *WireGuardEngine) Uninstall() error {
	if err := e.Stop(); err != nil {
		return err
	}
	_ = os.Remove(e.configPath)
	return nil
}
