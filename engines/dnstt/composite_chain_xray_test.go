package dnstt

// Preuve de data-path réelle pour DNSTT -> Xray (le seul backend "possible
// directement" identifié dans l'audit des 7 combinaisons demandées — voir
// ARCHITECTURE_HYBRIDES.md). Contrairement à composite_chain_test.go (SSH),
// ce test a révélé que le mécanisme historique à un seul blob de
// configuration partagée (CompositeEngine.ConfigureAll) NE fonctionne PAS
// pour un backend dont le schéma JSON n'a rien en commun avec celui de
// dnstt (xray attend {"log":...,"inbounds":[...]}, pas {"domain":...}) —
// d'où l'ajout de CompositeEngine.ComponentConfig (voir composite_engine.go
// et chain_test.go:TestCompositeEngineComponentConfigOverride) avant de
// pouvoir écrire ce test.
//
// Ce test télécharge et exécute le VRAI binaire xray-core officiel (réseau
// requis) : contrairement au reste de la suite (entièrement hors-ligne), il
// est DÉSACTIVÉ PAR DÉFAUT pour ne jamais rendre `go test ./...` flaky ou
// dépendant du réseau ailleurs que dans cette session. Activation explicite :
//
//	LABOSURF_TEST_REAL_XRAY=1 go test ./engines/dnstt/... -run RealXray -v
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"

	_ "labosurf/engines/xray"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("port TCP libre : %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startBannerTargetServer démarre un vrai serveur TCP qui, à l'acceptation
// d'une connexion, écrit immédiatement une bannière distinctive — c'est ce
// que le "freedom" outbound d'xray-core doit réellement atteindre et
// relayer si la chaîne fonctionne de bout en bout.
func startBannerTargetServer(t *testing.T, banner string) (port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("target server : %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = c.Write([]byte(banner))
				time.Sleep(500 * time.Millisecond) // laisse le temps au relais de lire avant fermeture
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// formatUUID formate 16 octets bruts en chaîne UUID standard (8-4-4-4-12) —
// format attendu par la config xray-core ("clients":[{"id":"..."}]) ; les
// mêmes octets bruts sont utilisés tels quels dans la requête VLESS binaire.
func formatUUID(raw [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

// buildVLESSRequest encode une requête VLESS minimale (version 0, pas
// d'addons, commande TCP, adresse IPv4) — schéma binaire officiel du
// protocole VLESS (décryption "none" côté serveur, voir la config xray
// générée par plaintextVLESSConfig). Aucun chiffrement à ce niveau : VLESS
// délègue la confidentialité à la couche transport (ici volontairement
// absente — "security":"none" — pour isoler et prouver uniquement le
// mécanisme de chaînage générique, pas la configuration REALITY de
// production, qui est une fonctionnalité xray-core déjà existante et
// indépendante de ce qui est testé ici).
func buildVLESSRequest(uuid [16]byte, targetIP [4]byte, targetPort int) []byte {
	req := make([]byte, 0, 26)
	req = append(req, 0x00)       // version
	req = append(req, uuid[:]...) // 16 octets UUID brut
	req = append(req, 0x00)       // longueur des addons = 0
	req = append(req, 0x01)       // commande = TCP
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(targetPort))
	req = append(req, portBytes...)
	req = append(req, 0x01)           // type d'adresse = IPv4
	req = append(req, targetIP[:]...) // adresse
	return req
}

func plaintextVLESSXrayConfig(listenPort int, uuidStr string) []byte {
	cfg := fmt.Sprintf(`{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "port": %d,
    "listen": "127.0.0.1",
    "protocol": "vless",
    "settings": {
      "clients": [{"id": "%s", "level": 0}],
      "decryption": "none"
    },
    "streamSettings": {"network": "tcp", "security": "none"}
  }],
  "outbounds": [{"protocol": "freedom", "tag": "direct"}]
}`, listenPort, uuidStr)
	return []byte(cfg)
}

func skipUnlessRealXrayTestsEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("LABOSURF_TEST_REAL_XRAY") != "1" {
		t.Skip("désactivé par défaut (réseau + vrai binaire xray-core requis) — activer avec LABOSURF_TEST_REAL_XRAY=1")
	}
}

// TestCompositeEngineChainsDNSTTToRealXrayBackend est la preuve de data-path
// réelle pour DNSTT -> Xray, décrite comme "POSSIBLE DIRECTEMENT" dans
// l'audit : le VRAI binaire xray-core officiel est téléchargé et vérifié
// (Install() réel, sans mock), configuré en VLESS/tcp/security=none (voir
// buildVLESSRequest), câblé par CompositeEngine (avec ComponentConfig, seul
// moyen de lui donner un schéma JSON propre — voir le commentaire en tête
// de fichier), puis un client parlant le protocole DNSTT réel envoie une
// requête VLESS réelle. La chaîne complète exercée est :
//
//	client UDP -> dnstt (réel) -> [TCP câblé par CompositeEngine] -> xray-core (réel, VLESS)
//	    -> [TCP, "freedom" outbound d'xray-core, PAS notre code] -> vrai serveur cible TCP
//
// La preuve que les octets traversent réellement toute la chaîne (dans les
// DEUX sens) est la réception, à travers le tunnel DNSTT, de la bannière
// distinctive émise par le vrai serveur cible — relayée par xray-core lui
// -même, pas fabriquée par ce test.
func TestCompositeEngineChainsDNSTTToRealXrayBackend(t *testing.T) {
	skipUnlessRealXrayTestsEnabled(t)

	tmp := t.TempDir()
	t.Setenv("LABOSURF_DNSTT_CONFIG", filepath.Join(tmp, "dnstt.json"))
	t.Setenv("LABOSURF_XRAY_CONFIG", filepath.Join(tmp, "xray.json"))
	t.Setenv("LABOSURF_XRAY_BINARY_DIR", filepath.Join(tmp, "bin"))
	t.Setenv("LABOSURF_XRAY_ASSET_DIR", filepath.Join(tmp, "assets"))
	t.Setenv("LABOSURF_XRAY_REALITY_DIR", filepath.Join(tmp, "reality"))

	const banner = "XRAY-CHAIN-CONFIRMED"
	targetPort := startBannerTargetServer(t, banner)

	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		t.Fatalf("génération uuid : %v", err)
	}
	uuidStr := formatUUID(uuid)
	xrayPort := freeTCPPort(t)
	xrayCfg := plaintextVLESSXrayConfig(xrayPort, uuidStr)

	ce := &engineutil.CompositeEngine{
		Components:   []string{"dnstt", "xray"},
		ConfigureAll: true,
		ComponentConfig: map[string]engine.EngineConfig{
			"xray": {JSON: xrayCfg},
		},
	}
	ce.Spec.Name = "dnstt-xray"
	ce.Spec.Version = "hybrid"
	ce.Spec.Description = "test réel dnstt->xray"

	// Le téléchargement du binaire xray-core officiel (~21 Mo) a été observé
	// à ~130 Ko/s dans cet environnement (curl manuel : ~160s pour le
	// fichier complet) — marge large pour éviter un abandon prématuré.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Log("installation (télécharge et vérifie le vrai binaire xray-core officiel, réseau requis)...")
	if err := ce.Install(ctx, engine.InstallConfig{}); err != nil {
		t.Fatalf("Install : %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}
	dnsttPort := freeTCPPort(t) // réutilisé comme port UDP libre (même espace de recherche)
	shared := fmt.Sprintf(`{
  "domain": "t.example.com",
  "port": %d,
  "backend": "203.0.113.1:9",
  "users": [{"user":"alice","username":"alice","public_key":"%s","enabled":true}]
}`, dnsttPort, hex.EncodeToString(pub))

	if err := ce.Configure(ctx, engine.EngineConfig{JSON: []byte(shared)}); err != nil {
		t.Fatalf("Configure : %v", err)
	}
	if err := ce.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}
	t.Cleanup(func() { ce.Stop() })

	xraySub, err := ce.Component("xray")
	if err != nil {
		t.Fatalf("Component(xray) : %v", err)
	}
	xrayEpr, ok := xraySub.(engine.Endpointer)
	if !ok {
		t.Fatal("xray n'implémente pas engine.Endpointer")
	}
	xrayEp, ready := xrayEpr.Endpoint()
	if !ready || xrayEp.Network != "tcp" {
		t.Fatalf("endpoint xray réel non prêt ou non tcp : %+v (ready=%v)", xrayEp, ready)
	}
	t.Logf("✓ xray-core réellement démarré et câblé comme backend dnstt : %s", xrayEp.Addr)

	dnsttSub, err := ce.Component("dnstt")
	if err != nil {
		t.Fatalf("Component(dnstt) : %v", err)
	}
	dnsttEpr, ok := dnsttSub.(engine.Endpointer)
	if !ok {
		t.Fatal("dnstt n'implémente pas engine.Endpointer")
	}
	front, ready := dnsttEpr.Endpoint()
	if !ready {
		t.Fatal("endpoint dnstt jamais prêt")
	}

	serverAddr, err := net.ResolveUDPAddr("udp", front.Addr)
	if err != nil {
		t.Fatalf("résolution endpoint dnstt %s : %v", front.Addr, err)
	}
	client, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("DialUDP : %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(6 * time.Second))

	var sessionID [8]byte
	copy(sessionID[:], []byte("vlesstst"))
	signature := ed25519.Sign(priv, sessionID[:])

	vlessReq := buildVLESSRequest(uuid, [4]byte{127, 0, 0, 1}, targetPort)
	firstPayload := append(append([]byte(nil), signature...), vlessReq...)

	q1 := buildDNSTTQuery(t, "t.example.com", sessionID, 1, firstPayload)
	if _, err := client.Write(q1); err != nil {
		t.Fatalf("envoi requête DNSTT (auth + requête VLESS) : %v", err)
	}

	buf := make([]byte, 4096)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n, err := client.Read(buf)
		if err != nil {
			continue
		}
		pkt := buf[:n]
		if len(pkt) < 12 {
			continue
		}
		sub := extractSubdomain(pkt[12:], "t.example.com")
		raw, err := decodeSubdomainB32(sub)
		if err != nil || len(raw) < headerLen {
			continue
		}
		chunk := string(raw[headerLen:])
		if strings.Contains(chunk, banner) {
			t.Logf("✓ CHAÎNAGE RÉEL DNSTT->XRAY DÉMONTRÉ : client UDP -> dnstt (%s) -> xray-core réel (%s, VLESS) -> freedom outbound -> vrai serveur cible -> bannière reçue à travers toute la chaîne : %q", front.Addr, xrayEp.Addr, chunk)
			return
		}
	}
	t.Fatal("bannière du serveur cible jamais reçue à travers la chaîne dnstt->xray : le chaînage ne transporte pas réellement les octets de bout en bout")
}
