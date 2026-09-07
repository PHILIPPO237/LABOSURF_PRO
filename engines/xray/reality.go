package xray

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/crypto/curve25519"
)

// RealityKeyPair represents a REALITY key pair
type RealityKeyPair struct {
	PrivateKey [32]byte
	PublicKey  [32]byte
}

// GenerateRealityKeyPair generates a new X25519 key pair for REALITY
func GenerateRealityKeyPair() (*RealityKeyPair, error) {
	var privateKey [32]byte
	if _, err := rand.Read(privateKey[:]); err != nil {
		return nil, fmt.Errorf("génération clé privée: %w", err)
	}

	var publicKey [32]byte
	curve25519.ScalarBaseMult(&publicKey, &privateKey)

	return &RealityKeyPair{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
	}, nil
}

// PrivateKeyPEM returns the private key in PEM format
func (k *RealityKeyPair) PrivateKeyPEM() ([]byte, error) {
	privKey, err := x509.MarshalPKCS8PrivateKey(k.PrivateKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKey,
	}), nil
}

// PublicKeyHex returns the public key in hex encoding
func (k *RealityKeyPair) PublicKeyHex() string {
	return fmt.Sprintf("%x", k.PublicKey[:])
}

// PublicKeyForVLESS returns the public key in base64url encoding for VLESS links
func (k *RealityKeyPair) PublicKeyForVLESS() string {
	return base64.RawURLEncoding.EncodeToString(k.PublicKey[:])
}

// SaveRealityKeys saves the REALITY key pair to files
func SaveRealityKeys(keys *RealityKeyPair, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("création répertoire: %w", err)
	}

	privPEM, err := keys.PrivateKeyPEM()
	if err != nil {
		return err
	}

	privPath := filepath.Join(dir, "reality_private.pem")
	pubPath := filepath.Join(dir, "reality_public.key")

	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return fmt.Errorf("écriture clé privée: %w", err)
	}
	if err := os.WriteFile(pubPath, []byte(keys.PublicKeyHex()), 0o644); err != nil {
		return fmt.Errorf("écriture clé publique: %w", err)
	}

	log.Printf("✔ Clés REALITY générées: %s, %s", privPath, pubPath)
	return nil
}

// LoadRealityKeys loads the REALITY key pair from files
func LoadRealityKeys(dir string) (*RealityKeyPair, error) {
	privPath := filepath.Join(dir, "reality_private.pem")
	pubPath := filepath.Join(dir, "reality_public.key")

	privPEM, err := os.ReadFile(privPath)
	if err != nil {
		return nil, fmt.Errorf("lecture clé privée: %w", err)
	}

	block, _ := pem.Decode(privPEM)
	if block == nil {
		return nil, fmt.Errorf("clé privée PEM invalide")
	}

	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing clé privée: %w", err)
	}

	// Extract the raw 32-byte private key from the parsed key
	var privKeyArray [32]byte
	switch pk := parsedKey.(type) {
	case [32]byte:
		copy(privKeyArray[:], pk[:])
	case []byte:
		copy(privKeyArray[:], pk)
	default:
		raw, err := x509.MarshalPKCS8PrivateKey(parsedKey)
		if err != nil {
			return nil, fmt.Errorf("sérialisation clé: %w", err)
		}
		if len(raw) >= 32 {
			copy(privKeyArray[:], raw[len(raw)-32:])
		} else {
			return nil, fmt.Errorf("clé privée trop courte")
		}
	}

	pubBytes, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, fmt.Errorf("lecture clé publique: %w", err)
	}

	pubHex := string(pubBytes)
	pubKey, err := hex.DecodeString(pubHex)
	if err != nil {
		return nil, fmt.Errorf("décodage clé publique hex: %w", err)
	}

	var pubKeyArray [32]byte
	copy(pubKeyArray[:], pubKey)

	return &RealityKeyPair{
		PrivateKey: privKeyArray,
		PublicKey:  pubKeyArray,
	}, nil
}

// EnsureRealityKeys ensures REALITY keys exist, generating them if needed
func EnsureRealityKeys(dir string) (*RealityKeyPair, error) {
	privPath := filepath.Join(dir, "reality_private.pem")
	pubPath := filepath.Join(dir, "reality_public.key")

	if _, err := os.Stat(privPath); err == nil {
		if _, err := os.Stat(pubPath); err == nil {
			return LoadRealityKeys(dir)
		}
	}

	keys, err := GenerateRealityKeyPair()
	if err != nil {
		return nil, err
	}

	if err := SaveRealityKeys(keys, dir); err != nil {
		return nil, err
	}

	return keys, nil
}