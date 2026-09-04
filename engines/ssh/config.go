package ssh

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSSHPort = 22
	defaultSSHDir  = "/etc/labosurf/ssh"
)

type SSHConfig struct {
	Port  int        `json:"port"`
	Users []SSHUser  `json:"users"`
	Dir   string     `json:"-"`
}

type SSHUser struct {
	Username  string `json:"username"`
	PublicKey string `json:"public_key"`
	Password  string `json:"password,omitempty"`
	Enabled   bool   `json:"enabled"`
}

func loadSSHConfig(path string) (SSHConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SSHConfig{}, fmt.Errorf("lecture config SSH : %w", err)
	}
	var cfg SSHConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return SSHConfig{}, fmt.Errorf("config SSH invalide : %w", err)
	}
	if cfg.Port <= 0 {
		cfg.Port = defaultSSHPort
	}
	if cfg.Dir == "" {
		cfg.Dir = defaultSSHDir
	}
	return cfg, nil
}

func (cfg *SSHConfig) AuthorizedKeysBytes() []byte {
	var lines []string
	for _, u := range cfg.Users {
		if !u.Enabled || u.PublicKey == "" {
			continue
		}
		b64, err := hexToBase64(u.PublicKey)
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("ssh-ed25519 %s %s@labosurf", b64, u.Username))
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func hexToBase64(hexKey string) (string, error) {
	b, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil {
		return "", err
	}
	return encodeBase64(b), nil
}

func encodeBase64(data []byte) string {
	const table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	if len(data) == 0 {
		return ""
	}
	outLen := (len(data) + 2) / 3 * 4
	out := make([]byte, outLen)
	for i := 0; i < outLen; i += 4 {
		var buf [3]byte
		for j := 0; j < 3; j++ {
			idx := i/4*3 + j
			if idx < len(data) {
				buf[j] = data[idx]
			}
		}
		out[i] = table[buf[0]>>2]
		if i+1 < outLen {
			out[i+1] = table[(buf[0]&3)<<4|buf[1]>>4]
		}
		if i+2 < outLen {
			out[i+2] = table[(buf[1]&0xF)<<2|buf[2]>>6]
		}
		if i+3 < outLen {
			out[i+3] = table[buf[2]&0x3F]
		}
	}
	return string(out)
}

func ensureSSHDir(dir string) error {
	if dir == "" {
		dir = defaultSSHDir
	}
	return os.MkdirAll(dir, 0o700)
}

func authorizedKeysPath(dir string) string {
	return filepath.Join(dir, "authorized_keys")
}

func hostKeyPath(dir string) string {
	return filepath.Join(dir, "host_key")
}
