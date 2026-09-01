package main

import (
	"net"
	"testing"
	"time"
)

func TestTunnelRouterSend(t *testing.T) {
	serverAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	serverConn, err := net.ListenUDP("udp", serverAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	clientAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	clientConn, err := net.ListenUDP("udp", clientAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	router := NewTunnelRouter(serverConn)

	clientID := uint64(123456789)
	payload := []byte("RESPONSE_TEST")

	if err := router.Send(
		clientConn.LocalAddr().(*net.UDPAddr),
		clientID,
		payload,
	); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))

	buffer := make([]byte, 65535)

	n, _, err := clientConn.ReadFromUDP(buffer)
	if err != nil {
		t.Fatalf("réception: %v", err)
	}

	packet, err := DecodeTunnelPacket(buffer[:n])
	if err != nil {
		t.Fatalf("DecodeTunnelPacket: %v", err)
	}

	if packet.ClientID != clientID {
		t.Fatalf(
			"ClientID incorrect: got %d, want %d",
			packet.ClientID,
			clientID,
		)
	}

	if string(packet.Payload) != string(payload) {
		t.Fatalf(
			"payload incorrect: got %q, want %q",
			packet.Payload,
			payload,
		)
	}
}
