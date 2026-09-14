// Assistant interactif de configuration du moteur Xray. Il remplace le simple
// « chemin de fichier JSON » par un formulaire guidé qui rassemble les
// éléments réels de l'inbound Xray : port, transport, sécurité, cible
// REALITY/SNI, et les comptes rattachés (UUID du store central). La config
// générée est ensuite appliquée via engine.Configure (qui injecte la clé
// privée REALITY réelle au besoin).
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"labosurf/internal/clientcfg"
	"labosurf/internal/engine"
	"labosurf/internal/engineutil"
	"labosurf/internal/srvcfg"
	"labosurf/internal/store"
)

// menuXrayConfig est atteint par l'option [2] CONFIGURER du sous-menu xray.
func menuXrayConfig(e engine.Engine) {
	prof, err := srvcfg.Load()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}
	s, err := openStore()
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	accounts := xrayGrantedAccounts(s)
	if len(accounts) == 0 {
		fmt.Println("  " + yellow("⚠ Aucun compte rattaché au moteur xray."))
		fmt.Println("      La config sera posée sans client : rattachez d'abord un")
		fmt.Println("      compte via GESTION DES UTILISATEURS → [3].")
	}

	clearScreen()
	printCentralHeader()
	fmt.Println()
	fmt.Println("  ── ⚙️ CONFIGURATION XRAY (assistant) ─────────────────")
	fmt.Println()
	fmt.Println("  Vides = valeurs par défaut (Entre = défaut).")
	fmt.Println()

	opts := clientcfg.XrayDefaultOptions()
	opts.Port = prof.Port(store.EngineXray)
	if first := firstDomainOrHost(prof); first != "" {
		opts.ServerName = first
		opts.Dest = first + ":443"
	}
	// autoCert retient le mode du certificat TLS pour le récapitulatif :
	// true = auto-signé généré (chemins à /etc/labosurf/engines/xray/),
	// false = certificat fourni par ses chemins.
	autoCert := false

	// Port.
	fmt.Println("  " + cyan("─ PORT D'ÉCOUTE ───────────────────────────────"))
	hint("Port sur lequel Xray accepte les connexions clientes : public en mode")
	hint("FRONT DIRECT, interne quand un transport/reverse proxy relaie vers lui.")
	fmt.Printf("  Port Xray [%d] : ", opts.Port)
	if v := promptLine(""); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			opts.Port = n
		}
	}

	// Mode de déploiement : front public, ou backend local derrière une
	// chaîne (dnstt/slowdns) / un reverse proxy (freeway-gate). Dans le
	// second cas l'inbound n'écoute que sur la boucle locale : le composant
	// amont (transport de chaîne ou proxy CONNECT) relaie vers lui.
	fmt.Println()
	fmt.Println("  " + cyan("─ MODE DE DÉPLOIEMENT ─────────────────────────"))
	echoDeployMode(opts.Listen)
	fmt.Printf("  Mode (1/2) [1] : ")
	switch strings.TrimSpace(promptLine("")) {
	case "2":
		opts.Listen = "127.0.0.1"
	default:
		opts.Listen = "0.0.0.0"
	}

	// Transport.
	fmt.Println("  " + cyan("─ TYPE DE TRANSPORT ─────────────────────────────"))
	hint("Codec entre le client et le serveur : tcp (brut), ws (WebSocket),")
	hint("grpc, kcp (Datagram), xhttp (splithttp, praticité filtrage).")
	transports := []string{"tcp", "ws", "grpc", "kcp", "xhttp"}
	echoTransport(opts.Network, transports)
	fmt.Printf("  Transport (tcp/ws/grpc/kcp/xhttp) [%s] : ", opts.Network)
	if v := strings.ToLower(strings.TrimSpace(promptLine(""))); v != "" && validIn(v, transports) {
		opts.Network = v
	}
	if opts.Network == "ws" {
		fmt.Println()
		fmt.Println("  " + cyan("─ WEBSOCKET ─────────────────────────────────"))
		hint("Path WebSocket : chemin partagé avec le client (ex. /ws-tvlabo),")
		hint("visible dans les logs et le filtrage : choisissez-le discret.")
		fmt.Printf("  Path (ex. /ws-tvlabo) [%s] : ", opts.WSPath)
		if v := strings.TrimSpace(promptLine("")); v != "" {
			opts.WSPath = v
		}
	}
	if opts.Network == "grpc" {
		fmt.Println()
		fmt.Println("  " + cyan("─ GRPC ─────────────────────────────────────"))
		hint("ServiceName : nom du service gRPC partagé avec le client ; le")
		hint("flux est empaqueté dans un stream HTTP/2 sur ce service.")
		fmt.Printf("  ServiceName [%s] : ", opts.GRPCServiceName)
		if v := strings.TrimSpace(promptLine("")); v != "" {
			opts.GRPCServiceName = v
		}
	}
	if opts.Network == "xhttp" {
		fmt.Println()
		fmt.Println("  " + cyan("─ XHTTP (splithttp) ─────────────────────────"))
		hint("Mode : auto accepte les deux saveurs, packet-up envoie les uploads")
		hint("par paquets, stream-up/stream-one créent un flux bidirectionnel.")
		echoXHTTPMode(opts.XHTTPMode)
		fmt.Printf("  Mode (auto/packet-up/stream-up/stream-one) [%s] : ", opts.XHTTPMode)
		if v := strings.ToLower(strings.TrimSpace(promptLine(""))); v != "" && validIn(v, []string{"auto", "packet-up", "stream-up", "stream-one"}) {
			opts.XHTTPMode = v
		}
		fmt.Printf("  Path (ex. /tvlabo/xyz) [/] : ")
		hint("Chemin HTTP partagé par le client et le serveur (confusion avec le")
		hint("trafic web si le domaine est masqué derrière un petit CDN).")
		if v := strings.TrimSpace(promptLine("")); v != "" {
			opts.XHTTPPath = v
		}
		fmt.Printf("  Host (HEADER hôte, vide = défaut) [] : ")
		hint("Valeur du header Host à copier au reverse proxy amont, vide = défaut.")
		opts.XHTTPHost = strings.TrimSpace(promptLine(""))
	}

	// Sécurité.
	fmt.Println("  " + cyan("─ SÉCURITÉ ──────────────────────────────────────"))
	hint("reality : masquage TLS incognito (vraie poignée de main + site vrai),")
	hint("tls      : certificat réel fourni, none : trafic en clair.")
	echoSecurity(opts.Security)
	fmt.Printf("  Sécurité (reality/tls/none) [%s] : ", opts.Security)
	if v := strings.ToLower(strings.TrimSpace(promptLine(""))); v != "" {
		switch v {
		case "reality", "tls":
			opts.Security = v
		case "none":
			opts.Security = "none"
		}
	}
	// Garde-fou immédiat : REALITY n'est supporté que sur tcp, xhttp et grpc.
	if opts.Security == "reality" && (opts.Network == "ws" || opts.Network == "kcp") {
		fmt.Println()
		fmt.Println("  " + red("✗ REALITY n'est PAS supporté sur "+opts.Network+"."))
		fmt.Println("      Transport acceptés : tcp, xhttp, grpc.")
		fmt.Println("      Choisissez un transport compatible ou une sécurité none/tls.")
		pauseMenu()
		return
	}

	switch opts.Security {
	case "reality":
		fmt.Println()
		fmt.Println("  " + cyan("─ REALITY ───────────────────────────────────"))
		hint("Sous-domaine : sélection parmi ceux du profil (☁ = proxysé")
		hint("Cloudflare). REALITY exige un domaine DNS-only — jamais orange.")
		prevSNI := opts.ServerName
		opts.ServerName = pickDomain(prof, opts.ServerName)
		if opts.ServerName != prevSNI && opts.Dest == prevSNI+":443" {
			opts.Dest = opts.ServerName + ":443"
		}
		if prof.IsProxied(opts.ServerName) {
			fmt.Println()
			fmt.Println("  " + red("✗ "+opts.ServerName+" est proxysé Cloudflare (nuage orange)."))
			fmt.Println("      Cloudflare termine le TLS : le handshake REALITY")
			fmt.Println("      n'atteint jamais le vrai site dest. ⇒ utilisez un")
			fmt.Println("      sous-domaine en DNS-only (nuage gris) ou tls/none.")
			pauseMenu()
			return
		}
		hint("SNI (serverNames) : domaine accepté/masqué — celui que le client")
		hint("présente ; il échappe à un check par le vrai site.")
		fmt.Printf("  Domaine SNI (serverNames) [%s] : ", opts.ServerName)
		if v := strings.TrimSpace(promptLine("")); v != "" {
			opts.ServerName = v
		}
		hint("Dest fallback : vrai site joint quand la requête n'est pas un")
		hint("client LABOSURF (anti-détection : tout le reste semble normal).")
		fmt.Printf("  Dest fallback (host:port) [%s] : ", opts.Dest)
		if v := strings.TrimSpace(promptLine("")); v != "" {
			opts.Dest = v
		}
		hint("shortId : identifiant court du serveur (hex). Vide = ok, le serveur")
		hint("et le client se reconnaissent par la clé publique REALITY.")
		fmt.Printf("  shortId hex (vide = ok) [] : ")
		opts.ShortID = strings.TrimSpace(promptLine(""))
	case "tls":
		fmt.Println()
		fmt.Println("  " + cyan("─ TLS ─────────────────────────────────────"))
		hint("Sous-domaine : sélection parmi ceux du profil (☁ = proxysé).")
		opts.ServerName = pickDomain(prof, opts.ServerName)
		if prof.IsProxied(opts.ServerName) {
			fmt.Println("  " + yellow("⚠ "+opts.ServerName+" proxysé Cloudflare : assurez-vous que le tunnel/"+
				"origine relaie vers cet inbound, idéalement avec le mode DERRIÈRE CHAÎNE"+
				" (écoute 127.0.0.1)."))
			if opts.Listen == "0.0.0.0" || opts.Listen == "::" {
				fmt.Println("      ⚠ Transports passant par le proxy CF : ws, xhttp.")
			}
		}
		hint("Domaine : nom hébergé par le certificat (ex. vpn.mondomaine.com).")
		fmt.Printf("  Domaine (serverName) [%s] : ", opts.ServerName)
		if v := strings.TrimSpace(promptLine("")); v != "" {
			opts.ServerName = v
		}
		hint("Certificat : auto-signé (généré ici, client en allowInsecure) ou")
		hint("vrai certificat fourni par toi (ex. Let's Encrypt) via ses chemins.")
		echoTLSCertMode()
		fmt.Printf("  Certificat (1 auto-signé / 2 chemins) [1] : ")
		if strings.TrimSpace(promptLine("")) == "2" {
			autoCert = false
			hint("Chemin PEM du certificat (chaîne complète) sur le serveur.")
			fmt.Printf("  Certificat (path) : ")
			opts.CertFile = strings.TrimSpace(promptLine(""))
			hint("Chemin PEM de la clé privée sur le serveur.")
			fmt.Printf("  Clé privée (path) : ")
			opts.KeyFile = strings.TrimSpace(promptLine(""))
			if opts.CertFile == "" || opts.KeyFile == "" {
				fmt.Println("  " + red("✗ Certificat ET clé requis pour TLS."))
				pauseMenu()
				return
			}
		} else {
			autoCert = true
			// Génère (idempotent) le certificat auto-signé du moteur xray,
			// puis réutilise ses chemins réels pour la config et le recap.
			certPath, keyPath, err := engineutil.EnsureXrayCerts(engineutil.DefaultDataDir)
			if err != nil {
				fmt.Println("  " + red("✗ "+err.Error()))
				pauseMenu()
				return
			}
			opts.CertFile = certPath
			opts.KeyFile = keyPath
		}
	}

	// Flow XTLS : n'a de sens qu'en REALITY + TCP (vision reposant sur TLS 1.3).
	if opts.Security == "reality" && opts.Network == "tcp" {
		fmt.Println()
		fmt.Println("  " + cyan("─ FLOW XTLS (REALITY + TCP) ────────────────"))
		hint("xtls-rprx-vision (recommandé) : uTLS, flux TLS révélé à Xray pour")
		hint("un routage optimisé ; xtls-rprx-direct : sans twist. 0 = aucun flow.")
		echoXTLSFlow(opts.Flow)
		fmt.Printf("  Flow XTLS (vision/direct, 0 = aucun) [%s] : ", opts.Flow)
		if v := strings.TrimSpace(promptLine("")); v != "" {
			switch strings.ToLower(v) {
			case "0", "none", "aucun":
				opts.Flow = ""
			case "vision", "xtls-rprx-vision":
				opts.Flow = "xtls-rprx-vision"
			case "direct", "xtls-rprx-direct":
				opts.Flow = "xtls-rprx-direct"
			}
		}
	}

	// Comptes clients (UUID du store central, générés au besoin).
	clients := make([]store.Account, 0, len(accounts))
	for _, a := range accounts {
		acc, err := s.EnsureEngineSecrets(a.ID, store.EngineXray)
		if err != nil {
			fmt.Println("  " + red("✗ "+err.Error()))
			pauseMenu()
			return
		}
		clients = append(clients, acc)
	}

	data, err := clientcfg.BuildXrayServerConfig(opts, clients)
	if err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
		pauseMenu()
		return
	}

	// Récapitulatif.
	fmt.Println()
	fmt.Println("  " + cyan("─ RÉCAPITULATIF ─────────────────────────────────"))
	fmt.Printf("  Port        : %d\n", opts.Port)
	fmt.Printf("  Écoute      : %s\n", opts.Listen)
	if opts.Listen != "0.0.0.0" && opts.Listen != "::" {
		fmt.Printf("  Mode        : %s (le transport de chaîne ou le reverse\n", yellow("chaîné"))
		fmt.Printf("                proxy relaiera vers %s:%d)\n", opts.Listen, opts.Port)
	}
	fmt.Printf("  Transport   : %s\n", opts.Network)
	fmt.Printf("  Sécurité    : %s\n", opts.Security)
	switch opts.Security {
	case "reality":
		fmt.Printf("  SNI         : %s\n", opts.ServerName)
		fmt.Printf("  Dest        : %s\n", opts.Dest)
		if opts.ShortID != "" {
			fmt.Printf("  shortId     : %s\n", opts.ShortID)
		}
	case "tls":
		if autoCert {
			fmt.Printf("  Mode        : %s (ouvertures client en allowInsecure)\n", yellow("auto-signé"))
		}
		fmt.Printf("  Domaine     : %s\n", opts.ServerName)
		fmt.Printf("  Certificat  : %s\n", opts.CertFile)
		fmt.Printf("  Clé         : %s\n", opts.KeyFile)
	}
	if opts.Flow != "" {
		fmt.Printf("  XTLS flow   : %s\n", opts.Flow)
	}
	switch opts.Network {
	case "ws":
		fmt.Printf("  WS path     : %s\n", opts.WSPath)
	case "grpc":
		fmt.Printf("  GRPC service: %s\n", opts.GRPCServiceName)
	}
	if opts.Network == "xhttp" {
		fmt.Printf("  XHTTP mode  : %s\n", opts.XHTTPMode)
		fmt.Printf("  XHTTP path  : %s\n", opts.XHTTPPath)
		if opts.XHTTPHost != "" {
			fmt.Printf("  XHTTP host  : %s\n", opts.XHTTPHost)
		}
	}
	fmt.Printf("  Clients     : %d\n", len(clients))
	for _, a := range clients {
		uuid := clientcfg.XrayAccountUUID(s, a.ID)
		fmt.Printf("    - %-14s uuid %s\n", a.ID, uuid)
	}
	fmt.Println()
	fmt.Println("  Appliquer cette configuration au moteur xray ? (o/N)")
	if !strings.EqualFold(promptLine(""), "o") {
		fmt.Println("\n  Annulé.")
		pauseMenu()
		return
	}

	if err := e.Configure(context.Background(), engine.EngineConfig{JSON: data}); err != nil {
		fmt.Println("  " + red("✗ "+err.Error()))
	} else {
		fmt.Println("  " + green("✔ Configuration Xray appliquée."))
	}

	// Persiste le port dans le profil serveur si l'opérateur l'a changé.
	if prof.Port(store.EngineXray) != opts.Port {
		prof.SetPort(store.EngineXray, opts.Port)
		if err := prof.Save(); err != nil {
			fmt.Println("  " + yellow("⚠ Port non persisté dans le profil : "+err.Error()))
		} else {
			fmt.Println("  " + dim("    Port xray mis à jour dans le profil serveur."))
		}
	}

	pauseMenu()
}

// xrayGrantedAccounts liste les comptes ayant un grant actif vers xray.
func xrayGrantedAccounts(s *store.Store) []store.Account {
	var out []store.Account
	for _, a := range s.ListAccounts() {
		if a.HasEngine(store.EngineXray) {
			out = append(out, a)
		}
	}
	return out
}

// firstDomainOrHost retourne le premier domaine du profil, sinon l'hôte.
func firstDomainOrHost(prof srvcfg.Profile) string {
	if len(prof.Domains) > 0 && strings.TrimSpace(prof.Domains[0]) != "" {
		return prof.Domains[0]
	}
	return strings.TrimSpace(prof.Host)
}

// pickDomain présente les sous-domaines enregistrés du profil (☁ = proxysé
// Cloudflare), laisse choisir par numéro et retourne le domaine retenu. Sans
// domaine enregistré (ou choix vide/invalide) : retourne current tel quel, la
// saisie manuelle du champ suivant fera le reste.
func pickDomain(prof srvcfg.Profile, current string) string {
	var domains []string
	for _, d := range prof.Domains {
		if d = strings.TrimSpace(d); d != "" {
			domains = append(domains, strings.TrimSuffix(d, "."))
		}
	}
	if len(domains) == 0 {
		return current
	}
	for i, d := range domains {
		mark := dim("·")
		if prof.IsProxied(d) {
			mark = yellow("☁")
		}
		sel := "  "
		if strings.EqualFold(d, current) {
			sel = green("▶")
		}
		fmt.Printf("    %s %d) %s %s\n", sel, i+1, mark, d)
	}
	fmt.Printf("  Domaine (1-%d, 0 ou vide = garder [%s]) : ", len(domains), current)
	v := strings.TrimSpace(promptLine(""))
	if v == "" || v == "0" {
		return current
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(domains) {
		return domains[n-1]
	}
	return current
}

func validIn(v string, list []string) bool {
	for _, s := range list {
		if v == s {
			return true
		}
	}
	return false
}

// hint imprime une ligne d'explication estompée sous un champ du formulaire.
func hint(s string) {
	fmt.Println("      " + dim(s))
}

func echoDeployMode(current string) {
	front := "  "
	behind := "  "
	if current == "0.0.0.0" || current == "::" {
		front = green("▶")
	} else {
		behind = green("▶")
	}
	fmt.Printf("    %s 1) FRONT DIRECT    : inbound écouté en public (0.0.0.0),\n", front)
	fmt.Println("       les clients se connectent directement.")
	fmt.Printf("    %s 2) DERRIÈRE CHAÎNE : inbound en local (127.0.0.1), un\n", behind)
	fmt.Println("       transport (dnstt/slowdns) ou un reverse proxy")
	fmt.Println("       (freeway-gate CONNECT) relaie vers lui.")
}

func echoTransport(current string, list []string) {
	for _, t := range list {
		mark := "  "
		if t == current {
			mark = green("▶")
		}
		fmt.Printf("    %s %-5s\n", mark, t)
	}
}

func echoSecurity(current string) {
	for _, s := range []string{"reality", "tls", "none"} {
		mark := "  "
		if s == current {
			mark = green("▶")
		}
		fmt.Printf("    %s %-7s\n", mark, s)
	}
}

func echoTLSCertMode() {
	fmt.Println("    1) AUTO-SIGNÉ : certificat ECDSA généré localement (openssl),")
	fmt.Println("       client en allowInsecure — pratique sans domaine public.")
	fmt.Println("    2) CHEMINS    : vrai certificat fourni (ex. Let's Encrypt).")
}

func echoXTLSFlow(current string) {
	for _, s := range []string{"xtls-rprx-vision", "xtls-rprx-direct", "aucun"} {
		mark := "  "
		if (s == "aucun" && current == "") || s == current {
			mark = green("▶")
		}
		fmt.Printf("    %s %-20s\n", mark, s)
	}
}

func echoXHTTPMode(current string) {
	for _, s := range []string{"auto", "packet-up", "stream-up", "stream-one"} {
		mark := "  "
		if s == current {
			mark = green("▶")
		}
		fmt.Printf("    %s %s\n", mark, s)
	}
}
