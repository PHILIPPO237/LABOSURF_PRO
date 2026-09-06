package engineutil

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"labosurf/internal/engine"
)

// CompositeEngine implémente engine.Engine pour les moteurs hybrides qui
// combinent plusieurs moteurs (ex : xray-slowdns, xray-dnstt).
//
// Les sous-moteurs sont résolus via le registre global (engine.Get). Le
// démarrage se fait dans l'ordre : transport d'abord, puis VPN.
// Le trafic VPN est routé via le transport.
type CompositeEngine struct {
	Spec struct {
		Name        string
		Version     string
		Description string
	}

	// Components énumère les sous-moteurs, dans l'ordre de démarrage.
	// L'ordre recommandé est : [VPN, Transport] (VPN en premier car c'est
	// le composant principal qui guide le cycle de vie, mais le transport
	// doit être démarré et prêt avant que le VPN ne s'y connecte).
	Components []string

	// ConfigureAll applique la configuration à tous les sous-moteurs.
	ConfigureAll bool

	// transportEndpoint stocke l'adresse d'écoute du transport pour le VPN.
	transportEndpoint string
}

// Name retourne l'identifiant unique du moteur hybride.
func (e *CompositeEngine) Name() string { return e.Spec.Name }

// Version retourne la version du moteur.
func (e *CompositeEngine) Version() string { return e.Spec.Version }

// Description fournit une courte description du moteur.
func (e *CompositeEngine) Description() string { return e.Spec.Description }

// component instancie un sous-moteur du registre.
func (e *CompositeEngine) component(name string) (engine.Engine, error) {
	sub, err := engine.Get(name)
	if err != nil {
		return nil, fmt.Errorf("moteur hybride %s : composant %q : %w", e.Name(), name, err)
	}
	return sub, nil
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

// Configure applique la configuration à chaque composant.
// Pour les hybrides VPN+Transport, configure le VPN avec l'endpoint du transport.
func (e *CompositeEngine) Configure(ctx context.Context, cfg engine.EngineConfig) error {
	if !e.ConfigureAll {
		return nil
	}

	// Séparer transport et VPN
	var transportName, vpnName string
	for _, name := range e.Components {
		switch Role(name) {
		case RoleTransport:
			transportName = name
		case RoleVPN:
			vpnName = name
		}
	}

	// Si on a un transport, le démarrer d'abord pour obtenir son endpoint
	if transportName != "" {
		transport, err := e.component(transportName)
		if err != nil {
			return err
		}
		if err := transport.Configure(ctx, cfg); err != nil {
			return err
		}
		if err := transport.Start(ctx); err != nil {
			return fmt.Errorf("démarrage transport %s : %w", transportName, err)
		}
		// TODO: Récupérer l'endpoint réel du transport (ex: 127.0.0.1:port)
		e.transportEndpoint = "127.0.0.1:0" // placeholder
	}

	// Configurer le VPN avec l'endpoint du transport
	if vpnName != "" {
		vpn, err := e.component(vpnName)
		if err != nil {
			return err
		}
		// Injecter l'endpoint du transport dans la config du VPN
		if e.transportEndpoint != "" {
			var jsonMap map[string]any
			if len(cfg.JSON) > 0 {
				if err := json.Unmarshal(cfg.JSON, &jsonMap); err != nil {
					return err
				}
			} else {
				jsonMap = map[string]any{}
			}
			outbound, ok := jsonMap["outbound"].(map[string]any)
			if !ok {
				outbound = map[string]any{}
			}
			outbound["transport_endpoint"] = e.transportEndpoint
			jsonMap["outbound"] = outbound

			cfgBytes, err := json.Marshal(jsonMap)
			if err != nil {
				return err
			}
			cfg.JSON = cfgBytes
		}
		if err := vpn.Configure(ctx, cfg); err != nil {
			return fmt.Errorf("configuration VPN %s : %w", vpnName, err)
		}
	}

	// Configurer les autres composants (ssh, etc.)
	for _, name := range e.Components {
		if name == transportName || name == vpnName {
			continue
		}
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.Configure(ctx, cfg); err != nil {
			return err
		}
	}

	return nil
}

// Start démarre les composants : transport d'abord, puis VPN, puis autres.
func (e *CompositeEngine) Start(ctx context.Context) error {
	// Démarrer le transport en premier
	for _, name := range e.Components {
		if Role(name) != RoleTransport {
			continue
		}
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.Start(ctx); err != nil {
			return fmt.Errorf("démarrage transport %s : %w", name, err)
		}
	}

	// Démarrer le VPN
	for _, name := range e.Components {
		if Role(name) != RoleVPN {
			continue
		}
		sub, err := e.component(name)
		if err != nil {
			return err
		}
		if err := sub.Start(ctx); err != nil {
			return fmt.Errorf("démarrage VPN %s : %w", name, err)
		}
	}

	// Démarrer les autres
	for _, name := range e.Components {
		if Role(name) == RoleTransport || Role(name) == RoleVPN {
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