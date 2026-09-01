package main

import (
	"fmt"
	"net"
)

type TunnelRouter struct {
	conn *net.UDPConn
}

func NewTunnelRouter(conn *net.UDPConn) *TunnelRouter {
	return &TunnelRouter{
		conn: conn,
	}
}

// Send renvoie un payload au client sous forme de paquet tunnel.
func (r *TunnelRouter) Send(
	addr *net.UDPAddr,
	clientID uint64,
	payload []byte,
) error {
	if r == nil || r.conn == nil {
		return net.ErrClosed
	}

	packet, err := EncodeTunnelPacket(clientID, payload)
	if err != nil {
		return fmt.Errorf("encodage tunnel : %w", err)
	}

	if _, err := r.conn.WriteToUDP(packet, addr); err != nil {
		return fmt.Errorf("envoi tunnel : %w", err)
	}

	return nil
}
