package main

import (
	"context"
	"errors"
	"fmt"
	"net"
)

var ErrForwarderUnavailable = errors.New("forwarder réseau indisponible")

type Forwarder interface {
	Forward(ctx context.Context, payload []byte) ([]byte, error)
	Close() error
}

type UDPForwarder struct {
	addr *net.UDPAddr
}

func NewUDPForwarder(address string) (*UDPForwarder, error) {
	if address == "" {
		return nil, errors.New("adresse du forwarder vide")
	}

	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("adresse UDP invalide : %w", err)
	}

	return &UDPForwarder{addr: addr}, nil
}

func (f *UDPForwarder) Forward(
	ctx context.Context,
	payload []byte,
) ([]byte, error) {
	if f == nil || f.addr == nil {
		return nil, ErrForwarderUnavailable
	}

	conn, err := net.DialUDP("udp", nil, f.addr)
	if err != nil {
		return nil, fmt.Errorf("connexion UDP : %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("envoi UDP : %w", err)
	}

	buffer := make([]byte, 65535)

	n, err := conn.Read(buffer)
	if err != nil {
		return nil, fmt.Errorf("réception UDP : %w", err)
	}

	return append([]byte(nil), buffer[:n]...), nil
}

func (f *UDPForwarder) Close() error {
	return nil
}
