package license

import (
	"crypto/ed25519"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// ============================================================
// LICENCE LABOSURF PRO — Nouveau modèle
// ============================================================
//
// La licence ouvre l'ACCÈS AU SCRIPT D'INSTALLATION, pas au serveur :
// 1 clé = 1 installation, dans les 3 heures suivant l'émission.
// Une fois installé, le serveur tourne librement, sans contrôle.
//
// Le reçu d'installation (<dir>/.install_<ID>.receipt) empêche de
// réutiliser la même clé sur LA MÊME machine. Même format que le
// binaire engines/udp : les deux CLIs partagent les reçus.

const (
	productName      = "LABOSURF PRO"
	licensePrefix    = "LABOSURF"
	licenseLength    = 40
	activationWindow = 3 * time.Hour
	licenseAlphabet  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*_-+=?"
)

// embeddedVerifyKeyHex is the production public key.
// It can be overridden by LABOSURF_LICENSE_PUBKEY env var or labosurf_pub.key file.
var EmbeddedVerifyKeyHex = "7b27e59816d60f38a7299e226c714a3cb31a011f91f424099368506ded209595"

// Test keys - only used in tests
var (
	testSignKey   ed25519.PrivateKey
	testVerifyKey ed25519.PublicKey
)

var (
	ErrLicenseExpired = errors.New("fenêtre d'installation dépassée")
	ErrLicenseRevoked = errors.New("licence révoquée")
	ErrAlreadyUsed    = errors.New("licence déjà utilisée pour une installation")
	ErrNoReceipt      = errors.New("aucun reçu d'installation")
)

// InstallReceipt est la preuve qu'une licence a ouvert une installation.
// Même format que engines/udp : les reçus sont partagés entre les CLIs.
type InstallReceipt struct {
	LicenseID   string `json:"license_id"`
	InstalledAt string `json:"installed_at"`
	Key         string `json:"key,omitempty"`
}

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

// activationKeyPrefix identifie le format "clé d'activation" du jeton :
// LABOSURF-<payload en base32 groupé>@<signature en base32 groupée>.
// Remplace l'ancien base64url(payload).base64url(signature) — voir
// encodeActivationKey côté LABOSURF_LICENSE_MAKER (license.go) pour le
// producteur exact de ce format. Changement de PRÉSENTATION uniquement :
// les octets JSON signés et la vérification Ed25519 ci-dessous sont
// strictement inchangés.
const activationKeyPrefix = "LABOSURF-"

// encodeActivationKey et encodeKeyBlock ne sont pas utilisés par le
// chemin de vérification (LABOSURF_PRO ne génère pas de licences en
// production — voir README), mais sont conservés ici, identiques à
// LICENSE_MAKER/license.go, pour que les tests de ce paquet puissent
// construire des jetons valides sans dupliquer l'algorithme d'encodage
// dans license_test.go.
func encodeActivationKey(payload, signature []byte) string {
	return activationKeyPrefix + encodeKeyBlock(payload) + "@" + encodeKeyBlock(signature)
}

// EncodeActivationKey est l'export public d'encodeActivationKey, pour les
// tests d'autres packages (cmd/labosurf) qui doivent construire un jeton
// de test valide sans dupliquer l'algorithme d'encodage.
func EncodeActivationKey(payload, signature []byte) string {
	return encodeActivationKey(payload, signature)
}

func encodeKeyBlock(b []byte) string {
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	var out strings.Builder
	out.Grow(len(raw) + len(raw)/5)
	for i, r := range raw {
		if i > 0 && i%5 == 0 {
			out.WriteByte('-')
		}
		if (i/5)%2 == 1 {
			r = unicode.ToLower(r)
		}
		out.WriteRune(r)
	}
	return out.String()
}

// decodeKeyBlock inverse encodeKeyBlock (LICENSE_MAKER/license.go) :
// retire les tirets décoratifs, uniformise la casse (l'alternance de
// casse par bloc n'est que cosmétique) puis décode le base32.
func decodeKeyBlock(s string) ([]byte, error) {
	clean := strings.ToUpper(strings.ReplaceAll(s, "-", ""))
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clean)
}

// ParseLicenseToken decodes a license token without verifying its signature.
func ParseLicenseToken(token string) (LicenseData, []byte, error) {
	t := strings.TrimSpace(token)
	if len(t) <= len(activationKeyPrefix) || !strings.EqualFold(t[:len(activationKeyPrefix)], activationKeyPrefix) {
		return LicenseData{}, nil, fmt.Errorf("format de jeton invalide (préfixe %q attendu)", activationKeyPrefix)
	}

	blocks := strings.SplitN(t[len(activationKeyPrefix):], "@", 2)
	if len(blocks) != 2 {
		return LicenseData{}, nil, fmt.Errorf("format de jeton invalide (attendu: payload@signature)")
	}

	payload, err := decodeKeyBlock(blocks[0])
	if err != nil {
		return LicenseData{}, nil, fmt.Errorf("décodage payload : %w", err)
	}

	signature, err := decodeKeyBlock(blocks[1])
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

// getDataDir returns the data directory path
func getDataDir() string {
	if dir := os.Getenv("LABOSURF_DATA_DIR"); dir != "" {
		return dir
	}
	return "/etc/labosurf"
}

// receiptPathFor retourne le chemin du reçu pour un ID de licence.
func receiptPathFor(dir, licenseID string) string {
	if dir == "" {
		dir = getDataDir()
	}
	var b strings.Builder
	for _, r := range licenseID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	safe := b.String()
	if safe == "" {
		safe = "unknown"
	}
	return filepath.Join(dir, ".install_"+safe+".receipt")
}

// VerifyToken vérifie un jeton : signature + produit + fenêtre de 3h.
// Sans effet de bord : n'écrit aucun reçu.
func VerifyToken(token string) (LicenseData, error) {
	data, signature, err := ParseLicenseToken(token)
	if err != nil {
		return data, err
	}

	pub, err := resolveVerifyKey()
	if err != nil {
		return data, fmt.Errorf("clé publique de vérification : %w", err)
	}
	payloadBytes, err := canonicalPayload(data)
	if err != nil {
		return data, fmt.Errorf("sérialisation payload : %w", err)
	}
	if !verifySignature(payloadBytes, signature, pub) {
		return data, fmt.Errorf("signature Ed25519 invalide : licence altérée")
	}

	if data.Product != "LABOSURF PRO" {
		return data, fmt.Errorf("produit incompatible : %s", data.Product)
	}

	if data.ActivationUntil != "" {
		activationUntil, err := time.Parse(time.RFC3339, data.ActivationUntil)
		if err != nil {
			return data, fmt.Errorf("date d'activation invalide : %w", err)
		}
		if time.Now().UTC().After(activationUntil) {
			return data, ErrLicenseExpired
		}
	}

	return data, nil
}

// Activate utilise une licence pour autoriser UNE installation.
// 1 clé = 1 installation : un reçu local bloque toute réutilisation
// de la même clé sur cette machine.
func Activate(token string) error {
	data, err := VerifyToken(token)
	if err != nil {
		return err
	}

	path := receiptPathFor("", data.ID)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w : %s", ErrAlreadyUsed, data.ID)
	}

	rec := InstallReceipt{
		LicenseID:   data.ID,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Key:         data.Key,
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("sérialisation du reçu : %w", err)
	}
	if err := os.MkdirAll(getDataDir(), 0o755); err != nil {
		return fmt.Errorf("création dossier données : %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("écriture du reçu : %w", err)
	}

	fmt.Printf("✔ Licence %s acceptée : installation autorisée (1 clé = 1 installation).\n", data.ID)
	return nil
}

// Status affiche les reçus d'installation de cette machine.
func Status() error {
	dir := getDataDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Aucune installation (aucune licence utilisée sur cette machine).")
			return ErrNoReceipt
		}
		return fmt.Errorf("lecture du dossier %s : %w", dir, err)
	}

	found := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, ".install_") || !strings.HasSuffix(name, ".receipt") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var rec InstallReceipt
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		fmt.Printf("Licence     : %s\n", rec.LicenseID)
		fmt.Printf("Installée le: %s\n", rec.InstalledAt)
		fmt.Println()
		found = true
	}

	if !found {
		fmt.Println("Aucune installation (aucune licence utilisée sur cette machine).")
		return ErrNoReceipt
	}
	return nil
}

// Verify vérifie un jeton passé en argument, sans l'utiliser.
func Verify(token string) error {
	data, err := VerifyToken(token)
	if err != nil {
		return err
	}
	fmt.Printf("✔ Signature valide : licence %s utilisable pour UNE installation.\n", data.ID)
	return nil
}
