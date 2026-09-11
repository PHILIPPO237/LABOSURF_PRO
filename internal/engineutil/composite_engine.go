package engineutil

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"labosurf/internal/engine"
)

// CompositeEngine implémente engine.Engine pour les moteurs hybrides qui
// combinent plusieurs moteurs (ex : slowdns-ssh, dnstt-xray), et généralise
// ce chaînage à une chaîne A -> B -> C -> ... de profondeur arbitraire
// (voir ARCHITECTURE_HYBRIDES.md). Components[0] est le composant le plus
// proche du client ("A", l'entrée de la chaîne), Components[len-1] le
// backend final — convention déjà promise à l'utilisateur par
// runHybridCreateMenu ("l'ordre de saisie définit l'ordre des composants").
//
// Les sous-moteurs sont résolus via le registre global (engine.Get).
//
// Mécanisme de chaînage réel (voir AUDIT_PHASE3_HYBRIDS.md pour l'analyse
// historique à 2 étages) : les deux seuls moteurs qui déclarent une
// capacité de relais (EngineCapability.RelaysTo, voir compat.go) — dnstt et
// slowdns — relaient déjà, en conditions réelles, les octets d'un tunnel
// client vers une adresse TCP "backend" configurable (net.Dial("tcp",
// cfg.Backend)) — c'est le mécanisme existant et fonctionnel (audité en
// Phase 2) généralisé ici, PAS une nouvelle invention. Pour chaque paire
// adjacente (front, back) où front sait relayer :
//  1. démarre en premier le composant "back" (le relais suivant ou le
//     backend terminal que front doit atteindre) ;
//  2. lui demande son endpoint RÉEL via l'interface optionnelle
//     engine.Endpointer (jamais un placeholder) ;
//  3. si cet endpoint ne correspond pas au réseau déclaré par
//     front.RelaysTo, refuse de chaîner (erreur claire) plutôt que de
//     prétendre que ça fonctionne : un relais DNS ne peut relayer que vers
//     un backend TCP, donc un VPN purement UDP (hysteria, tuic) ne peut
//     structurellement pas être chaîné derrière dnstt/slowdns avec le
//     mécanisme actuel ;
//  4. reconfigure front avec ce backend réel, puis le démarre.
//
// Avec les moteurs produits actuels, au plus UNE paire de ce type peut
// exister dans une composition (ValidateHybrid limite à un seul moteur de
// rôle RoleTransport, et RelaysTo n'est déclaré aujourd'hui que par
// dnstt/slowdns, tous deux RoleTransport) : une chaîne A -> B -> C réelle
// avec relais à CHAQUE étage n'est donc pas démontrable avec le catalogue
// actuel de moteurs — voir wireAdjacentChain, qui généralise néanmoins ce
// mécanisme pour tout futur moteur "relais intermédiaire" sans qu'aucune
// modification de ce fichier soit nécessaire le jour où il existera (test
// d'architecture avec des moteurs de test dans chain_test.go).
//
// Le moteur "udp" (internal/engineudp, qui supervise en sous-processus le
// serveur historique engines/udp) EST enregistré dans le registre utilisé
// ici (contrairement à ce qu'affirmait une version antérieure de ce
// commentaire) mais n'implémente pas engine.Endpointer : il ne peut donc
// structurellement ni servir de backend câblé, ni relayer, avec le
// mécanisme actuel — même limitation pratique que documenté, pour une
// raison différente (absence d'Endpointer, pas absence du registre).
type CompositeEngine struct {
	Spec struct {
		Name        string
		Version     string
		Description string
	}

	// Components énumère les sous-moteurs, dans l'ordre de démarrage par
	// défaut (utilisé uniquement pour les composants qui ne sont ni le
	// transport ni son backend désigné — voir pickTransportAndBackend).
	Components []string

	// ConfigureAll applique la configuration à tous les sous-moteurs.
	ConfigureAll bool

	// ComponentConfig, si non nil, fournit une configuration JSON PROPRE à
	// un composant nommé, prioritaire sur la configuration partagée
	// (l'argument de Configure()) pour CE composant. Nécessaire dès qu'un
	// composant a un schéma JSON incompatible avec celui des autres : le
	// mécanisme historique à un seul blob partagé suppose que tous les
	// composants lisent des clés compatibles d'un même objet JSON — vrai
	// pour dnstt+ssh (tous deux lisent un même tableau "users" avec des
	// sous-champs différents) ou dnstt+slowdns, mais FAUX pour un backend
	// comme xray, dont la config ({"log":..., "inbounds":[...],
	// "outbounds":[...]}) n'a strictement aucun champ en commun avec celle
	// de dnstt ({"domain":..., "port":..., "backend":..., "users":[...]})
	// — découvert en tentant de câbler réellement dnstt->xray (voir
	// ARCHITECTURE_HYBRIDES.md). Laisser ce champ nil préserve exactement
	// le comportement historique (chaque composant reçoit la même
	// configuration partagée).
	ComponentConfig map[string]engine.EngineConfig

	mu sync.Mutex

	// lastCfg mémorise la configuration JSON PARTAGÉE transmise au dernier
	// appel à Configure() — conservé pour compatibilité diagnostique, mais
	// n'est plus la source utilisée pour le câblage (voir
	// lastCfgByComponent) dès qu'un ComponentConfig est en jeu.
	lastCfg engine.EngineConfig

	// lastCfgByComponent mémorise, PAR composant, la configuration
	// RÉELLEMENT appliquée par le dernier Configure() (celle de
	// ComponentConfig[name] si présente, sinon la configuration partagée)
	// — c'est celle-ci que Start() ré-injecte avec le champ "backend"
	// corrigé une fois l'endpoint réel du composant suivant connu. Quand
	// ComponentConfig est nil, cette valeur est identique pour tous les
	// composants (même comportement qu'avant l'introduction de ce champ).
	lastCfgByComponent map[string]engine.EngineConfig

	// backendAddr mémorise le dernier endpoint réel du backend câblé dans
	// le transport (diagnostic uniquement, jamais une valeur fictive).
	backendAddr string

	// frontAddr mémorise l'adresse d'écoute réelle du transport lui-même
	// une fois démarré — c'est celle-ci que Status() expose comme
	// ListenAddr, car c'est elle que compose un client (le backend est un
	// détail interne du chaînage, pas une adresse cliente).
	frontAddr string

	// subs met en cache une instance par nom de composant, réutilisée pour
	// tout le cycle de vie de CE CompositeEngine.
	//
	// engine.Get(name) (voir internal/engine/registry.go) retourne
	// délibérément une INSTANCE NEUVE à chaque appel — un moteur simple
	// piloté par le menu fonctionne correctement avec ça uniquement parce
	// que l'appelant (cmd/labosurf/menu.go : runSingleEngineMenu) récupère
	// SON instance UNE SEULE FOIS et la réutilise pour tout Start/Stop/
	// Restart de la session. Une version antérieure de component() ne
	// faisait pas ça : chaque appel à e.component(name) — donc chaque
	// Start(), Stop(), Status(), etc. de CompositeEngine — obtenait une
	// instance différente et jamais démarrée du composant. Conséquence
	// vérifiée par exécution : CompositeEngine.Stop() ne faisait STRICTEMENT
	// RIEN (il appelait Stop() sur une instance fraîche dont cancel/server
	// valaient nil), et un Start() ultérieur du même composant échouait par
	// "address already in use" contre le VRAI processus resté en
	// fonctionnement — un défaut plus grave que le simple endpoint
	// placeholder documenté jusqu'ici. subs corrige ça en donnant à
	// CompositeEngine la même discipline qu'un appelant de premier niveau :
	// une instance par composant, tenue pendant toute sa propre durée de
	// vie.
	subs map[string]engine.Engine
}

// Name retourne l'identifiant unique du moteur hybride.
func (e *CompositeEngine) Name() string { return e.Spec.Name }

// Version retourne la version du moteur.
func (e *CompositeEngine) Version() string { return e.Spec.Version }

// Description fournit une courte description du moteur.
func (e *CompositeEngine) Description() string { return e.Spec.Description }

// component retourne l'instance mise en cache du sous-moteur nommé,
// l'obtenant du registre global (engine.Get) et la mémorisant au premier
// appel. Voir le commentaire du champ subs pour le pourquoi.
func (e *CompositeEngine) component(name string) (engine.Engine, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if sub, ok := e.subs[name]; ok {
		return sub, nil
	}
	sub, err := engine.Get(name)
	if err != nil {
		return nil, fmt.Errorf("moteur hybride %s : composant %q : %w", e.Name(), name, err)
	}
	if e.subs == nil {
		e.subs = make(map[string]engine.Engine, len(e.Components))
	}
	e.subs[name] = sub
	return sub, nil
}

// Component retourne l'instance du sous-moteur nommé telle qu'utilisée par
// CE CompositeEngine (mise en cache, potentiellement démarrée) — utile pour
// inspecter l'état réel d'un composant précis (ex : son Endpoint() via
// engine.Endpointer) depuis l'extérieur. Contrairement à engine.Get(name),
// qui retournerait une instance neuve et jamais démarrée, ceci retourne la
// même instance que celle réellement pilotée par Start()/Stop().
func (e *CompositeEngine) Component(name string) (engine.Engine, error) {
	return e.component(name)
}

// Install installe tous les composants.
func (e *CompositeEngine) Install(ctx context.Context, cfg engine.InstallConfig) error {
	for _, name := range e.Components {
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.Install(ctx, cfg); err != nil {
			return err
		}
	}
	return nil
}

// Configure applique la configuration brute à chaque composant et mémorise
// cette configuration pour Start().
//
// Contrairement à une version antérieure, Configure() ne démarre plus
// aucun composant : démarrer un moteur pendant sa propre "configuration"
// était déjà incohérent avec le contrat de tous les autres moteurs
// (aucun d'eux ne démarre quoi que ce soit dans Configure()), et provoquait
// un double-démarrage réel du transport le jour où Start() serait appelé
// séparément ensuite (le cas normal via le menu : "2. CONFIGURER" puis,
// plus tard, "3. DÉMARRER") — le second net.ListenUDP sur le même port
// aurait échoué avec "address already in use". Le câblage réel avec
// l'endpoint du backend est désormais entièrement fait dans Start(), seul
// moment où cet endpoint peut être connu (voir le commentaire du type).
func (e *CompositeEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if !e.ConfigureAll {
		return nil
	}

	e.mu.Lock()
	e.lastCfg = cfg
	if e.lastCfgByComponent == nil {
		e.lastCfgByComponent = make(map[string]engine.EngineConfig, len(e.Components))
	}
	e.mu.Unlock()

	for _, name := range e.Components {
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		componentCfg := cfg
		if override, ok := e.ComponentConfig[name]; ok {
			componentCfg = override
		}
		if err := sub.Configure(ctx, componentCfg); err != nil {
			return fmt.Errorf("configuration %s : %w", name, err)
		}
		e.mu.Lock()
		e.lastCfgByComponent[name] = componentCfg
		e.mu.Unlock()
	}
	return nil
}

// endpointWaitTimeout borne l'attente d'un endpoint réel après Start() d'un
// composant (nécessaire pour ssh, dont le listener TCP n'est ouvert que de
// façon asynchrone à l'intérieur de Run() — voir engines/ssh/engine.go).
const endpointWaitTimeout = 3 * time.Second

// pickTransportAndBackend identifie, parmi e.Components, le moteur
// transport (dnstt/slowdns, le seul rôle qui relaie déjà réellement des
// octets vers un backend TCP configurable) et le composant qui doit servir
// de backend à ce transport. Retourne des chaînes vides si aucun transport
// n'est présent (rien à câbler : cas d'un hybride sans transport).
//
// Préférence de backend : VPN d'abord (composant "principal" historique de
// la composition), puis compte (ssh) — cohérent avec le fait que
// dnstt/slowdns utilisent déjà "127.0.0.1:22" (ssh) comme backend par
// défaut. S'il y a un troisième composant non retenu comme backend (ex :
// ssh-xray-slowdns, où xray est retenu), il est simplement démarré en plus,
// de façon indépendante — pas câblé.
func pickTransportAndBackend(components []string) (transportName, backendName string) {
	for _, n := range components {
		if Role(n) == RoleTransport && transportName == "" {
			transportName = n
		}
	}
	if transportName == "" {
		return "", ""
	}
	for _, n := range components {
		if n != transportName && Role(n) == RoleVPN {
			return transportName, n
		}
	}
	for _, n := range components {
		if n != transportName && Role(n) == RoleAccount {
			return transportName, n
		}
	}
	return transportName, ""
}

// waitForEndpoint interroge sub.Endpoint() (via l'interface optionnelle
// engine.Endpointer) jusqu'à obtenir un endpoint réel et prêt, ou jusqu'au
// timeout donné. Retourne une erreur explicite — jamais un endpoint
// fictif — si le composant n'implémente pas Endpointer ou si son endpoint
// n'est jamais devenu prêt.
func waitForEndpoint(sub engine.Engine, timeout time.Duration) (engine.Endpoint, error) {
	epr, ok := sub.(engine.Endpointer)
	if !ok {
		return engine.Endpoint{}, fmt.Errorf(
			"le moteur %s n'implémente pas engine.Endpointer : aucun endpoint réel exposable pour le chaînage",
			sub.Name(),
		)
	}
	deadline := time.Now().Add(timeout)
	for {
		ep, ready := epr.Endpoint()
		if ready {
			return ep, nil
		}
		if time.Now().After(deadline) {
			return engine.Endpoint{}, fmt.Errorf(
				"endpoint réel de %s toujours indisponible après %s (le moteur a peut-être échoué à démarrer)",
				sub.Name(), timeout,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// injectBackend retourne une copie de cfg avec son champ JSON top-level
// "backend" réglé sur addr — le champ réellement lu par
// dnstt.DNSTTConfig.Backend / slowdns.SlowDNSConfig.Backend au chargement
// de leur configuration (pas un champ inventé pour l'occasion).
func injectBackend(cfg engine.EngineConfig, addr string) (engine.EngineConfig, error) {
	var m map[string]any
	if len(cfg.JSON) > 0 {
		if err := json.Unmarshal(cfg.JSON, &m); err != nil {
			return engine.EngineConfig{}, fmt.Errorf("configuration transport invalide : %w", err)
		}
	} else {
		m = map[string]any{}
	}
	m["backend"] = addr
	out, err := json.Marshal(m)
	if err != nil {
		return engine.EngineConfig{}, err
	}
	return engine.EngineConfig{JSON: out}, nil
}

// Start démarre les composants du moteur hybride et câble réellement
// chaque paire adjacente relayable de la chaîne A -> B -> C -> ... :
//  1. l'étape historique (transport dnstt/slowdns <-> son backend, trouvés
//     par rôle indépendamment de leur position — voir
//     pickTransportAndBackend) est exécutée EN PREMIER, inchangée, pour ne
//     jamais modifier le comportement des hybrides déjà en production ;
//  2. wireAdjacentChain généralise ensuite le MÊME mécanisme à toute paire
//     adjacente restante dont le composant "front" déclare une capacité de
//     relais (EngineCapability.RelaysTo) — inerte aujourd'hui avec le
//     catalogue de moteurs actuel (voir le commentaire du type), mais
//     permet à un futur moteur "relais intermédiaire" de chaîner
//     réellement sans modifier ce fichier.
//
// Si une erreur survient à n'importe quelle étape, tous les composants déjà
// démarrés par CET appel sont arrêtés avant de remonter l'erreur — la
// chaîne n'est jamais déclarée "ON" partiellement. Voir le commentaire du
// type CompositeEngine.
func (e *CompositeEngine) Start(ctx context.Context) (err error) {
	started := make(map[string]bool, len(e.Components))
	defer func() {
		if err == nil {
			return
		}
		// Nettoyage best-effort : on n'écrase jamais l'erreur d'origine
		// avec une éventuelle erreur d'arrêt (déjà attendu qu'un composant
		// jamais vraiment démarré, ou dont le pair a échoué, renvoie une
		// erreur bénigne au second Stop() — voir Stop()).
		for name, ok := range started {
			if !ok {
				continue
			}
			if sub, cerr := e.component(name); cerr == nil {
				_ = sub.Stop()
			}
		}
	}()

	transportName, backendName := pickTransportAndBackend(e.Components)

	if transportName != "" && backendName != "" {
		backendSub, cerr := e.component(backendName)
		if cerr != nil {
			err = cerr
			return err
		}
		if serr := backendSub.Start(ctx); serr != nil {
			err = fmt.Errorf("démarrage de %s (backend du transport %s) : %w", backendName, transportName, serr)
			return err
		}
		started[backendName] = true

		ep, werr := waitForEndpoint(backendSub, endpointWaitTimeout)
		if werr != nil {
			err = fmt.Errorf("moteur hybride %s : %w", e.Name(), werr)
			return err
		}
		transportCap, _ := GetEngineCapability(transportName)
		if ep.Network != transportCap.RelaysTo {
			err = fmt.Errorf(
				"moteur hybride %s : %s expose un endpoint %s (%s), mais le transport %s ne peut relayer que vers un backend %s — "+
					"chaînage impossible avec le mécanisme actuel (protocoles incompatibles), pas un problème d'endpoint",
				e.Name(), backendName, ep.Network, ep.Addr, transportName, strings.ToUpper(transportCap.RelaysTo),
			)
			return err
		}

		transportSub, cerr := e.component(transportName)
		if cerr != nil {
			err = cerr
			return err
		}

		e.mu.Lock()
		lastCfg := e.lastCfgByComponent[transportName]
		e.mu.Unlock()

		wiredCfg, ierr := injectBackend(lastCfg, ep.Addr)
		if ierr != nil {
			err = fmt.Errorf("moteur hybride %s : préparation config %s avec backend réel %s : %w", e.Name(), transportName, ep.Addr, ierr)
			return err
		}
		if cferr := transportSub.Configure(ctx, wiredCfg); cferr != nil {
			err = fmt.Errorf("configuration %s avec le backend réel de %s (%s) : %w", transportName, backendName, ep.Addr, cferr)
			return err
		}
		if serr := transportSub.Start(ctx); serr != nil {
			err = fmt.Errorf("démarrage transport %s : %w", transportName, serr)
			return err
		}
		started[transportName] = true

		e.mu.Lock()
		e.backendAddr = ep.Addr
		e.mu.Unlock()
	}

	if werr := e.wireAdjacentChain(ctx, started); werr != nil {
		err = werr
		return err
	}

	// Démarrer les composants restants (non câblés ci-dessus), dans
	// l'ordre de Components — inclut le cas sans aucune paire relayable,
	// où cette boucle démarre alors simplement tout dans l'ordre déclaré.
	for _, name := range e.Components {
		if started[name] {
			continue
		}
		sub, cerr := e.component(name)
		if cerr != nil {
			err = cerr
			return err
		}
		if serr := sub.Start(ctx); serr != nil {
			err = fmt.Errorf("démarrage %s : %w", name, serr)
			return err
		}
		started[name] = true
	}

	// L'endpoint front de toute la chaîne est celui du tout premier
	// composant déclaré (Components[0]) — c'est lui que compose un client
	// externe, quel que soit le mécanisme qui l'a câblé (étape historique
	// ou généralisation N-aire). Jamais un placeholder : si son endpoint
	// réel n'est pas déterminable (composant sans Endpointer), ListenAddr
	// reste simplement vide plutôt que de mentir.
	if len(e.Components) > 0 {
		if sub, cerr := e.component(e.Components[0]); cerr == nil {
			if ep, everr := waitForEndpoint(sub, endpointWaitTimeout); everr == nil {
				e.mu.Lock()
				e.frontAddr = ep.Addr
				e.mu.Unlock()
			}
		}
	}

	return nil
}

// wireAdjacentChain câble, dans l'ordre e.Components (front -> back, en
// partant de la fin de la chaîne comme l'étape historique), toute paire
// ADJACENTE non déjà câblée dont le composant "front" déclare une capacité
// de relais réelle (EngineCapability.RelaysTo != ""). Généralise le
// mécanisme "1 transport -> 1 backend" à une chaîne A -> B -> C -> ... de
// profondeur arbitraire :
//   - le déclenchement d'une TENTATIVE de câblage se fonde uniquement sur
//     la capacité déclarée de "front" (RelaysTo != ""), exactement comme
//     l'étape historique se déclenche dès qu'un transport existe quelque
//     part dans la composition — jamais sur une comparaison statique
//     préalable (CanConnect n'est qu'un guide, pas une porte d'exécution) ;
//   - le VERDICT, lui, vient toujours de l'endpoint RÉEL du composant
//     suivant (waitForEndpoint), jamais d'une supposition : une tentative
//     déclenchée peut donc échouer clairement (voir
//     TestCompositeEngineChainRefusesIncompatibleLinkAtAnyDepth) plutôt que
//     d'être silencieusement acceptée ou silencieusement ignorée.
//
// Avec le catalogue de moteurs actuel, cette fonction est inerte au-delà de
// la paire déjà couverte par l'étape historique (voir le commentaire du
// type CompositeEngine) : elle est néanmoins exercée et prouvée par de
// vrais moteurs de test à 3 étages dans chain_test.go, qui démontrent que
// le mécanisme généralisé fonctionne réellement pour une profondeur
// arbitraire dès qu'un futur moteur "relais intermédiaire" existera.
func (e *CompositeEngine) wireAdjacentChain(ctx context.Context, started map[string]bool) error {
	n := len(e.Components)
	for i := n - 2; i >= 0; i-- {
		front, back := e.Components[i], e.Components[i+1]
		if started[front] {
			// Déjà câblé par l'étape historique (ou une itération
			// précédente de cette boucle) : ne pas re-câbler ni
			// redémarrer le même composant "front" deux fois.
			continue
		}

		frontCap, ok := GetEngineCapability(front)
		if !ok || frontCap.RelaysTo == "" {
			continue // rien à tenter pour cette paire : front est terminal
		}

		backSub, err := e.component(back)
		if err != nil {
			return err
		}
		if !started[back] {
			if err := backSub.Start(ctx); err != nil {
				return fmt.Errorf("moteur hybride %s : démarrage de %s (relais de %s) : %w", e.Name(), back, front, err)
			}
			started[back] = true
		}

		ep, err := waitForEndpoint(backSub, endpointWaitTimeout)
		if err != nil {
			return fmt.Errorf("moteur hybride %s : %w", e.Name(), err)
		}
		if ep.Network != frontCap.RelaysTo {
			return fmt.Errorf(
				"moteur hybride %s : %s expose un endpoint %s (%s), mais %s ne relaie que vers un backend %s — "+
					"chaînage impossible avec le mécanisme actuel (protocoles incompatibles), pas un problème d'endpoint",
				e.Name(), back, ep.Network, ep.Addr, front, strings.ToUpper(frontCap.RelaysTo),
			)
		}

		frontSub, err := e.component(front)
		if err != nil {
			return err
		}
		e.mu.Lock()
		lastCfg := e.lastCfgByComponent[front]
		e.mu.Unlock()
		wiredCfg, err := injectBackend(lastCfg, ep.Addr)
		if err != nil {
			return fmt.Errorf("moteur hybride %s : préparation config %s avec relais réel %s (%s) : %w", e.Name(), front, back, ep.Addr, err)
		}
		if err := frontSub.Configure(ctx, wiredCfg); err != nil {
			return fmt.Errorf("configuration %s avec le relais réel de %s (%s) : %w", front, back, ep.Addr, err)
		}
		if err := frontSub.Start(ctx); err != nil {
			return fmt.Errorf("démarrage relais %s : %w", front, err)
		}
		started[front] = true
	}
	return nil
}

// RunForeground lance tous les composants en avant-plan et bloque jusqu'à
// l'arrêt du premier composant (celui qui guide le cycle de vie). Le premier
// composant de e.Components est considéré comme principal (ex : xray).
func (e *CompositeEngine) RunForeground(ctx context.Context) error {
	var (
		wg      sync.WaitGroup
		firstCh chan error
	)
	for i, name := range e.Components {
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		ch := make(chan error, 1)
		if i == 0 {
			firstCh = ch
		}
		wg.Add(1)
		go func(sub engine.Engine, ch chan error) {
			defer wg.Done()
			ch <- sub.RunForeground(ctx)
		}(sub, ch)
	}

	err := <-firstCh
	// À l'arrêt du composant principal, on coupe l'ensemble.
	_ = e.Stop()
	wg.Wait()
	return err
}

// Stop arrête les composants en ordre inverse.
func (e *CompositeEngine) Stop() error {
	for i := len(e.Components) - 1; i >= 0; i-- {
		sub, err := e.component(e.Components[i])
		if err != nil {
			return err
		}
		if err := sub.Stop(); err != nil {
			return err
		}
	}
	return nil
}

// Restart redémarre le moteur hybride (arrêt puis démarrage).
func (e *CompositeEngine) Restart(ctx context.Context) error {
	if err := e.Stop(); err != nil {
		return err
	}
	return e.Start(ctx)
}

// Status agrège l'état des composants.
func (e *CompositeEngine) Status() engine.EngineStatus {
	st := engine.EngineStatus{Installed: true}
	for _, name := range e.Components {
		sub, err := e.component(name)
		if err != nil {
			st.Error = err.Error()
			break
		}
		subSt := sub.Status()
		if !subSt.Installed {
			st.Installed = false
		}
		if subSt.Running {
			st.Running = true
			st.PID = subSt.PID
		}
	}
	// ListenAddr reporte l'adresse réelle sur laquelle le transport écoute
	// (celle qu'un client compose), obtenue lors du dernier Start() réussi
	// — jamais un placeholder.
	e.mu.Lock()
	front := e.frontAddr
	e.mu.Unlock()
	if front != "" && st.Running {
		st.ListenAddr = front
	}
	return st
}

// HealthCheck vérifie que tous les composants sont opérationnels.
func (e *CompositeEngine) HealthCheck() error {
	for _, name := range e.Components {
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.HealthCheck(); err != nil {
			return err
		}
	}
	return nil
}

// Logs relaie les journaux du premier composant.
func (e *CompositeEngine) Logs(lines int) ([]string, error) {
	if len(e.Components) == 0 {
		return nil, fmt.Errorf("aucun composant")
	}
	sub, err := e.component(e.Components[0])
	if err != nil {
		return nil, err
	}
	return sub.Logs(lines)
}

// Update met à jour tous les composants.
func (e *CompositeEngine) Update() error {
	for _, name := range e.Components {
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.Update(); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall désinstalle tous les composants.
func (e *CompositeEngine) Uninstall() error {
	for _, name := range e.Components {
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.Uninstall(); err != nil {
			return err
		}
	}
	return nil
}

// HybridName compose un nom de moteur hybride à partir de ses composants,
// séparés par un tiret (ex: ["xray","slowdns"] -> "xray-slowdns").
func HybridName(components []string) string {
	return strings.Join(components, "-")
}

// RegisterHybrid enregistre dynamiquement un moteur hybride composé des
// moteurs donnés, dans l'ordre de démarrage. Il réutilise CompositeEngine et
// devient visible du registre (engine.Names) pour le menu central.
// La combinaison est validée (ValidateHybrid) avant enregistrement.
func RegisterHybrid(components []string) (string, error) {
	name := HybridName(components)
	if engine.Has(name) {
		return name, nil
	}
	if err := ValidateHybrid(components); err != nil {
		return "", fmt.Errorf("hybride %s invalide : %w", name, err)
	}
	for _, c := range components {
		if !engine.Has(c) {
			return "", fmt.Errorf("hybride %s : composant inconnu %q", name, c)
		}
	}
	factory := func() (engine.Engine, error) {
		e := &CompositeEngine{}
		e.Spec.Name = name
		e.Spec.Version = "hybrid"
		e.Spec.Description = "Hybride composé : " + strings.Join(components, " + ")
		e.Components = append([]string(nil), components...)
		e.ConfigureAll = true
		return e, nil
	}
	engine.Register(name, factory)
	return name, nil
}
