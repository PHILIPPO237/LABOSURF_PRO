// Assistant interactif de configuration du moteur SSH. Permet de définir le
// port d'écoute et l'utilisateur système de drop-privilege, puis écrit la
// configuration JSON directement (le run_as_user était absent de
// buildGroupedConfig et donc jamais appliqué).
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

// menuSSHConfig est atteint par l'option [2] CONFIGURER du sous-menu ssh.
func menuSSHConfig(e engine.Engine) {
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
	ep, _ := engcfg.Load(store.EngineSSH)

	accounts := grantedAccounts(s, store.EngineSSH)
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun compte rattaché au moteur ssh."))
		fmt.Println("      Rattachez d'abord un compte via GESTION DES UTILISATEURS → [3].")
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⚙️ CONFIGURATION SSH (assistant) ──────────────────")
	fmt.Println()
	fmt.Println("  Vides = valeurs actuelles (Entrée = garder).")
	fmt.Println()

	port := prof.Port(store.EngineSSH)
	if port <= 0 {
		port = 22
	}

	fmt.Println("  " + cyan("─ PORT D'ÉCOUTE ───────────────────────────────"))
	hint("Port TCP du serveur SSH LABOSURF (22 = standard, ou non-standard")
	hint("pour limiter les scans automatisés — 22222, 2222, etc.).")
	fmt.Printf("  Port SSH [%d] : ", port)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 && n <= 65535 {
			port = n
		}
	}

	// Utilisateur système de drop-privilege
	fmt.Println()
	fmt.Println("  " + cyan("─ UTILISATEUR SYSTÈME ──────────────────────────"))
	hint("Compte Linux non-root vers lequel les sessions SSH droppent leurs")
	hint("privilèges après l'authentification par clé. Par défaut : labosurf.")
	hint("Doit exister sur le système (créé par labosurf-pro.sh).")
	runAsUser := ep.Get("run_as_user", "labosurf")
	fmt.Printf("  Utilisateur système [%s] : ", runAsUser)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		runAsUser = v
	}

	fmt.Println()
	fmt.Println("  " + cyan("─ SÉCURITÉ ──────────────────────────────────────"))
	hint("Authentification : clés Ed25519 uniquement (pas de mot de passe).")
	hint("Les clés publiques des comptes rattachés sont écrites dans authorized_keys.")
	fmt.Println("  Auth mode   : authorized_keys (clés Ed25519 uniquement)")
	fmt.Println("  Root login  : interdit (PermitRootLogin no)")
	fmt.Println("  Passwords   : interdits (PasswordAuthentication no)")

	fmt.Println()
	fmt.Println("  " + cyan("─ COMPTES SSH ──────────────────────────────────"))
	if len(accounts) > 0 {
		for _, a := range accounts {
			key := ""
			if a.Grants != nil {
				if g := a.Grants[store.EngineSSH]; g != nil {
					if v, ok := g.Config["public_key"].(string); ok {
						key = v[:sshKeyPreviewLen(v)] + "..."
					}
				}
			}
			status := dim("no key")
			if key != "" {
				status = green("✔ clé présente")
			}
			fmt.Printf("    - %-14s %s\n", a.ID, status)
		}
	} else {
		fmt.Println("    (aucun)")
	}

	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Port        : %d (TCP)\n", port)
	fmt.Printf("  Auth        : authorized_keys\n")
	fmt.Printf("  Run-as user : %s\n", runAsUser)
	fmt.Printf("  Clients     : %d\n", len(accounts))
	fmt.Println()
	fmt.Println("  Appliquer cette configuration au moteur ssh ? (o/N)")
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	savePort(&prof, store.EngineSSH, port)

	// Génère les paires de clés ed25519 manquantes et construit la liste d'utilisateurs.
	type sshUser struct {
		Username  string `json:"username"`
		PublicKey string `json:"public_key"`
		Enabled   bool   `json:"enabled"`
	}
	var users []sshUser
	for _, a := range accounts {
		acc, err := s.EnsureEngineSecrets(a.ID, store.EngineSSH)
		if err != nil {
			fmt.Println("  " + yellow("⚠ Secrets non générés pour "+a.ID+": "+err.Error()))
			continue
		}
		pubKey := ""
		if acc.Grants != nil {
			if g := acc.Grants[store.EngineSSH]; g != nil {
				pubKey, _ = g.Config["public_key"].(string)
			}
		}
		users = append(users, sshUser{
			Username:  acc.ID,
			PublicKey: pubKey,
			Enabled:   true,
		})
	}

	cfg := map[string]any{
		"port":        port,
		"dir":         "/etc/labosurf/ssh",
		"run_as_user": runAsUser,
		"users":       users,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")

	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		ep.Set("run_as_user", runAsUser)
		if err := engcfg.Save(ep); err != nil {
			fmt.Println("  " + dim("    (profil non persisté : "+err.Error()+")"))
		}
		fmt.Println("  " + green("✔ Configuration SSH appliquée."))
	}
	pauseMenu()
}

func sshKeyPreviewLen(v string) int {
	if len(v) > 16 {
		return 16
	}
	return len(v)
}
