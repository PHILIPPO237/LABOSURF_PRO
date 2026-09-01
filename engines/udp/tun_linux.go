//go:build linux && !android

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	iffTun    = 0x0001
	iffNoPI   = 0x1000
	tunSetIFF = 0x400454CA
)

type ifreq struct {
	Name  [16]byte
	Flags uint16
	_     [22]byte
}

type TUNDevice struct {
	name string
	file *os.File
}

func NewTUNDevice(name string) (*TUNDevice, error) {
	if name == "" {
		name = "labosurf0"
	}

	file, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("ouverture de /dev/net/tun : %w", err)
	}

	var req ifreq
	copy(req.Name[:], name)
	req.Flags = iffTun | iffNoPI

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		file.Fd(),
		uintptr(tunSetIFF),
		uintptr(unsafe.Pointer(&req)),
	)

	if errno != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("TUNSETIFF : %w", errno)
	}

	actualName := string(req.Name[:])
	for i, b := range req.Name {
		if b == 0 {
			actualName = string(req.Name[:i])
			break
		}
	}

	return &TUNDevice{name: actualName, file: file}, nil
}

func (t *TUNDevice) Name() string {
	if t == nil {
		return ""
	}
	return t.name
}

func (t *TUNDevice) Read(p []byte) (int, error) {
	if t == nil || t.file == nil {
		return 0, errors.New("TUN fermé")
	}
	return t.file.Read(p)
}

func (t *TUNDevice) Write(p []byte) (int, error) {
	if t == nil || t.file == nil {
		return 0, errors.New("TUN fermé")
	}
	return t.file.Write(p)
}

func (t *TUNDevice) Close() error {
	if t == nil || t.file == nil {
		return nil
	}

	err := t.file.Close()
	t.file = nil
	return err
}
