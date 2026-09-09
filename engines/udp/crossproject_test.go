package main

// Ce test vérifie la compatibilité RÉELLE entre les deux projets frères,
// pour la SECONDE implémentation de vérification de licence que possède
// LABOSURF_PRO (celle du module séparé engines/udp — labosurf/engine — qui
// duplique intentionnellement le format avec internal/license, voir le
// commentaire au-dessus de LicenseData dans license.go : "Ce schéma doit
// rester identique dans le License Maker").
//
// Un jeton produit par le VRAI binaire LABOSURF_LICENSE_MAKER (compilé à la
// volée depuis ses sources, jamais réécrit ni simulé ici) doit être accepté
// par VerifyLicenseToken() de ce module. Aucune clé privée réelle n'est
// utilisée : une paire ed25519 jetable est générée pour la durée du test.
//
// LABOSURF_LICENSE_MAKER vit dans un dépôt Git frère, pas un sous-module de
// LABOSURF_PRO : le test se saute proprement (t.Skip, jamais un échec) si ce
// dépôt frère est introuvable — voir locateLicenseMaker().
//
// Symétrique à internal/license/crossproject_test.go (module racine de
// LABOSURF_PRO) ; dupliqué ici car engines/udp est un module Go distinct qui
// ne peut pas importer internal/license.

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func locateLicenseMakerUDP(t *testing.T) (string, bool) {
	t.Helper()

	if v := strings.TrimSpace(os.Getenv("LABOSURF_LICENSE_MAKER_DIR")); v != "" {
		if isGoModuleUDP(v) {
			return v, true
		}
		return "", false
	}

	// engines/udp -> engines -> LABOSURF_PRO -> Bureau -> LABOSURF_LICENSE_MAKER
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(wd, "..", "..", "..", "LABOSURF_LICENSE_MAKER")
	if isGoModuleUDP(candidate) {
		return candidate, true
	}
	return "", false
}

func isGoModuleUDP(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil && !info.IsDir()
}

func buildLicenseMakerUDP(t *testing.T, sourceDir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "license-maker-under-test")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = sourceDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compilation de LABOSURF_LICENSE_MAKER échouée : %v\n%s", err, output)
	}
	return out
}

var tokenPatternUDP = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

func runLicenseMakerGenerateUDP(t *testing.T, binPath, workDir, id, comment string) string {
	t.Helper()

	stdin := fmt.Sprintf("1\n%s\n%s\n\n0\n", id, comment)

	cmd := exec.Command(binPath)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exécution de LABOSURF_LICENSE_MAKER échouée : %v\n--- sortie ---\n%s", err, output)
	}

	scanner := bufio.NewScanner(strings.NewReader(stripANSIUDP(string(output))))
	var token string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if tokenPatternUDP.MatchString(line) {
			token = line
			break
		}
	}
	if token == "" {
		t.Fatalf("aucun jeton trouvé dans la sortie de LABOSURF_LICENSE_MAKER :\n%s", output)
	}
	return token
}

func stripANSIUDP(s string) string {
	const esc = "\x1b["
	var b strings.Builder
	for {
		i := strings.Index(s, esc)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		rest := s[i+len(esc):]
		j := 0
		for j < len(rest) && !((rest[j] >= 'a' && rest[j] <= 'z') || (rest[j] >= 'A' && rest[j] <= 'Z')) {
			j++
		}
		if j < len(rest) {
			j++
		}
		s = rest[j:]
	}
	return b.String()
}

// TestUDPModuleAcceptsLicenseFromRealLicenseMaker est le test de
// compatibilité central pour cette seconde implémentation : un jeton produit
// par le vrai LABOSURF_LICENSE_MAKER doit être accepté par
// VerifyLicenseToken() de engines/udp, avec le statut LicenseActive.
func TestUDPModuleAcceptsLicenseFromRealLicenseMaker(t *testing.T) {
	makerDir, ok := locateLicenseMakerUDP(t)
	if !ok {
		t.Skip("dépôt frère LABOSURF_LICENSE_MAKER introuvable dans cet environnement (voir LABOSURF_LICENSE_MAKER_DIR) — test de compatibilité inter-projets sauté")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération de la paire de clés de test : %v", err)
	}
	privHex := hex.EncodeToString(priv)
	pubHex := hex.EncodeToString(pub)

	binPath := buildLicenseMakerUDP(t, makerDir)

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "labosurf_admin.key"), []byte(privHex), 0o600); err != nil {
		t.Fatalf("écriture clé privée de test : %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "labosurf_pub.key"), []byte(pubHex), 0o644); err != nil {
		t.Fatalf("écriture clé publique de test : %v", err)
	}

	token := runLicenseMakerGenerateUDP(t, binPath, workDir, "CROSSPROJECT-UDP-TEST", "généré par le test de compatibilité (module engines/udp)")

	// TestMain (license_test.go) fixe déjà testSignKey/testVerifyKey à une
	// paire aléatoire pour toute la suite : on la restaure après ce test
	// pour ne pas perturber les tests suivants (pas de t.Parallel() dans ce
	// package — sûr de faire cette substitution temporaire).
	prevVerifyKey := testVerifyKey
	testVerifyKey = pub
	defer func() { testVerifyKey = prevVerifyKey }()

	data, status, err := VerifyLicenseToken(token)
	if err != nil {
		t.Fatalf("engines/udp refuse un jeton pourtant généré et signé par le vrai LABOSURF_LICENSE_MAKER : %v", err)
	}
	if status != LicenseActive {
		t.Fatalf("statut attendu %q, obtenu %q", LicenseActive, status)
	}
	if data.ID != "CROSSPROJECT-UDP-TEST" {
		t.Fatalf("ID attendu CROSSPROJECT-UDP-TEST, obtenu %q", data.ID)
	}
	if data.Product != "LABOSURF PRO" {
		t.Fatalf("Product attendu %q, obtenu %q", "LABOSURF PRO", data.Product)
	}
	if !validateLicenseKey(data.Key) {
		t.Fatalf("format de clé Key inattendu (incompatibilité de format entre les deux projets) : %q", data.Key)
	}
}

// TestUDPModuleRejectsLicenseFromRealLicenseMakerWithWrongPublicKey vérifie
// l'autre sens : ce module, configuré avec la MAUVAISE clé publique, doit
// rejeter un jeton par ailleurs authentique émis par le vrai
// LABOSURF_LICENSE_MAKER.
func TestUDPModuleRejectsLicenseFromRealLicenseMakerWithWrongPublicKey(t *testing.T) {
	makerDir, ok := locateLicenseMakerUDP(t)
	if !ok {
		t.Skip("dépôt frère LABOSURF_LICENSE_MAKER introuvable dans cet environnement (voir LABOSURF_LICENSE_MAKER_DIR) — test de compatibilité inter-projets sauté")
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération de la paire de clés de test : %v", err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération d'une autre paire de clés : %v", err)
	}

	binPath := buildLicenseMakerUDP(t, makerDir)
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "labosurf_admin.key"), []byte(hex.EncodeToString(priv)), 0o600); err != nil {
		t.Fatalf("écriture clé privée de test : %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "labosurf_pub.key"), []byte(hex.EncodeToString(priv.Public().(ed25519.PublicKey))), 0o644); err != nil {
		t.Fatalf("écriture clé publique de test : %v", err)
	}

	token := runLicenseMakerGenerateUDP(t, binPath, workDir, "CROSSPROJECT-UDP-WRONGKEY", "")

	prevVerifyKey := testVerifyKey
	testVerifyKey = otherPub
	defer func() { testVerifyKey = prevVerifyKey }()

	if _, status, err := VerifyLicenseToken(token); err == nil {
		t.Fatalf("jeton accepté avec la mauvaise clé publique (statut %q) — la vérification cryptographique ne protège rien", status)
	}
}
