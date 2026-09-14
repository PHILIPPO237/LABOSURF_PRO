// Assistant interactif de configuration du moteur freeway-gate (reverse
// proxy CONNECT multi-opérateur zero-rating). Remplace le simple « chemin de
// fichier JSON » par un formulaire guidé qui gère : l'écoute du proxy, les
// origines HTTP internes par opérateur (ports cibles default 80=mtn,
// 81=orange — les ports d'origine servis derrière le CONNECT), l'opérateur
// par défaut et la limite de rate. Les blocs non gérés ici (hosts, headers,
// ua_models, chains) sont préservés tels quels depuis la config courante.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"labosurf/engines/freewaygate"
	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
)

// menuFreewayConfig est atteint par l'option [2] CONFIGURER du sous-menu
// freeway-gate (routé dans runSingleEngineMenu).
func menuFreewayConfig(e engine.Engine) {
	raw, err := loadFreewayConfigRaw()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		fmt.Println("  " + red("✗ config freeway-gate invalide : "+err.Error()))
		pauseMenu()
		return
	}
	profiles, ok := cfg["profiles"].(map[string]any)
	if !ok || len(profiles) == 0 {
		fmt.Println("  " + red("✗ config sans profils opérateurs."))
		pauseMenu()
		return
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⚙️ CONFIGURATION FREEWAY-GATE (reverse proxy) ──")
	fmt.Println()
	fmt.Println("  Vides = valeurs actuelles (Entre).")
	fmt.Println()

	// Écoute du proxy CONNECT.
	fmt.Println("  " + cyan("─ ÉCOUTE DU PROXY ─────────────────────────────"))
	hint("Adresse:port du proxy CONNECT multi-opérateur (zero-rating).")
	listen := str(cfg["listen"])
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	fmt.Printf("  Écoute (adresse:port) [%s] : ", listen)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		listen = v
	}
	cfg["listen"] = listen

	// Origines HTTP par opérateur (ports cibles 80/81 par défaut).
	fmt.Println()
	fmt.Println("  " + cyan("─ ORIGINES HTTP PAR OPÉRATEUR ──────────────────"))
	hint("Chaque opérateur zero-rating relaie vers une origine HTTP interne")
	hint("sur le VPS (ex. 80 = portail MTN, 81 = portail Orange).")
	targets := make(map[string]int, len(profiles))
	ops := make([]string, 0, len(profiles))
	for op, profVal := range profiles {
		if _, ok := profVal.(map[string]any); ok {
			ops = append(ops, op)
		}
	}
	sort.Strings(ops)
	for _, op := range ops {
		prof, _ := profiles[op].(map[string]any)
		curTarget := str(prof["target"])
		curPort := targetPortOf(curTarget, defaultTargetPort(op))
		fmt.Println()
		fmt.Printf("  Opérateur : %s\n", op)
		hint("Port de l'origine HTTP servie derrière le CONNECT.")
		fmt.Printf("  Port target %-8s [%d] : ", op, curPort)
		if v := strings.TrimSpace(promptLine("")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
				curPort = n
			}
		}
		host := targetHostOf(curTarget, "127.0.0.1")
		prof["target"] = fmt.Sprintf("http://%s:%d", host, curPort)
		targets[op] = curPort
	}

	// Opérateur par défaut.
	fmt.Println()
	fmt.Println("  " + cyan("─ OPÉRATEUR PAR DÉFAUT ─────────────────────────"))
	hint("Profil appliqué quand aucun hôte ne matche un opérateur.")
	defaultOp := str(cfg["default_operator"])
	if defaultOp == "" {
		defaultOp = "mtn"
	}
	for i, op := range ops {
		mark := "  "
		if op == defaultOp {
			mark = green("▶")
		}
		fmt.Printf("    %s %d) %s\n", mark, i+1, op)
	}
	fmt.Printf("  Opérateur par défaut [%s] : ", defaultOp)
	if v := strings.TrimSpace(promptLine("")); v != "" && containsString(ops, v) {
		defaultOp = v
	}
	cfg["default_operator"] = defaultOp

	// Limite de rate.
	fmt.Println()
	fmt.Println("  " + cyan("─ LIMITE DE RATE ───────────────────────────────"))
	hint("Limite globale de requêtes (rate_limit_max).")
	rate := intFromRaw(cfg["rate_limit_max"], 300)
	fmt.Printf("  rate_limit_max [%d] : ", rate)
	if v := strings.TrimSpace(promptLine("")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rate = n
		}
	}
	cfg["rate_limit_max"] = rate

	// Avertissement de collision de ports avec d'autres moteurs / l'écoute.
	listenPort := portOnly(listen)
	warned := false
	for _, op := range ops {
		p := targets[op]
		if p == listenPort {
			fmt.Println("  " + yellow("⚠ "+op+": le port target "+itoa(p)+" = port d'écoute du proxy."))
			warned = true
		}
	}
	if !warned {
		if prof, err := srvcfg.Load(); err == nil {
			for _, n := range engine.Names() {
				ep := prof.Port(n)
				for _, op := range ops {
					if targets[op] == ep && ep > 0 {
						fmt.Println("  " + yellow("⚠ "+op+": le port target "+itoa(ep)+" est aussi le port moteur "+n+"."))
					}
				}
			}
		}
	}

	// Récapitulatif.
	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Écoute         : %s\n", listen)
	for _, op := range ops {
		host := targetHostOf(str(profiles[op].(map[string]any)["target"]), "127.0.0.1")
		fmt.Printf("  %-14s : http://%s:%d\n", "target "+op, host, targets[op])
	}
	fmt.Printf("  Opérateur déf. : %s\n", defaultOp)
	fmt.Printf("  rate_limit_max : %d\n", rate)
	fmt.Println()
	fmt.Println("  Appliquer cette configuration à freeway-gate ? (o/N)")
	if !strings.EqualFold(strings.TrimSpace(promptLine("")), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Configuration freeway-gate appliquée."))
	}
	pauseMenu()
}

// loadFreewayConfigRaw retourne la config disque du moteur, ou la config par
// défaut embarquée si le fichier n'existe pas encore.
func loadFreewayConfigRaw() ([]byte, error) {
	path := filepath.Join(engineutil.DefaultDataDir, "freeway-gate", "config.json")
	raw, err := os.ReadFile(path)
	if err == nil {
		return raw, nil
	}
	if os.IsNotExist(err) {
		return []byte(freewaygate.DefaultConfigJSON), nil
	}
	return nil, err
}

// defaultTargetPort donne le port par défaut de l'origine d'un opérateur
// (sans config antérieure : 80 = mtn, 81 = orange — convention freeway-gate).
func defaultTargetPort(op string) int {
	if strings.EqualFold(op, "orange") {
		return 81
	}
	return 80
}

// targetPortOf extrait le port de "http://host:port" (défaut sinon).
func targetPortOf(target string, def int) int {
	if i := strings.LastIndexByte(target, ':'); i >= 0 {
		if n, err := strconv.Atoi(target[i+1:]); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// targetHostOf extrait l'hôte de "http://host:port" (défaut sinon).
func targetHostOf(target, def string) string {
	t := strings.TrimPrefix(target, "http://")
	if strings.Contains(t, ":") {
		t = t[:strings.LastIndexByte(t, ':')]
	}
	if t == "" {
		return def
	}
	return t
}

// portOnly extrait le port de "host:port" (0 sinon).
func portOnly(addr string) int {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(addr[i+1:])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case fmt.Stringer:
		return s.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", s)
	}
}

func intFromRaw(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		if nv, err := strconv.Atoi(n); err == nil {
			return nv
		}
	}
	return def
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
