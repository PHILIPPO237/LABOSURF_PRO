package main

import "context"

// Protocol représente un transport pris en charge par LABOSURF PRO.
type Protocol interface {
	Name() string
	Start(ctx context.Context) error
	Stop() error
}
