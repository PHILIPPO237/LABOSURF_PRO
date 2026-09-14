// Assistant interactif de configuration du moteur WireGuard. Permet de définir
// le port UDP et génère la configuration wg-quick (format INI) avec tous les
// comptes rattachés comme pairs. Le forwarding/NAT reste désactivé par défaut.
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"labosurf/internal/clientcfg"
	"labosurf/internal/engine"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// menuWireGuardConfig est atteint par l'option [2] CONFIGURER du sous-menu wireguard.
func menuWireGuardConfig(e engine.Engine) {
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

	accounts := grantedAccounts(s, store.EngineWireGuard)
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun compte rattaché au moteur wireguard."))
		fmt.Println("      Rattachez d'abord un compte via GESTION DES UTILISATEURS → [3].")
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⚙️ CONFIGURATION WIREGUARD (assistant) ────────────")
	fmt.Println()
	fmt.Println("  Vides = valeurs actuelles (Entrée = garder).")
	fmt.Println()

	port := prof.Port(store.EngineWireGuard)
	if port <= 0 {
		port = 51820
	}

	fmt.Println("  " + cyan("─ PORT D'ÉCOUTE ───────────────────────────────"))
	hint("Port UDP sur lequel le serveur WireGuard accepte les connexions.")
	hint("51820 est la convention WireGuard ; le module noyau l'exige ouvert.")
	fmt.Printf("  Port WireGuard [%d] : ", port)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 && n <= 65535 {
			port = n
		}
	}

	fmt.Println()
	fmt.Println("  " + cyan("─ RÉSEAU VPN ────────────────────────────────────"))
	hint("Adresse serveur  : 10.66.0.1/24 (réservée au serveur, fixe).")
	hint("Adresses clients : 10.66.0.2+/32 (générées à l'ajout de compte).")
	hint("DNS clients      : 1.1.1.1 (configuré dans chaque fichier .conf client).")
	fmt.Println("  Adresse serveur : 10.66.0.1/24 (fixe)")
	fmt.Println("  DNS clients     : 1.1.1.1")

	fmt.Println()
	fmt.Println("  " + cyan("─ CLÉS ──────────────────────────────────────────"))
	hint("La paire de clés serveur est générée une fois à l'installation")
	hint("(wg genkey/pubkey), puis persistée — elle ne change pas ici.")
	hint("Les clés des comptes sont générées à leur création.")
	fmt.Println("  Clés serveur : persistées (générées à l'installation)")

	fmt.Println()
	fmt.Println("  " + cyan("─ FORWARDING / NAT ─────────────────────────────"))
	hint("LABOSURF PRO ne modifie jamais le routage global de la machine")
	hint("sans action explicite de l'opérateur. Les règles PostUp/PostDown")
	hint("iptables restent en commentaire dans la configuration générée —")
	hint("décommentez-les si vous voulez que les clients WireGuard sortent")
	hint("vers Internet via ce serveur (accès NAT complet).")
	fmt.Println("  Forwarding/NAT : désactivé (commenté — décommenter si NAT désiré)")

	fmt.Println()
	fmt.Println("  " + cyan("─ PAIRS (CLIENTS) ──────────────────────────────"))
	if len(accounts) > 0 {
		for _, a := range accounts {
			pubKey := ""
			if a.Grants != nil {
				if g := a.Grants[store.EngineWireGuard]; g != nil {
					pubKey, _ = g.Config["public_key"].(string)
				}
			}
			status := dim("no key")
			if pubKey != "" {
				status = green("✔ clé publique")
			}
			fmt.Printf("    - %-14s %s\n", a.ID, status)
		}
	} else {
		fmt.Println("    (aucun pair enregistré)")
	}

	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Port        : %d (UDP)\n", port)
	fmt.Printf("  Réseau      : 10.66.0.0/24\n")
	fmt.Printf("  Pairs       : %d\n", len(accounts))
	fmt.Println()
	fmt.Println("  Appliquer cette configuration au moteur wireguard ? (o/N)")
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	savePort(&prof, store.EngineWireGuard, port)

	// Génère les secrets (paires de clés) pour les comptes qui n'en ont pas.
	for _, a := range accounts {
		if _, err := s.EnsureEngineSecrets(a.ID, store.EngineWireGuard); err != nil {
			fmt.Println("  " + yellow("⚠ Secrets non générés pour "+a.ID+": "+err.Error()))
		}
	}

	if err := clientcfg.ApplyServerConfig(context.Background(), s, store.EngineWireGuard, prof); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Configuration WireGuard appliquée."))
	}
	pauseMenu()
}
