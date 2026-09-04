package main

import (
	"flag"
	"fmt"
	"strings"
)

const defaultStorePath = "/etc/labosurf/users_db.json"

// runAdmin est le point d'entrée de la couche d'administration en ligne de
// commande. Elle agit sur le store (source de vérité unique).
//
//	labosurf admin <commande> [options]
func runAdmin(args []string) error {
	if len(args) == 0 {
		printAdminUsage()
		return nil
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "create":
		return adminCreate(rest)
	case "list":
		return adminList(rest)
	case "show":
		return adminShow(rest)
	case "enable":
		return adminSetEnabled(rest, true)
	case "disable":
		return adminSetEnabled(rest, false)
	case "renew":
		return adminRenew(rest)
	case "set-quota":
		return adminSetQuota(rest)
	case "set-limits":
		return adminSetLimits(rest)
	case "set-password":
		return adminSetPassword(rest)
	case "delete":
		return adminDelete(rest)
	case "offer-add":
		return adminOfferAdd(rest)
	case "offer-list":
		return adminOfferList(rest)
	case "offer-del":
		return adminOfferDel(rest)
	case "subscribe":
		return adminSubscribe(rest)
	case "token-new":
		return adminTokenNew(rest)
	case "token-revoke":
		return adminTokenRevoke(rest)
	case "link":
		return adminLink(rest)
	case "help", "-h", "--help":
		printAdminUsage()
		return nil
	default:
		printAdminUsage()
		return fmt.Errorf("commande admin inconnue : %s", cmd)
	}
}

func printAdminUsage() {
	fmt.Println("LABOSURF ADMIN — gestion des comptes (source de vérité : store)")
	fmt.Println()
	fmt.Println("Utilisation :")
	fmt.Println("  labosurf admin <commande> [options]")
	fmt.Println()
	fmt.Println("Commandes :")
	fmt.Println("  create        Créer un compte client")
	fmt.Println("  list          Lister les comptes")
	fmt.Println("  show          Afficher un compte")
	fmt.Println("  enable        Activer un compte")
	fmt.Println("  disable       Désactiver un compte")
	fmt.Println("  renew         Prolonger l'accès (jours)")
	fmt.Println("  set-quota     Modifier le quota (octets)")
	fmt.Println("  set-limits    Modifier MaxConnections / MaxIPs")
	fmt.Println("  set-password  Modifier le mot de passe")
	fmt.Println("  delete        Supprimer / révoquer un compte")
	fmt.Println()
	fmt.Println("  offer-add     Créer une offre")
	fmt.Println("  offer-list    Lister les offres")
	fmt.Println("  offer-del     Supprimer une offre")
	fmt.Println("  subscribe     Rattacher un compte à une offre")
	fmt.Println()
	fmt.Println("  token-new     (Re)générer le lien client d'un compte")
	fmt.Println("  token-revoke  Révoquer le lien client d'un compte")
	fmt.Println("  link          Afficher le lien client (URL)")
	fmt.Println()
	fmt.Println("Option commune : -store <chemin>  (défaut : store.json)")
	fmt.Println()
	fmt.Println("Licences : voir « labosurf license help »")
}

// openStoreFlag ajoute et lit l'option -store commune.
func storeFlag(fs *flag.FlagSet) *string {
	return fs.String("store", defaultStorePath, "chemin du fichier store")
}

func adminCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte (obligatoire)")
	password := fs.String("password", "", "mot de passe (généré si vide)")
	days := fs.Int("days", 0, "durée de validité en jours (0 = illimité)")
	quota := fs.Uint64("quota", 0, "quota en octets (0 = illimité)")
	maxConn := fs.Int("max-conn", 1, "connexions simultanées maximum")
	maxIPs := fs.Int("max-ips", 1, "adresses IP distinctes maximum")
	enabled := fs.Bool("enabled", true, "compte actif")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("-id est obligatoire")
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, err := s.CreateAccount(Account{
		ID:             *id,
		Password:       *password,
		ExpiresAt:      expiryFromDays(*days),
		QuotaBytes:     *quota,
		MaxConnections: *maxConn,
		MaxIPs:         *maxIPs,
		Enabled:        *enabled,
	})
	if err != nil {
		return err
	}

	fmt.Println("✔ Compte créé.")
	printAccount(acc)
	return nil
}

func adminList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	store := storeFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	accounts := s.ListAccounts()
	if len(accounts) == 0 {
		fmt.Println("(aucun compte)")
		return nil
	}

	fmt.Printf("%-16s %-8s %-20s %-12s %-6s %-6s\n",
		"ID", "ÉTAT", "EXPIRATION", "QUOTA", "CONN", "IPS")
	fmt.Println(strings.Repeat("-", 74))

	for _, a := range accounts {
		state := "actif"
		if !a.Enabled {
			state = "inactif"
		}
		exp := a.ExpiresAt
		if exp == "" {
			exp = "illimité"
		}
		quota := "illimité"
		if a.QuotaBytes > 0 {
			quota = FormatBytes(a.QuotaBytes)
		}
		fmt.Printf("%-16s %-8s %-20s %-12s %-6d %-6d\n",
			a.ID, state, exp, quota, a.MaxConnections, a.MaxIPs)
	}

	return nil
}

func adminShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, ok := s.GetAccount(*id)
	if !ok {
		return ErrAccountNotFound
	}

	printAccount(acc)
	return nil
}

func adminSetEnabled(args []string, enabled bool) error {
	name := "enable"
	if !enabled {
		name = "disable"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, err := s.SetEnabled(*id, enabled)
	if err != nil {
		return err
	}

	if enabled {
		fmt.Println("✔ Compte activé.")
	} else {
		fmt.Println("✔ Compte désactivé.")
	}
	printAccount(acc)
	return nil
}

func adminRenew(args []string) error {
	fs := flag.NewFlagSet("renew", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	days := fs.Int("days", 0, "jours à ajouter")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, err := s.Renew(*id, *days)
	if err != nil {
		return err
	}

	fmt.Printf("✔ Accès prolongé jusqu'au %s.\n", acc.ExpiresAt)
	printAccount(acc)
	return nil
}

func adminSetQuota(args []string) error {
	fs := flag.NewFlagSet("set-quota", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	quota := fs.Uint64("quota", 0, "quota en octets (0 = illimité)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, err := s.SetQuota(*id, *quota)
	if err != nil {
		return err
	}

	fmt.Println("✔ Quota mis à jour.")
	printAccount(acc)
	return nil
}

func adminSetLimits(args []string) error {
	fs := flag.NewFlagSet("set-limits", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	maxConn := fs.Int("max-conn", 0, "connexions simultanées maximum")
	maxIPs := fs.Int("max-ips", 0, "adresses IP distinctes maximum")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, err := s.SetLimits(*id, *maxConn, *maxIPs)
	if err != nil {
		return err
	}

	fmt.Println("✔ Limites mises à jour.")
	printAccount(acc)
	return nil
}

func adminSetPassword(args []string) error {
	fs := flag.NewFlagSet("set-password", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	password := fs.String("password", "", "nouveau mot de passe")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*password) == "" {
		return fmt.Errorf("-password est obligatoire")
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, err := s.SetPassword(*id, *password)
	if err != nil {
		return err
	}

	fmt.Println("✔ Mot de passe mis à jour.")
	printAccount(acc)
	return nil
}

func adminDelete(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	if err := s.DeleteAccount(*id); err != nil {
		return err
	}

	fmt.Printf("✔ Compte '%s' supprimé / révoqué.\n", normalizeID(*id))
	return nil
}

func adminOfferAdd(args []string) error {
	fs := flag.NewFlagSet("offer-add", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant de l'offre")
	name := fs.String("name", "", "nom de l'offre")
	days := fs.Int("days", 0, "durée en jours (0 = illimité)")
	quota := fs.Uint64("quota", 0, "quota en octets (0 = illimité)")
	maxConn := fs.Int("max-conn", 1, "connexions simultanées maximum")
	maxIPs := fs.Int("max-ips", 1, "adresses IP distinctes maximum")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("-id est obligatoire")
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	offer, err := s.CreateOffer(Offer{
		ID:             *id,
		Name:           *name,
		DurationDays:   *days,
		QuotaBytes:     *quota,
		MaxConnections: *maxConn,
		MaxIPs:         *maxIPs,
	})
	if err != nil {
		return err
	}

	fmt.Println("✔ Offre créée.")
	printOffer(offer)
	return nil
}

func adminOfferList(args []string) error {
	fs := flag.NewFlagSet("offer-list", flag.ContinueOnError)
	store := storeFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	offers := s.ListOffers()
	if len(offers) == 0 {
		fmt.Println("(aucune offre)")
		return nil
	}

	fmt.Printf("%-16s %-20s %-8s %-12s %-6s %-6s\n",
		"ID", "NOM", "DURÉE", "QUOTA", "CONN", "IPS")
	fmt.Println(strings.Repeat("-", 74))

	for _, o := range offers {
		dur := "illimité"
		if o.DurationDays > 0 {
			dur = fmt.Sprintf("%dj", o.DurationDays)
		}
		quota := "illimité"
		if o.QuotaBytes > 0 {
			quota = FormatBytes(o.QuotaBytes)
		}
		fmt.Printf("%-16s %-20s %-8s %-12s %-6d %-6d\n",
			o.ID, o.Name, dur, quota, o.MaxConnections, o.MaxIPs)
	}

	return nil
}

func adminOfferDel(args []string) error {
	fs := flag.NewFlagSet("offer-del", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant de l'offre")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	if err := s.DeleteOffer(*id); err != nil {
		return err
	}

	fmt.Printf("✔ Offre '%s' supprimée.\n", normalizeID(*id))
	return nil
}

func adminSubscribe(args []string) error {
	fs := flag.NewFlagSet("subscribe", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	offer := fs.String("offer", "", "identifiant de l'offre")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*id) == "" || strings.TrimSpace(*offer) == "" {
		return fmt.Errorf("-id et -offer sont obligatoires")
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, err := s.Subscribe(*id, *offer)
	if err != nil {
		return err
	}

	fmt.Printf("✔ Compte '%s' abonné à l'offre '%s'.\n", acc.ID, acc.OfferID)
	printAccount(acc)
	return nil
}

func adminTokenNew(args []string) error {
	fs := flag.NewFlagSet("token-new", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	base := fs.String("base", "", "base URL du portail (ex: https://serveur.example)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, err := s.GenerateToken(*id)
	if err != nil {
		return err
	}

	fmt.Println("✔ Lien client (re)généré. L'ancien lien est invalidé.")
	fmt.Printf("  Compte : %s\n", acc.ID)
	fmt.Printf("  Token  : %s\n", acc.Token)
	fmt.Printf("  Lien   : %s\n", formatLink(*base, acc.Token))
	return nil
}

func adminTokenRevoke(args []string) error {
	fs := flag.NewFlagSet("token-revoke", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	if _, err := s.RevokeToken(*id); err != nil {
		return err
	}

	fmt.Printf("✔ Lien client du compte '%s' révoqué.\n", normalizeID(*id))
	return nil
}

func adminLink(args []string) error {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	store := storeFlag(fs)
	id := fs.String("id", "", "identifiant du compte")
	base := fs.String("base", "", "base URL du portail (ex: https://serveur.example)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := LoadStore(*store)
	if err != nil {
		return err
	}

	acc, ok := s.GetAccount(*id)
	if !ok {
		return ErrAccountNotFound
	}

	if acc.Token == "" {
		fmt.Println("(aucun lien : générez-en un avec 'token-new')")
		return nil
	}

	fmt.Println(formatLink(*base, acc.Token))
	return nil
}

// formatLink construit l'URL (ou le chemin) du lien client.
func formatLink(base, token string) string {
	path := ClientLinkPath(token)
	if strings.TrimSpace(base) == "" {
		return path
	}
	return strings.TrimRight(base, "/") + path
}

func printOffer(o Offer) {
	dur := "illimité"
	if o.DurationDays > 0 {
		dur = fmt.Sprintf("%d jours", o.DurationDays)
	}
	quota := "illimité"
	if o.QuotaBytes > 0 {
		quota = FormatBytes(o.QuotaBytes)
	}

	fmt.Println("  ── Offre ──────────────────────────────")
	fmt.Printf("  ID              : %s\n", o.ID)
	fmt.Printf("  Nom             : %s\n", o.Name)
	fmt.Printf("  Durée           : %s\n", dur)
	fmt.Printf("  Quota           : %s\n", quota)
	fmt.Printf("  MaxConnections  : %d\n", o.MaxConnections)
	fmt.Printf("  MaxIPs          : %d\n", o.MaxIPs)
}

func printAccount(a Account) {
	quota := "illimité"
	if a.QuotaBytes > 0 {
		quota = FormatBytes(a.QuotaBytes)
	}
	exp := a.ExpiresAt
	if exp == "" {
		exp = "illimité"
	}
	state := "actif"
	if !a.Enabled {
		state = "inactif"
	}

	srv := serverAddress()

	fmt.Println("  ── Compte ─────────────────────────────")
	fmt.Printf("  ID              : %s\n", a.ID)
	fmt.Printf("  Utilisateur     : %s\n", a.Username)
	if a.Password != "" {
		fmt.Printf("  Mot de passe    : %s... (%d caractères)\n", a.Password[:min(4, len(a.Password))], len(a.Password))
	}
	fmt.Printf("  État            : %s\n", state)
	fmt.Printf("  Expiration      : %s\n", exp)
	fmt.Printf("  Quota           : %s\n", quota)
	fmt.Printf("  MaxConnections  : %d\n", a.MaxConnections)
	fmt.Printf("  MaxIPs          : %d\n", a.MaxIPs)
	if a.OfferID != "" {
		fmt.Printf("  Offre           : %s\n", a.OfferID)
	}
	if a.Token != "" {
		fmt.Printf("  Lien (token)    : %s\n", a.Token)
	}
	fmt.Println()
	fmt.Println("  ── Connexion VPN ──────────────────────")
	fmt.Printf("  Serveur         : %s\n", srv)
	fmt.Printf("  Port UDP        : 5667\n")
	fmt.Printf("  Protocole       : UDP\n")
	fmt.Printf("  Réseau VPN      : 10.77.0.0/24\n")
	fmt.Println("  Client          : saisir ces infos dans l'app VPN")
}
