package main

import (
	"errors"
)

var ErrDeviceUnavailable = errors.New("interface réseau indisponible")

// NetworkDevice représente l'interface réseau virtuelle utilisée
// par LABOSURF PRO.
type NetworkDevice interface {
	Name() string
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}
