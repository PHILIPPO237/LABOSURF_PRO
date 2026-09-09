package dnstt

// Ce fichier vit délibérément dans le package dnstt (pas engineutil_test) :
// il a besoin des mêmes helpers de protocole propriétaire que
// server_test.go (buildDNSTTQuery, extractSubdomain, decodeSubdomainB32,
// headerLen), qui sont non-exportés et donc invisibles depuis un autre
// package. C'est le seul endroit du dépôt où un test peut à la fois piloter
// le protocole DNSTT au niveau octet ET démarrer un vrai CompositeEngine
// dnstt+ssh — exactement ce qu'exige la Phase 3 : "le test doit démontrer
// le chemin de données", pas seulement que le câblage de configuration a
// l'air correct.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"labosurf/internal/engine"
	"labosurf/internal/engineutil"

	_ "labosurf/engines/ssh"
)

// newDNSTTSSHComposite construit (sans passer par le registre persistant
// hybrids.json — inutile pour un test) un CompositeEngine dnstt+ssh
// exactement comme le ferait engineutil.RegisterHybrid, et pointe les
// fichiers de config de chaque composant vers des chemins temporaires.
func newDNSTTSSHComposite(t *testing.T) *engineutil.CompositeEngine {
	t.Helper()
	t.Setenv("LABOSURF_DNSTT_CONFIG", filepath.Join(t.TempDir(), "dnstt.json"))
	t.Setenv("LABOSURF_SSH_CONFIG", filepath.Join(t.TempDir(), "ssh.json"))

	ce := &engineutil.CompositeEngine{
		Components:   []string{"dnstt", "ssh"},
		ConfigureAll: true,
	}
	ce.Spec.Name = "dnstt-ssh"
	ce.Spec.Version = "hybrid"
	ce.Spec.Description = "test"
	return ce
}

// sharedChainConfig construit la configuration JSON partagée dnstt+ssh.
// dnstt et ssh lisent chacun uniquement les clés qu'ils comprennent dans ce
// même objet (comportement réel de CompositeEngine.Configure — voir
// composite_engine.go). "port" est partagé sans collision : dnstt écoute
// en UDP, ssh en TCP, deux espaces de ports indépendants au niveau du
// noyau. "backend" est un placeholder volontairement injoignable
// (TEST-NET, RFC 5737) : si le composite ne le remplaçait pas réellement
// par l'endpoint SSH réel, le test échouerait par timeout de connexion au
// lieu de réussir par coïncidence sur un défaut local plausible.
func sharedChainConfig(t *testing.T, port int, pubHex, sshDir string) []byte {
	t.Helper()
	shared := map[string]any{
		"domain":  "t.example.com",
		"port":    port,
		"backend": "203.0.113.1:9",
		"users": []map[string]any{
			{"user": "alice", "username": "alice", "public_key": pubHex, "enabled": true},
		},
		"dir": sshDir,
	}
	raw, err := json.Marshal(shared)
	if err != nil {
		t.Fatalf("marshal config partagée : %v", err)
	}
	return raw
}

// realSSHEndpoint récupère l'endpoint réel actuel du composant ssh TEL QUE
// PILOTÉ par ce CompositeEngine précis (via Component(), pas engine.Get(),
// qui retournerait une instance neuve et jamais démarrée — voir le
// commentaire du champ CompositeEngine.subs).
func realSSHEndpoint(t *testing.T, ce *engineutil.CompositeEngine) engine.Endpoint {
	t.Helper()
	sub, err := ce.Component("ssh")
	if err != nil {
		t.Fatalf("Component(ssh) : %v", err)
	}
	epr, ok := sub.(engine.Endpointer)
	if !ok {
		t.Fatal("le moteur ssh n'implémente pas engine.Endpointer")
	}
	ep, ready := epr.Endpoint()
	if !ready {
		t.Fatal("endpoint ssh jamais prêt")
	}
	if ep.Network != "tcp" || ep.Addr == "" || ep.Addr == "203.0.113.1:9" {
		t.Fatalf("endpoint ssh invalide ou non substitué : %+v", ep)
	}
	return ep
}

// realDNSTTEndpoint récupère l'endpoint réel actuel du composant dnstt tel
// que piloté par ce CompositeEngine précis.
func realDNSTTEndpoint(t *testing.T, ce *engineutil.CompositeEngine) engine.Endpoint {
	t.Helper()
	sub, err := ce.Component("dnstt")
	if err != nil {
		t.Fatalf("Component(dnstt) : %v", err)
	}
	epr, ok := sub.(engine.Endpointer)
	if !ok {
		t.Fatal("le moteur dnstt n'implémente pas engine.Endpointer")
	}
	ep, ready := epr.Endpoint()
	if !ready {
		t.Fatal("endpoint dnstt jamais prêt")
	}
	return ep
}

// assertWiredBackendOnDisk relit le fichier de config dnstt réellement écrit
// sur disque et vérifie que son champ "backend" est EXACTEMENT l'endpoint
// réel ssh actuel — jamais le placeholder, jamais une valeur périmée.
func assertWiredBackendOnDisk(t *testing.T, want string) {
	t.Helper()
	raw, err := os.ReadFile(os.Getenv("LABOSURF_DNSTT_CONFIG"))
	if err != nil {
		t.Fatalf("lecture config dnstt sur disque : %v", err)
	}
	var written map[string]any
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("config dnstt sur disque invalide : %v", err)
	}
	if written["backend"] != want {
		t.Fatalf("backend dnstt sur disque = %v, attendu l'endpoint réel ssh (%s)", written["backend"], want)
	}
}

// expectSSHBannerThroughTunnel envoie, via le protocole DNSTT réel (comme
// dans server_test.go), une requête authentifiée par la signature ed25519
// donnée, vers l'endpoint dnstt fourni. La preuve que les octets
// traversent réellement toute la chaîne (client UDP -> dnstt -> backend
// TCP réel injecté par le composite -> vrai serveur SSH -> retour) est la
// réception, à travers le tunnel, de la bannière protocolaire SSH
// ("SSH-2.0-...") que seul un vrai serveur SSH émet à la connexion — pas
// une valeur fabriquée par le test.
func expectSSHBannerThroughTunnel(t *testing.T, dnsttAddr string, priv ed25519.PrivateKey, sessionTag string) {
	t.Helper()

	serverAddr, err := net.ResolveUDPAddr("udp", dnsttAddr)
	if err != nil {
		t.Fatalf("résolution endpoint dnstt %s : %v", dnsttAddr, err)
	}
	client, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("DialUDP : %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(6 * time.Second))

	var sessionID [8]byte
	copy(sessionID[:], []byte(sessionTag))
	signature := ed25519.Sign(priv, sessionID[:])

	q := buildDNSTTQuery(t, "t.example.com", sessionID, 1, signature)
	if _, err := client.Write(q); err != nil {
		t.Fatalf("envoi requête DNSTT : %v", err)
	}

	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
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
		if strings.HasPrefix(chunk, "SSH-2.0") {
			t.Logf("✓ CHAÎNAGE RÉEL DÉMONTRÉ : client UDP -> dnstt (%s) -> vrai serveur SSH -> bannière reçue à travers le tunnel : %q", dnsttAddr, chunk)
			return
		}
	}
	t.Fatal("bannière SSH jamais reçue à travers la chaîne dnstt->ssh : le chaînage ne transporte pas réellement les octets")
}

// TestCompositeEngineChainsDNSTTToRealSSHBackend démontre le chaînage réel
// mis en place en Phase 3 : CompositeEngine démarre d'abord le composant
// SSH, obtient son endpoint TCP RÉEL, l'injecte comme "backend" dans la
// config dnstt, puis démarre dnstt avec cette adresse réelle. Un client UDP
// parlant le protocole DNSTT envoie ensuite une requête authentifiée à
// travers ce chaînage jusqu'au vrai serveur SSH, et reçoit sa bannière en
// retour à travers le tunnel.
func TestCompositeEngineChainsDNSTTToRealSSHBackend(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}

	ce := newDNSTTSSHComposite(t)
	ctx := context.Background()
	rawCfg := sharedChainConfig(t, freeUDPPort(t), hex.EncodeToString(pub), t.TempDir())

	if err := ce.Configure(ctx, engine.EngineConfig{JSON: rawCfg}); err != nil {
		t.Fatalf("Configure : %v", err)
	}
	if err := ce.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}
	t.Cleanup(func() { ce.Stop() })

	realBackend := realSSHEndpoint(t, ce)
	assertWiredBackendOnDisk(t, realBackend.Addr)
	t.Logf("✓ câblage réel confirmé : dnstt.backend == %s (endpoint réel ssh, pas le placeholder 203.0.113.1:9)", realBackend.Addr)

	front := realDNSTTEndpoint(t, ce)
	expectSSHBannerThroughTunnel(t, front.Addr, priv, "chaintst")
}

// TestCompositeEngineRestartRewiresRealBackend démontre le Stop/Start
// propre du chaînage complet : après un cycle Stop->Start, le composite
// doit re-découvrir l'endpoint ssh réel (jamais réutiliser une valeur
// simplement supposée inchangée) et la chaîne complète doit rester
// fonctionnelle — pas seulement "démarrée sans erreur".
func TestCompositeEngineRestartRewiresRealBackend(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}

	ce := newDNSTTSSHComposite(t)
	ctx := context.Background()
	rawCfg := sharedChainConfig(t, freeUDPPort(t), hex.EncodeToString(pub), t.TempDir())

	if err := ce.Configure(ctx, engine.EngineConfig{JSON: rawCfg}); err != nil {
		t.Fatalf("Configure : %v", err)
	}
	if err := ce.Start(ctx); err != nil {
		t.Fatalf("Start : %v", err)
	}

	firstBackend := realSSHEndpoint(t, ce)
	assertWiredBackendOnDisk(t, firstBackend.Addr)
	expectSSHBannerThroughTunnel(t, realDNSTTEndpoint(t, ce).Addr, priv, "restart1")

	if err := ce.Stop(); err != nil {
		t.Fatalf("Stop : %v", err)
	}
	if err := ce.Start(ctx); err != nil {
		t.Fatalf("Start (2e fois, après Stop) : %v", err)
	}
	t.Cleanup(func() { ce.Stop() })

	secondBackend := realSSHEndpoint(t, ce)
	assertWiredBackendOnDisk(t, secondBackend.Addr)
	expectSSHBannerThroughTunnel(t, realDNSTTEndpoint(t, ce).Addr, priv, "restart2")

	t.Logf("✓ Stop puis Start propres : chaîne toujours fonctionnelle après redémarrage (backend ssh réel : %s -> %s)", firstBackend.Addr, secondBackend.Addr)
}
