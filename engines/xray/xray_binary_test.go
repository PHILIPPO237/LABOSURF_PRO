package xray

import (
	"encoding/hex"
	"testing"
)

// TestGetArchSuffixMatchesRealAssetNames est un test de non-régression :
// "linux-arm64" (sans le suffixe "-v8a") n'a jamais été un nom d'asset
// publié par XTLS/Xray-core — Install() échouait avec une 404 sur toute
// machine arm64. Ce test fige le mapping arch Go → suffixe d'asset attendu.
func TestGetArchSuffixMatchesRealAssetNames(t *testing.T) {
	cases := map[string]string{
		"amd64": "linux-64",
		"arm64": "linux-arm64-v8a",
	}
	for goarch, want := range cases {
		got := archSuffixFor(goarch)
		if got != want {
			t.Errorf("archSuffixFor(%q) = %q, attendu %q", goarch, got, want)
		}
	}
}

// TestGetExpectedSHA256NotEmptyForSupportedArch vérifie que la vérification
// SHA256 n'est plus désactivée (chaîne vide) pour les architectures
// réellement utilisées par le pipeline de release (amd64, arm64) et que la
// valeur a le format attendu d'un SHA-256 (64 caractères hexadécimaux).
func TestGetExpectedSHA256NotEmptyForSupportedArch(t *testing.T) {
	for _, arch := range []string{"linux-64", "linux-arm64-v8a"} {
		sum := expectedSHA256For(arch)
		if sum == "" {
			t.Errorf("getExpectedSHA256 vide pour %q — vérification SHA256 désactivée", arch)
			continue
		}
		raw, err := hex.DecodeString(sum)
		if err != nil {
			t.Errorf("%q n'est pas un hex valide pour %q: %v", sum, arch, err)
			continue
		}
		if len(raw) != 32 {
			t.Errorf("%q pour %q : %d octets décodés, 32 attendus (SHA-256)", sum, arch, len(raw))
		}
	}
}
