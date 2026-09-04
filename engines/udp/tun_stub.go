//go:build !linux

package main

import "fmt"

type TUNDevice struct {
	name string
}

func NewTUNDevice(name string) (*TUNDevice, error) {
	return nil, fmt.Errorf("TUN non disponible sur cette plateforme")
}

func (t *TUNDevice) Name() string        { return t.name }
func (t *TUNDevice) Read(p []byte) (int, error)  { return 0, fmt.Errorf("TUN non disponible") }
func (t *TUNDevice) Write(p []byte) (int, error) { return 0, fmt.Errorf("TUN non disponible") }
func (t *TUNDevice) Close() error        { return nil }
