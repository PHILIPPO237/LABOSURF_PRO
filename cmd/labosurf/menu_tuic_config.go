// Assistant interactif de configuration du moteur TUIC. TUIC utilise QUIC/TLS
// avec authentification UUID+password — configure port, algorithme de
// congestion et génère la configuration pour tous les comptes rattachés.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"labosurf/internal/engcfg"
	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// menuTUICConfig est atteint par l'option [2] CONFIGURER du sous-menu tuic.
func menuTUICConfig(e engine.Engine) {
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
	ep, _ := engcfg.Load(store.EngineTUIC)

	accounts := grantedAccounts(s, store.EngineTUIC)
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun compte rattaché au moteur tuic."))
		fmt.Println("      Rattachez d'abord un compte via GESTION DES UTILISATEURS → [3].")
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⚙️ CONFIGURATION TUIC (assistant) ─────────────────")
	fmt.Println()
	fmt.Println("  Vides = valeurs actuelles (Entrée = garder).")
	fmt.Println()

	port := prof.Port(store.EngineTUIC)
	if port <= 0 {
		port = 443
	}

	fmt.Println("  " + cyan("─ PORT D'ÉCOUTE ───────────────────────────────"))
	hint("Port UDP sur lequel tuic-server accepte les connexions QUIC/TLS.")
	hint("443/UDP est la convention : passe souvent les pare-feux d'opérateur.")
	fmt.Printf("  Port TUIC [%d] : ", port)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 && n <= 65535 {
			port = n
		}
	}

	// Congestion control
	fmt.Println()
	fmt.Println("  " + cyan("─ CONTRÔLE DE CONGESTION ──────────────────────"))
	hint("cubic : algorithme standard TCP, stable sur réseaux stables.")
	hint("bbr   : algorithme Google, meilleur débit sur liens à forte latence")
	hint("        (réseaux mobiles africains, bypass opérateur).")
	congestion := ep.Get("congestion_control", "cubic")
	echoCongestion(congestion)
	fmt.Printf("  Algorithme (cubic/bbr) [%s] : ", congestion)
	if v := strings.ToLower(strings.TrimSpace(promptLine(""))); v == "bbr" || v == "cubic" {
		congestion = v
	}

	fmt.Println()
	fmt.Println("  " + cyan("─ SÉCURITÉ TLS ─────────────────────────────────"))
	hint("TLS : certificat auto-signé généré par tuic-server à l'installation.")
	hint("Le client doit activer allow_insecure=1 (pas de CA publique requise).")
	fmt.Printf("  Certificat : %s/tuic/cert.pem (auto-signé)\n", engineutil.DefaultDataDir)
	fmt.Printf("  ALPN       : h3\n")

	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Port        : %d (UDP/QUIC)\n", port)
	fmt.Printf("  Congestion  : %s\n", congestion)
	fmt.Printf("  ALPN        : h3\n")
	fmt.Printf("  TLS         : auto-signé (cert dans %s/tuic/)\n", engineutil.DefaultDataDir)
	fmt.Printf("  Zero-RTT    : désactivé (sécurité anti-replay)\n")
	fmt.Printf("  Clients     : %d\n", len(accounts))
	fmt.Println()
	fmt.Println("  Appliquer cette configuration au moteur tuic ? (o/N)")
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	savePort(&prof, store.EngineTUIC, port)

	// Génère les secrets UUID/password pour chaque compte, construit le JSON.
	users := map[string]any{}
	for _, a := range accounts {
		acc, err := s.EnsureEngineSecrets(a.ID, store.EngineTUIC)
		if err != nil {
			fmt.Println("  " + yellow("⚠ Secrets non générés pour "+a.ID+": "+err.Error()))
			continue
		}
		uuid := tuicGrant(acc, "uuid")
		pw := tuicGrant(acc, "password")
		if pw == "" {
			pw = acc.Password
		}
		if uuid != "" {
			users[uuid] = pw
		}
	}

	cfg := map[string]any{
		"server":             fmt.Sprintf("[::]:%d", port),
		"users":              users,
		"congestion_control": congestion,
		"alpn":               []string{"h3"},
		"zero_rtt_handshake": false,
		"udp_relay_ipv6":     true,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")

	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		ep.Set("congestion_control", congestion)
		if err := engcfg.Save(ep); err != nil {
			fmt.Println("  " + dim("    (profil non persisté : "+err.Error()+")"))
		}
		fmt.Println("  " + green("✔ Configuration TUIC appliquée."))
	}
	pauseMenu()
}

func tuicGrant(a store.Account, key string) string {
	if a.Grants == nil {
		return ""
	}
	g := a.Grants[store.EngineTUIC]
	if g == nil {
		return ""
	}
	v, _ := g.Config[key].(string)
	return v
}


func echoCongestion(current string) {
	for _, s := range []string{"cubic", "bbr"} {
		mark := "  "
		if s == current {
			mark = green("▶")
		}
		fmt.Printf("    %s %s\n", mark, s)
	}
}
