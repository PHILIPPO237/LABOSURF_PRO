// Assistant interactif de configuration du moteur UDP (natif LABOSURF PRO).
// Permet de configurer le port d'écoute, le portail HTTP de gestion et le
// mode d'authentification, puis écrit le fichier de configuration JSON.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"labosurf/internal/engcfg"
	"labosurf/internal/engine"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// menuUDPConfig est atteint par l'option [2] CONFIGURER du sous-menu udp.
func menuUDPConfig(e engine.Engine) {
	prof, err := srvcfg.Load()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	ep, _ := engcfg.Load(store.EngineUDP)

	accounts := grantedAccounts(s, store.EngineUDP)
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun compte rattaché au moteur udp."))
		fmt.Println("      Rattachez d'abord un compte via GESTION DES UTILISATEURS → [3].")
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⚙️ CONFIGURATION UDP (assistant) ──────────────────")
	fmt.Println()
	fmt.Println("  Vides = valeurs actuelles (Entrée = garder).")
	fmt.Println()

	port := prof.Port(store.EngineUDP)
	if port <= 0 {
		port = 5667
	}

	// Port d'écoute
	fmt.Println("  " + cyan("─ PORT D'ÉCOUTE ───────────────────────────────"))
	hint("Port UDP principal sur lequel le moteur reçoit les clients.")
	fmt.Printf("  Port UDP [%d] : ", port)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 && n <= 65535 {
			port = n
		}
	}

	// Portail HTTP
	fmt.Println()
	fmt.Println("  " + cyan("─ PORTAIL HTTP ─────────────────────────────────"))
	hint("Le portail HTTP expose une API de gestion locale (désactivé par défaut).")
	hint("Activez-le uniquement sur une adresse locale (127.0.0.1) — jamais public.")
	portalEnabled := ep.GetBool("portal_enabled", false)
	portalListen := ep.Get("portal_listen", "127.0.0.1:8080")
	defaultPortalStr := "N"
	if portalEnabled {
		defaultPortalStr = "O"
	}
	fmt.Printf("  Activer le portail HTTP ? (o/N) [%s] : ", defaultPortalStr)
	if v := strings.ToLower(strings.TrimSpace(promptLine(""))); v == "o" {
		portalEnabled = true
		fmt.Printf("  Adresse portail [%s] : ", portalListen)
		if v2 := strings.TrimSpace(promptLine("")); v2 != "" {
			portalListen = v2
		}
	} else if v == "n" {
		portalEnabled = false
	}

	// Mode d'authentification
	fmt.Println()
	fmt.Println("  " + cyan("─ AUTHENTIFICATION ─────────────────────────────"))
	hint("store      : le moteur lit les comptes du store central LABOSURF.")
	hint("passwords  : les mots de passe sont listés explicitement dans le JSON.")
	authMode := ep.Get("auth_mode", "store")
	echoAuthMode(authMode)
	fmt.Printf("  Mode auth (store/passwords) [%s] : ", authMode)
	if v := strings.ToLower(strings.TrimSpace(promptLine(""))); v == "store" || v == "passwords" {
		authMode = v
	}

	// Récapitulatif
	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Port UDP    : %d\n", port)
	fmt.Printf("  Portail     : ")
	if portalEnabled {
		fmt.Printf("%s (actif)\n", portalListen)
	} else {
		fmt.Println("désactivé")
	}
	fmt.Printf("  Auth mode   : %s\n", authMode)
	fmt.Printf("  Clients     : %d\n", len(accounts))
	fmt.Println()
	fmt.Println("  Appliquer cette configuration au moteur udp ? (o/N)")
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	savePort(&prof, store.EngineUDP, port)

	cfg := map[string]any{
		"listen": fmt.Sprintf(":%d", port),
		"store":  store.StorePath(),
		"portal": map[string]any{
			"enabled": portalEnabled,
			"listen":  portalListen,
		},
		"auth": map[string]any{
			"mode":  authMode,
			"users": map[string]any{},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")

	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		ep.SetBool("portal_enabled", portalEnabled)
		ep.Set("portal_listen", portalListen)
		ep.Set("auth_mode", authMode)
		if err := engcfg.Save(ep); err != nil {
			fmt.Println("  " + dim("    (profil non persisté : "+err.Error()+")"))
		}
		fmt.Println("  " + green("✔ Configuration UDP appliquée."))
	}
	pauseMenu()
}

func echoAuthMode(current string) {
	for _, s := range []string{"store", "passwords"} {
		mark := "  "
		if s == current {
			mark = green("▶")
		}
		fmt.Printf("    %s %s\n", mark, s)
	}
}
