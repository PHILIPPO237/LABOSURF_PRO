// Panneau d'informations système affiché sous le header central.
// LABOSURF PRO étant destiné à tourner sur un VPS Linux, les valeurs sont
// collectées depuis /proc et les commandes système usuelles. Tout accès
// défaillant retombe sur "N/D" sans bloquer le menu.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"labosurf/internal/engine"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// ansiEscapeRE reconnaît les séquences SGR (couleurs) ANSI.
var ansiEscapeRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visibleLen retourne la longueur affichée d'une chaîne, sans compter les
// octets des séquences ANSI de couleur.
func visibleLen(s string) int {
	return len([]rune(ansiEscapeRE.ReplaceAllString(s, "")))
}

// readOSInfo retourne le nom du système d'exploitation (ex. Debian 12).
func readOSInfo() string {
	if raw, err := os.ReadFile("/etc/os-release"); err == nil {
		var name, version string
		for _, line := range strings.Split(string(raw), "\n") {
			if v, ok := osReleaseValue(line, "PRETTY_NAME"); ok {
				return v
			}
			if v, ok := osReleaseValue(line, "NAME"); ok {
				name = v
			}
			if v, ok := osReleaseValue(line, "VERSION_ID"); ok {
				version = v
			}
		}
		if name != "" {
			if version != "" {
				return name + " " + version
			}
			return name
		}
	}
	return "N/D"
}

// osReleaseValue extrait la valeur d'une ligne KEY=VALUE d'un fichier os-release.
func osReleaseValue(line, key string) (string, bool) {
	if !strings.HasPrefix(line, key+"=") {
		return "", false
	}
	value := strings.TrimPrefix(line, key+"=")
	value = strings.ReplaceAll(value, "\"", "")
	if value != "" {
		return value, true
	}
	return "", false
}

// readKernel retourne la version du noyau.
func readKernel() string {
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "N/D"
}

// readCPUBrand retourne le modèle de la première unité CPU (fallback "N/D").
func readCPUBrand() string {
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "N/D"
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "model name") {
			if i := strings.Index(line, ":"); i >= 0 {
				v := strings.TrimSpace(line[i+1:])
				if v != "" {
					return truncateEllipsis(v, 24)
				}
			}
		}
	}
	return "N/D"
}

// readRAM retourne l'usage mémoire "utilisé/total Go".
func readRAM() string {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "N/D"
	}
	var total, available uint64
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			total, _ = strconv.ParseUint(f[1], 10, 64)
		}
		if len(f) >= 2 && f[0] == "MemAvailable:" {
			available, _ = strconv.ParseUint(f[1], 10, 64)
		}
	}
	if total == 0 {
		return "N/D"
	}
	used := total
	if available < total {
		used = total - available
	}
	return fmt.Sprintf("%d/%d Go", used/1024/1024, total/1024/1024)
}

// readSwap retourne l'usage swap "utilisé/total Go".
func readSwap() string {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "N/D"
	}
	var total, free uint64
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "SwapTotal:" {
			total, _ = strconv.ParseUint(f[1], 10, 64)
		}
		if len(f) >= 2 && f[0] == "SwapFree:" {
			free, _ = strconv.ParseUint(f[1], 10, 64)
		}
	}
	if total == 0 {
		return "N/D"
	}
	used := total - free
	return fmt.Sprintf("%d/%d Mo", used/1024, total/1024)
}

// readDisk retourne l'usage du disque racine "utilisé/total Go" via `df`.
func readDisk() string {
	if out, err := exec.Command("df", "-k", "/").Output(); err == nil {
		parts := strings.Fields(string(out))
		if len(parts) >= 4 {
			total, err := strconv.ParseUint(parts[1], 10, 64)
			if err == nil && total > 0 {
				used, _ := strconv.ParseUint(parts[2], 10, 64)
				return fmt.Sprintf("%d/%d Go", used/1024/1024, total/1024/1024)
			}
		}
	}
	return "N/D"
}

// readUptime retourne l'uptime (jours/heures).
func readUptime() string {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "N/D"
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return "N/D"
	}
	sec, _ := strconv.ParseUint(fields[0], 10, 64)
	days := sec / 86400
	hours := (sec % 86400) / 3600
	if days > 0 {
		return fmt.Sprintf("%d j %d h", days, hours)
	}
	return fmt.Sprintf("%d h", hours)
}

// readLoad retourne la charge CPU moyenne (1/5/15 min).
func readLoad() string {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "N/D"
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return "N/D"
	}
	return strings.Join(fields[:3], " ")
}

// detectPublicIP déduit l'IP/IPv6 publique via la table de routage (best effort).
func detectPublicIP() string {
	if out, err := exec.Command("ip", "route", "get", "8.8.8.8").Output(); err == nil {
		parts := strings.Fields(string(out))
		for i, part := range parts {
			if part == "src" && i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	if out, err := exec.Command("hostname", "-I").Output(); err == nil {
		if fields := strings.Fields(string(out)); len(fields) > 0 {
			return fields[0]
		}
	}
	return "N/D"
}

// readActiveConns compte les connexions réseau ESTABLISHED (best effort).
func readActiveConns() string {
	if out, err := exec.Command("ss", "-tun", "state", "established").Output(); err == nil {
		n := 0
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "ESTAB") {
				n++
			}
		}
		return strconv.Itoa(n)
	}
	return "N/D"
}

// accountSummary retourne les totaux de comptes (total / actifs / expirés) du store.
func accountSummary() (total, active, expired int) {
	s, err := store.LoadStore(store.StorePath())
	if err != nil {
		return 0, 0, 0
	}
	now := time.Now().UTC()
	for _, a := range s.ListAccounts() {
		total++
		if a.Enabled {
			active++
		}
		if a.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, a.ExpiresAt); err == nil && now.After(t) {
				expired++
			}
		}
	}
	return total, active, expired
}

// licenseActivated indique si cette machine a un reçu d'activation de
// licence local (voir internal/license : 1 clé = 1 installation).
func licenseActivated() bool {
	dir := os.Getenv("LABOSURF_DATA_DIR")
	if dir == "" {
		dir = "/etc/labosurf"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, ".install_") && strings.HasSuffix(n, ".receipt") {
			return true
		}
	}
	return false
}

// networkAddress retourne l'adresse serveur configurée, ou détecte l'IP.
func networkAddress() string {
	prof, err := srvcfg.Load()
	if err == nil && prof.Host != "" {
		return prof.Host
	}
	return detectPublicIP()
}

// truncateEllipsis coupe une chaîne à n runes et ajoute "…" si tronquée.
// Utilisé pour les valeurs système à longueur imprévisible (ex. modèle CPU)
// afin de préserver la mise en page en colonnes fixes du panneau.
func truncateEllipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// padRight complète une chaîne à largeur fixe (en colonnes visibles, pas en
// octets bruts — une chaîne colorée ANSI ne doit jamais être tronquée en
// plein milieu d'une séquence d'échappement, ce qui casserait l'affichage).
func padRight(s string, width int) string {
	vis := visibleLen(s)
	if vis >= width {
		return s
	}
	return s + strings.Repeat(" ", width-vis)
}

// printSystemPanel affiche le panneau d'informations système + comptes +
// moteurs + ports sous le header central sur 4 colonnes compactes.
// Robustesse : tout échec d'accès renvoie "N/D" sans bloquer.
func printSystemPanel() {
	prof, _ := srvcfg.Load()
	total, active, expired := accountSummary()

	// Largeur de colonne pour 4 colonnes dans ~78 colonnes.
	const cw = 18 // 2 indent + 4×18 + 3 séparateurs = 77

	// Ligne de statut rapide (comptes/licence/connexions), à la manière
	// d'un tableau de bord : puces colorées, un coup d'œil suffit.
	licenceState := red("● non activée")
	if licenseActivated() {
		licenceState = green("● activée")
	}
	expiredState := dim(itoa(expired))
	if expired > 0 {
		expiredState = yellow(itoa(expired))
	}
	fmt.Println()
	fmt.Println(
		"  " + green("●") + " Comptes " + itoa(total) +
			"   " + yellow("●") + " Expirés " + expiredState +
			"   " + cyan("●") + " Connexions " + readActiveConns() +
			"   " + "Licence " + licenceState,
	)

	fmt.Println(dim("  ── SYSTÈME ─────────────────────────────────────────"))

	// 8 items système → 2 lignes de 4 colonnes.
	sysItems := []struct{ label, value string; ok bool }{
		{"OS", readOSInfo(), true},
		{"Noyau", readKernel(), true},
		{"CPU", itoa(runtime.NumCPU())+"c " + runtime.GOARCH, true},
		{"Modele", readCPUBrand(), true},
		{"RAM", readRAM(), true},
		{"Swap", readSwap(), false},
		{"Disk", readDisk(), true},
		{"Load", readLoad(), true},
	}
	for row := 0; row < 2; row++ {
		var cells []string
		for col := 0; col < 4; col++ {
			it := sysItems[row*4+col]
			cell := padRight(dim(it.label+" ")+it.value, cw)
			cells = append(cells, cell)
		}
		fmt.Println("  " + strings.Join(cells, " "))
	}

	fmt.Println(dim("  ── RÉSEAU & UTILISATEURS ───────────────────────────"))

	// 8 items réseau → 2 lignes de 4 colonnes.
	netItems := []struct{ label, value string; ok bool }{
		{"IP", networkAddress(), true},
		{"Uptime", readUptime(), true},
		{"Comptes", itoa(total), true},
		{"Actifs", itoa(active), true},
		{"Conn.", readActiveConns(), false},
		{"Arch", runtime.GOARCH, true},
		{"Version", "PRO", true},
		{"Ports", itoa(countSrvPorts(prof)), true},
	}
	for row := 0; row < 2; row++ {
		var cells []string
		for col := 0; col < 4; col++ {
			it := netItems[row*4+col]
			cell := padRight(dim(it.label+" ")+it.value, cw)
			cells = append(cells, cell)
		}
		fmt.Println("  " + strings.Join(cells, " "))
	}

	fmt.Println(dim("  ── MOTEURS & PORTS ─────────────────────────────────"))
	names := engine.Names()
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Println("  Aucun moteur enregistré.")
	} else {
		// 3 colonnes par ligne pour les moteurs.
		for i := 0; i < len(names); i += 3 {
			var cells []string
			for j := 0; j < 3; j++ {
				if i+j >= len(names) {
					break
				}
				n := names[i+j]
				e, err := engine.Get(n)
				if err != nil {
					continue
				}
				st := e.Status()
				state := "·"
				colored := false
				switch {
				case st.Running:
					state = green("●")
					colored = true
				case st.Installed:
					state = dim("○")
				}
				cell := fmt.Sprintf("%-7s %s v%-5s p%d", n, state, e.Version(), prof.Port(n))
				if colored {
					cell = green(n) + " " + state + fmt.Sprintf(" v%-5s p%d", e.Version(), prof.Port(n))
				} else {
					cell = dim(n) + " " + state + fmt.Sprintf(" v%-5s p%d", e.Version(), prof.Port(n))
				}
				cells = append(cells, padRight(cell, 25))
			}
			fmt.Println("  " + strings.Join(cells, " "))
		}
	}
	fmt.Println()
}

// countSrvPorts compte les ports explicitement configurés dans le profil.
func countSrvPorts(prof srvcfg.Profile) int {
	n := 0
	if prof.Ports != nil {
		for _, v := range prof.Ports {
			if v > 0 {
				n++
			}
		}
	}
	return n
}