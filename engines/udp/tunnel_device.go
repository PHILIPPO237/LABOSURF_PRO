package main

import "io"

// TunnelDevice représente l'interface réseau virtuelle utilisée
// par le module UDP Engine.
//
// L'implémentation réelle pourra être différente selon la plateforme :
// Linux/TUN, Android/VpnService, etc.
type TunnelDevice interface {
	io.ReadWriteCloser

	// Name retourne le nom du périphérique.
	Name() string
}
