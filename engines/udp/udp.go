package main

import (
	"context"
	"net"
	"sync"
)

type UDPProtocol struct {
	conn *net.UDPConn
	mu   sync.Mutex
}

func NewUDPProtocol(conn *net.UDPConn) *UDPProtocol {
	return &UDPProtocol{conn: conn}
}

func (u *UDPProtocol) Name() string {
	return "udp"
}

func (u *UDPProtocol) Start(ctx context.Context) error {
	if u == nil || u.conn == nil {
		return net.ErrClosed
	}

	<-ctx.Done()
	return nil
}

func (u *UDPProtocol) Stop() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.conn == nil {
		return nil
	}

	err := u.conn.Close()
	u.conn = nil
	return err
}
