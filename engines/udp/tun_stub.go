//go:build !linux && !android

package main

import "errors"

var ErrTUNUnavailable = errors.New("TUN indisponible sur cette plateforme")

type TUNDevice struct {
	name string
}

func NewTUNDevice(name string) (*TUNDevice, error) {
	if name == "" {
		name = "labosurf0"
	}

	return &TUNDevice{name: name}, ErrTUNUnavailable
}

func (t *TUNDevice) Name() string {
	if t == nil {
		return ""
	}

	return t.name
}

func (t *TUNDevice) Read([]byte) (int, error) {
	return 0, ErrTUNUnavailable
}

func (t *TUNDevice) Write([]byte) (int, error) {
	return 0, ErrTUNUnavailable
}

func (t *TUNDevice) Close() error {
	return nil
}
