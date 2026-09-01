package main

import (
	"bytes"
	"testing"
)

func TestTunnelEncodeDecode(t *testing.T) {
	clientID := uint64(123456789)
	payload := []byte("TEST_TUNNEL")

	encoded, err := EncodeTunnelPacket(clientID, payload)
	if err != nil {
		t.Fatalf("EncodeTunnelPacket: %v", err)
	}

	if len(encoded) != tunnelHeaderSz+len(payload) {
		t.Fatalf("taille incorrecte: got %d", len(encoded))
	}

	packet, err := DecodeTunnelPacket(encoded)
	if err != nil {
		t.Fatalf("DecodeTunnelPacket: %v", err)
	}

	if packet.Version != tunnelVersion {
		t.Fatalf("version incorrecte: got %d", packet.Version)
	}

	if packet.ClientID != clientID {
		t.Fatalf("ClientID incorrect: got %d", packet.ClientID)
	}

	if !bytes.Equal(packet.Payload, payload) {
		t.Fatalf("payload incorrect: got %q", packet.Payload)
	}
}

func TestTunnelRejectsOversizedPayload(t *testing.T) {
	payload := make([]byte, maxTunnelPayload+1)

	_, err := EncodeTunnelPacket(1, payload)
	if err != ErrPacketTooBig {
		t.Fatalf("erreur attendue ErrPacketTooBig, got %v", err)
	}
}

func TestTunnelRejectsInvalidVersion(t *testing.T) {
	packet := make([]byte, tunnelHeaderSz)
	packet[0] = 99

	_, err := DecodeTunnelPacket(packet)
	if err == nil {
		t.Fatal("une version invalide doit être refusée")
	}
}

func TestTunnelAcceptsMaximumPayload(t *testing.T) {
	payload := make([]byte, maxTunnelPayload)

	encoded, err := EncodeTunnelPacket(1, payload)
	if err != nil {
		t.Fatalf("le payload maximal devrait être accepté: %v", err)
	}

	if len(encoded) != tunnelHeaderSz+maxTunnelPayload {
		t.Fatalf("taille incorrecte: got %d", len(encoded))
	}

	decoded, err := DecodeTunnelPacket(encoded)
	if err != nil {
		t.Fatalf("DecodeTunnelPacket: %v", err)
	}

	if len(decoded.Payload) != maxTunnelPayload {
		t.Fatalf("payload incorrect: got %d", len(decoded.Payload))
	}
}
