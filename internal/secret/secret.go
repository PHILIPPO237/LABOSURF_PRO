// Package secret centralise la génération des secrets et identifiants
// nécessaires aux moteurs LABOSURF : UUID (Xray), secrets aléatoires,
// paires de clés Ed25519 (dnstt/slowdns, licences) et paires de clés
// Curve25519/X25519 (WireGuard).
//
// Toutes les fonctions sont pures (aucun I/O). Sans dépendance externe à
// l'exception de golang.org/x/crypto/curve25519 (X25519Keypair) — déjà une
// dépendance du module racine (utilisée par engines/ssh), pas une nouvelle
// entrée go.mod.
package secret

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/curve25519"
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

// X25519Keypair génère une paire de clés Curve25519 (X25519), utilisée par
// WireGuard — clé privée/publique 32 octets, encodées en base64 STANDARD
// (convention WireGuard officielle, différente du hex utilisé par
// Ed25519Keypair ci-dessus). Le "clamping" appliqué à la clé privée suit
// RFC 7748 §5 (identique à la convention WireGuard officielle : wg genkey
// applique le même clamping avant dérivation).
func X25519Keypair() (privBase64, pubBase64 string, err error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return "", "", fmt.Errorf("x25519 : %w", err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("x25519 dérivation clé publique : %w", err)
	}
	return base64.StdEncoding.EncodeToString(priv[:]), base64.StdEncoding.EncodeToString(pub), nil
}

// X25519PublicFromPrivate dérive la clé publique WireGuard (base64) d'une
// clé privée WireGuard (base64) — équivalent de `wg pubkey`, en Go pur.
func X25519PublicFromPrivate(privBase64 string) (string, error) {
	priv, err := base64.StdEncoding.DecodeString(privBase64)
	if err != nil || len(priv) != 32 {
		return "", fmt.Errorf("clé privée WireGuard invalide (attendu 32 octets en base64)")
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("x25519 dérivation clé publique : %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), nil
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