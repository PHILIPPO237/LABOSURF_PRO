package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// ============================================================
// CLI LICENCE — LABOSURF PRO
// ============================================================
//
// Nouveau modèle : la licence ouvre l'ACCÈS AU SCRIPT D'INSTALLATION,
// pas au serveur. 1 clé = 1 installation. Une fois installé, le
// serveur tourne librement, sans contrôle de licence.
//
// Deux rôles distincts :
//
//	ADMINISTRATEUR (nécessite la clé privée)
//	  keygen    génère la paire de clés Ed25519
//	  create    émet et signe une licence
//	  revoke    révoque une licence
//	  list      liste les licences connues
//
//	INSTALLATION (clé publique uniquement)
//	  activate    utilise une licence pour autoriser UNE installation
//	  status      affiche les reçus d'installation de cette machine
//	  verify      vérifie un jeton de licence (sans l'utiliser)
//	  deactivate  supprime les reçus (réinstallation avec NOUVELLE licence)

func runLicense(args []string) error {
	if len(args) == 0 {
		printLicenseUsage()
		return nil
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	// --- Administrateur ---
	case "keygen":
		return licenseKeygen(rest)
	case "create":
		return licenseCreate(rest)
	case "revoke":
		return licenseRevoke(rest)
	case "list":
		return licenseList(rest)

	// --- Installation ---
	case "activate":
		return licenseActivate(rest)
	case "status":
		return licenseStatus(rest)
	case "verify":
		return licenseVerify(rest)
	case "deactivate":
		return licenseDeactivate(rest)

	case "help", "-h", "--help":
		printLicenseUsage()
		return nil
	default:
		printLicenseUsage()
		return fmt.Errorf("commande licence inconnue : %s", cmd)
	}
}

func printLicenseUsage() {
	fmt.Println("LABOSURF PRO — Gestion des licences (Ed25519)")
	fmt.Println()
	fmt.Println("Utilisation :")
	fmt.Println("  labosurf license <commande> [options]")
	fmt.Println()
	fmt.Println("ADMINISTRATEUR (nécessite la clé privée de signature) :")
	fmt.Println("  keygen      Générer la paire de clés Ed25519")
	fmt.Println("  create      Émettre et signer une licence")
	fmt.Println("  revoke      Révoquer une licence")
	fmt.Println("  list        Lister les licences émises")
	fmt.Println()
	fmt.Println("INSTALLATION (clé publique uniquement, 1 clé = 1 installation) :")
	fmt.Println("  activate    Utiliser une licence pour autoriser UNE installation")
	fmt.Println("  status      Afficher les reçus d'installation de cette machine")
	fmt.Println("  verify      Vérifier un jeton de licence (sans l'utiliser)")
	fmt.Println("  deactivate  Supprimer les reçus (réinstallation avec NOUVELLE licence)")
	fmt.Println()
	fmt.Println("Clés :")
	fmt.Println("  Clé privée (ADMIN)  : LABOSURF_LICENSE_PRIVKEY ou labosurf_admin.key")
	fmt.Println("  Clé publique        : LABOSURF_LICENSE_PUBKEY  ou labosurf_pub.key")
	fmt.Println()
	fmt.Println("La clé privée ne doit JAMAIS être distribuée aux exploitants de serveurs.")
}

// registryFlag ajoute l'option -registry commune.
func registryFlag(fs *flag.FlagSet) *string {
	return fs.String("registry", defaultRegistryPath, "chemin du registre des licences")
}

// ---------- Administrateur ----------

func licenseKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	privOut := fs.String("priv", "labosurf_admin.key", "fichier de la clé privée (ADMIN)")
	pubOut := fs.String("pub", "labosurf_pub.key", "fichier de la clé publique")
	force := fs.Bool("force", false, "écraser des clés existantes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*force {
		if _, err := os.Stat(*privOut); err == nil {
			return fmt.Errorf("%s existe déjà (utilisez -force pour écraser)", *privOut)
		}
	}

	priv, pub, err := GenerateKeyPair()
	if err != nil {
		return err
	}

	if err := writeFileAtomic(*privOut, []byte(priv), 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(*pubOut, []byte(pub), 0o644); err != nil {
		return err
	}

	fmt.Println("✔ Paire de clés Ed25519 générée.")
	fmt.Println()
	fmt.Printf("  Clé privée  : %s  (permissions 0600)\n", *privOut)
	fmt.Printf("  Clé publique: %s  (permissions 0644)\n", *pubOut)
	fmt.Println()
	fmt.Println("⚠ IMPORTANT")
	fmt.Println("  • Conservez la clé PRIVÉE en lieu sûr.")
	fmt.Println("  • Ne la distribuez JAMAIS aux exploitants de serveurs.")
	fmt.Println("  • Distribuez uniquement la clé PUBLIQUE (elle n'est pas secrète).")
	return nil
}

func licenseCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	registry := registryFlag(fs)
	id := fs.String("id", "", "identifiant de la licence (obligatoire)")
	comment := fs.String("comment", "", "commentaire libre")
	out := fs.String("out", "", "écrire le jeton dans un fichier")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("-id est obligatoire")
	}

	token, lic, err := CreateLicense(*id, *comment)
	if err != nil {
		return err
	}

	reg, err := LoadLicenseRegistry(*registry)
	if err != nil {
		return err
	}

	if err := reg.Add(lic.Data, token); err != nil {
		return err
	}

	if *out != "" {
		if err := writeFileAtomic(*out, []byte(token), 0o600); err != nil {
			return err
		}
	}

	expires := lic.Data.ActivationUntil
	if expires == "" {
		expires = "illimité"
	}

	fmt.Println("✔ Licence émise et signée (Ed25519).")
	fmt.Println()
	fmt.Printf("  ID          : %s\n", lic.Data.ID)
	fmt.Printf("  Produit     : %s\n", lic.Data.Product)
	fmt.Printf("  Émise le    : %s\n", lic.Data.IssuedAt)
	fmt.Printf("  Expire le   : %s\n", expires)
	fmt.Printf("  Activation avant : %s\n", lic.Data.ActivationUntil)
	fmt.Printf("  État        : %s\n", LicenseNew)
	if *out != "" {
		fmt.Printf("  Fichier     : %s\n", *out)
	}
	fmt.Println()
	fmt.Println("Jeton à transmettre à l'exploitant du serveur :")
	fmt.Println()
	fmt.Println(token)
	return nil
}

func licenseRevoke(args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ContinueOnError)
	registry := registryFlag(fs)
	id := fs.String("id", "", "identifiant de la licence")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("-id est obligatoire")
	}

	reg, err := LoadLicenseRegistry(*registry)
	if err != nil {
		return err
	}

	if err := reg.Revoke(*id); err != nil {
		return err
	}

	fmt.Printf("✔ Licence '%s' révoquée (état persisté).\n", *id)
	return nil
}

func licenseList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	registry := registryFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	reg, err := LoadLicenseRegistry(*registry)
	if err != nil {
		return err
	}

	entries := reg.List()
	if len(entries) == 0 {
		fmt.Println("(aucune licence émise)")
		return nil
	}

	fmt.Printf("%-16s %-10s %-22s %-20s %s\n",
		"ID", "ÉTAT", "ACTIVATION AVANT", "COMMENTAIRE", "ACTIVÉ LE")
	fmt.Println(strings.Repeat("-", 92))

	for _, e := range entries {
		exp := e.ActivationUntil
		if exp == "" {
			exp = "illimité"
		}
		act := e.ActivatedAt
		if act == "" {
			act = "-"
		}
		comment := e.Comment
		if len(comment) > 20 {
			comment = comment[:17] + "..."
		}
		if comment == "" {
			comment = "-"
		}
		fmt.Printf("%-16s %-10s %-22s %-20s %s\n",
			e.ID, e.Status, exp, comment, act)
	}

	return nil
}

// ---------- Installation (1 clé = 1 installation) ----------

// receiptDirFlag ajoute l'option -receipt-dir commune.
func receiptDirFlag(fs *flag.FlagSet) *string {
	return fs.String("receipt-dir", "", "dossier des reçus d'installation (défaut /etc/labosurf)")
}

// readToken lit un jeton depuis -token, -file ou l'entrée fournie.
func readToken(token, file string) (string, error) {
	if strings.TrimSpace(token) != "" {
		return strings.TrimSpace(token), nil
	}
	if strings.TrimSpace(file) != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("lecture du jeton : %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	return "", fmt.Errorf("-token ou -file est obligatoire")
}

func licenseActivate(args []string) error {
	fs := flag.NewFlagSet("activate", flag.ContinueOnError)
	registry := registryFlag(fs)
	token := fs.String("token", "", "jeton de licence")
	file := fs.String("file", "", "fichier contenant le jeton")
	receiptDir := receiptDirFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	tok, err := readToken(*token, *file)
	if err != nil {
		return err
	}

	// Le registre local est optionnel (blocage manuel d'un ID révoqué).
	reg, _ := LoadLicenseRegistry(*registry)

	data, err := UseLicense(tok, *receiptDir, reg)
	if err != nil {
		switch err {
		case ErrAlreadyUsed:
			fmt.Println("✘ Cette licence a déjà ouvert une installation et ne peut pas être réutilisée.")
			fmt.Printf("  Licence : %s\n", data.ID)
			fmt.Println("  Pour installer à nouveau, demandez une NOUVELLE licence.")
			return err
		case ErrLicenseExpired:
			fmt.Println("✘ Fenêtre d'installation dépassée (3h après émission).")
			fmt.Println("  Demandez une NOUVELLE licence.")
			return err
		case ErrLicenseRevoked:
			fmt.Println("✘ Licence révoquée par l'administrateur.")
			return err
		case ErrLicenseTampered:
			fmt.Println("✘ Licence altérée : la signature ne correspond pas.")
			return err
		case ErrNoVerifyKey:
			fmt.Println("✘ Clé publique de vérification absente.")
			fmt.Println("  Placez labosurf_pub.key ou définissez LABOSURF_LICENSE_PUBKEY.")
			return err
		default:
			return err
		}
	}

	fmt.Println("✔ Licence acceptée. Installation autorisée (1 clé = 1 installation).")
	fmt.Println()
	fmt.Printf("  Licence     : %s\n", data.ID)
	fmt.Printf("  Produit     : %s\n", data.Product)
	fmt.Printf("  Installée le: %s\n", time.Now().UTC().Format(time.RFC3339))
	return nil
}

func licenseStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	receiptDir := receiptDirFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	recs, err := ListReceipts(*receiptDir)
	if err != nil {
		return err
	}

	if len(recs) == 0 {
		fmt.Println("✘ Aucune installation (aucune licence utilisée sur cette machine).")
		fmt.Println("  Installez avec labosurf-pro.sh ou : labosurf license activate -token <jeton>")
		return ErrNoReceipt
	}

	fmt.Println("✔ Installations autorisées sur cette machine :")
	fmt.Println()
	for _, r := range recs {
		fmt.Printf("  Licence     : %s\n", r.LicenseID)
		fmt.Printf("  Installée le: %s\n", r.InstalledAt)
		fmt.Println()
	}
	return nil
}

func licenseVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	token := fs.String("token", "", "jeton de licence")
	file := fs.String("file", "", "fichier contenant le jeton")
	printID := fs.Bool("print-id", false, "afficher seulement l'ID de la licence")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tok, err := readToken(*token, *file)
	if err != nil {
		return err
	}

	data, status, verifyErr := VerifyLicenseToken(tok)
	if verifyErr != nil {
		// Affichage détaillé plus bas.
	} else if data.ActivationUntil != "" {
		// verify ne contrôle que la signature : la fenêtre de 3h est
		// contrôlée ici, au moment d'autoriser UNE installation.
		if until, err := time.Parse(time.RFC3339, data.ActivationUntil); err == nil &&
			time.Now().UTC().After(until) {
			status = LicenseExpired
			verifyErr = ErrLicenseExpired
		}
	}
	if *printID && verifyErr == nil {
		fmt.Println(data.ID)
		return nil
	}

	window := data.ActivationUntil
	if window == "" {
		window = "illimité"
	}

	fmt.Printf("  ID          : %s\n", data.ID)
	fmt.Printf("  Produit     : %s\n", data.Product)
	fmt.Printf("  Émise le    : %s\n", data.IssuedAt)
	fmt.Printf("  Installation avant : %s\n", window)
	fmt.Printf("  Statut      : %s\n", status)
	fmt.Println()

	switch status {
	case LicenseActive:
		fmt.Println("✔ Signature valide : licence utilisable pour UNE installation.")
		return nil
	case LicenseExpired:
		fmt.Println("✘ Fenêtre d'installation dépassée (3h).")
	case LicenseTampered:
		fmt.Println("✘ Signature invalide : licence altérée ou clé publique incorrecte.")
	default:
		fmt.Printf("✘ Licence non valide : %v\n", verifyErr)
	}

	return verifyErr
}

func licenseDeactivate(args []string) error {
	fs := flag.NewFlagSet("deactivate", flag.ContinueOnError)
	receiptDir := receiptDirFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := ClearReceipts(*receiptDir); err != nil {
		if err == ErrNoReceipt {
			fmt.Println("Aucun reçu à supprimer.")
			return nil
		}
		return err
	}

	fmt.Println("✔ Reçus d'installation supprimés.")
	fmt.Println("  Réinstallez avec une NOUVELLE licence (1 clé = 1 installation).")
	return nil
}
