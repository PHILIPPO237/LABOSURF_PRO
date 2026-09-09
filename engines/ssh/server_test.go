package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// ============================================================
// Ce moteur utilise le vrai protocole SSH (golang.org/x/crypto/ssh), donc
// ces tests emploient un VRAI client SSH (le même package, côté client) —
// pas une reconstruction manuelle du protocole comme pour les autres
// moteurs DNS-tunnel de ce dépôt. Vraie négociation SSH, vraie
// authentification par clé publique ed25519, vrai sous-processus exécuté.
// ============================================================

func waitForAddr(t *testing.T, srv *Server) net.Addr {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if addr := srv.Addr(); addr != nil {
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("le serveur SSH n'a jamais démarré son écoute (Addr() toujours nil)")
	return nil
}

// dialSSH effectue un vrai handshake SSH + authentification par clé
// publique contre le serveur de test, et renvoie le client connecté.
func dialSSH(t *testing.T, addr net.Addr, username string, priv ed25519.PrivateKey) *gossh.Client {
	t.Helper()
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey : %v", err)
	}
	cfg := &gossh.ClientConfig{
		User:            username,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}
	client, err := gossh.Dial("tcp", addr.String(), cfg)
	if err != nil {
		t.Fatalf("Dial SSH : %v", err)
	}
	return client
}

func newTestSSHServer(t *testing.T, runAsUser string, users []SSHUser) *Server {
	t.Helper()
	cfg := SSHConfig{
		Port:      0,
		Dir:       t.TempDir(),
		Users:     users,
		RunAsUser: runAsUser,
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer : %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx) }()
	t.Cleanup(func() { srv.Close() })
	return srv
}

func makeSSHUser(t *testing.T, username string) (SSHUser, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey : %v", err)
	}
	_ = pub
	return SSHUser{
		Username:  username,
		PublicKey: hex.EncodeToString(signer.PublicKey().Marshal()),
		Enabled:   true,
	}, priv
}

// TestSSHRealPrivilegeDrop est la preuve directe que le correctif
// fonctionne : la commande "id -u" exécutée dans une session SSH doit
// renvoyer l'UID de l'utilisateur système "nobody" (65534, garanti présent
// sur Linux), PAS 0 (root, l'UID du process de test lui-même) — ce test
// tourne en root, donc si applySysProcAttr ne droppait rien (comme avant
// correction), "id -u" renverrait "0".
func TestSSHRealPrivilegeDrop(t *testing.T) {
	user, priv := makeSSHUser(t, "alice")
	srv := newTestSSHServer(t, "nobody", []SSHUser{user})
	addr := waitForAddr(t, srv)

	client := dialSSH(t, addr, "alice", priv)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession : %v", err)
	}
	defer session.Close()

	out, err := session.Output("id -u")
	if err != nil {
		t.Fatalf("exécution 'id -u' : %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "65534" {
		t.Fatalf("✗ DROP DE PRIVILÈGES NON EFFECTIF : attendu UID 65534 (nobody), obtenu %q", got)
	}
	t.Logf("✓ session shell tourne réellement sous UID %s (nobody), pas root", got)
}

// TestSSHFallbackWhenRunAsUserMissing vérifie qu'un RunAsUser introuvable
// sur la machine ne casse pas la session (elle continue avec les
// privilèges du process, comportement antérieur, plutôt que d'échouer).
func TestSSHFallbackWhenRunAsUserMissing(t *testing.T) {
	user, priv := makeSSHUser(t, "bob")
	srv := newTestSSHServer(t, "this-user-does-not-exist-xyz", []SSHUser{user})
	addr := waitForAddr(t, srv)

	client := dialSSH(t, addr, "bob", priv)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession : %v", err)
	}
	defer session.Close()

	out, err := session.Output("id -u")
	if err != nil {
		t.Fatalf("exécution 'id -u' : %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "0" {
		t.Fatalf("attendu repli sur privilèges du process (UID 0, le test tourne en root), obtenu %q", got)
	}
	t.Logf("✓ RunAsUser introuvable : session non cassée, repli sur les privilèges du process (UID %s)", got)
}

// TestSSHRejectsUnknownKey vérifie qu'une clé non enregistrée est refusée.
func TestSSHRejectsUnknownKey(t *testing.T) {
	user, _ := makeSSHUser(t, "carol")
	_, unknownPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("génération clé ed25519 : %v", err)
	}
	srv := newTestSSHServer(t, "nobody", []SSHUser{user})
	addr := waitForAddr(t, srv)

	signer, err := gossh.NewSignerFromKey(unknownPriv)
	if err != nil {
		t.Fatalf("NewSignerFromKey : %v", err)
	}
	cfg := &gossh.ClientConfig{
		User:            "carol",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}
	_, err = gossh.Dial("tcp", addr.String(), cfg)
	if err == nil {
		t.Fatal("attendu un refus d'authentification pour une clé inconnue")
	}
	t.Logf("✓ clé inconnue correctement refusée : %v", err)
}

// TestSSHStartStopRestart couvre démarrage / arrêt / redémarrage : un
// second serveur doit démarrer et accepter une connexion normalement après
// l'arrêt complet du premier.
func TestSSHStartStopRestart(t *testing.T) {
	user, priv := makeSSHUser(t, "dave")

	dir := t.TempDir()
	newSrv := func() *Server {
		cfg := SSHConfig{Port: 0, Dir: dir, Users: []SSHUser{user}, RunAsUser: "nobody"}
		srv, err := NewServer(cfg)
		if err != nil {
			t.Fatalf("NewServer : %v", err)
		}
		return srv
	}

	// Cycle 1 : démarrage, connexion, arrêt.
	srv1 := newSrv()
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() { _ = srv1.Run(ctx1) }()
	addr1 := waitForAddr(t, srv1)

	client1 := dialSSH(t, addr1, "dave", priv)
	if _, err := client1.NewSession(); err != nil {
		t.Fatalf("session cycle 1 : %v", err)
	}
	client1.Close()
	cancel1()

	done := make(chan error, 1)
	go func() { done <- srv1.Close() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("srv1.Close() n'est jamais revenu (deadlock à l'arrêt)")
	}

	// Cycle 2 ("redémarrage") : un nouveau serveur doit fonctionner
	// normalement après l'arrêt complet du premier.
	srv2 := newSrv()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = srv2.Run(ctx2) }()
	defer srv2.Close()
	addr2 := waitForAddr(t, srv2)

	client2 := dialSSH(t, addr2, "dave", priv)
	defer client2.Close()
	session2, err := client2.NewSession()
	if err != nil {
		t.Fatalf("session cycle 2 (après redémarrage) : %v", err)
	}
	out, err := session2.Output("echo restarted-ok")
	if err != nil {
		t.Fatalf("exécution après redémarrage : %v", err)
	}
	if strings.TrimSpace(string(out)) != "restarted-ok" {
		t.Fatalf("sortie inattendue après redémarrage : %q", out)
	}
	t.Logf("✓ cycle démarrage -> connexion -> arrêt -> redémarrage -> connexion fonctionnel")
}
