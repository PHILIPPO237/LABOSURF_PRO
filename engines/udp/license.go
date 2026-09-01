package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// ============================================================
// SYSTÈME DE LICENCE LABOSURF PRO — Cryptographie asymétrique
// ============================================================
//
// Le License Maker possède la clé PRIVÉE.
// LABOSURF PRO possède uniquement la clé PUBLIQUE.
//
// Format du jeton :
//   base64url(payload JSON).base64url(signature Ed25519)
//
// Le payload signé contient une clé LABOSURF de exactement 40 caractères.
// La fenêtre de 3 heures concerne uniquement la PREMIÈRE activation.
// Après activation réussie, l'activation reste valide sur le VPS lié.
//

const (
	productName      = "LABOSURF PRO"
	activationWindow = 3 * time.Hour

	licensePrefix   = "LABOSURF"
	licenseLength   = 40
	licenseAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*_-+=?"
)

var (
	ErrLicenseInvalid    = errors.New("licence invalide")
	ErrLicenseExpired    = errors.New("licence expirée")
	ErrLicenseRevoked    = errors.New("licence révoquée")
	ErrLicenseTampered   = errors.New("licence altérée (signature invalide)")
	ErrNoSigningKey      = errors.New("clé privée de signature absente (réservé à l'administrateur)")
	ErrNoVerifyKey       = errors.New("clé publique de vérification absente")
	ErrLicenseFormat     = errors.New("format de licence invalide")
	ErrAlreadyActivated  = errors.New("licence déjà activée")
	ErrWrongDevice       = errors.New("licence activée sur un autre appareil")
	ErrActivationMissing = errors.New("aucune activation enregistrée")
)

// LicenseStatus décrit l'état d'une licence.
type LicenseStatus string

const (
	LicenseNew      LicenseStatus = "NEW"
	LicenseActive   LicenseStatus = "ACTIVE"
	LicenseExpired  LicenseStatus = "EXPIRED"
	LicenseRevoked  LicenseStatus = "REVOKED"
	LicenseTampered LicenseStatus = "TAMPERED"
	LicenseUnknown  LicenseStatus = "UNKNOWN"
)

// LicenseData représente exactement les données signées.
// Ce schéma doit rester identique dans le License Maker.
type LicenseData struct {
	ID              string `json:"id"`
	Key             string `json:"key"`
	IssuedAt        string `json:"issued_at"`
	ActivationUntil string `json:"activation_until"`
	Product         string `json:"product"`
	Comment         string `json:"comment,omitempty"`
}

// License est un jeton signé.
type License struct {
	Data      LicenseData
	Signature []byte
}

// embeddedVerifyKeyHex est la clé PUBLIQUE de production.
// Elle peut être injectée à la compilation ou remplacée par la clé publique
// fournie dans labosurf_pub.key.
var embeddedVerifyKeyHex = ""

// Clés injectées uniquement par les tests.
var (
	testSignKey   ed25519.PrivateKey
	testVerifyKey ed25519.PublicKey
)

// ---------- Résolution des clés ----------

func resolveVerifyKey() (ed25519.PublicKey, error) {
	if testVerifyKey != nil {
		return testVerifyKey, nil
	}

	if v := strings.TrimSpace(os.Getenv("LABOSURF_LICENSE_PUBKEY")); v != "" {
		return decodePublicKey(v)
	}

	for _, path := range []string{"labosurf_pub.key", ".labosurf_pub.key"} {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return decodePublicKey(strings.TrimSpace(string(raw)))
		}
	}

	if embeddedVerifyKeyHex != "" {
		return decodePublicKey(embeddedVerifyKeyHex)
	}

	return nil, ErrNoVerifyKey
}

// Réservé aux tests/outils administrateur.
// Le binaire client ne doit pas embarquer de clé privée.
func resolveSignKey() (ed25519.PrivateKey, error) {
	if testSignKey != nil {
		return testSignKey, nil
	}

	if v := strings.TrimSpace(os.Getenv("LABOSURF_LICENSE_PRIVKEY")); v != "" {
		return decodePrivateKey(v)
	}

	for _, path := range []string{"labosurf_admin.key", ".labosurf_admin.key"} {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return decodePrivateKey(strings.TrimSpace(string(raw)))
		}
	}

	return nil, ErrNoSigningKey
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

func decodePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("clé privée invalide : %w", err)
	}

	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf(
			"clé privée : taille %d attendue %d",
			len(b),
			ed25519.PrivateKeySize,
		)
	}

	return ed25519.PrivateKey(b), nil
}

// GenerateKeyPair est conservé pour les tests/outils administrateur.
// La clé privée ne doit jamais être distribuée avec LABOSURF PRO.
func GenerateKeyPair() (privHex string, pubHex string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("génération de clés : %w", err)
	}

	return hex.EncodeToString(priv), hex.EncodeToString(pub), nil
}

// ---------- Clé LABOSURF de 40 caractères ----------

func validateLicenseKey(key string) bool {
	if len(key) != licenseLength {
		return false
	}

	if !strings.HasPrefix(key, licensePrefix) {
		return false
	}

	for _, c := range key[len(licensePrefix):] {
		if !strings.ContainsRune(licenseAlphabet, c) {
			return false
		}
	}

	return true
}

// ---------- Signature & vérification ----------

func canonicalPayload(data LicenseData) ([]byte, error) {
	return json.Marshal(data)
}

// CreateLicense est conservé pour les tests/outils administrateur.
// La génération officielle doit être effectuée par le License Maker privé.
func CreateLicense(id, comment string) (string, License, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", License{}, ErrInvalidID
	}

	priv, err := resolveSignKey()
	if err != nil {
		return "", License{}, err
	}

	keyBytes := make([]byte, licenseLength-len(licensePrefix))
	if _, err := rand.Read(keyBytes); err != nil {
		return "", License{}, fmt.Errorf("génération de clé : %w", err)
	}

	var keyBuilder strings.Builder
	keyBuilder.Grow(licenseLength)
	keyBuilder.WriteString(licensePrefix)

	for _, v := range keyBytes {
		keyBuilder.WriteByte(licenseAlphabet[int(v)%len(licenseAlphabet)])
	}

	now := time.Now().UTC()

	data := LicenseData{
		ID:              id,
		Key:             keyBuilder.String(),
		IssuedAt:        now.Format(time.RFC3339),
		ActivationUntil: now.Add(activationWindow).Format(time.RFC3339),
		Product:         productName,
		Comment:         strings.TrimSpace(comment),
	}

	payload, err := canonicalPayload(data)
	if err != nil {
		return "", License{}, err
	}

	signature := ed25519.Sign(priv, payload)

	token := base64.RawURLEncoding.EncodeToString(payload) +
		"." +
		base64.RawURLEncoding.EncodeToString(signature)

	return token, License{
		Data:      data,
		Signature: signature,
	}, nil
}

// ParseLicenseToken décode un jeton sans vérifier sa signature.
func ParseLicenseToken(token string) (License, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return License{}, ErrLicenseFormat
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return License{}, fmt.Errorf("%w : payload", ErrLicenseFormat)
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return License{}, fmt.Errorf("%w : signature", ErrLicenseFormat)
	}

	var data LicenseData
	if err := json.Unmarshal(payload, &data); err != nil {
		return License{}, fmt.Errorf("%w : json", ErrLicenseFormat)
	}

	if !validateLicenseKey(data.Key) {
		return License{}, ErrLicenseInvalid
	}

	return License{
		Data:      data,
		Signature: signature,
	}, nil
}

func verifySignature(lic License, pub ed25519.PublicKey) bool {
	payload, err := canonicalPayload(lic.Data)
	if err != nil {
		return false
	}

	return ed25519.Verify(pub, payload, lic.Signature)
}

// VerifyLicenseToken vérifie un jeton : format + signature.
// La fenêtre ActivationUntil est volontairement vérifiée lors de
// la première activation, pas ici.
func VerifyLicenseToken(token string) (LicenseData, LicenseStatus, error) {
	lic, err := ParseLicenseToken(token)
	if err != nil {
		return LicenseData{}, LicenseUnknown, err
	}

	return VerifyLicense(lic)
}

// VerifyLicense vérifie une licence déjà décodée.
//
// Important : ActivationUntil n'est PAS une date d'expiration permanente.
// Elle indique seulement jusqu'à quand la licence peut être activée pour
// la première fois. Une licence déjà activée reste valide sur son VPS.
func VerifyLicense(lic License) (LicenseData, LicenseStatus, error) {
	pub, err := resolveVerifyKey()
	if err != nil {
		return lic.Data, LicenseUnknown, err
	}

	if !validateLicenseKey(lic.Data.Key) {
		return lic.Data, LicenseUnknown, ErrLicenseInvalid
	}

	if lic.Data.Product != productName {
		return lic.Data, LicenseUnknown, ErrLicenseInvalid
	}

	if !verifySignature(lic, pub) {
		return lic.Data, LicenseTampered, ErrLicenseTampered
	}

	return lic.Data, LicenseActive, nil
}
