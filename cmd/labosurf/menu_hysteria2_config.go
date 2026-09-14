// Assistant interactif de configuration du moteur Hysteria2 (officiel).
// Hysteria2 utilise QUIC+TLS, une obfuscation salamander persistante et une
// authentification userpass — configure port UDP, puis applique la config
// groupée à tous les comptes rattachés.
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"labosurf/internal/clientcfg"
	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// menuHysteria2Config est atteint par l'option [2] CONFIGURER du sous-menu hysteria2.
func menuHysteria2Config(e engine.Engine) {
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

	accounts := grantedAccounts(s, store.EngineHysteria2)
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun compte rattaché au moteur hysteria2."))
		fmt.Println("      Rattachez d'abord un compte via GESTION DES UTILISATEURS → [3].")
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⚙️ CONFIGURATION HYSTERIA2 (assistant) ────────────")
	fmt.Println()
	fmt.Println("  Vides = valeurs actuelles (Entrée = garder).")
	fmt.Println()

	port := prof.Port(store.EngineHysteria2)
	if port <= 0 {
		port = 443
	}

	fmt.Println("  " + cyan("─ PORT D'ÉCOUTE ───────────────────────────────"))
	hint("Port UDP sur lequel Hysteria2 accepte les connexions QUIC/TLS.")
	hint("443/UDP est la convention officielle ; ouvert en UDP dans le pare-feu.")
	fmt.Printf("  Port Hysteria2 [%d] : ", port)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 && n <= 65535 {
			port = n
		}
	}

	fmt.Println()
	fmt.Println("  " + cyan("─ SÉCURITÉ & OBFUSCATION ──────────────────────"))
	hint("TLS : certificat auto-signé (EnsureHysteria2Certs à l'installation).")
	hint("Obfs : salamander, secret auto-généré et persisté à l'installation.")
	hint("Auth : userpass — un mot de passe par compte rattaché à hysteria2.")
	fmt.Printf("  Certificat : %s/hysteria2/cert.pem (auto-signé)\n", engineutil.DefaultDataDir)
	fmt.Println("  Obfuscation : salamander (secret persistant — généré une fois)")

	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Port        : %d (UDP/QUIC)\n", port)
	fmt.Printf("  TLS         : auto-signé (cert dans %s/hysteria2/)\n", engineutil.DefaultDataDir)
	fmt.Printf("  Obfs        : salamander (secret auto)\n")
	fmt.Printf("  Masquerade  : proxy → https://www.bing.com\n")
	fmt.Printf("  Clients     : %d\n", len(accounts))
	fmt.Println()
	fmt.Println("  Appliquer cette configuration au moteur hysteria2 ? (o/N)")
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	savePort(&prof, store.EngineHysteria2, port)

	if err := clientcfg.ApplyServerConfig(context.Background(), s, store.EngineHysteria2, prof); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Configuration Hysteria2 appliquée."))
	}
	pauseMenu()
}
