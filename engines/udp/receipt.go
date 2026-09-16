package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================
// REÇU D'INSTALLATION — Nouveau modèle de licence
// ============================================================
//
// La licence LABOSURF PRO ouvre l'ACCÈS AU SCRIPT D'INSTALLATION,
// pas au serveur. Une fois installé, le serveur tourne librement.
//
// Règles :
//   1 clé = 1 installation (usage unique strict)
//   + fenêtre de 3h (la clé meurt après ActivationUntil)
//   + signature Ed25519 valide
//
// Le reçu est un simple fichier local :
//   <receiptDir>/.install_<ID>.receipt
// Il empêche de réutiliser la même clé sur LA MÊME machine.
// Sur une machine neuve (réinstallation OS), seul le délai de 3h
// protège : passé ce délai, la clé est inutilisable partout.
//
// LIMITATION HONNÊTE : sans serveur central d'activation, l'unicité
// globale ne peut pas être garantie entre machines indépendantes.
// La fenêtre de 3h + le reçu local constituent la protection réelle.

const defaultReceiptDir = "/etc/labosurf"

// ErrAlreadyUsed indique qu'une licence a déjà ouvert une installation.
var ErrAlreadyUsed = errors.New("licence déjà utilisée pour une installation")

// ErrNoReceipt indique qu'aucun reçu d'installation n'existe localement.
var ErrNoReceipt = errors.New("aucun reçu d'installation")

// InstallReceipt est la preuve qu'une licence a ouvert une installation.
// Key (la clé de 40 caractères "LABOSURF...") est optionnelle et absente
// des reçus écrits avant le serveur central de licences — champ additif,
// rétrocompatible : un reçu sans Key fonctionne exactement comme avant
// (1 clé = 1 installation via le fichier reçu), il n'envoie simplement
// pas de heartbeat au serveur central.
type InstallReceipt struct {
	LicenseID   string `json:"license_id"`
	InstalledAt string `json:"installed_at"`
	Key         string `json:"key,omitempty"`
}

// receiptPathFor retourne le chemin du reçu pour un ID de licence.
// L'ID est assaini pour un usage sûr en nom de fichier.
func receiptPathFor(dir, licenseID string) string {
	if dir == "" {
		dir = defaultReceiptDir
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

// UseLicense utilise une licence pour autoriser UNE installation.
//
// Contrôles, dans l'ordre :
//  1. signature Ed25519 (clé publique) ;
//  2. produit LABOSURF PRO ;
//  3. fenêtre d'activation de 3h non dépassée ;
//  4. non révoquée (si un registre local est fourni) ;
//  5. usage unique : aucun reçu existant pour cet ID sur cette machine.
//
// En cas de succès, le reçu est écrit atomiquement.
func UseLicense(token, receiptDir string, registry *LicenseRegistry) (LicenseData, error) {
	data, _, err := VerifyLicenseToken(token)
	if err != nil {
		return data, err
	}

	// Fenêtre d'activation : la clé doit ouvrir l'installation avant
	// l'expiration de ActivationUntil.
	if data.ActivationUntil != "" {
		until, err := time.Parse(time.RFC3339, data.ActivationUntil)
		if err == nil && time.Now().UTC().After(until) {
			return data, ErrLicenseExpired
		}
	}

	// Révocation locale : l'administrateur peut bloquer un ID à la main.
	if registry != nil && registry.IsRevoked(data.ID) {
		return data, ErrLicenseRevoked
	}

	path := receiptPathFor(receiptDir, data.ID)
	if _, err := os.Stat(path); err == nil {
		return data, ErrAlreadyUsed
	}

	rec := InstallReceipt{
		LicenseID:   data.ID,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Key:         data.Key,
	}

	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return data, fmt.Errorf("sérialisation du reçu : %w", err)
	}

	if err := writeFileAtomic(path, raw, 0o600); err != nil {
		return data, err
	}

	// Marque la licence comme utilisée dans le registre local si fourni.
	if registry != nil {
		_ = registry.MarkUsed(data.ID)
	}

	return data, nil
}

// ListReceipts retourne les reçus d'installation présents dans le dossier.
func ListReceipts(receiptDir string) ([]InstallReceipt, error) {
	if receiptDir == "" {
		receiptDir = defaultReceiptDir
	}

	entries, err := os.ReadDir(receiptDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture du dossier %s : %w", receiptDir, err)
	}

	var out []InstallReceipt
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, ".install_") || !strings.HasSuffix(name, ".receipt") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(receiptDir, name))
		if err != nil {
			continue
		}
		var rec InstallReceipt
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}

	return out, nil
}

// ClearReceipts supprime tous les reçus d'installation du dossier.
// Permet une réinstallation propre avec une NOUVELLE licence.
// Note : cela ne rend PAS une ancienne clé réutilisable après ses 3h.
func ClearReceipts(receiptDir string) error {
	recs, err := ListReceipts(receiptDir)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return ErrNoReceipt
	}

	if receiptDir == "" {
		receiptDir = defaultReceiptDir
	}

	for _, r := range recs {
		_ = os.Remove(receiptPathFor(receiptDir, r.LicenseID))
	}

	return nil
}
