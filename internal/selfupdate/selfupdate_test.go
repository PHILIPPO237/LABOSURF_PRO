package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ============================================================
// Comparaison de versions
// ============================================================

func TestParseVersionValid(t *testing.T) {
	got, err := ParseVersion("v1.4.0")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if got != [3]int{1, 4, 0} {
		t.Fatalf("attendu [1 4 0], obtenu %v", got)
	}
}

func TestParseVersionWithoutV(t *testing.T) {
	got, err := ParseVersion("2.0.1")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if got != [3]int{2, 0, 1} {
		t.Fatalf("attendu [2 0 1], obtenu %v", got)
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, v := range []string{"dev", "v1.2", "v1.2.3.4", "vx.y.z", ""} {
		if _, err := ParseVersion(v); err == nil {
			t.Errorf("ParseVersion(%q) : attendu une erreur", v)
		}
	}
}

func TestCompareVersionsLess(t *testing.T) {
	cmp, err := CompareVersions("v1.3.0", "v1.4.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if cmp != -1 {
		t.Fatalf("attendu -1, obtenu %d", cmp)
	}
}

func TestCompareVersionsGreater(t *testing.T) {
	cmp, err := CompareVersions("v2.0.0", "v1.9.9")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if cmp != 1 {
		t.Fatalf("attendu 1, obtenu %d", cmp)
	}
}

func TestCompareVersionsEqual(t *testing.T) {
	cmp, err := CompareVersions("v1.4.0", "v1.4.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if cmp != 0 {
		t.Fatalf("attendu 0, obtenu %d", cmp)
	}
}

func TestCompareVersionsPatchAndMinor(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.4", -1},
		{"v1.2.9", "v1.3.0", -1},
		{"v1.10.0", "v1.9.0", 1}, // comparaison numérique, pas lexicographique
	}
	for _, c := range cases {
		got, err := CompareVersions(c.a, c.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q,%q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, attendu %d", c.a, c.b, got, c.want)
		}
	}
}

// ============================================================
// Mock serveur GitHub (API releases + assets)
// ============================================================

// mockGitHub construit un serveur httptest simulant l'API GitHub Releases et
// le téléchargement d'assets. Aucun accès réseau réel n'est effectué par les
// tests de ce fichier.
func mockGitHub(t *testing.T, rel Release, assetContent map[string][]byte) (*httptest.Server, Release) {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/PHILIPPO237/LABOSURF_PRO/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})
	for name, content := range assetContent {
		name := name
		content := content
		mux.HandleFunc("/assets/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Write(content)
		})
	}
	mux.HandleFunc("/notfound", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Réécrit les URL d'assets pour pointer vers ce serveur.
	for i := range rel.Assets {
		rel.Assets[i].BrowserDownloadURL = srv.URL + "/assets/" + rel.Assets[i].Name
	}
	return srv, rel
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// ============================================================
// CheckForUpdate — absence / présence de mise à jour
// ============================================================

func TestCheckForUpdateNoUpdate(t *testing.T) {
	rel := Release{TagName: "v1.4.0", PublishedAt: time.Now()}
	srv, rel := mockGitHub(t, rel, nil)
	setAPIBaseURL(t, srv.URL)

	check, err := CheckForUpdate(context.Background(), srv.Client(), "PHILIPPO237/LABOSURF_PRO", "v1.4.0", "linux", "amd64")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if check.UpdateFound {
		t.Fatal("aucune mise à jour attendue (versions identiques)")
	}
}

func TestCheckForUpdateNewVersionAvailable(t *testing.T) {
	binContent := []byte("fake-binary-content")
	rel := Release{
		TagName: "v1.5.0",
		Body:    "Notes de version",
		Assets: []Asset{
			{Name: "labosurf-mgr-linux-amd64", Size: int64(len(binContent))},
		},
	}
	srv, rel := mockGitHub(t, rel, map[string][]byte{"labosurf-mgr-linux-amd64": binContent})
	setAPIBaseURL(t, srv.URL)

	check, err := CheckForUpdate(context.Background(), srv.Client(), "PHILIPPO237/LABOSURF_PRO", "v1.4.0", "linux", "amd64")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if !check.UpdateFound {
		t.Fatal("mise à jour attendue (v1.4.0 -> v1.5.0)")
	}
	if !check.AssetFound {
		t.Fatal("asset linux/amd64 attendu trouvé")
	}
	_ = rel
}

func TestCheckForUpdateUnparsableCurrentVersionAlwaysOffersUpdate(t *testing.T) {
	rel := Release{TagName: "v1.4.0", Assets: []Asset{{Name: "labosurf-mgr-linux-amd64"}}}
	srv, _ := mockGitHub(t, rel, map[string][]byte{"labosurf-mgr-linux-amd64": []byte("x")})
	setAPIBaseURL(t, srv.URL)

	check, err := CheckForUpdate(context.Background(), srv.Client(), "PHILIPPO237/LABOSURF_PRO", "dev", "linux", "amd64")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if !check.UpdateFound {
		t.Fatal("une version 'dev' non parseable doit toujours se voir proposer la mise à jour disponible")
	}
}

// ============================================================
// Architecture non supportée
// ============================================================

func TestCheckForUpdateUnsupportedArchitecture(t *testing.T) {
	rel := Release{
		TagName: "v1.5.0",
		Assets: []Asset{
			{Name: "labosurf-mgr-linux-amd64"},
			{Name: "labosurf-mgr-linux-arm64"},
		},
	}
	srv, _ := mockGitHub(t, rel, map[string][]byte{
		"labosurf-mgr-linux-amd64": []byte("x"),
		"labosurf-mgr-linux-arm64": []byte("x"),
	})
	setAPIBaseURL(t, srv.URL)

	check, err := CheckForUpdate(context.Background(), srv.Client(), "PHILIPPO237/LABOSURF_PRO", "v1.4.0", "windows", "amd64")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if !check.UpdateFound {
		t.Fatal("une nouvelle version existe, même sans asset compatible")
	}
	if check.AssetFound {
		t.Fatal("aucun asset windows/amd64 ne devrait être trouvé")
	}
}

// ============================================================
// Fichier de release absent
// ============================================================

func TestFetchLatestRelease404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/PHILIPPO237/LABOSURF_PRO/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	setAPIBaseURL(t, srv.URL)

	_, err := FetchLatestRelease(context.Background(), srv.Client(), "PHILIPPO237/LABOSURF_PRO")
	if err == nil {
		t.Fatal("attendu une erreur : aucune release publiée")
	}
}

func TestDownloadAndVerifyMissingSHA256SUMS(t *testing.T) {
	rel := Release{
		TagName: "v1.5.0",
		Assets:  []Asset{{Name: "labosurf-mgr-linux-amd64"}},
	}
	srv, rel := mockGitHub(t, rel, map[string][]byte{"labosurf-mgr-linux-amd64": []byte("x")})
	asset, _ := FindAsset(rel, "labosurf-mgr-linux-amd64")

	dir := t.TempDir()
	_, err := DownloadAndVerify(context.Background(), srv.Client(), rel, asset, dir)
	if err == nil {
		t.Fatal("attendu une erreur : SHA256SUMS absent de la release")
	}
}

// ============================================================
// Checksum incorrect
// ============================================================

func TestDownloadAndVerifyChecksumMismatch(t *testing.T) {
	binContent := []byte("real-binary-content")
	sums := "0000000000000000000000000000000000000000000000000000000000000000  labosurf-mgr-linux-amd64\n"
	rel := Release{
		TagName: "v1.5.0",
		Assets: []Asset{
			{Name: "labosurf-mgr-linux-amd64"},
			{Name: "SHA256SUMS"},
		},
	}
	srv, rel := mockGitHub(t, rel, map[string][]byte{
		"labosurf-mgr-linux-amd64": binContent,
		"SHA256SUMS":               []byte(sums),
	})
	asset, _ := FindAsset(rel, "labosurf-mgr-linux-amd64")

	dir := t.TempDir()
	_, err := DownloadAndVerify(context.Background(), srv.Client(), rel, asset, dir)
	if err == nil {
		t.Fatal("attendu une erreur : SHA-256 incorrect")
	}
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("attendu ErrChecksumMismatch, obtenu : %v", err)
	}
}

func TestDownloadAndVerifySuccess(t *testing.T) {
	binContent := []byte("real-binary-content")
	expectedHash := sha256Hex(binContent)
	sums := expectedHash + "  labosurf-mgr-linux-amd64\n"
	rel := Release{
		TagName: "v1.5.0",
		Assets: []Asset{
			{Name: "labosurf-mgr-linux-amd64"},
			{Name: "SHA256SUMS"},
		},
	}
	srv, rel := mockGitHub(t, rel, map[string][]byte{
		"labosurf-mgr-linux-amd64": binContent,
		"SHA256SUMS":               []byte(sums),
	})
	asset, _ := FindAsset(rel, "labosurf-mgr-linux-amd64")

	dir := t.TempDir()
	path, err := DownloadAndVerify(context.Background(), srv.Client(), rel, asset, dir)
	if err != nil {
		t.Fatalf("DownloadAndVerify: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("lecture fichier téléchargé: %v", err)
	}
	if string(got) != string(binContent) {
		t.Fatal("contenu téléchargé différent de l'original")
	}
}

// ============================================================
// Téléchargement interrompu
// ============================================================

func TestDownloadAndVerifyInterruptedDownload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/assets/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		body := []byte("aaa  labosurf-mgr-linux-amd64\n")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)+50)) // annonce plus que ce qui est envoyé
		w.Write(body)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := Release{
		TagName: "v1.5.0",
		Assets: []Asset{
			{Name: "labosurf-mgr-linux-amd64"},
			{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/assets/SHA256SUMS"},
		},
	}
	asset, _ := FindAsset(rel, "labosurf-mgr-linux-amd64")

	dir := t.TempDir()
	_, err := DownloadAndVerify(context.Background(), srv.Client(), rel, asset, dir)
	if err == nil {
		t.Fatal("attendu une erreur : téléchargement interrompu (Content-Length incohérent)")
	}
	// Le fichier temporaire ne doit pas rester sous le nom final.
	if _, statErr := os.Stat(filepath.Join(dir, "SHA256SUMS")); statErr == nil {
		t.Fatal("un téléchargement interrompu ne doit pas laisser de fichier au nom final")
	}
}

// ============================================================
// Refus de l'utilisateur
// ============================================================

func TestShouldInstall(t *testing.T) {
	cases := map[string]bool{
		"o": true, "O": true, " o ": true,
		"n": false, "N": false, "": false, "oui": false, "yes": false,
	}
	for input, want := range cases {
		if got := ShouldInstall(input); got != want {
			t.Errorf("ShouldInstall(%q) = %v, attendu %v", input, got, want)
		}
	}
}

// ============================================================
// Install — conservation des données, rollback
// ============================================================

func TestInstallPreservesUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "labosurf")
	dataPath := filepath.Join(dir, "data-untouched.json")

	writeFileT(t, binPath, elfLike("old-version"))
	writeFileT(t, dataPath, []byte(`{"accounts":[]}`))

	newBin := filepath.Join(dir, "downloaded")
	writeFileT(t, newBin, elfLike("new-version"))

	setGOOSLinux(t)
	sanityOK := func(path string) error { return nil }

	res, err := Install(newBin, binPath, sanityOK)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Installed {
		t.Fatal("Installed attendu true")
	}

	gotData, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("lecture données : %v", err)
	}
	if string(gotData) != `{"accounts":[]}` {
		t.Fatal("les données non liées au binaire ont été modifiées")
	}

	gotBin, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("lecture binaire : %v", err)
	}
	if string(gotBin) != string(elfLike("new-version")) {
		t.Fatal("le binaire n'a pas été remplacé par le nouveau contenu")
	}
}

func TestInstallRollbackOnSanityCheckFailure(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "labosurf")
	oldContent := elfLike("old-version")
	writeFileT(t, binPath, oldContent)

	newBin := filepath.Join(dir, "downloaded")
	writeFileT(t, newBin, elfLike("broken-new-version"))

	setGOOSLinux(t)
	sanityFail := func(path string) error { return errors.New("le nouveau binaire ne démarre pas") }

	_, err := Install(newBin, binPath, sanityFail)
	if err == nil {
		t.Fatal("attendu une erreur : sanity-check en échec doit remonter une erreur")
	}

	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("lecture binaire après rollback : %v", err)
	}
	if string(got) != string(oldContent) {
		t.Fatal("rollback : le binaire d'origine aurait dû être restauré")
	}
}

func TestInstallRejectsEmptyDownload(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "labosurf")
	writeFileT(t, binPath, elfLike("old-version"))

	newBin := filepath.Join(dir, "downloaded")
	writeFileT(t, newBin, []byte{})

	setGOOSLinux(t)
	_, err := Install(newBin, binPath, func(string) error { return nil })
	if err == nil {
		t.Fatal("attendu une erreur : fichier téléchargé vide")
	}

	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("lecture binaire : %v", err)
	}
	if string(got) != string(elfLike("old-version")) {
		t.Fatal("le binaire d'origine ne doit pas être touché si le téléchargement est invalide")
	}
}

// ============================================================
// Helpers de test
// ============================================================

func setAPIBaseURL(t *testing.T, url string) {
	t.Helper()
	orig := apiBaseURL
	apiBaseURL = url
	t.Cleanup(func() { apiBaseURL = orig })
}

func setGOOSLinux(t *testing.T) {
	t.Helper()
	orig := currentGOOSFunc
	currentGOOSFunc = func() string { return "linux" }
	t.Cleanup(func() { currentGOOSFunc = orig })
}

func writeFileT(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("écriture %s : %v", path, err)
	}
}

// elfLike simule un binaire ELF minimal (en-tête correct) pour satisfaire le
// contrôle isExecutableFile dans les tests, sans dépendre d'un vrai binaire.
func elfLike(marker string) []byte {
	return append([]byte{0x7F, 'E', 'L', 'F'}, []byte(marker)...)
}
