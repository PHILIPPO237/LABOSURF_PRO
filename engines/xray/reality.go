package xray

import (
	"crypto/rand"
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

// PrivateKeyPEM retourne la clé privée encodée en PEM, pour stockage sur
// disque. Il ne s'agit PAS d'un encodage x509/PKCS8 : un scalaire X25519
// brut de 32 octets n'est pas un type que x509.MarshalPKCS8PrivateKey sait
// sérialiser (il attend ed25519.PrivateKey, *ecdsa.PrivateKey, etc.) —
// tenter de le faire retournait auparavant systématiquement une erreur
// ("x509: unknown key type while marshaling PKCS#8: [32]uint8"), ce qui
// faisait échouer EnsureRealityKeys/SaveRealityKeys à chaque appel et
// empêchait toute installation du moteur Xray de se terminer. On stocke
// donc directement les 32 octets bruts dans un bloc PEM dédié.
func (k *RealityKeyPair) PrivateKeyPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "X25519 PRIVATE KEY",
		Bytes: k.PrivateKey[:],
	})
}

// PublicKeyHex returns the public key in hex encoding
func (k *RealityKeyPair) PublicKeyHex() string {
	return fmt.Sprintf("%x", k.PublicKey[:])
}

// PublicKeyForVLESS returns the public key in base64url encoding for VLESS links
func (k *RealityKeyPair) PublicKeyForVLESS() string {
	return base64.RawURLEncoding.EncodeToString(k.PublicKey[:])
}

// PrivateKeyBase64 returns the raw 32-byte X25519 private key encoded in
// base64url (no padding) — the exact format Xray-core expects in
// streamSettings.realitySettings.privateKey (the same format its own
// "xray x25519" keygen tool prints). This is NOT the PEM/PKCS8 form
// returned by PrivateKeyPEM, which is only used for on-disk storage.
func (k *RealityKeyPair) PrivateKeyBase64() string {
	return base64.RawURLEncoding.EncodeToString(k.PrivateKey[:])
}

// SaveRealityKeys saves the REALITY key pair to files
func SaveRealityKeys(keys *RealityKeyPair, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("création répertoire: %w", err)
	}

	privPEM := keys.PrivateKeyPEM()

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
	if len(block.Bytes) != 32 {
		return nil, fmt.Errorf("clé privée REALITY invalide : %d octets (32 attendus)", len(block.Bytes))
	}

	var privKeyArray [32]byte
	copy(privKeyArray[:], block.Bytes)

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