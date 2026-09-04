package slowdns

import (
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

const (
	defaultSlowDNSPort = 53
	defaultBackend     = "127.0.0.1:22"
)

type SlowDNSConfig struct {
	Domain  string         `json:"domain"`
	Port    int            `json:"port"`
	Backend string         `json:"backend"`
	Users   []SlowDNSUser  `json:"users"`
}

type SlowDNSUser struct {
	User       string `json:"user"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	Enabled    bool   `json:"enabled"`
}

func loadSlowDNSConfig(path string) (SlowDNSConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SlowDNSConfig{}, fmt.Errorf("lecture config SlowDNS : %w", err)
	}
	var cfg SlowDNSConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return SlowDNSConfig{}, fmt.Errorf("config SlowDNS invalide : %w", err)
	}
	if cfg.Port <= 0 {
		cfg.Port = defaultSlowDNSPort
	}
	if cfg.Backend == "" {
		cfg.Backend = defaultBackend
	}
	return cfg, nil
}

func encodeSubdomain(data []byte) string {
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)
	var parts []string
	for i := 0; i < len(encoded); i += 63 {
		end := i + 63
		if end > len(encoded) {
			end = len(encoded)
		}
		parts = append(parts, strings.ToLower(encoded[i:end]))
	}
	return strings.Join(parts, ".")
}

func decodeSubdomain(subdomain string) ([]byte, error) {
cleaned := strings.ReplaceAll(subdomain, ".", "")
	cleaned = strings.ToUpper(cleaned)
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleaned)
}

type DNSPacket struct {
	ID        uint16
	Flags     uint16
	Questions uint16
	Answers   uint16
	Authority uint16
	Additionals uint16
}

func buildDNSResponse(query []byte, answerData []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	resp := make([]byte, len(query)+16+len(answerData))
	copy(resp, query)

	resp[2] = 0x81
	resp[3] = 0x80

	binary.BigEndian.PutUint16(resp[6:], 1)

	nameOffset := 12
	for nameOffset < len(query) && query[nameOffset] != 0 {
		nameOffset += int(query[nameOffset]) + 1
	}
	nameOffset++

	qtype := binary.BigEndian.PutUint16
	_ = qtype

	pos := len(query)
	resp[pos] = 0xC0
	resp[pos+1] = byte(12)
	binary.BigEndian.PutUint16(resp[pos+2:], 16)
	binary.BigEndian.PutUint16(resp[pos+4:], 1)
	binary.BigEndian.PutUint16(resp[pos+6:], 300)
	binary.BigEndian.PutUint16(resp[pos+8:], uint16(len(answerData)))
	copy(resp[pos+10:], answerData)

	return resp[:pos+10+len(answerData)]
}

func startTCPServer(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

func handleTCPConn(conn net.Conn, backend string) {
	defer conn.Close()
	backendConn, err := net.Dial("tcp", backend)
	if err != nil {
		return
	}
	defer backendConn.Close()

	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				backendConn.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := backendConn.Read(buf)
			if n > 0 {
				conn.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
}
