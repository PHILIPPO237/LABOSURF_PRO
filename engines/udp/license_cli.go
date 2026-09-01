package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// ============================================================
// CLI LICENCE — LABOSURF PRO
// ============================================================
//
// Deux rôles distincts :
//
//	ADMINISTRATEUR (nécessite la clé privée)
//	  keygen    génère la paire de clés Ed25519
//	  create    émet et signe une licence
//	  revoke    révoque une licence
//	  list      liste les licences émises
//
//	UTILISATEUR LABOSURF PRO (clé publique uniquement)
//	  activate    active une licence reçue
//	  status      affiche l'état d'activation
//	  verify      vérifie un jeton de licence
//	  deactivate  supprime l'activation locale

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

	// --- Utilisateur ---
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
	fmt.Println("UTILISATEUR (clé publique uniquement) :")
	fmt.Println("  activate    Activer une licence reçue")
	fmt.Println("  status      Afficher l'état d'activation")
	fmt.Println("  verify      Vérifier un jeton de licence")
	fmt.Println("  deactivate  Supprimer l'activation locale")
	fmt.Println()
	fmt.Println("Clés :")
	fmt.Println("  Clé privée (ADMIN)  : LABOSURF_LICENSE_PRIVKEY ou labosurf_admin.key")
	fmt.Println("  Clé publique        : LABOSURF_LICENSE_PUBKEY  ou labosurf_pub.key")
	fmt.Println()
	fmt.Println("La clé privée ne doit JAMAIS être distribuée aux utilisateurs.")
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
	fmt.Println("  • Ne la distribuez JAMAIS aux utilisateurs.")
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
	fmt.Println("Jeton à transmettre à l'utilisateur :")
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

	fmt.Printf("%-16s %-10s %-22s %-8s %s\n",
		"ID", "ÉTAT", "EXPIRATION", "COMPTES", "ACTIVÉE LE")
	fmt.Println(strings.Repeat("-", 78))

	for _, e := range entries {
		exp := e.ActivationUntil
		if exp == "" {
			exp = "illimité"
		}
		act := e.ActivatedAt
		if act == "" {
			act = "-"
		}
		fmt.Printf("%-16s %-10s %-22s %-8d %s\n",
			e.ID, e.Status, exp, 0, act)
	}

	return nil
}

// ---------- Utilisateur ----------

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
	actPath := fs.String("activation", defaultActivationPath, "fichier d'activation")
	machinePath := fs.String("machine", defaultMachineIDPath, "fichier d'identifiant d'installation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tok, err := readToken(*token, *file)
	if err != nil {
		return err
	}

	as, err := LoadActivationStore(*actPath, *machinePath)
	if err != nil {
		return err
	}

	// Le registre est optionnel côté utilisateur (déploiement autonome).
	reg, _ := LoadLicenseRegistry(*registry)

	res, err := as.Activate(tok, reg)
	if err != nil {
		switch err {
		case ErrAlreadyActivated:
			// L'identifiant vient de l'enregistrement local si présent,
			// sinon des données de la licence (cas du refus par registre).
			id := res.Record.LicenseID
			if id == "" {
				id = res.Data.ID
			}
			fmt.Println("✘ Cette licence a déjà été activée et ne peut pas l'être une seconde fois.")
			fmt.Printf("  Licence : %s\n", id)
			if res.Record.ActivatedAt != "" {
				fmt.Printf("  Activée le : %s\n", res.Record.ActivatedAt)
			}
			return err
		case ErrLicenseExpired:
			fmt.Println("✘ Licence expirée.")
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

	expires := res.Data.ActivationUntil
	if expires == "" {
		expires = "illimité"
	}

	fmt.Println("✔ Licence activée. LABOSURF PRO est autorisé.")
	fmt.Println()
	fmt.Printf("  Licence     : %s\n", res.Data.ID)
	fmt.Printf("  Produit     : %s\n", res.Data.Product)
	fmt.Printf("  Expire le   : %s\n", expires)
	fmt.Printf("  Activée le  : %s\n", res.Record.ActivatedAt)
	fmt.Printf("  Installation: %s…\n", res.Record.MachineID[:16])
	return nil
}

func licenseStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	registry := registryFlag(fs)
	actPath := fs.String("activation", defaultActivationPath, "fichier d'activation")
	machinePath := fs.String("machine", defaultMachineIDPath, "fichier d'identifiant d'installation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	as, err := LoadActivationStore(*actPath, *machinePath)
	if err != nil {
		return err
	}

	reg, _ := LoadLicenseRegistry(*registry)

	res, err := as.Check(reg)
	if err != nil {
		switch err {
		case ErrActivationMissing:
			fmt.Println("✘ Aucune licence activée.")
			fmt.Println("  Activez avec : labosurf license activate -token <jeton>")
		case ErrWrongDevice:
			fmt.Println("✘ Activation invalide : elle provient d'une autre installation.")
		case ErrLicenseExpired:
			fmt.Println("✘ Licence expirée.")
		case ErrLicenseRevoked:
			fmt.Println("✘ Licence révoquée par l'administrateur.")
		case ErrLicenseTampered:
			fmt.Println("✘ Activation altérée : signature invalide.")
		default:
			fmt.Printf("✘ Licence non valide : %v\n", err)
		}
		return err
	}

	expires := res.Data.ActivationUntil
	if expires == "" {
		expires = "illimité"
	}

	fmt.Println("✔ Licence active. LABOSURF PRO est autorisé.")
	fmt.Println()
	fmt.Printf("  Licence     : %s\n", res.Data.ID)
	fmt.Printf("  État        : %s\n", res.Status)
	fmt.Printf("  Expire le   : %s\n", expires)
	fmt.Printf("  Activée le  : %s\n", res.Record.ActivatedAt)
	return nil
}

func licenseVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	token := fs.String("token", "", "jeton de licence")
	file := fs.String("file", "", "fichier contenant le jeton")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tok, err := readToken(*token, *file)
	if err != nil {
		return err
	}

	data, status, verifyErr := VerifyLicenseToken(tok)

	expires := data.ActivationUntil
	if expires == "" {
		expires = "illimité"
	}

	fmt.Printf("  ID          : %s\n", data.ID)
	fmt.Printf("  Produit     : %s\n", data.Product)
	fmt.Printf("  Émise le    : %s\n", data.IssuedAt)
	fmt.Printf("  Expire le   : %s\n", expires)
	fmt.Printf("  Statut      : %s\n", status)
	fmt.Println()

	switch status {
	case LicenseActive:
		fmt.Println("✔ Signature valide, licence utilisable.")
		return nil
	case LicenseExpired:
		fmt.Println("✘ Licence expirée.")
	case LicenseTampered:
		fmt.Println("✘ Signature invalide : licence altérée ou clé publique incorrecte.")
	default:
		fmt.Printf("✘ Licence non valide : %v\n", verifyErr)
	}

	return verifyErr
}

func licenseDeactivate(args []string) error {
	fs := flag.NewFlagSet("deactivate", flag.ContinueOnError)
	actPath := fs.String("activation", defaultActivationPath, "fichier d'activation")
	machinePath := fs.String("machine", defaultMachineIDPath, "fichier d'identifiant d'installation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	as, err := LoadActivationStore(*actPath, *machinePath)
	if err != nil {
		return err
	}

	if err := as.Deactivate(); err != nil {
		if err == ErrActivationMissing {
			fmt.Println("Aucune activation à supprimer.")
			return nil
		}
		return err
	}

	fmt.Println("✔ Activation locale supprimée.")
	return nil
}
