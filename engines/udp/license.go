package main

import (
	"crypto/ed25519"
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
// MODÈLE DE SÉCURITÉ (Ed25519) :
//
//	OUTIL ADMIN                       CLIENT LABOSURF PRO
//	    │                                    │
//	    │ clé PRIVÉE (jamais distribuée)     │ clé PUBLIQUE (non secrète)
//	    ▼                                    ▼
//	signature de licence  ───────────►  vérification de licence
//
// - Seul l'administrateur possède la clé privée (fichier local, hors binaire).
// - Le client ne possède QUE la clé publique : il peut vérifier une licence
//   mais ne peut JAMAIS en fabriquer une nouvelle.
// - Aucune clé privée de production n'est embarquée dans le binaire client.
// - Aucune clé déterministe/secours de production n'existe.
//
// LIMITATION : ce système empêche la fabrication de fausses licences et
// leur altération. Il NE protège PAS contre la rétro-ingénierie du binaire
// ni contre le partage volontaire d'une même installation.

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

// LicenseData représente les données signées d'une licence.
// Ces champs sont couverts par la signature Ed25519.
type LicenseData struct {
	ID              string `json:"id"`
	IssuedAt        string `json:"issued_at"`
	ExpiresAt       string `json:"expires_at"`
	ActivationUntil string `json:"activation_until"`
	Product         string `json:"product"`
	MaxUsers        int    `json:"max_users"`
	Comment         string `json:"comment,omitempty"`
}

// License est un jeton de licence signé.
//
// Format textuel distribué au client :
//
//	<base64url(payload_json)>.<base64url(signature)>
type License struct {
	Data      LicenseData
	Signature []byte
}

// ---------- Résolution des clés ----------

// embeddedVerifyKeyHex est la clé PUBLIQUE de vérification embarquée.
// Vide par défaut : l'administrateur configure sa propre clé publique via
// le fichier labosurf_pub.key ou la variable LABOSURF_LICENSE_PUBKEY après
// avoir généré sa paire de clés. Une clé PUBLIQUE n'est pas un secret.
var embeddedVerifyKeyHex = ""

// testKeys permet aux tests d'injecter une paire de clés sans toucher au
// système de fichiers ni aux variables d'environnement.
var (
	testSignKey   ed25519.PrivateKey
	testVerifyKey ed25519.PublicKey
)

// resolveVerifyKey retourne la clé publique de vérification.
// Priorité : 1) injection de test, 2) env, 3) fichier, 4) embarquée.
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

// resolveSignKey retourne la clé privée de signature (RÉSERVÉ ADMIN).
// Priorité : 1) injection de test, 2) env, 3) fichier admin.
// Il n'existe AUCUNE clé privée embarquée ni de secours.
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
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("clé publique invalide : %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("clé publique : taille %d attendue %d", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

func decodePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("clé privée invalide : %w", err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("clé privée : taille %d attendue %d", len(b), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(b), nil
}

// GenerateKeyPair génère une nouvelle paire de clés Ed25519.
// Retourne (clé privée hex, clé publique hex).
//
// L'administrateur conserve la clé privée en lieu sûr (jamais distribuée)
// et publie la clé publique auprès des clients.
func GenerateKeyPair() (privHex string, pubHex string, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", fmt.Errorf("génération de clés : %w", err)
	}
	return hex.EncodeToString(priv), hex.EncodeToString(pub), nil
}

// ---------- Signature & vérification ----------

// canonicalPayload sérialise les données de licence de façon déterministe
// pour la signature.
func canonicalPayload(data LicenseData) ([]byte, error) {
	return json.Marshal(data)
}

// CreateLicense génère et signe une nouvelle licence (RÉSERVÉ ADMIN).
// Nécessite la clé privée. Le résultat est un jeton texte distribuable.
func CreateLicense(id string, durationDays int, maxUsers int, comment string) (string, License, error) {
	if strings.TrimSpace(id) == "" {
		return "", License{}, ErrInvalidID
	}
	if durationDays < 0 {
		return "", License{}, errors.New("durée invalide")
	}

	priv, err := resolveSignKey()
	if err != nil {
		return "", License{}, err
	}

	now := time.Now().UTC()
	expires := ""
	if durationDays > 0 {
		expires = now.Add(time.Duration(durationDays) * 24 * time.Hour).Format(time.RFC3339)
	}
	// La fenêtre de remise/activation est courte (3 h). Une fois activée,
	// cette échéance n'est plus consultée par ActivationStore.Check.
	activationUntil := now.Add(3 * time.Hour).Format(time.RFC3339)

	data := LicenseData{
		ID:              id,
		IssuedAt:        now.Format(time.RFC3339),
		ExpiresAt:       expires,
		ActivationUntil: activationUntil,
		Product:         "LABOSURF PRO",
		MaxUsers:        maxUsers,
		Comment:         comment,
	}

	payload, err := canonicalPayload(data)
	if err != nil {
		return "", License{}, err
	}

	sig := ed25519.Sign(priv, payload)

	token := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)

	return token, License{Data: data, Signature: sig}, nil
}

// ParseLicenseToken décode un jeton texte en License (sans vérifier la
// signature).
func ParseLicenseToken(token string) (License, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return License{}, ErrLicenseFormat
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return License{}, fmt.Errorf("%w : payload", ErrLicenseFormat)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return License{}, fmt.Errorf("%w : signature", ErrLicenseFormat)
	}

	var data LicenseData
	if err := json.Unmarshal(payload, &data); err != nil {
		return License{}, fmt.Errorf("%w : json", ErrLicenseFormat)
	}

	return License{Data: data, Signature: sig}, nil
}

// verifySignature vérifie la signature Ed25519 d'une licence avec la clé
// publique. Recalcule le payload canonique à partir des données.
func verifySignature(lic License, pub ed25519.PublicKey) bool {
	payload, err := canonicalPayload(lic.Data)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, payload, lic.Signature)
}

// VerifyLicenseToken vérifie un jeton texte : signature + expiration.
// Retourne les données et le statut. C'est le point d'entrée CLIENT.
func VerifyLicenseToken(token string) (LicenseData, LicenseStatus, error) {
	lic, err := ParseLicenseToken(token)
	if err != nil {
		return LicenseData{}, LicenseUnknown, err
	}
	return VerifyLicense(lic)
}

// VerifyLicense vérifie une licence déjà décodée (signature + expiration).
func VerifyLicense(lic License) (LicenseData, LicenseStatus, error) {
	pub, err := resolveVerifyKey()
	if err != nil {
		return lic.Data, LicenseUnknown, err
	}

	if !verifySignature(lic, pub) {
		return lic.Data, LicenseTampered, ErrLicenseTampered
	}

	if lic.Data.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, lic.Data.ExpiresAt)
		if err != nil {
			// Date illisible => on considère la licence invalide par prudence.
			return lic.Data, LicenseExpired, ErrLicenseExpired
		}
		if time.Now().After(exp) {
			return lic.Data, LicenseExpired, ErrLicenseExpired
		}
	}

	return lic.Data, LicenseActive, nil
}
