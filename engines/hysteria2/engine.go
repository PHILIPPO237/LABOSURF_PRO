// Package hysteria2 fournit le moteur Hysteria2 OFFICIEL pour la plateforme
// LABOSURF PRO.
//
// Ce moteur télécharge, vérifie et supervise le binaire officiel `hysteria`
// (implémentation de référence du protocole Hysteria2, dépôt
// apernet/hysteria — canoniquement hébergé sous HyNetworks/hysteria, voir
// hysteria2_binary.go) — jamais une réimplémentation maison du protocole.
//
// Distinction importante, à ne jamais confondre : le moteur historique
// "hysteria" (engines/hysteria) de ce dépôt est une RÉIMPLÉMENTATION Go
// propriétaire avec sa propre trame UDP (magic bytes "HHAL"/"HHAT"/"HHAD"/
// "HHAP", voir engines/hysteria/server.go) — PAS le protocole Hysteria2
// réel (pas de QUIC, pas d'obfuscation Salamander, pas de masquerade HTTP).
// "hysteria2" (ce paquet) est le vrai protocole officiel : QUIC + TLS 1.3,
// obfuscation Salamander, masquerade HTTP — les deux moteurs coexistent
// délibérément, sans que l'un ne remplace l'autre.
//
// Protocole : Hysteria2 (TLS-over-QUIC). Transport : QUIC (UDP), TLS
// obligatoire. Authentification : type "userpass" (utilisateur + mot de
// passe par compte). Licence du projet officiel : MIT.
package hysteria2

import (
	"labosurf/internal/engine"
)

const (
	engineName = "hysteria2"
	engineDesc = "Serveur Hysteria2 OFFICIEL (binaire apernet/hysteria, protocole Hysteria2 sur QUIC/TLS avec obfuscation Salamander — distinct du moteur 'hysteria' maison de ce dépôt)"
)

func New() (engine.Engine, error) {
	return NewHysteria2Engine()
}

func init() {
	engine.Register(engineName, New)
}
