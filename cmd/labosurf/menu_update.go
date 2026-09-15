// Mise à jour du gestionnaire LABOSURF PRO (menu [8]).
//
// Vérifie la dernière release GitHub officielle du dépôt et propose son
// installation après confirmation explicite. Toute la logique métier
// (comparaison de versions, téléchargement, vérification SHA-256,
// remplacement atomique avec sauvegarde/rollback) vit dans
// internal/selfupdate, testée indépendamment de cet écran — ce fichier ne
// fait qu'appeler ces API et afficher le résultat dans le style existant.
//
// Portée : ce binaire (cmd/labosurf, "labosurf-mgr" en release) n'est pas
// installé par labosurf-pro.sh sur une installation VPS standard aujourd'hui
// (voir README.md, section « Limitation connue : gestionnaire
// multi-moteurs ») — cette fonctionnalité partage donc la même portée
// réduite que les écrans SERVICES/ACCÈS de M4.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"labosurf/internal/selfupdate"
)

func runUpdateMenu() {
	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⬆️  MISE À JOUR DU PROJET ────────────────────────────")
	fmt.Println()
	fmt.Println("  Version actuelle : " + green(version))
	fmt.Println()
	fmt.Println("  Recherche des mises à jour...")

	client := &http.Client{Timeout: 5 * time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	check, err := selfupdate.CheckForUpdate(ctx, client, selfupdate.DefaultRepo, version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		fmt.Println()
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	if !check.UpdateFound {
		fmt.Println()
		fmt.Println("  " + green("✔ Aucune nouvelle version disponible."))
		fmt.Println("  Version actuelle : " + version)
		pauseMenu()
		return
	}

	fmt.Println()
	fmt.Println("  " + green("★ Nouvelle version disponible !"))
	fmt.Println()
	fmt.Println("  Version actuelle : " + version)
	fmt.Println("  Nouvelle version : " + check.Release.TagName)
	fmt.Println()
	fmt.Println("  Informations de la release :")
	if !check.Release.PublishedAt.IsZero() {
		fmt.Println("    Date         : " + check.Release.PublishedAt.Format("2006-01-02 15:04 UTC"))
	}
	if notes := strings.TrimSpace(check.Release.Body); notes != "" {
		fmt.Println("    Notes principales :")
		for _, line := range strings.Split(notes, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fmt.Println("      " + line)
		}
	}
	fmt.Println("    Architecture : " + runtime.GOOS + "/" + runtime.GOARCH)
	fmt.Println()

	if !check.AssetFound {
		fmt.Println("  " + yellow("⚠ Aucune mise à jour compatible avec cette architecture."))
		pauseMenu()
		return
	}

	fmt.Printf("  Voulez-vous installer cette mise à jour ? (o/N) : ")
	if !selfupdate.ShouldInstall(promptLine("")) {
		fmt.Println("  Mise à jour annulée.")
		pauseMenu()
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Println("  " + red("✗ Impossible de localiser le binaire courant : "+err.Error()))
		pauseMenu()
		return
	}

	workDir, err := os.MkdirTemp("", "labosurf-update-*")
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	defer os.RemoveAll(workDir)

	fmt.Println()
	fmt.Println("  Téléchargement et vérification SHA-256...")

	downloadCtx, downloadCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer downloadCancel()

	downloaded, err := selfupdate.DownloadAndVerify(downloadCtx, client, check.Release, check.Asset, workDir)
	if err != nil {
		if errors.Is(err, selfupdate.ErrChecksumMismatch) {
			fmt.Println()
			fmt.Println("  " + red("✗ ERREUR : vérification SHA-256 échouée."))
			fmt.Println("  " + red("  La mise à jour est annulée."))
		} else {
			fmt.Println("  " + red("✗ "+err.Error()))
		}
		pauseMenu()
		return
	}

	fmt.Println("  " + green("✔ Intégrité SHA-256 vérifiée."))
	fmt.Println("  Installation (sauvegarde préalable de l'ancien binaire)...")

	res, err := selfupdate.Install(downloaded, exePath, nil)
	if err != nil {
		fmt.Println()
		fmt.Println("  " + red("✗ "+err.Error()))
		if res.BackupPath != "" {
			fmt.Println("  " + yellow("  Sauvegarde conservée : "+res.BackupPath))
		}
		pauseMenu()
		return
	}

	fmt.Println()
	fmt.Println("  " + green("✔ Mise à jour installée : "+check.Release.TagName))
	fmt.Println("  Ancien binaire sauvegardé : " + res.BackupPath)
	fmt.Println("  Relancez « menu » (ou reconnectez-vous en SSH) pour utiliser la nouvelle version.")
	pauseMenu()
}
