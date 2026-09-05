package license

import (
	"encoding/base64"
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

	// Basic format validation
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 2 {
		return fmt.Errorf("format de jeton invalide (attendu: payload.signature)")
	}

	// TODO: Full signature verification with public key
	// For now, just check format and expiration
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("décodage payload : %w", err)
	}

	var data struct {
		ID              string `json:"id"`
		Key             string `json:"key"`
		IssuedAt        string `json:"issued_at"`
		ActivationUntil string `json:"activation_until"`
		Product         string `json:"product"`
		Comment         string `json:"comment,omitempty"`
	}
	if err := json.Unmarshal(payloadBytes, &data); err != nil {
		return fmt.Errorf("payload JSON invalide : %w", err)
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
	// Verify token format
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return fmt.Errorf("format de jeton invalide")
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
		LicenseID:     "extracted-from-token", // TODO: parse from payload
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
	_, _ = os.ReadFile("/dev/urandom") // just to stir
	if _, err := os.ReadFile("/dev/urandom"); err != nil {
		// fallback
	}
	if _, err := os.ReadFile("/dev/urandom"); err != nil {
	}
	// Use crypto/rand
	// Simplified for now
	b = []byte(fmt.Sprintf("machine-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(getDataDir(), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return string(b), nil
}