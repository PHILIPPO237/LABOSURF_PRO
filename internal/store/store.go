package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultStoreFilename est la base du fichier store dans le répertoire de
// données LABOSURF.
const DefaultStoreFilename = "users_db.json"

// StorePath retourne le chemin central du store. Le répertoire de données
// est surchargeable via LABOSURF_DATA_DIR (défaut /etc/labosurf).
func StorePath() string {
	dataDir := os.Getenv("LABOSURF_DATA_DIR")
	if dataDir == "" {
		dataDir = "/etc/labosurf"
	}
	return filepath.Join(dataDir, DefaultStoreFilename)
}

// storeData est la sérialisation persistée du store central.
type storeData struct {
	Accounts map[string]*Account `json:"accounts"`
	Offers   map[string]*Offer   `json:"offers"`
}

// Store gère la persistance et l'accès concurrent aux comptes/offres/grants.
type Store struct {
	path string
	mu   sync.RWMutex
	data storeData

	// index token -> accountID, reconstruit au chargement.
	tokens map[string]string
}

// LoadStore charge (ou initialise) le store depuis path. Un fichier absent
// donne un store vide (non écrit tant qu'aucune modification n'a lieu).
func LoadStore(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: storeData{
			Accounts: make(map[string]*Account),
			Offers:   make(map[string]*Offer),
		},
		tokens: make(map[string]string),
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("lecture du store %s : %w", path, err)
	}

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("store JSON invalide : %w", err)
		}
	}

	if s.data.Accounts == nil {
		s.data.Accounts = make(map[string]*Account)
	}
	if s.data.Offers == nil {
		s.data.Offers = make(map[string]*Offer)
	}

	s.rebuildTokenIndexLocked()

	return s, nil
}

func (s *Store) rebuildTokenIndexLocked() {
	s.tokens = make(map[string]string)
	for id, acc := range s.data.Accounts {
		if acc.Token != "" {
			s.tokens[acc.Token] = id
		}
	}
}

// saveLocked écrit le store de façon atomique (fichier temporaire + rename).
// L'appelant doit détenir le verrou en écriture.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}

	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("création du dossier %s : %w", dir, err)
		}
	}

	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("sérialisation du store : %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("écriture temporaire du store : %w", err)
	}

	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("remplacement atomique du store : %w", err)
	}

	return nil
}

// NormalizeID normalise un identifiant de compte (minuscules, sans espaces).
// Exportée pour permettre aux moteurs de reproduire la même normalisation.
func NormalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func normalizeID(id string) string {
	return NormalizeID(id)
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func randToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("génération aléatoire : %w", err)
	}
	return hex.EncodeToString(b), nil
}

func expiryFromDays(days int) string {
	if days <= 0 {
		return ""
	}
	return time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
}

// ExpiryFromDays calcule une date d'expiration RFC3339 à partir de maintenant
// + days jours. days <= 0 => pas d'expiration (chaîne vide). Exportée pour
// les moteurs qui créent des comptes.
func ExpiryFromDays(days int) string {
	return expiryFromDays(days)
}

// ---------- Comptes ----------

// CreateAccount crée un nouveau compte. Si Password est vide, un mot de
// passe aléatoire est généré. Username par défaut = ID.
func (s *Store) CreateAccount(acc Account) (Account, error) {
	id := normalizeID(acc.ID)
	if id == "" {
		return Account{}, ErrInvalidID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.data.Accounts[id]; exists {
		return Account{}, ErrAccountExists
	}

	if acc.Username == "" {
		acc.Username = id
	}

	if acc.Password == "" {
		pw, err := randToken(8)
		if err != nil {
			return Account{}, err
		}
		acc.Password = pw
	}

	if acc.MaxConnections <= 0 {
		acc.MaxConnections = 1
	}
	if acc.MaxIPs <= 0 {
		acc.MaxIPs = 1
	}

	if acc.Token == "" {
		tk, err := s.uniqueTokenLocked()
		if err != nil {
			return Account{}, err
		}
		acc.Token = tk
	}

	acc.ID = id
	acc.CreatedAt = nowRFC3339()
	acc.UpdatedAt = acc.CreatedAt

	stored := acc
	s.data.Accounts[id] = &stored

	if stored.Token != "" {
		s.tokens[stored.Token] = id
	}

	if err := s.saveLocked(); err != nil {
		delete(s.data.Accounts, id)
		return Account{}, err
	}

	return stored, nil
}

// GetAccount retourne une COPIE du compte (pas de mutation externe).
func (s *Store) GetAccount(id string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acc, ok := s.data.Accounts[normalizeID(id)]
	if !ok {
		return Account{}, false
	}
	return *acc, true
}

// ListAccounts retourne la liste des comptes, triée par ID.
func (s *Store) ListAccounts() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Account, 0, len(s.data.Accounts))
	for _, acc := range s.data.Accounts {
		out = append(out, *acc)
	}
	sortByID(out)

	return out
}

func sortByID(accs []Account) {
	// Petit tri par insertion : les stores sont de petite taille.
	for i := 1; i < len(accs); i++ {
		for j := i; j > 0 && accs[j].ID < accs[j-1].ID; j-- {
			accs[j], accs[j-1] = accs[j-1], accs[j]
		}
	}
}

// mutateAccount applique une mutation à un compte existant puis persiste.
func (s *Store) mutateAccount(id string, fn func(*Account)) (Account, error) {
	nid := normalizeID(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	acc, ok := s.data.Accounts[nid]
	if !ok {
		return Account{}, ErrAccountNotFound
	}

	before := *acc
	fn(acc)
	acc.ID = nid
	acc.UpdatedAt = nowRFC3339()

	if before.Token != acc.Token {
		if before.Token != "" {
			delete(s.tokens, before.Token)
		}
		if acc.Token != "" {
			s.tokens[acc.Token] = nid
		}
	}

	if err := s.saveLocked(); err != nil {
		*acc = before
		s.rebuildTokenIndexLocked()
		return Account{}, err
	}

	return *acc, nil
}

// DeleteAccount supprime un compte (révocation totale de l'accès).
func (s *Store) DeleteAccount(id string) error {
	nid := normalizeID(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	acc, ok := s.data.Accounts[nid]
	if !ok {
		return ErrAccountNotFound
	}

	token := acc.Token
	delete(s.data.Accounts, nid)
	if token != "" {
		delete(s.tokens, token)
	}

	if err := s.saveLocked(); err != nil {
		s.data.Accounts[nid] = acc
		if token != "" {
			s.tokens[token] = nid
		}
		return err
	}

	return nil
}

func (s *Store) SetEnabled(id string, enabled bool) (Account, error) {
	return s.mutateAccount(id, func(a *Account) { a.Enabled = enabled })
}

func (s *Store) SetPassword(id, password string) (Account, error) {
	return s.mutateAccount(id, func(a *Account) { a.Password = password })
}

func (s *Store) SetQuota(id string, quota uint64) (Account, error) {
	return s.mutateAccount(id, func(a *Account) { a.QuotaBytes = quota })
}

func (s *Store) SetLimits(id string, maxConn, maxIPs int) (Account, error) {
	return s.mutateAccount(id, func(a *Account) {
		if maxConn > 0 {
			a.MaxConnections = maxConn
		}
		if maxIPs > 0 {
			a.MaxIPs = maxIPs
		}
	})
}

// SetExpiry fixe explicitement la date d'expiration (RFC3339 ou vide).
func (s *Store) SetExpiry(id, expiresAt string) (Account, error) {
	return s.mutateAccount(id, func(a *Account) { a.ExpiresAt = expiresAt })
}

// Renew prolonge l'accès de `days` jours.
func (s *Store) Renew(id string, days int) (Account, error) {
	if days <= 0 {
		return Account{}, fmt.Errorf("nombre de jours invalide")
	}

	return s.mutateAccount(id, func(a *Account) {
		base := time.Now().UTC()
		if a.ExpiresAt != "" {
			if cur, err := time.Parse(time.RFC3339, a.ExpiresAt); err == nil && cur.After(base) {
				base = cur
			}
		}
		a.ExpiresAt = base.Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	})
}

// AddGrant rattache un compte à un moteur. Le grant ne doit pas déjà exister.
func (s *Store) AddGrant(accountID, engine string, cfg map[string]any) (Account, error) {
	return s.mutateAccount(accountID, func(a *Account) {
		if a.Grants == nil {
			a.Grants = make(map[string]*EngineGrant)
		}
		if _, exists := a.Grants[engine]; exists {
			return
		}
		a.Grants[engine] = &EngineGrant{
			Engine:  engine,
			Config:  cfg,
			Enabled: true,
		}
	})
}

// RemoveGrant détache un compte d'un moteur.
func (s *Store) RemoveGrant(accountID, engine string) (Account, error) {
	return s.mutateAccount(accountID, func(a *Account) {
		if a.Grants != nil {
			delete(a.Grants, engine)
		}
	})
}

// SetGrantEnabled active/désactive l'accès d'un compte à un moteur.
func (s *Store) SetGrantEnabled(accountID, engine string, enabled bool) (Account, error) {
	return s.mutateAccount(accountID, func(a *Account) {
		if a.Grants != nil {
			if g := a.Grants[engine]; g != nil {
				g.Enabled = enabled
			}
		}
	})
}

// SetGrantConfig remplace la configuration d'un compte pour un moteur.
func (s *Store) SetGrantConfig(accountID, engine string, cfg map[string]any) (Account, error) {
	return s.mutateAccount(accountID, func(a *Account) {
		if a.Grants != nil {
			if g := a.Grants[engine]; g != nil {
				g.Config = cfg
			}
		}
	})
}

// UserConfigs projette les comptes vers la map attendue par le moteur UDP
// (clé = Username). Un compte n'y figure QUE s'il a un grant UDP actif :
// c'est ainsi que « un compte peut se connecter à plusieurs moteurs » se
// traduit — seul le moteur auquel le compte a accès l'autorise.
func (s *Store) UserConfigs() map[string]UserConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]UserConfig, len(s.data.Accounts))
	for _, acc := range s.data.Accounts {
		// Le compte a l'accès UDP ? (grant UDP présent et activé)
		if acc.Grants == nil {
			continue
		}
		g, ok := acc.Grants[EngineUDP]
		if !ok || !g.Enabled {
			continue
		}
		username := acc.Username
		if username == "" {
			username = acc.ID
		}

		out[username] = UserConfig{
			Password:       acc.Password,
			ExpiresAt:      acc.ExpiresAt,
			QuotaBytes:     acc.QuotaBytes,
			MaxConnections: acc.MaxConnections,
			MaxIPs:         acc.MaxIPs,
			Enabled:        acc.Enabled,
		}
	}

	return out
}

// Count retourne le nombre de comptes enregistrés.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data.Accounts)
}

// ---------- Offres et abonnements ----------

func (s *Store) CreateOffer(o Offer) (Offer, error) {
	id := normalizeID(o.ID)
	if id == "" {
		return Offer{}, ErrInvalidID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.data.Offers[id]; exists {
		return Offer{}, ErrOfferExists
	}

	o.ID = id
	if o.MaxConnections <= 0 {
		o.MaxConnections = 1
	}
	if o.MaxIPs <= 0 {
		o.MaxIPs = 1
	}

	stored := o
	s.data.Offers[id] = &stored

	if err := s.saveLocked(); err != nil {
		delete(s.data.Offers, id)
		return Offer{}, err
	}

	return stored, nil
}

func (s *Store) GetOffer(id string) (Offer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	o, ok := s.data.Offers[normalizeID(id)]
	if !ok {
		return Offer{}, false
	}
	return *o, true
}

func (s *Store) ListOffers() []Offer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Offer, 0, len(s.data.Offers))
	for _, o := range s.data.Offers {
		out = append(out, *o)
	}

	// Tri par ID.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}

	return out
}

func (s *Store) DeleteOffer(id string) error {
	nid := normalizeID(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.data.Offers[nid]
	if !ok {
		return ErrOfferNotFound
	}

	delete(s.data.Offers, nid)

	if err := s.saveLocked(); err != nil {
		s.data.Offers[nid] = o
		return err
	}

	return nil
}

// Subscribe rattache un compte à une offre et applique ses paramètres.
func (s *Store) Subscribe(accountID, offerID string) (Account, error) {
	nid := normalizeID(accountID)
	oid := normalizeID(offerID)

	s.mu.Lock()
	defer s.mu.Unlock()

	offer, ok := s.data.Offers[oid]
	if !ok {
		return Account{}, ErrOfferNotFound
	}

	acc, ok := s.data.Accounts[nid]
	if !ok {
		return Account{}, ErrAccountNotFound
	}

	before := *acc

	acc.OfferID = offer.ID
	acc.ExpiresAt = expiryFromDays(offer.DurationDays)
	acc.QuotaBytes = offer.QuotaBytes
	acc.MaxConnections = offer.MaxConnections
	acc.MaxIPs = offer.MaxIPs
	if acc.MaxConnections <= 0 {
		acc.MaxConnections = 1
	}
	if acc.MaxIPs <= 0 {
		acc.MaxIPs = 1
	}
	acc.UpdatedAt = nowRFC3339()

	if err := s.saveLocked(); err != nil {
		*acc = before
		return Account{}, err
	}

	return *acc, nil
}

// OfferCount retourne le nombre d'offres enregistrées.
func (s *Store) OfferCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data.Offers)
}

// ---------- Liens clients ----------
//
// Chaque compte possède un token opaque serv de lien client unique vers le
// portail HTTP. Un token ne référence qu'UN SEUL compte.

func (s *Store) uniqueTokenLocked() (string, error) {
	for {
		tk, err := randToken(32)
		if err != nil {
			return "", err
		}
		if _, exists := s.tokens[tk]; !exists {
			return tk, nil
		}
	}
}

// GenerateToken (ré)génère le lien client d'un compte.
func (s *Store) GenerateToken(accountID string) (Account, error) {
	nid := normalizeID(accountID)

	s.mu.Lock()
	defer s.mu.Unlock()

	acc, ok := s.data.Accounts[nid]
	if !ok {
		return Account{}, ErrAccountNotFound
	}

	token, err := s.uniqueTokenLocked()
	if err != nil {
		return Account{}, err
	}

	before := *acc

	if before.Token != "" {
		delete(s.tokens, before.Token)
	}
	acc.Token = token
	acc.UpdatedAt = nowRFC3339()
	s.tokens[token] = nid

	if err := s.saveLocked(); err != nil {
		*acc = before
		s.rebuildTokenIndexLocked()
		return Account{}, err
	}

	return *acc, nil
}

// RevokeToken supprime le lien client d'un compte.
func (s *Store) RevokeToken(accountID string) (Account, error) {
	nid := normalizeID(accountID)

	s.mu.Lock()
	defer s.mu.Unlock()

	acc, ok := s.data.Accounts[nid]
	if !ok {
		return Account{}, ErrAccountNotFound
	}

	before := *acc

	if acc.Token != "" {
		delete(s.tokens, acc.Token)
	}
	acc.Token = ""
	acc.UpdatedAt = nowRFC3339()

	if err := s.saveLocked(); err != nil {
		*acc = before
		s.rebuildTokenIndexLocked()
		return Account{}, err
	}

	return *acc, nil
}

// GetByToken résout un token vers son compte (copie défensive).
func (s *Store) GetByToken(token string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if token == "" {
		return Account{}, false
	}

	id, ok := s.tokens[token]
	if !ok {
		return Account{}, false
	}

	acc, ok := s.data.Accounts[id]
	if !ok {
		return Account{}, false
	}

	return *acc, true
}

// UpdateUsedBytes met à jour atomiquement le compteur de trafic d'un compte
// (incrément ou remplacement absolu). Retourne la nouvelle valeur.
func (s *Store) UpdateUsedBytes(accountID string, delta int64) (int64, error) {
	nid := normalizeID(accountID)

	s.mu.Lock()
	defer s.mu.Unlock()

	acc, ok := s.data.Accounts[nid]
	if !ok {
		return 0, ErrAccountNotFound
	}

	acc.UsedBytes += delta
	if acc.UsedBytes < 0 {
		acc.UsedBytes = 0
	}
	acc.UpdatedAt = nowRFC3339()

	if err := s.saveLocked(); err != nil {
		return 0, err
	}

	return acc.UsedBytes, nil
}

// SetUsedBytes remplace directement la valeur du compteur de trafic.
func (s *Store) SetUsedBytes(accountID string, value int64) (int64, error) {
	nid := normalizeID(accountID)

	s.mu.Lock()
	defer s.mu.Unlock()

	acc, ok := s.data.Accounts[nid]
	if !ok {
		return 0, ErrAccountNotFound
	}

	acc.UsedBytes = value
	if acc.UsedBytes < 0 {
		acc.UsedBytes = 0
	}
	acc.UpdatedAt = nowRFC3339()

	if err := s.saveLocked(); err != nil {
		return 0, err
	}

	return acc.UsedBytes, nil
}

// ClientLinkPath retourne le chemin du portail pour un token donné.
func ClientLinkPath(token string) string {
	return "/client/" + token
}