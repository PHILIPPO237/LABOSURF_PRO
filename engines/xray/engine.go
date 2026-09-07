// Package xray fournit le moteur Xray-core officiel pour la plateforme LABOSURF PRO.
//
// Ce moteur télécharge, vérifie, configure et supervise le binaire
// Xray-core officiel (https://github.com/XTLS/Xray-core).
//
// Protocoles supportés : VLESS, Trojan, VMess, Shadowsocks, etc.
// Transports : TCP, WebSocket, gRPC, HTTP/2, HTTP/3, QUIC
// Sécurité : TLS, XTLS, REALITY
// Routage, DNS, sniffing, etc.
package xray

import (
	"labosurf/internal/engine"
)

const (
	engineName = "xray"
	engineDesc = "Serveur Xray-core officiel (VLESS, Trojan, VMess, Shadowsocks, etc.)"
	engineVer  = "1.0.0"
)

func New() (engine.Engine, error) {
	return NewXrayCoreEngine()
}

func init() {
	engine.Register(engineName, New)
}
