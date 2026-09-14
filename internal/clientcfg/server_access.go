package clientcfg

import (
	"context"
	"fmt"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/service"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// ApplyServerConfigFromAccess applique la configuration serveur d'un Service à
// partir d'une liste d'Access (service.Access) — nouvelle API M3.
//
// Cette fonction est le pendant de ApplyServerConfig (ancienne API) pour le
// nouveau système Access. Les deux fonctions coexistent pendant la transition.
//
// # Fonctionnement
//
// Les Access sont convertis en []store.Account synthétiques (un compte par
// Access, avec un seul Grant portant les Secrets de l'Access) puis les
// fonctions buildGroupedConfig / buildComponentConfigs existantes sont réutilisées
// telles quelles — aucune logique de génération n'est dupliquée.
//
// # Hybrides
//
// Pour un Service hybride (svc.IsHybrid() == true), buildComponentConfigs est
// appelé avec les composants du Service ; aliasGrantForComponent s'occupe du
// mapping, comme pour l'ancienne API.
//
// # Limites
//
// La fonction n'appelle PAS EnsureAccessSecrets : les secrets doivent exister
// avant l'appel (via EnsureAccessSecrets ou MigrateGrantsToAccess).
//
// prof.Host doit être non-vide ; une erreur explicite est retournée sinon.
func ApplyServerConfigFromAccess(
	ctx context.Context,
	svc service.Service,
	accesses []service.Access,
	prof srvcfg.Profile,
) error {
	if prof.Host == "" {
		return fmt.Errorf("hôte du serveur non défini : configurez le profil serveur")
	}

	e, err := engine.Get(svc.Engine)
	if err != nil {
		return fmt.Errorf("moteur %q introuvable : %w", svc.Engine, err)
	}

	synth := accessesToSyntheticAccounts(accesses, svc.Engine)

	if ce, ok := e.(*engineutil.CompositeEngine); ok {
		componentCfgs := buildComponentConfigs(svc.Engine, ce.Components, synth, prof)
		ce.ComponentConfig = componentCfgs

		shared := engine.EngineConfig{}
		primary := primaryVPN(svc.Engine)
		if primary == "" && len(ce.Components) > 0 {
			primary = ce.Components[0]
		}
		if cfg, ok := componentCfgs[primary]; ok {
			shared = cfg
		}
		return e.Configure(ctx, shared)
	}

	serverJSON := buildGroupedConfig(svc.Engine, synth, prof)
	return e.Configure(ctx, engine.EngineConfig{JSON: serverJSON})
}

// accessesToSyntheticAccounts convertit une liste d'Access en []store.Account
// synthétiques compatibles avec buildGroupedConfig / buildComponentConfigs.
//
// Chaque Access devient un Account minimal avec un seul Grant portant
// a.Secrets comme Config. Le nom du Grant est engineName, qui correspond au
// nom cherché par buildGroupedConfig(engineName, ...).
//
// Pour les hybrides (ex: "dnstt-xray"), engineName == "dnstt-xray" et
// aliasGrantForComponent copiera ce grant sous le nom de chaque composant,
// exactement comme avec l'ancienne API.
func accessesToSyntheticAccounts(accesses []service.Access, engineName string) []store.Account {
	out := make([]store.Account, 0, len(accesses))
	for _, a := range accesses {
		acc := store.Account{
			ID:      a.AccountID,
			Enabled: a.Enabled,
		}
		acc.Grants = map[string]*store.EngineGrant{
			engineName: {
				Engine:  engineName,
				Config:  a.Secrets,
				Enabled: a.Enabled,
			},
		}
		out = append(out, acc)
	}
	return out
}
