package freewaygate

import (
	"encoding/json"
	"testing"
)

func TestAssetNameFor(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
	}{
		{"linux", "amd64", "freeway-gate-linux-amd64"},
		{"linux", "arm64", "freeway-gate-linux-arm64"},
		{"linux", "386", "freeway-gate-linux-386"},
		{"linux", "arm", ""},
		{"windows", "amd64", ""},
	}
	for _, c := range cases {
		if got := assetNameFor(c.goos, c.goarch); got != c.want {
			t.Errorf("assetNameFor(%s,%s)=%q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestListenFromData(t *testing.T) {
	if got := listenFromData([]byte(`{"listen":"127.0.0.1:9090"}`)); got != "127.0.0.1:9090" {
		t.Errorf("custom listen=%q", got)
	}
	if got := listenFromData([]byte(`{}`)); got != "127.0.0.1:8080" {
		t.Errorf("default listen=%q", got)
	}
	if got := listenFromData([]byte(`garbage`)); got != "127.0.0.1:8080" {
		t.Errorf("fallback listen=%q", got)
	}
}

func TestPortOf(t *testing.T) {
	if got := portOf("127.0.0.1:8080"); got != 8080 {
		t.Errorf("portOf=%d", got)
	}
	if got := portOf("bogus"); got != 0 {
		t.Errorf("portOf bogus=%d", got)
	}
}

func TestLogRingLast(t *testing.T) {
	r := newLogRing(3)
	for _, l := range []string{"a", "b", "c", "d", "e"} {
		_, _ = r.Write([]byte(l + "\n"))
	}
	got := r.Last(10)
	if len(got) != 3 || got[0] != "c" || got[2] != "e" {
		t.Fatalf("ring buffer=%v", got)
	}
}

func TestDefaultConfigValidJSON(t *testing.T) {
	var c struct {
		Listen string `json:"listen"`
	}
	if err := json.Unmarshal([]byte(DefaultConfigJSON), &c); err != nil {
		t.Fatalf("DefaultConfigJSON invalide: %v", err)
	}
	if c.Listen != "127.0.0.1:8080" {
		t.Errorf("default config listen=%q", c.Listen)
	}
}
