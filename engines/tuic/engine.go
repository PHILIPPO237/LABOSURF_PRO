// Package tuic fournit le moteur TUIC officiel pour la plateforme LABOSURF PRO.
//
// Ce moteur télécharge, vérifie et supervise le binaire officiel
// tuic-server (implémentation de référence du protocole TUIC v5, dépôt
// tuic-protocol/tuic) — jamais une réimplémentation maison du protocole.
//
// Protocole : TUIC v5. Transport : QUIC (UDP), TLS obligatoire.
// Authentification : UUID + mot de passe (voir tuic_binary.go).
package tuic

import (
	"labosurf/internal/engine"
)

const (
	engineName = "tuic"
	engineDesc = "Serveur TUIC officiel (tuic-server, protocole TUIC v5 sur QUIC/TLS, authentification UUID + mot de passe)"
)

func New() (engine.Engine, error) {
	return NewTUICEngine()
}

func init() {
	engine.Register(engineName, New)
}
