package main

import "testing"

type mockDevice struct {
	name string
}

func (m *mockDevice) Name() string {
	return m.name
}

func (m *mockDevice) Read([]byte) (int, error) {
	return 0, ErrDeviceUnavailable
}

func (m *mockDevice) Write([]byte) (int, error) {
	return 0, ErrDeviceUnavailable
}

func (m *mockDevice) Close() error {
	return nil
}

func TestNetworkDevice(t *testing.T) {
	device := &mockDevice{name: "test0"}

	if device.Name() != "test0" {
		t.Fatalf("nom incorrect : %s", device.Name())
	}

	if _, err := device.Read(make([]byte, 1500)); err != ErrDeviceUnavailable {
		t.Fatalf("erreur Read inattendue : %v", err)
	}

	if _, err := device.Write([]byte("test")); err != ErrDeviceUnavailable {
		t.Fatalf("erreur Write inattendue : %v", err)
	}

	if err := device.Close(); err != nil {
		t.Fatalf("Close : %v", err)
	}
}
