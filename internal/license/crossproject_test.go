package license

// Ce test vérifie la compatibilité RÉELLE entre les deux projets frères :
// une licence produite par le vrai binaire LABOSURF_LICENSE_MAKER (compilé
// à la volée depuis ses sources, jamais réécrit ni simulé ici) doit être
// acceptée par LABOSURF_PRO (ce package license). Aucune clé privée réelle
// n'est utilisée : une paire ed25519 jetable est générée pour la durée du
// test et injectée via labosurf_admin.key/labosurf_pub.key locaux au
// répertoire de travail temporaire du sous-processus — jamais les clés de
// production.
//
// LABOSURF_LICENSE_MAKER vit dans un dépôt Git frère, PAS un sous-module de
// LABOSURF_PRO : son chemin n'est donc pas garanti dans tous les
// environnements (en particulier une CI qui ne checkout que LABOSURF_PRO).
// Le test se saute proprement (t.Skip, jamais un échec) si ce dépôt frère
// est introuvable — voir locateLicenseMaker().

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
	"runtime"
	"strings"
	"testing"
)

// locateLicenseMaker retourne le chemin du dépôt LABOSURF_LICENSE_MAKER, ou
// ok=false s'il est introuvable dans cet environnement. Cherche d'abord la
// variable d'environnement LABOSURF_LICENSE_MAKER_DIR (surcharge explicite),
// puis l'emplacement conventionnel utilisé dans cet environnement de
// développement : un dossier frère de LABOSURF_PRO au même niveau.
func locateLicenseMaker(t *testing.T) (string, bool) {
	t.Helper()

	if v := strings.TrimSpace(os.Getenv("LABOSURF_LICENSE_MAKER_DIR")); v != "" {
		if isGoModule(v) {
			return v, true
		}
		return "", false
	}

	// internal/license -> internal -> LABOSURF_PRO -> Bureau -> LABOSURF_LICENSE_MAKER
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(wd, "..", "..", "..", "LABOSURF_LICENSE_MAKER")
	if isGoModule(candidate) {
		return candidate, true
	}
	return "", false
}

func isGoModule(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil && !info.IsDir()
}

// buildLicenseMaker compile le binaire LABOSURF_LICENSE_MAKER dans un
// répertoire temporaire et retourne son chemin.
func buildLicenseMaker(t *testing.T, sourceDir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "license-maker-under-test")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = sourceDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compilation de LABOSURF_LICENSE_MAKER échouée : %v\n%s", err, output)
	}
	return out
}

// tokenPattern reconnaît un jeton base64url(payload).base64url(signature)
// tel qu'imprimé par LABOSURF_LICENSE_MAKER sur sa propre ligne de sortie.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// runLicenseMakerGenerate pilote le menu interactif du binaire (aucune
// interface non-interactive n'existe dans LABOSURF_LICENSE_MAKER) pour
// générer UNE licence, et retourne le jeton imprimé sur stdout.
func runLicenseMakerGenerate(t *testing.T, binPath, workDir, id, comment string) string {
	t.Helper()

	// Séquence stdin exacte attendue par menu.go :
	//   readChoice() (menu principal)         -> "1"        (générer une licence)
	//   ask("Identifiant...")                  -> id
	//   ask("Commentaire...")                  -> comment (peut être vide)
	//   pause() après generateNewLicense       -> "" (Entrée)
	//   readChoice() (retour menu principal)   -> "0"        (quitter)
	stdin := fmt.Sprintf("1\n%s\n%s\n\n0\n", id, comment)

	cmd := exec.Command(binPath)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(stdin)
	// TERM vide + pas de tty : le programme n'utilise que des séquences
	// ANSI dans ses Println, aucune dépendance à un vrai terminal.
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exécution de LABOSURF_LICENSE_MAKER échouée : %v\n--- sortie ---\n%s", err, output)
	}

	scanner := bufio.NewScanner(strings.NewReader(stripANSI(string(output))))
	var token string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if tokenPattern.MatchString(line) {
			token = line
			break
		}
	}
	if token == "" {
		t.Fatalf("aucun jeton trouvé dans la sortie de LABOSURF_LICENSE_MAKER :\n%s", output)
	}
	return token
}

// stripANSI retire les séquences d'échappement couleur ANSI (le binaire les
// émet inconditionnellement) pour permettre une comparaison de ligne fiable.
func stripANSI(s string) string {
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

// TestLicenseGeneratedByLicenseMakerIsVerifiedByLabosurfPro est le test de
// compatibilité central de cet audit (exigence "clé générée par
// LABOSURF_LICENSE_MAKER puis vérifiée par LABOSURF_PRO") : un jeton produit
// par le VRAI binaire LICENSE_MAKER (compilé depuis ses sources actuelles,
// pas une réimplémentation ici) doit être accepté par VerifyToken et
// utilisable par Activate, avec une paire de clés ed25519 jetable (jamais
// les clés de production).
func TestLicenseGeneratedByLicenseMakerIsVerifiedByLabosurfPro(t *testing.T) {
	makerDir, ok := locateLicenseMaker(t)
	if !ok {
		t.Skip("dépôt frère LABOSURF_LICENSE_MAKER introuvable dans cet environnement (voir LABOSURF_LICENSE_MAKER_DIR) — test de compatibilité inter-projets sauté")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération de la paire de clés de test : %v", err)
	}
	privHex := hex.EncodeToString(priv)
	pubHex := hex.EncodeToString(pub)

	binPath := buildLicenseMaker(t, makerDir)

	workDir := t.TempDir()
	// Pré-place la paire de clés de TEST (jamais la production) dans le
	// répertoire de travail du binaire : évite que l'assistant "premier
	// démarrage" ne génère et n'écrive une clé aléatoire différente.
	if err := os.WriteFile(filepath.Join(workDir, "labosurf_admin.key"), []byte(privHex), 0o600); err != nil {
		t.Fatalf("écriture clé privée de test : %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "labosurf_pub.key"), []byte(pubHex), 0o644); err != nil {
		t.Fatalf("écriture clé publique de test : %v", err)
	}

	token := runLicenseMakerGenerate(t, binPath, workDir, "CROSSPROJECT-TEST", "généré par le test de compatibilité")

	useTempDataDir(t)
	testVerifyKey = pub

	data, err := VerifyToken(token)
	if err != nil {
		t.Fatalf("LABOSURF_PRO refuse un jeton pourtant généré et signé par le vrai LABOSURF_LICENSE_MAKER : %v", err)
	}
	if data.ID != "CROSSPROJECT-TEST" {
		t.Fatalf("ID attendu CROSSPROJECT-TEST, obtenu %q", data.ID)
	}
	if data.Product != "LABOSURF PRO" {
		t.Fatalf("Product attendu %q, obtenu %q", "LABOSURF PRO", data.Product)
	}
	if len(data.Key) != licenseLength || !strings.HasPrefix(data.Key, licensePrefix) {
		t.Fatalf("format de clé Key inattendu (incompatibilité de format entre les deux projets) : %q", data.Key)
	}

	if err := Activate(token); err != nil {
		t.Fatalf("Activate() refuse un jeton généré par le vrai LABOSURF_LICENSE_MAKER : %v", err)
	}
}

// TestLicenseGeneratedByLicenseMakerRejectedWithWrongPublicKey vérifie
// l'autre sens du même scénario : LABOSURF_PRO configuré avec la MAUVAISE
// clé publique doit rejeter un jeton par ailleurs authentique émis par le
// vrai LABOSURF_LICENSE_MAKER — la garantie de sécurité qui rend tout le
// système de licence utile.
func TestLicenseGeneratedByLicenseMakerRejectedWithWrongPublicKey(t *testing.T) {
	makerDir, ok := locateLicenseMaker(t)
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

	binPath := buildLicenseMaker(t, makerDir)
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "labosurf_admin.key"), []byte(hex.EncodeToString(priv)), 0o600); err != nil {
		t.Fatalf("écriture clé privée de test : %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "labosurf_pub.key"), []byte(hex.EncodeToString(priv.Public().(ed25519.PublicKey))), 0o644); err != nil {
		t.Fatalf("écriture clé publique de test : %v", err)
	}

	token := runLicenseMakerGenerate(t, binPath, workDir, "CROSSPROJECT-WRONGKEY", "")

	useTempDataDir(t)
	testVerifyKey = otherPub // clé publique qui NE correspond PAS à celle utilisée pour signer

	if _, err := VerifyToken(token); err == nil {
		t.Fatal("jeton accepté avec la mauvaise clé publique — la vérification cryptographique ne protège rien")
	}
}
