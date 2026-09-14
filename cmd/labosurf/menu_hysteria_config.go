// Assistant interactif de configuration du moteur Hysteria (natif LABOSURF).
// Permet de définir le port UDP, le backend SSH et applique la configuration
// JSON directement — ApplyServerConfig générait du YAML pour hysteria2 et
// ignorait le backend ; le moteur natif consomme du JSON avec {port, obfs,
// users, backend}.
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

// hysteriaStableObfs est le secret XOR d'obfuscation partagé client/serveur.
// Doit correspondre à la valeur dans les profils client distribués.
const hysteriaStableObfs = "labosurf-sal4mander-obfs-secret"

// menuHysteriaConfig est atteint par l'option [2] CONFIGURER du sous-menu hysteria.
func menuHysteriaConfig(e engine.Engine) {
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
	ep, _ := engcfg.Load(store.EngineHysteria)

	accounts := grantedAccounts(s, store.EngineHysteria)
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun compte rattaché au moteur hysteria."))
		fmt.Println("      Rattachez d'abord un compte via GESTION DES UTILISATEURS → [3].")
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⚙️ CONFIGURATION HYSTERIA (assistant) ─────────────")
	fmt.Println()
	fmt.Println("  Vides = valeurs actuelles (Entrée = garder).")
	fmt.Println()

	port := prof.Port(store.EngineHysteria)
	if port <= 0 {
		port = 8443
	}

	fmt.Println("  " + cyan("─ PORT D'ÉCOUTE ───────────────────────────────"))
	hint("Port UDP sur lequel le serveur Hysteria accepte les connexions.")
	hint("Ce port doit être ouvert en UDP dans le pare-feu du VPS.")
	fmt.Printf("  Port Hysteria [%d] : ", port)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 && n <= 65535 {
			port = n
		}
	}

	// Backend SSH
	fmt.Println()
	fmt.Println("  " + cyan("─ BACKEND SSH ───────────────────────────────────"))
	hint("Adresse du serveur SSH vers lequel Hysteria relaie le trafic déchiffré.")
	hint("En déploiement standard : 127.0.0.1:22 (sshd local).")
	hint("Pour chaîner avec un autre serveur : <ip>:<port>.")
	backend := ep.Get("backend", "127.0.0.1:22")
	fmt.Printf("  Backend SSH [%s] : ", backend)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		backend = v
	}

	fmt.Println()
	fmt.Println("  " + cyan("─ OBFUSCATION ───────────────────────────────────"))
	hint("Secret XOR partagé entre le serveur et les clients (géré automatiquement).")
	hint("Le moteur Hysteria natif utilise XOR, pas TLS — pas de certificat requis.")
	fmt.Printf("  Certificat TLS : non utilisé (moteur natif XOR)\n")
	fmt.Println("  Obfuscation    : XOR (secret interne fixe partagé avec les clients)")

	fmt.Println()
	fmt.Println("  " + cyan("─ COMPTES ───────────────────────────────────────"))
	if len(accounts) > 0 {
		for _, a := range accounts {
			hasPW := false
			if a.Grants != nil {
				if g := a.Grants[store.EngineHysteria]; g != nil {
					pw, _ := g.Config["password"].(string)
					hasPW = pw != "" || a.Password != ""
				}
			}
			if !hasPW {
				hasPW = a.Password != ""
			}
			status := dim("no password")
			if hasPW {
				status = green("✔ mot de passe")
			}
			fmt.Printf("    - %-14s %s\n", a.ID, status)
		}
	} else {
		fmt.Println("    (aucun)")
	}

	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Port        : %d (UDP)\n", port)
	fmt.Printf("  Backend SSH : %s\n", backend)
	fmt.Printf("  Obfs        : XOR (secret interne)\n")
	fmt.Printf("  Cert TLS    : non utilisé (%s/hysteria/ ignoré par ce moteur)\n", engineutil.DefaultDataDir)
	fmt.Printf("  Clients     : %d\n", len(accounts))
	fmt.Println()
	fmt.Println("  Appliquer cette configuration au moteur hysteria ? (o/N)")
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	savePort(&prof, store.EngineHysteria, port)

	// Génère les mots de passe manquants et construit la liste d'utilisateurs.
	type hysteriaUser struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Enabled  bool   `json:"enabled"`
	}
	var users []hysteriaUser
	for _, a := range accounts {
		acc, err := s.EnsureEngineSecrets(a.ID, store.EngineHysteria)
		if err != nil {
			fmt.Println("  " + yellow("⚠ Secrets non générés pour "+a.ID+": "+err.Error()))
			continue
		}
		pw := ""
		if acc.Grants != nil {
			if g := acc.Grants[store.EngineHysteria]; g != nil {
				pw, _ = g.Config["password"].(string)
			}
		}
		if pw == "" {
			pw = acc.Password
		}
		if pw == "" {
			continue
		}
		users = append(users, hysteriaUser{
			Name:     acc.ID,
			Password: pw,
			Enabled:  true,
		})
	}

	cfg := map[string]any{
		"port":    port,
		"obfs":    hysteriaStableObfs,
		"backend": backend,
		"users":   users,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")

	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		ep.Set("backend", backend)
		if err := engcfg.Save(ep); err != nil {
			fmt.Println("  " + dim("    (profil non persisté : "+err.Error()+")"))
		}
		fmt.Println("  " + green("✔ Configuration Hysteria appliquée."))
	}
	pauseMenu()
}

// grantedAccounts liste les comptes ayant un grant actif vers le moteur donné.
func grantedAccounts(s *store.Store, engineName string) []store.Account {
	var out []store.Account
	for _, a := range s.ListAccounts() {
		if a.HasEngine(engineName) {
			out = append(out, a)
		}
	}
	return out
}

// savePort persiste le port d'un moteur dans le profil serveur si modifié.
func savePort(prof *srvcfg.Profile, engineName string, port int) {
	if prof.Port(engineName) != port {
		prof.SetPort(engineName, port)
		if err := prof.Save(); err != nil {
			fmt.Println("  " + yellow("⚠ Port non persisté dans le profil : "+err.Error()))
		} else {
			fmt.Println("  " + dim("    Port "+engineName+" mis à jour dans le profil serveur."))
		}
	}
}

