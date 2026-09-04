package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// adminStoreArgs construit les arguments d'un store temporaire.
func adminStoreArgs(t *testing.T) (storePath string) {
	t.Helper()
	return filepath.Join(t.TempDir(), "store.json")
}

// reloadStore recharge le store depuis le disque : la CLI admin agit sur un
// store rechargé, l'instance précédente n'est donc plus à jour.
func reloadStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := LoadStore(path)
	if err != nil {
		t.Fatalf("reloadStore : %v", err)
	}
	return s
}

func TestAdminCreateAccount(t *testing.T) {
	path := adminStoreArgs(t)
	err := adminCreate([]string{"-store", path, "-id", "client1", "-password", "secret"})
	if err != nil {
		t.Fatalf("adminCreate : %v", err)
	}

	acc, ok := reloadStore(t, path).GetAccount("client1")
	if !ok {
		t.Fatal("le compte doit exister après adminCreate")
	}
	if acc.Password != "secret" {
		t.Fatalf("mot de passe incorrect : %q", acc.Password)
	}
	if !acc.Enabled {
		t.Fatal("le compte doit être actif par défaut")
	}
	if acc.Username != "client1" {
		t.Fatalf("username par défaut incorrect : %q", acc.Username)
	}
}

func TestAdminCreateMissingID(t *testing.T) {
	path := adminStoreArgs(t)
	err := adminCreate([]string{"-store", path})
	if err == nil {
		t.Fatal("adminCreate doit refuser un -id absent")
	}
}

func TestAdminCreateWithOfferParams(t *testing.T) {
	path := adminStoreArgs(t)
	err := adminCreate([]string{
		"-store", path,
		"-id", "client1",
		"-days", "30",
		"-quota", "1048576",
		"-max-conn", "3",
		"-max-ips", "2",
		"-enabled=false",
	})
	if err != nil {
		t.Fatalf("adminCreate : %v", err)
	}

	acc, _ := reloadStore(t, path).GetAccount("client1")
	if acc.ExpiresAt == "" {
		t.Fatal("une expiration doit être fixée après -days 30")
	}
	if acc.QuotaBytes != 1048576 {
		t.Fatalf("quota attendu 1048576, obtenu %d", acc.QuotaBytes)
	}
	if acc.MaxConnections != 3 || acc.MaxIPs != 2 {
		t.Fatalf("limites attendues 3/2, obtenu %d/%d", acc.MaxConnections, acc.MaxIPs)
	}
	if acc.Enabled {
		t.Fatal("le compte doit être désactivé avec -enabled=false")
	}
}

func TestAdminListAccounts(t *testing.T) {
	path := adminStoreArgs(t)
	s := reloadStore(t, path)
	s.CreateAccount(Account{ID: "zeta", Password: "p1", Enabled: true})
	s.CreateAccount(Account{ID: "alpha", Password: "p2", Enabled: true})

	if err := adminList([]string{"-store", path}); err != nil {
		t.Fatalf("adminList : %v", err)
	}
}

func TestAdminListEmpty(t *testing.T) {
	path := adminStoreArgs(t)
	if err := adminList([]string{"-store", path}); err != nil {
		t.Fatalf("adminList (vide) : %v", err)
	}
}

func TestAdminShowAccount(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "secret", Enabled: true})

	if err := adminShow([]string{"-store", path, "-id", "client1"}); err != nil {
		t.Fatalf("adminShow : %v", err)
	}
}

func TestAdminShowNotFound(t *testing.T) {
	path := adminStoreArgs(t)
	err := adminShow([]string{"-store", path, "-id", "inconnu"})
	if err != ErrAccountNotFound {
		t.Fatalf("attendu ErrAccountNotFound, obtenu %v", err)
	}
}

func TestAdminEnableDisable(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "p", Enabled: false})

	if err := adminSetEnabled([]string{"-store", path, "-id", "client1"}, true); err != nil {
		t.Fatalf("adminSetEnabled : %v", err)
	}
	acc, _ := reloadStore(t, path).GetAccount("client1")
	if !acc.Enabled {
		t.Fatal("le compte doit être activé")
	}

	if err := adminSetEnabled([]string{"-store", path, "-id", "client1"}, false); err != nil {
		t.Fatalf("adminSetEnabled(false) : %v", err)
	}
	acc, _ = reloadStore(t, path).GetAccount("client1")
	if acc.Enabled {
		t.Fatal("le compte doit être désactivé")
	}
}

func TestAdminRenew(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})

	if err := adminRenew([]string{"-store", path, "-id", "client1", "-days", "30"}); err != nil {
		t.Fatalf("adminRenew : %v", err)
	}

	acc, _ := reloadStore(t, path).GetAccount("client1")
	exp, err := time.Parse(time.RFC3339, acc.ExpiresAt)
	if err != nil {
		t.Fatalf("expiration invalide : %v", err)
	}
	if time.Until(exp) < 29*24*time.Hour {
		t.Fatalf("l'expiration doit être à ~30 jours, obtenu %v", time.Until(exp))
	}
}

func TestAdminSetQuota(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})

	if err := adminSetQuota([]string{"-store", path, "-id", "client1", "-quota", "2048"}); err != nil {
		t.Fatalf("adminSetQuota : %v", err)
	}
	acc, _ := reloadStore(t, path).GetAccount("client1")
	if acc.QuotaBytes != 2048 {
		t.Fatalf("quota attendu 2048, obtenu %d", acc.QuotaBytes)
	}
}

func TestAdminSetLimits(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})

	if err := adminSetLimits([]string{"-store", path, "-id", "client1", "-max-conn", "4", "-max-ips", "3"}); err != nil {
		t.Fatalf("adminSetLimits : %v", err)
	}
	acc, _ := reloadStore(t, path).GetAccount("client1")
	if acc.MaxConnections != 4 || acc.MaxIPs != 3 {
		t.Fatalf("limites attendues 4/3, obtenu %d/%d", acc.MaxConnections, acc.MaxIPs)
	}
}

func TestAdminSetPassword(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "ancien", Enabled: true})

	if err := adminSetPassword([]string{"-store", path, "-id", "client1", "-password", "nouveau"}); err != nil {
		t.Fatalf("adminSetPassword : %v", err)
	}
	acc, _ := reloadStore(t, path).GetAccount("client1")
	if acc.Password != "nouveau" {
		t.Fatalf("mot de passe attendu nouveau, obtenu %q", acc.Password)
	}
}

func TestAdminSetPasswordMissing(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})

	if err := adminSetPassword([]string{"-store", path, "-id", "client1"}); err == nil {
		t.Fatal("adminSetPassword doit refuser un -password absent")
	}
}

func TestAdminDelete(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})

	if err := adminDelete([]string{"-store", path, "-id", "client1"}); err != nil {
		t.Fatalf("adminDelete : %v", err)
	}
	if _, ok := reloadStore(t, path).GetAccount("client1"); ok {
		t.Fatal("le compte doit être supprimé")
	}
}

func TestAdminOfferAddAndList(t *testing.T) {
	path := adminStoreArgs(t)
	if err := adminOfferAdd([]string{
		"-store", path,
		"-id", "premium",
		"-name", "Premium",
		"-days", "30",
		"-quota", "1048576",
		"-max-conn", "2",
		"-max-ips", "1",
	}); err != nil {
		t.Fatalf("adminOfferAdd : %v", err)
	}

	s := reloadStore(t, path)
	offer, ok := s.GetOffer("premium")
	if !ok {
		t.Fatal("l'offre doit exister après adminOfferAdd")
	}
	if offer.Name != "Premium" || offer.DurationDays != 30 {
		t.Fatalf("offre incorrecte : %+v", offer)
	}

	if err := adminOfferList([]string{"-store", path}); err != nil {
		t.Fatalf("adminOfferList : %v", err)
	}
}

func TestAdminOfferAddMissingID(t *testing.T) {
	path := adminStoreArgs(t)
	if err := adminOfferAdd([]string{"-store", path}); err == nil {
		t.Fatal("adminOfferAdd doit refuser un -id absent")
	}
}

func TestAdminOfferDel(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateOffer(Offer{ID: "premium", Name: "Premium", DurationDays: 30})

	if err := adminOfferDel([]string{"-store", path, "-id", "premium"}); err != nil {
		t.Fatalf("adminOfferDel : %v", err)
	}
	if _, ok := reloadStore(t, path).GetOffer("premium"); ok {
		t.Fatal("l'offre doit être supprimée")
	}
}

func TestAdminSubscribe(t *testing.T) {
	path := adminStoreArgs(t)
	s := reloadStore(t, path)
	s.CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})
	s.CreateOffer(Offer{ID: "premium", Name: "Premium", DurationDays: 30, QuotaBytes: 4096, MaxConnections: 2, MaxIPs: 1})

	if err := adminSubscribe([]string{"-store", path, "-id", "client1", "-offer", "premium"}); err != nil {
		t.Fatalf("adminSubscribe : %v", err)
	}

	acc, _ := reloadStore(t, path).GetAccount("client1")
	if acc.OfferID != "premium" {
		t.Fatalf("offre attachée attendue premium, obtenu %q", acc.OfferID)
	}
	if acc.QuotaBytes != 4096 || acc.MaxConnections != 2 {
		t.Fatalf("paramètres de l'offre non appliqués : %+v", acc)
	}
}

func TestAdminSubscribeMissingArgs(t *testing.T) {
	path := adminStoreArgs(t)
	if err := adminSubscribe([]string{"-store", path}); err == nil {
		t.Fatal("adminSubscribe doit exiger -id et -offer")
	}
}

func TestAdminTokenNewRevokeLink(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})

	if err := adminTokenNew([]string{"-store", path, "-id", "client1", "-base", "https://srv.example"}); err != nil {
		t.Fatalf("adminTokenNew : %v", err)
	}

	acc, _ := reloadStore(t, path).GetAccount("client1")
	if acc.Token == "" {
		t.Fatal("un token doit être généré")
	}

	if err := adminLink([]string{"-store", path, "-id", "client1", "-base", "https://srv.example"}); err != nil {
		t.Fatalf("adminLink : %v", err)
	}

	if err := adminTokenRevoke([]string{"-store", path, "-id", "client1"}); err != nil {
		t.Fatalf("adminTokenRevoke : %v", err)
	}
	acc, _ = reloadStore(t, path).GetAccount("client1")
	if acc.Token != "" {
		t.Fatal("le token doit être révoqué")
	}
}

func TestAdminLinkWithoutToken(t *testing.T) {
	path := adminStoreArgs(t)
	reloadStore(t, path).CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})

	if err := adminLink([]string{"-store", path, "-id", "client1"}); err != nil {
		t.Fatalf("adminLink sans token : %v", err)
	}
}

func TestAdminLinkNotFound(t *testing.T) {
	path := adminStoreArgs(t)
	err := adminLink([]string{"-store", path, "-id", "inconnu"})
	if err != ErrAccountNotFound {
		t.Fatalf("attendu ErrAccountNotFound, obtenu %v", err)
	}
}

func TestFormatLink(t *testing.T) {
	if got := formatLink("", "TOKEN"); got != "/client/TOKEN" {
		t.Fatalf("chemin attendu /client/TOKEN, obtenu %q", got)
	}
	if got := formatLink("https://srv.example", "TOKEN"); got != "https://srv.example/client/TOKEN" {
		t.Fatalf("URL attendue, obtenue %q", got)
	}
	if got := formatLink("https://srv.example/", "TOKEN"); got != "https://srv.example/client/TOKEN" {
		t.Fatalf("slash final géré : obtenu %q", got)
	}
}

func TestRunAdminDispatch(t *testing.T) {
	path := adminStoreArgs(t)

	if err := runAdmin([]string{"create", "-store", path, "-id", "client1", "-password", "p"}); err != nil {
		t.Fatalf("runAdmin create : %v", err)
	}
	if err := runAdmin([]string{"list", "-store", path}); err != nil {
		t.Fatalf("runAdmin list : %v", err)
	}
	if err := runAdmin([]string{"help"}); err != nil {
		t.Fatalf("runAdmin help : %v", err)
	}
	if err := runAdmin(nil); err != nil {
		t.Fatalf("runAdmin sans argument : %v", err)
	}
}

func TestRunAdminUnknownCommand(t *testing.T) {
	err := runAdmin([]string{"frobnicate"})
	if err == nil {
		t.Fatal("une commande inconnue doit retourner une erreur")
	}
	if !strings.Contains(err.Error(), "inconnue") {
		t.Fatalf("message d'erreur attendu, obtenu %v", err)
	}
}

// ---------- Compléments store (couverture) ----------

func TestStoreListAccountsSorted(t *testing.T) {
	s := newTempStore(t)
	s.CreateAccount(Account{ID: "zeta", Password: "p1"})
	s.CreateAccount(Account{ID: "alpha", Password: "p2"})
	s.CreateAccount(Account{ID: "bravo", Password: "p3"})

	accounts := s.ListAccounts()
	if len(accounts) != 3 {
		t.Fatalf("3 comptes attendus, obtenu %d", len(accounts))
	}
	for i := 1; i < len(accounts); i++ {
		if accounts[i-1].ID >= accounts[i].ID {
			t.Fatalf("liste non triée : %v", accounts)
		}
	}
}

func TestStoreSetExpiry(t *testing.T) {
	s := newTempStore(t)
	s.CreateAccount(Account{ID: "client1", Password: "p"})

	exp := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	acc, err := s.SetExpiry("client1", exp)
	if err != nil {
		t.Fatalf("SetExpiry : %v", err)
	}
	if acc.ExpiresAt != exp {
		t.Fatalf("expiration attendue %q, obtenu %q", exp, acc.ExpiresAt)
	}

	got, _ := s.GetAccount("client1")
	if got.ExpiresAt != exp {
		t.Fatalf("expiration persistée incorrecte : %q", got.ExpiresAt)
	}

	if _, err := s.SetExpiry("absent", exp); err != ErrAccountNotFound {
		t.Fatalf("attendu ErrAccountNotFound, obtenu %v", err)
	}
}

func TestStoreOfferCount(t *testing.T) {
	s := newTempStore(t)
	if got := s.OfferCount(); got != 0 {
		t.Fatalf("0 offre attendue, obtenu %d", got)
	}

	s.CreateOffer(Offer{ID: "a", Name: "A"})
	s.CreateOffer(Offer{ID: "b", Name: "B"})
	if got := s.OfferCount(); got != 2 {
		t.Fatalf("2 offres attendues, obtenu %d", got)
	}
}

func TestStoreClientLinkPath(t *testing.T) {
	if got := ClientLinkPath("abc"); got != "/client/abc" {
		t.Fatalf("chemin attendu /client/abc, obtenu %q", got)
	}
	if got := ClientLinkPath(""); got != "/client/" {
		t.Fatalf("chemin token vide attendu /client/, obtenu %q", got)
	}
}

func TestStoreSaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "store.json")
	s, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore : %v", err)
	}
	if _, err := s.CreateAccount(Account{ID: "client1", Password: "p"}); err != nil {
		t.Fatalf("CreateAccount (dossier parent absent) : %v", err)
	}

	reloaded, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore (rechargement) : %v", err)
	}
	if _, ok := reloaded.GetAccount("client1"); !ok {
		t.Fatal("le compte doit persister dans le dossier parent créé")
	}
}