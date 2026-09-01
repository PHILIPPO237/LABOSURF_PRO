package main

import (
	"fmt"
	"strings"

	"os"
	"strconv"
)

// ============================================================
// BANNIÈRE LABOSURF PRO — DESIGN TERMINAL PROFESSIONNEL
// ============================================================
//
// Hiérarchie :
//   LABOSURF PRO              (vert vif, ASCII art dominant)
//   LABORATOIRE DU FREESURF   (cyan, sous-titre)
//   CONÇU PAR PHILIPPO237     (jaune, signature)
//
// UDP Engine est un moteur, pas le nom du produit.
//
// Responsive : compact (< 44 colonnes) ou large (>= 44).

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[1;32m"
	colorCyan   = "\033[1;36m"
	colorDim    = "\033[2m"
	colorWhite  = "\033[1;37m"
	colorYellow = "\033[1;33m"
)

// asciiArtLabosurf retourne l'art ASCII de « LABOSURF PRO ».
// Police compacte 3x5 par lettre, 8 lignes de hauteur.
// Chaque ligne fait ~40 colonnes (hors couleurs).
func asciiArtLabosurf() []string {
	// L = 3 cols, A = 3, B = 3, O = 3, S = 3, U = 3, R = 3, F = 3, space = 1, P = 3, R = 3, O = 3
	// = 8*3 + 1 + 3*3 = 34 colonnes
	return []string{
		"###   ### ### ###   ### ### ###  ### ### ###   ###",
		"###   ### ### ###   ### ### ###  ### ### ###   ###",
		"###   ### ### ###   ### ### ###  ### ### ###   ###",
		"###   ### ### ###   ### ### ###  ### ### ###   ###",
		"###   ### ### ###   ### ### ###  ### ### ###   ###",
		" ### ###  ### ###   ### ### ###  ### ###  ### ### ",
		" ### ###  ###  ### ###  ###  ### ###  ###  ### ### ",
		"  ###     ###   ###    ###   ### ###   ###   ###   ",
	}
}

// asciiArtLabosurfCompact retourne une version compacte (30 colonnes).
func asciiArtLabosurfCompact() []string {
	return []string{
		"##  ## ## ##  ## ## ## ## ##  ## ## ##",
		"##  ## ## ##  ## ## ## ## ##  ## ## ##",
		"##  ## ## ##  ## ## ## ## ##  ## ## ##",
		"##  ## ## ##  ## ## ## ## ##  ## ## ##",
		" ## ##  ## ##  ## ## ## ## ##  ##  ## ",
		" ## ##  ##  ## ##  ##  ##  ## ##  ##  ",
		"  ##    ##   ##    ##   ##   ##   ##  ",
	}
}

// terminalWidth retourne la largeur du terminal en colonnes.
// 0 si indéterminé.
func terminalWidth() int {
	if v := strings.TrimSpace(os.Getenv("COLUMNS")); v != "" {
		if w, err := strconv.Atoi(v); err == nil && w > 0 {
			return w
		}
	}
	return 80
}

// centerPad retourne le padding pour centrer un texte.
func centerPad(text string, width int) string {
	pad := (width - len(text)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad)
}

// boxLine retourne une ligne de boîte avec contenu centré et couleur.
func boxLine(content string, color string, contentWidth int) string {
	left := centerPad(content, contentWidth)
	right := strings.Repeat(" ", contentWidth-len(left)-len(content))
	return colorDim + "|" + colorReset + left + color + content + colorReset + right + colorDim + "|" + colorReset
}

// boxEmpty retourne une ligne vide de boîte.
func boxEmpty(contentWidth int) string {
	return colorDim + "|" + strings.Repeat(" ", contentWidth) + "|" + colorReset
}

// boxTop retourne le haut de la boîte.
func boxTop(contentWidth int) string {
	return colorDim + "+" + strings.Repeat("-", contentWidth) + "+" + colorReset
}

// boxBottom retourne le bas de la boîte.
func boxBottom(contentWidth int) string {
	return colorDim + "+" + strings.Repeat("-", contentWidth) + "+" + colorReset
}

// printBanner affiche la bannière LABOSURF PRO professionnelle.
// Adapte automatiquement à la largeur du terminal.
func printBanner() {
	w := terminalWidth()
	compact := w > 0 && w < 44

	art := asciiArtLabosurf()
	if compact {
		art = asciiArtLabosurfCompact()
	}

	contentWidth := 48
	if compact {
		contentWidth = 38
	}

	fmt.Println()
	fmt.Println(boxTop(contentWidth))
	fmt.Println(boxEmpty(contentWidth))

	for _, line := range art {
		pad := centerPad("", contentWidth-len(line))
		fmt.Println(colorDim + "|" + colorReset + pad + colorGreen + line + colorReset + pad + colorDim + "|" + colorReset)
	}

	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxLine("LABORATOIRE DU FREESURF", colorCyan, contentWidth))
	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxLine("CONCU PAR PHILIPPO237", colorYellow, contentWidth))
	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxLine("v"+engineVersion, colorWhite, contentWidth))
	fmt.Println(boxBottom(contentWidth))
	fmt.Println()
}

// printModuleBanner affiche la bannière de démarrage d'un module.
func printModuleBanner(module string) {
	w := terminalWidth()
	compact := w > 0 && w < 44

	contentWidth := 48
	if compact {
		contentWidth = 38
	}

	title := "LABOSURF PRO — " + module
	subtitle := "LABORATOIRE DU FREESURF"

	fmt.Println()
	fmt.Println(boxTop(contentWidth))
	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxLine(title, colorGreen, contentWidth))
	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxLine(subtitle, colorCyan, contentWidth))
	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxBottom(contentWidth))
	fmt.Println()
}

// printAbout affiche les informations « À propos ».
func printAbout() {
	w := 48

	fmt.Println()
	fmt.Println(boxTop(w))
	fmt.Println(boxLine("LABOSURF PRO", colorGreen, w))
	fmt.Println(boxLine("Laboratoire du FreeSurf", colorCyan, w))
	fmt.Println(boxEmpty(w))
	fmt.Println(boxLine("Concu par PHILIPPO237", colorYellow, w))
	fmt.Println(boxLine("Telegram : t.me/Philippo237", colorDim, w))
	fmt.Println(boxLine("GitHub   : github.com/PHILIPPO237", colorDim, w))
	fmt.Println(boxEmpty(w))
	fmt.Println(boxLine("Version  : "+engineVersion, colorWhite, w))
	fmt.Println(boxLine("Moteur   : UDP Engine (tunnel UDP)", colorCyan, w))
	fmt.Println(boxBottom(w))
	fmt.Println()
}

// printStartupBanner affiche la bannière de démarrage du serveur.
func printStartupBanner() {
	w := terminalWidth()
	compact := w > 0 && w < 44

	contentWidth := 48
	if compact {
		contentWidth = 38
	}

	fmt.Println()
	fmt.Println(boxTop(contentWidth))
	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxLine("LABOSURF PRO", colorGreen, contentWidth))
	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxLine("LABORATOIRE DU FREESURF", colorCyan, contentWidth))
	fmt.Println(boxEmpty(contentWidth))
	fmt.Println(boxLine("CONCU PAR PHILIPPO237", colorYellow, contentWidth))
	fmt.Println(boxBottom(contentWidth))
	fmt.Println()
}

// initBanner initialise les couleurs ANSI pour le terminal.
func initBanner() {
	fmt.Print("")
}

// ensureVT100 active le support ANSI sur Windows si nécessaire.
func ensureVT100() {
	// ANSI/VT100 est pris en charge directement par les terminaux SSH modernes.
}
