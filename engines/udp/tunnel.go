package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
)

const (
	tunnelVersion byte = 1

	// Taille de l'en-tête :
	// 1 octet version
	// 3 octets réservés
	// 8 octets ClientID
	tunnelHeaderSz = 12

	// Taille maximale d'un datagramme UDP IPv4.
	maxUDPPacketSize = 65507

	// Taille maximale du payload après notre en-tête.
	maxTunnelPayload = maxUDPPacketSize - tunnelHeaderSz
)

var (
	ErrInvalidPacket = errors.New("paquet tunnel invalide")
	ErrPacketTooBig  = errors.New("paquet tunnel trop grand")
)

var tunnelBufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, maxUDPPacketSize)
	},
}

type TunnelPacket struct {
	Version  byte
	ClientID uint64
	Payload  []byte
}

type Tunnel struct {
	conn *net.UDPConn
}

func NewTunnel(conn *net.UDPConn) *Tunnel {
	return &Tunnel{
		conn: conn,
	}
}

func EncodeTunnelPacket(clientID uint64, payload []byte) ([]byte, error) {
	if len(payload) > maxTunnelPayload {
		return nil, ErrPacketTooBig
	}

	packet := make([]byte, tunnelHeaderSz+len(payload))

	packet[0] = tunnelVersion

	binary.BigEndian.PutUint64(
		packet[4:12],
		clientID,
	)

	copy(packet[tunnelHeaderSz:], payload)

	return packet, nil
}

func DecodeTunnelPacket(data []byte) (TunnelPacket, error) {
	if len(data) < tunnelHeaderSz {
		return TunnelPacket{}, ErrInvalidPacket
	}

	if data[0] != tunnelVersion {
		return TunnelPacket{}, fmt.Errorf(
			"%w: version %d",
			ErrInvalidPacket,
			data[0],
		)
	}

	payloadLen := len(data) - tunnelHeaderSz

	if payloadLen > maxTunnelPayload {
		return TunnelPacket{}, ErrPacketTooBig
	}

	clientID := binary.BigEndian.Uint64(data[4:12])

	payload := make([]byte, payloadLen)
	copy(payload, data[tunnelHeaderSz:])

	return TunnelPacket{
		Version:  tunnelVersion,
		ClientID: clientID,
		Payload:  payload,
	}, nil
}

func (t *Tunnel) Send(
	addr *net.UDPAddr,
	clientID uint64,
	payload []byte,
) error {
	if t == nil || t.conn == nil {
		return errors.New("tunnel non initialisé")
	}

	if addr == nil {
		return errors.New("adresse client absente")
	}

	packet, err := EncodeTunnelPacket(clientID, payload)
	if err != nil {
		return err
	}

	if _, err := t.conn.WriteToUDP(packet, addr); err != nil {
		return fmt.Errorf("envoi tunnel UDP : %w", err)
	}

	return nil
}

func (t *Tunnel) Receive(
	buffer []byte,
) (TunnelPacket, *net.UDPAddr, error) {
	if t == nil || t.conn == nil {
		return TunnelPacket{}, nil, errors.New(
			"tunnel non initialisé",
		)
	}

	if len(buffer) < maxUDPPacketSize {
		return TunnelPacket{}, nil, errors.New(
			"buffer de réception trop petit",
		)
	}

	n, addr, err := t.conn.ReadFromUDP(buffer[:maxUDPPacketSize])
	if err != nil {
		return TunnelPacket{}, nil, fmt.Errorf(
			"réception tunnel UDP : %w",
			err,
		)
	}

	packet, err := DecodeTunnelPacket(buffer[:n])
	if err != nil {
		return TunnelPacket{}, addr, err
	}

	return packet, addr, nil
}

func AcquireTunnelBuffer() []byte {
	return tunnelBufferPool.Get().([]byte)
}

func ReleaseTunnelBuffer(buffer []byte) {
	if cap(buffer) < maxUDPPacketSize {
		return
	}

	buffer = buffer[:maxUDPPacketSize]

	tunnelBufferPool.Put(buffer)
}
