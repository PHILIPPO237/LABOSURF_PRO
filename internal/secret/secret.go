// Package secret centralise la génération des secrets et identifiants
// nécessaires aux moteurs LABOSURF : UUID (Xray), secrets aléatoires,
// et paires de clés Ed25519 (dnstt/slowdns, licences).
//
// Toutes les fonctions sont pures (aucun I/O) et sans dépendance externe,
// ce qui les rend testables à l'unité.
package secret

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// UUID génère un identifiant UUID v4 aléatoire (canonique, en minuscules),
// utilisé comme ID client Xray (VLESS) notamment.
func UUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("uuid : %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// RandHex retourne n octets aléatoires encodés en hexadécimal.
func RandHex(n int) (string, error) {
	if n < 0 {
		n = 0
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("randHex : %w", err)
	}
	return hex.EncodeToString(b), nil
}

// RandToken retourne une chaîne lisible de nBytes octets aléatoires encodés
// en base64url (sans '=') — pratique pour des mots de passe/tokens d'accès.
func RandToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("randToken : %w", err)
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = alphabet[v>>3]
		out[i*2+1] = alphabet[v&0x1f]
	}
	return string(out), nil
}

// Ed25519Keypair retourne une paire de clés Ed25519 encodées en hexadécimal.
// pubHex = clé publique (ClientKeySize octets), privHex = clé privée (graine
// + clé publique, PrivateKeySize octets). Utilisable pour dnstt/slowdns.
func Ed25519Keypair() (pubHex, privHex string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("ed25519 : %w", err)
	}
	return hex.EncodeToString(pub), hex.EncodeToString(priv), nil
}

// Ed25519SecretKeyFromSeed construit une clé privée Ed25519 à partir d'une
// graine 32 octets (format PEM de dnstt : clé privée). Retourne l'hex.
func Ed25519SecretKeyFromSeed(seed []byte) string {
	return hex.EncodeToString(ed25519.NewKeyFromSeed(seed))
}

// PublicKeyHex dérive la clé publique (hex) d'une clé privée Ed25519 (hex).
// Utile pour extraire la clé publique d'une clé privée déployée.
func PublicKeyHex(privHex string) (string, error) {
	priv, err := hex.DecodeString(privHex)
	if err != nil {
		return "", fmt.Errorf("clé privée invalide : %w", err)
	}
	pub, ok := ed25519.PrivateKey(priv).Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("impossible d'obtenir la clé publique Ed25519")
	}
	return hex.EncodeToString(pub), nil
}