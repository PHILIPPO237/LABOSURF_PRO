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
// combinent plusieurs moteurs (ex : slowdns-ssh, dnstt-xray).
//
// Les sous-moteurs sont résolus via le registre global (engine.Get).
//
// Mécanisme de chaînage réel (voir AUDIT_PHASE3_HYBRIDS.md pour l'analyse
// complète) : les deux seuls moteurs "transport" (dnstt, slowdns) relaient
// déjà, en conditions réelles, les octets d'un tunnel client vers une
// adresse TCP "backend" configurable (net.Dial("tcp", cfg.Backend)) — c'est
// le mécanisme existant et fonctionnel (audité en Phase 2) utilisé ici pour
// le chaînage, PAS une nouvelle invention. Le composite :
//  1. démarre en premier le composant "backend" (le VPN ou le compte SSH
//     que le transport doit atteindre) ;
//  2. lui demande son endpoint RÉEL via l'interface optionnelle
//     engine.Endpointer (jamais un placeholder) ;
//  3. si cet endpoint n'est pas en TCP, refuse de chaîner (erreur claire —
//     voir requireTCPEndpoint) plutôt que de prétendre que ça fonctionne :
//     un transport DNS ne peut relayer que vers un backend TCP, donc un
//     VPN purement UDP (hysteria) ne peut structurellement pas être
//     chaîné derrière dnstt/slowdns avec le mécanisme actuel ;
//  4. reconfigure le transport avec ce backend réel, puis le démarre.
//
// Limitation documentée (voir le rapport) : le moteur "udp" vit dans un
// module Go séparé (engines/udp, binaire labosurf-udp autonome) qui n'est
// pas importé par cmd/labosurf — il n'est donc jamais présent dans le
// registre utilisé ici et ne peut structurellement pas participer à un
// hybride avec l'architecture actuelle.
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

	mu sync.Mutex

	// lastCfg mémorise la configuration JSON transmise au dernier appel à
	// Configure(), pour permettre à Start() de la ré-injecter (avec le
	// champ "backend" corrigé) dans la configuration du transport une fois
	// l'endpoint réel de son backend connu — ce qui n'est possible qu'une
	// fois ce backend effectivement démarré, donc après Configure().
	lastCfg engine.EngineConfig

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
	e.mu.Unlock()

	for _, name := range e.Components {
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.Configure(ctx, cfg); err != nil {
			return fmt.Errorf("configuration %s : %w", name, err)
		}
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

// Start démarre les composants du moteur hybride et, si la composition
// contient un transport (dnstt/slowdns), le chaîne réellement vers son
// backend : démarre le backend d'abord, récupère son endpoint réel, refuse
// de continuer si ce n'est pas un endpoint TCP (le seul type que le
// transport sait relayer), reconfigure le transport avec cette adresse
// réelle, puis le démarre. Voir le commentaire du type CompositeEngine.
func (e *CompositeEngine) Start(ctx context.Context) error {
	transportName, backendName := pickTransportAndBackend(e.Components)
	started := make(map[string]bool, len(e.Components))

	if transportName != "" && backendName != "" {
		backendSub, err := e.component(backendName)
		if err != nil {
			return err
		}
		if err := backendSub.Start(ctx); err != nil {
			return fmt.Errorf("démarrage de %s (backend du transport %s) : %w", backendName, transportName, err)
		}
		started[backendName] = true

		ep, err := waitForEndpoint(backendSub, endpointWaitTimeout)
		if err != nil {
			return fmt.Errorf("moteur hybride %s : %w", e.Name(), err)
		}
		if ep.Network != "tcp" {
			return fmt.Errorf(
				"moteur hybride %s : %s expose un endpoint %s (%s), mais le transport %s ne peut relayer que vers un backend TCP — "+
					"chaînage impossible avec le mécanisme actuel (protocoles incompatibles), pas un problème d'endpoint",
				e.Name(), backendName, ep.Network, ep.Addr, transportName,
			)
		}

		transportSub, err := e.component(transportName)
		if err != nil {
			return err
		}

		e.mu.Lock()
		lastCfg := e.lastCfg
		e.mu.Unlock()

		wiredCfg, err := injectBackend(lastCfg, ep.Addr)
		if err != nil {
			return fmt.Errorf("moteur hybride %s : préparation config %s avec backend réel %s : %w", e.Name(), transportName, ep.Addr, err)
		}
		if err := transportSub.Configure(ctx, wiredCfg); err != nil {
			return fmt.Errorf("configuration %s avec le backend réel de %s (%s) : %w", transportName, backendName, ep.Addr, err)
		}
		if err := transportSub.Start(ctx); err != nil {
			return fmt.Errorf("démarrage transport %s : %w", transportName, err)
		}
		started[transportName] = true

		e.mu.Lock()
		e.backendAddr = ep.Addr
		e.mu.Unlock()
		if frontEp, err := waitForEndpoint(transportSub, endpointWaitTimeout); err == nil {
			e.mu.Lock()
			e.frontAddr = frontEp.Addr
			e.mu.Unlock()
		}
	}

	// Démarrer les composants restants (non câblés ci-dessus), dans
	// l'ordre de Components — inclut le cas sans transport du tout, où
	// cette boucle démarre alors simplement tout dans l'ordre déclaré.
	for _, name := range e.Components {
		if started[name] {
			continue
		}
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.Start(ctx); err != nil {
			return fmt.Errorf("démarrage %s : %w", name, err)
		}
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