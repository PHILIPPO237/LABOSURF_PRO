package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	productName        = "LABOSURF PRO"
	licensePrefix      = "LABOSURF"
	licenseLength      = 40
	activationWindow   = 3 * time.Hour
	licenseAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*_-+=?"
	licenseFileName    = "license.token"
	machineIDFileName  = "machine.id"
	activationFileName = "activation.json"
)

// embeddedVerifyKeyHex is the production public key.
// It can be overridden by LABOSURF_LICENSE_PUBKEY env var or labosurf_pub.key file.
var EmbeddedVerifyKeyHex = "7b27e59816d60f38a7299e226c714a3cb31a011f91f424099368506ded209595"

// Test keys - only used in tests
var (
	testSignKey   ed25519.PrivateKey
	testVerifyKey ed25519.PublicKey
)

// resolveVerifyKey returns the Ed25519 public key for signature verification.
// Priority: test key > env var LABOSURF_LICENSE_PUBKEY > labosurf_pub.key file > embedded key.
func resolveVerifyKey() (ed25519.PublicKey, error) {
	if testVerifyKey != nil {
		return testVerifyKey, nil
	}

	if v := strings.TrimSpace(os.Getenv("LABOSURF_LICENSE_PUBKEY")); v != "" {
		return decodePublicKey(v)
	}

	for _, path := range []string{"labosurf_pub.key", "/etc/labosurf/labosurf_pub.key"} {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return decodePublicKey(strings.TrimSpace(string(raw)))
		}
	}

	if EmbeddedVerifyKeyHex != "" {
		return decodePublicKey(EmbeddedVerifyKeyHex)
	}

	return nil, fmt.Errorf("clé publique de vérification introuvable")
}

func decodePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("clé publique invalide : %w", err)
	}

	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf(
			"clé publique : taille %d attendue %d",
			len(b),
			ed25519.PublicKeySize,
		)
	}

	return ed25519.PublicKey(b), nil
}

// canonicalPayload returns the JSON payload that was signed.
func canonicalPayload(data LicenseData) ([]byte, error) {
	return json.Marshal(data)
}

// verifySignature verifies the Ed25519 signature of a license.
func verifySignature(payload []byte, signature []byte, pub ed25519.PublicKey) bool {
	return ed25519.Verify(pub, payload, signature)
}

// ParseLicenseToken decodes a license token without verifying its signature.
func ParseLicenseToken(token string) (LicenseData, []byte, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return LicenseData{}, nil, fmt.Errorf("format de jeton invalide (attendu: payload.signature)")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return LicenseData{}, nil, fmt.Errorf("décodage payload : %w", err)
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return LicenseData{}, nil, fmt.Errorf("décodage signature : %w", err)
	}

	var data LicenseData
	if err := json.Unmarshal(payload, &data); err != nil {
		return LicenseData{}, nil, fmt.Errorf("payload JSON invalide : %w", err)
	}

	return data, signature, nil
}

type LicenseData struct {
	ID              string `json:"id"`
	Key             string `json:"key"`
	IssuedAt        string `json:"issued_at"`
	ActivationUntil string `json:"activation_until"`
	Product         string `json:"product"`
	Comment         string `json:"comment,omitempty"`
}

type LicenseToken struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type ActivationRecord struct {
	LicenseID     string `json:"license_id"`
	MachineID     string `json:"machine_id"`
	ActivatedAt   string `json:"activated_at"`
	Token         string `json:"token"`
}

// getDataDir returns the data directory path
func getDataDir() string {
	if dir := os.Getenv("LABOSURF_DATA_DIR"); dir != "" {
		return dir
	}
	return "/etc/labosurf"
}

func licenseFilePath() string {
	return filepath.Join(getDataDir(), licenseFileName)
}

func machineIDPath() string {
	return filepath.Join(getDataDir(), machineIDFileName)
}

func activationFilePath() string {
	return filepath.Join(getDataDir(), activationFileName)
}

// VerifyPlatformLicense checks if the platform has a valid license
func VerifyPlatformLicense() error {
	tokenPath := licenseFilePath()
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		return fmt.Errorf("aucune licence trouvée (fichier %s manquant)", tokenPath)
	}

	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return fmt.Errorf("lecture licence : %w", err)
	}

	tokenStr := strings.TrimSpace(string(tokenBytes))
	if tokenStr == "" {
		return fmt.Errorf("licence vide")
	}

	// Parse and verify the license token (format + signature + expiration + product)
	data, signature, err := ParseLicenseToken(tokenStr)
	if err != nil {
		return err
	}

	// Verify Ed25519 signature
	pub, err := resolveVerifyKey()
	if err != nil {
		return fmt.Errorf("clé publique de vérification : %w", err)
	}
	payloadBytes, err := canonicalPayload(data)
	if err != nil {
		return fmt.Errorf("sérialisation payload : %w", err)
	}
	if !verifySignature(payloadBytes, signature, pub) {
		return fmt.Errorf("signature Ed25519 invalide : licence altérée")
	}

	if data.Product != "LABOSURF PRO" {
		return fmt.Errorf("produit incompatible : %s", data.Product)
	}

	activationUntil, err := time.Parse(time.RFC3339, data.ActivationUntil)
	if err != nil {
		return fmt.Errorf("date d'activation invalide : %w", err)
	}
	if time.Now().After(activationUntil) {
		return fmt.Errorf("fenêtre d'activation expirée (%s)", activationUntil.Format(time.RFC3339))
	}

	// Check machine binding
	machineIDPath := machineIDPath()
	if _, err := os.Stat(machineIDPath); err == nil {
		machineIDBytes, _ := os.ReadFile(machineIDPath)
		currentMachineID := strings.TrimSpace(string(machineIDBytes))
		activationPath := activationFilePath()
		if _, err := os.Stat(activationPath); err == nil {
			actBytes, _ := os.ReadFile(activationPath)
			var act ActivationRecord
			if json.Unmarshal(actBytes, &act) == nil {
				if act.MachineID != currentMachineID {
					return fmt.Errorf("cette licence est liée à une autre machine")
				}
			}
		}
	}

	log.Printf("✔ Licence valide : ID=%s, expire le %s", data.ID, activationUntil.Format(time.RFC3339))
	return nil
}

// Activate activates a license token
func Activate(token string) error {
	// Parse and verify the token first
	data, signature, err := ParseLicenseToken(token)
	if err != nil {
		return err
	}

	// Verify Ed25519 signature
	pub, err := resolveVerifyKey()
	if err != nil {
		return fmt.Errorf("clé publique de vérification : %w", err)
	}
	payloadBytes, err := canonicalPayload(data)
	if err != nil {
		return fmt.Errorf("sérialisation payload : %w", err)
	}
	if !verifySignature(payloadBytes, signature, pub) {
		return fmt.Errorf("signature Ed25519 invalide : licence altérée")
	}

	if data.Product != "LABOSURF PRO" {
		return fmt.Errorf("produit incompatible : %s", data.Product)
	}

	activationUntil, err := time.Parse(time.RFC3339, data.ActivationUntil)
	if err != nil {
		return fmt.Errorf("date d'activation invalide : %w", err)
	}
	if time.Now().After(activationUntil) {
		return fmt.Errorf("fenêtre d'activation expirée (%s)", activationUntil.Format(time.RFC3339))
	}

	// Generate or load machine ID
	machineID, err := getOrCreateMachineID()
	if err != nil {
		return fmt.Errorf("machine ID : %w", err)
	}

	// Store license token
	if err := os.MkdirAll(getDataDir(), 0o755); err != nil {
		return fmt.Errorf("création dossier données : %w", err)
	}

	if err := os.WriteFile(licenseFilePath(), []byte(token), 0o600); err != nil {
		return fmt.Errorf("écriture licence : %w", err)
	}

	// Record activation
	activation := ActivationRecord{
		LicenseID:     data.ID,
		MachineID:     machineID,
		ActivatedAt:   time.Now().UTC().Format(time.RFC3339),
		Token:         token,
	}
	actBytes, _ := json.MarshalIndent(activation, "", "  ")
	if err := os.WriteFile(activationFilePath(), actBytes, 0o600); err != nil {
		return fmt.Errorf("écriture activation : %w", err)
	}

	log.Printf("✔ Licence activée pour machine %s", machineID[:16]+"...")
	return nil
}

// Status shows license status
func Status() error {
	tokenPath := licenseFilePath()
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		fmt.Println("Aucune licence activée")
		return nil
	}

	tokenBytes, _ := os.ReadFile(tokenPath)
	token := strings.TrimSpace(string(tokenBytes))
	fmt.Printf("Licence : %s\n", maskToken(token))

	// Check activation record
	actPath := activationFilePath()
	if _, err := os.Stat(actPath); err == nil {
		actBytes, _ := os.ReadFile(actPath)
		var act struct {
			LicenseID   string `json:"license_id"`
			MachineID   string `json:"machine_id"`
			ActivatedAt string `json:"activated_at"`
		}
		json.Unmarshal(actBytes, &act)
		fmt.Printf("Machine ID : %s\n", act.MachineID)
		fmt.Printf("Activée le : %s\n", act.ActivatedAt)
	}

	// Verify
	if err := VerifyPlatformLicense(); err != nil {
		fmt.Printf("Statut : INVALIDE (%v)\n", err)
		return err
	}
	fmt.Println("Statut : VALIDE")
	return nil
}

// Verify verifies the license without activating
func Verify() error {
	return VerifyPlatformLicense()
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

func getOrCreateMachineID() (string, error) {
	path := machineIDPath()
	if data, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("génération machine ID : %w", err)
	}
	hexID := hex.EncodeToString(b)
	if err := os.MkdirAll(getDataDir(), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(hexID), 0o600); err != nil {
		return "", err
	}
	return hexID, nil
}