package main

import (
	"path/filepath"
	"testing"
	"time"
)

func newTempStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore : %v", err)
	}
	return s
}

func TestStoreCreateAndGet(t *testing.T) {
	s := newTempStore(t)

	acc, err := s.CreateAccount(Account{ID: "Client1", Enabled: true})
	if err != nil {
		t.Fatalf("CreateAccount : %v", err)
	}

	// Normalisation de l'ID.
	if acc.ID != "client1" {
		t.Fatalf("ID normalisé attendu client1, obtenu %q", acc.ID)
	}

	// Username par défaut = ID.
	if acc.Username != "client1" {
		t.Fatalf("Username par défaut attendu client1, obtenu %q", acc.Username)
	}

	// Mot de passe généré automatiquement.
	if acc.Password == "" {
		t.Fatal("un mot de passe doit être généré si absent")
	}

	// Limites par défaut.
	if acc.MaxConnections != 1 || acc.MaxIPs != 1 {
		t.Fatalf("limites par défaut attendues 1/1, obtenu %d/%d", acc.MaxConnections, acc.MaxIPs)
	}

	got, ok := s.GetAccount("CLIENT1")
	if !ok {
		t.Fatal("GetAccount insensible à la casse attendu")
	}
	if got.ID != "client1" {
		t.Fatalf("compte incorrect : %+v", got)
	}
}

func TestStoreCreateDuplicate(t *testing.T) {
	s := newTempStore(t)

	if _, err := s.CreateAccount(Account{ID: "client1"}); err != nil {
		t.Fatalf("CreateAccount : %v", err)
	}

	if _, err := s.CreateAccount(Account{ID: "client1"}); err != ErrAccountExists {
		t.Fatalf("attendu ErrAccountExists, obtenu %v", err)
	}
}

func TestStoreCreateInvalidID(t *testing.T) {
	s := newTempStore(t)
	if _, err := s.CreateAccount(Account{ID: "   "}); err != ErrInvalidID {
		t.Fatalf("attendu ErrInvalidID, obtenu %v", err)
	}
}

func TestStorePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")

	s1, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore : %v", err)
	}

	if _, err := s1.CreateAccount(Account{
		ID:         "client1",
		Password:   "secret",
		QuotaBytes: 1000,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("CreateAccount : %v", err)
	}

	// Rechargement depuis le disque.
	s2, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore (2) : %v", err)
	}

	acc, ok := s2.GetAccount("client1")
	if !ok {
		t.Fatal("le compte doit persister sur disque")
	}
	if acc.Password != "secret" || acc.QuotaBytes != 1000 {
		t.Fatalf("données persistées incorrectes : %+v", acc)
	}
}

func TestStoreGetReturnsCopy(t *testing.T) {
	s := newTempStore(t)
	s.CreateAccount(Account{ID: "client1", Password: "p", Enabled: true})

	acc, _ := s.GetAccount("client1")
	acc.Password = "modifié"

	again, _ := s.GetAccount("client1")
	if again.Password != "p" {
		t.Fatal("GetAccount doit retourner une copie défensive")
	}
}

func TestStoreUpdateOperations(t *testing.T) {
	s := newTempStore(t)
	s.CreateAccount(Account{ID: "client1", Enabled: true})

	if acc, err := s.SetEnabled("client1", false); err != nil || acc.Enabled {
		t.Fatalf("SetEnabled(false) : %+v %v", acc, err)
	}
	if acc, err := s.SetQuota("client1", 5000); err != nil || acc.QuotaBytes != 5000 {
		t.Fatalf("SetQuota : %+v %v", acc, err)
	}
	if acc, err := s.SetLimits("client1", 5, 3); err != nil || acc.MaxConnections != 5 || acc.MaxIPs != 3 {
		t.Fatalf("SetLimits : %+v %v", acc, err)
	}
	if acc, err := s.SetPassword("client1", "nouveau"); err != nil || acc.Password != "nouveau" {
		t.Fatalf("SetPassword : %+v %v", acc, err)
	}
}

func TestStoreUpdateNotFound(t *testing.T) {
	s := newTempStore(t)
	if _, err := s.SetEnabled("absent", true); err != ErrAccountNotFound {
		t.Fatalf("attendu ErrAccountNotFound, obtenu %v", err)
	}
}

func TestStoreRenew(t *testing.T) {
	s := newTempStore(t)
	s.CreateAccount(Account{ID: "client1", Enabled: true}) // sans expiration

	acc, err := s.Renew("client1", 10)
	if err != nil {
		t.Fatalf("Renew : %v", err)
	}

	exp, err := time.Parse(time.RFC3339, acc.ExpiresAt)
	if err != nil {
		t.Fatalf("ExpiresAt illisible : %v", err)
	}

	expected := time.Now().Add(10 * 24 * time.Hour)
	if exp.Before(expected.Add(-2*time.Minute)) || exp.After(expected.Add(2*time.Minute)) {
		t.Fatalf("expiration ~10j attendue, obtenu %s", acc.ExpiresAt)
	}

	// Un second renew part de l'expiration courante (cumul).
	acc2, err := s.Renew("client1", 5)
	if err != nil {
		t.Fatalf("Renew (2) : %v", err)
	}
	exp2, _ := time.Parse(time.RFC3339, acc2.ExpiresAt)
	if !exp2.After(exp.Add(4 * 24 * time.Hour)) {
		t.Fatalf("le renew doit cumuler ; obtenu %s puis %s", acc.ExpiresAt, acc2.ExpiresAt)
	}
}

func TestStoreDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	s, _ := LoadStore(path)
	s.CreateAccount(Account{ID: "client1", Enabled: true})

	if err := s.DeleteAccount("client1"); err != nil {
		t.Fatalf("DeleteAccount : %v", err)
	}
	if _, ok := s.GetAccount("client1"); ok {
		t.Fatal("le compte doit être supprimé")
	}

	// La suppression est persistée.
	s2, _ := LoadStore(path)
	if _, ok := s2.GetAccount("client1"); ok {
		t.Fatal("la suppression doit persister sur disque")
	}
}

func TestStoreUserConfigs(t *testing.T) {
	s := newTempStore(t)
	s.CreateAccount(Account{ID: "actif", Password: "p1", QuotaBytes: 10, MaxConnections: 2, MaxIPs: 3, Enabled: true})
	s.CreateAccount(Account{ID: "inactif", Password: "p2", Enabled: false})

	users := s.UserConfigs()

	if len(users) != 2 {
		t.Fatalf("2 UserConfig attendus, obtenu %d", len(users))
	}

	actif, ok := users["actif"]
	if !ok {
		t.Fatal("compte 'actif' manquant dans UserConfigs")
	}
	if actif.Password != "p1" || actif.QuotaBytes != 10 || actif.MaxConnections != 2 || actif.MaxIPs != 3 || !actif.Enabled {
		t.Fatalf("UserConfig 'actif' incorrect : %+v", actif)
	}

	inactif, ok := users["inactif"]
	if !ok || inactif.Enabled {
		t.Fatalf("le compte désactivé doit apparaître avec Enabled=false : %+v", inactif)
	}
}

func TestStoreLoadMissingFile(t *testing.T) {
	// Un fichier absent doit donner un store vide, sans erreur.
	s, err := LoadStore(filepath.Join(t.TempDir(), "inexistant.json"))
	if err != nil {
		t.Fatalf("LoadStore fichier absent : %v", err)
	}
	if s.Count() != 0 {
		t.Fatalf("store vide attendu, obtenu %d comptes", s.Count())
	}
}

func TestStoreOfferCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	s, _ := LoadStore(path)

	o, err := s.CreateOffer(Offer{ID: "PREMIUM", Name: "Premium 30j", DurationDays: 30, QuotaBytes: 1000, MaxConnections: 3, MaxIPs: 2})
	if err != nil {
		t.Fatalf("CreateOffer : %v", err)
	}
	if o.ID != "premium" {
		t.Fatalf("ID d'offre normalisé attendu premium, obtenu %q", o.ID)
	}

	if _, err := s.CreateOffer(Offer{ID: "premium"}); err != ErrOfferExists {
		t.Fatalf("attendu ErrOfferExists, obtenu %v", err)
	}

	got, ok := s.GetOffer("PREMIUM")
	if !ok || got.DurationDays != 30 {
		t.Fatalf("GetOffer incorrect : %+v ok=%v", got, ok)
	}

	if len(s.ListOffers()) != 1 {
		t.Fatalf("1 offre attendue, obtenu %d", len(s.ListOffers()))
	}

	// Persistance.
	s2, _ := LoadStore(path)
	if _, ok := s2.GetOffer("premium"); !ok {
		t.Fatal("l'offre doit persister sur disque")
	}

	if err := s.DeleteOffer("premium"); err != nil {
		t.Fatalf("DeleteOffer : %v", err)
	}
	if _, ok := s.GetOffer("premium"); ok {
		t.Fatal("l'offre doit être supprimée")
	}
}

func TestStoreSubscribeAppliesOfferParams(t *testing.T) {
	s := newTempStore(t)

	s.CreateOffer(Offer{ID: "premium", DurationDays: 30, QuotaBytes: 2000, MaxConnections: 4, MaxIPs: 3})
	s.CreateAccount(Account{ID: "client1", Enabled: true})

	acc, err := s.Subscribe("client1", "premium")
	if err != nil {
		t.Fatalf("Subscribe : %v", err)
	}

	if acc.OfferID != "premium" {
		t.Fatalf("OfferID attendu premium, obtenu %q", acc.OfferID)
	}
	if acc.QuotaBytes != 2000 || acc.MaxConnections != 4 || acc.MaxIPs != 3 {
		t.Fatalf("paramètres d'offre non appliqués : %+v", acc)
	}

	exp, err := time.Parse(time.RFC3339, acc.ExpiresAt)
	if err != nil {
		t.Fatalf("ExpiresAt illisible : %v", err)
	}
	expected := time.Now().Add(30 * 24 * time.Hour)
	if exp.Before(expected.Add(-2*time.Minute)) || exp.After(expected.Add(2*time.Minute)) {
		t.Fatalf("expiration ~30j attendue, obtenu %s", acc.ExpiresAt)
	}

	// Le moteur UDP Engine consomme bien ces paramètres via UserConfigs.
	uc := s.UserConfigs()["client1"]
	if uc.QuotaBytes != 2000 || uc.MaxConnections != 4 || uc.MaxIPs != 3 {
		t.Fatalf("UserConfig incohérent avec l'abonnement : %+v", uc)
	}
}

func TestStoreSubscribeErrors(t *testing.T) {
	s := newTempStore(t)
	s.CreateAccount(Account{ID: "client1", Enabled: true})
	s.CreateOffer(Offer{ID: "premium", DurationDays: 30})

	if _, err := s.Subscribe("client1", "inconnue"); err != ErrOfferNotFound {
		t.Fatalf("attendu ErrOfferNotFound, obtenu %v", err)
	}
	if _, err := s.Subscribe("inconnu", "premium"); err != ErrAccountNotFound {
		t.Fatalf("attendu ErrAccountNotFound, obtenu %v", err)
	}
}

func TestStoreTokenAutoGenerated(t *testing.T) {
	s := newTempStore(t)
	acc, _ := s.CreateAccount(Account{ID: "client1", Password: "secret", Enabled: true})

	if acc.Token == "" {
		t.Fatal("un token doit être généré à la création")
	}
	if acc.Token == acc.Password {
		t.Fatal("le token ne doit JAMAIS être le mot de passe")
	}
	if len(acc.Token) < 32 {
		t.Fatalf("token trop court (peu d'entropie) : %q", acc.Token)
	}
}

func TestStoreGetByTokenIsolation(t *testing.T) {
	s := newTempStore(t)
	a, _ := s.CreateAccount(Account{ID: "clientA", Enabled: true})
	b, _ := s.CreateAccount(Account{ID: "clientB", Enabled: true})

	if a.Token == b.Token {
		t.Fatal("deux comptes ne doivent pas partager le même token")
	}

	got, ok := s.GetByToken(a.Token)
	if !ok || got.ID != "clienta" {
		t.Fatalf("le token de A doit résoudre vers A, obtenu %+v ok=%v", got, ok)
	}

	got, ok = s.GetByToken(b.Token)
	if !ok || got.ID != "clientb" {
		t.Fatalf("le token de B doit résoudre vers B, obtenu %+v ok=%v", got, ok)
	}

	// Isolation : le token de A ne donne jamais accès à B.
	got, _ = s.GetByToken(a.Token)
	if got.ID == "clientb" {
		t.Fatal("isolation violée : le token de A donne accès à B")
	}

	if _, ok := s.GetByToken("inexistant"); ok {
		t.Fatal("un token inconnu ne doit rien résoudre")
	}
	if _, ok := s.GetByToken(""); ok {
		t.Fatal("un token vide ne doit rien résoudre")
	}
}

func TestStoreRegenerateInvalidatesOldToken(t *testing.T) {
	s := newTempStore(t)
	acc, _ := s.CreateAccount(Account{ID: "client1", Enabled: true})
	old := acc.Token

	acc2, err := s.GenerateToken("client1")
	if err != nil {
		t.Fatalf("GenerateToken : %v", err)
	}
	if acc2.Token == old {
		t.Fatal("le nouveau token doit différer de l'ancien")
	}

	if _, ok := s.GetByToken(old); ok {
		t.Fatal("l'ancien token doit être invalidé après régénération")
	}
	if _, ok := s.GetByToken(acc2.Token); !ok {
		t.Fatal("le nouveau token doit résoudre")
	}
}

func TestStoreRevokeToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	s, _ := LoadStore(path)
	acc, _ := s.CreateAccount(Account{ID: "client1", Enabled: true})
	tok := acc.Token

	acc2, err := s.RevokeToken("client1")
	if err != nil {
		t.Fatalf("RevokeToken : %v", err)
	}
	if acc2.Token != "" {
		t.Fatal("le token doit être vidé après révocation")
	}
	if _, ok := s.GetByToken(tok); ok {
		t.Fatal("un token révoqué ne doit plus résoudre")
	}

	// La révocation persiste sur disque.
	s2, _ := LoadStore(path)
	if _, ok := s2.GetByToken(tok); ok {
		t.Fatal("la révocation doit persister")
	}
}

func TestStoreTokenIndexRebuiltOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	s, _ := LoadStore(path)
	acc, _ := s.CreateAccount(Account{ID: "client1", Enabled: true})
	tok := acc.Token

	// Rechargement depuis le disque : l'index token doit être reconstruit.
	s2, _ := LoadStore(path)
	got, ok := s2.GetByToken(tok)
	if !ok || got.ID != "client1" {
		t.Fatal("l'index token doit être reconstruit au chargement")
	}
}
