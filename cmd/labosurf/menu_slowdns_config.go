// Assistant interactif de configuration du moteur slowdns. Permet de choisir
// le domaine DNS délégué, le port (53), le backend SSH et le jitter anti-DPI,
// puis écrit la configuration JSON directement (sans passer par ApplyServerConfig
// dont le buildGroupedConfig hardcode jitter_ms=40 et n'expose pas le backend).
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

// menuSlowDNSConfig est atteint par l'option [2] CONFIGURER du sous-menu slowdns.
func menuSlowDNSConfig(e engine.Engine) {
	menuDNSTunnelConfig(e, store.EngineSlowDNS, "SLOWDNS")
}

// menuDNSTunnelConfig est le formulaire commun aux moteurs de tunnel DNS
// (slowdns, dnstt). L'opérateur choisit le domaine délégué, le port (53),
// le backend SSH et le jitter anti-DPI, puis écrit la config JSON directement
// afin que les valeurs saisies soient réellement appliquées au moteur.
func menuDNSTunnelConfig(e engine.Engine, engineName, label string) {
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
	ep, _ := engcfg.Load(engineName)

	accounts := grantedAccounts(s, engineName)
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun compte rattaché au moteur "+engineName+"."))
		fmt.Println("      Rattachez d'abord un compte via GESTION DES UTILISATEURS → [3].")
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Printf("  ── ⚙️ CONFIGURATION %s (assistant) ──────────────────\n", label)
	fmt.Println()
	fmt.Println("  Vides = valeurs actuelles (Entrée = garder).")
	fmt.Println()

	// Domaine DNS délégué
	fmt.Println("  " + cyan("─ DOMAINE DNS DÉLÉGUÉ ─────────────────────────"))
	hint("Sous-domaine délégué à ce VPS via NS record : les requêtes DNS")
	hint("sur tunnel.mondomaine.com sont routées vers le VPS comme serveur DNS.")
	hint("Ce domaine DOIT avoir un enregistrement NS pointant sur ce VPS.")
	domain := firstDomainOrHost(prof)
	if domain == "" {
		domain = "tunnel.mondomaine.com"
	}
	domain = pickDomain(prof, domain)
	fmt.Printf("  Domaine NS [%s] : ", domain)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		domain = v
	}

	// Port d'écoute (normalement 53)
	fmt.Println()
	fmt.Println("  " + cyan("─ PORT DNS ─────────────────────────────────────"))
	hint("Port d'écoute UDP du serveur DNS (53 = standard, requis pour DPI bypass).")
	hint("Le port 53 nécessite des droits root ou cap_net_bind_service.")
	port := prof.Port(engineName)
	if port <= 0 {
		port = 53
	}
	fmt.Printf("  Port DNS [%d] : ", port)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 && n <= 65535 {
			port = n
		}
	}

	// Backend SSH
	fmt.Println()
	fmt.Println("  " + cyan("─ BACKEND SSH ───────────────────────────────────"))
	hint("Adresse du serveur SSH vers lequel le trafic déchiffré est relayé.")
	hint("En déploiement standard : 127.0.0.1:22 (sshd local).")
	hint("Pour chaîner avec un autre serveur : <ip>:<port>.")
	backend := ep.Get("backend", "127.0.0.1:22")
	fmt.Printf("  Backend SSH [%s] : ", backend)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		backend = v
	}

	// Jitter anti-DPI
	fmt.Println()
	fmt.Println("  " + cyan("─ JITTER ANTI-DPI ──────────────────────────────"))
	hint("Délai aléatoire (ms) ajouté aux réponses DNS pour brouiller l'empreinte.")
	hint("Recommandé : 40 ms (latence supplémentaire ~20 ms moyenne, discrétion ++).")
	jitter := ep.GetInt("jitter_ms", 40)
	fmt.Printf("  Jitter ms [%d] : ", jitter)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n >= 0 {
			jitter = n
		}
	}

	// Récapitulatif
	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Moteur      : %s\n", engineName)
	fmt.Printf("  Domaine NS  : %s\n", domain)
	fmt.Printf("  Port DNS    : %d (UDP)\n", port)
	fmt.Printf("  Backend SSH : %s\n", backend)
	fmt.Printf("  Jitter      : %d ms\n", jitter)
	fmt.Printf("  Clients     : %d\n", len(accounts))
	fmt.Println()
	fmt.Printf("  Appliquer cette configuration au moteur %s ? (o/N)\n", engineName)
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	savePort(&prof, engineName, port)

	// Persiste le domaine dans le profil.
	if len(prof.Domains) == 0 {
		prof.Domains = []string{domain}
		_ = prof.Save()
	} else if prof.Domains[0] != domain {
		newDomains := []string{domain}
		for _, d := range prof.Domains {
			if d != domain {
				newDomains = append(newDomains, d)
			}
		}
		prof.Domains = newDomains
		if err := prof.Save(); err != nil {
			fmt.Println("  " + yellow("⚠ Domaine non persisté dans le profil : "+err.Error()))
		}
	}

	// Génère les paires de clés ed25519 manquantes et construit la liste d'utilisateurs.
	type dnsUser struct {
		User       string `json:"user"`
		PublicKey  string `json:"public_key"`
		PrivateKey string `json:"private_key"`
		Enabled    bool   `json:"enabled"`
	}
	var users []dnsUser
	for _, a := range accounts {
		acc, err := s.EnsureEngineSecrets(a.ID, engineName)
		if err != nil {
			fmt.Println("  " + yellow("⚠ Secrets non générés pour "+a.ID+": "+err.Error()))
			continue
		}
		pubKey := ""
		privKey := ""
		if acc.Grants != nil {
			if g := acc.Grants[engineName]; g != nil {
				pubKey, _ = g.Config["public_key"].(string)
				privKey, _ = g.Config["private_key"].(string)
			}
		}
		users = append(users, dnsUser{
			User:       acc.ID,
			PublicKey:  pubKey,
			PrivateKey: privKey,
			Enabled:    true,
		})
	}

	cfg := map[string]any{
		"domain":    domain,
		"port":      port,
		"backend":   backend,
		"jitter_ms": jitter,
		"users":     users,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")

	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		ep.Set("backend", backend)
		ep.SetInt("jitter_ms", jitter)
		if err := engcfg.Save(ep); err != nil {
			fmt.Println("  " + dim("    (profil non persisté : "+err.Error()+")"))
		}
		fmt.Printf("  "+green("✔ Configuration %s appliquée.")+"\n", label)
	}
	pauseMenu()
}
