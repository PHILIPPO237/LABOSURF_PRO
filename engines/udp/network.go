//go:build linux && !android

package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// ============================================================
// NETWORK CONFIGURATION — LABOSURF PRO
// ============================================================
//
// Configure l'interface TUN, le forwarding IP, le NAT et les
// routes nécessaires au fonctionnement du VPN.
//
// Toutes les règles iptables sont créées dans une chaîne
// dédiée LABOSURF-FORWARD pour un nettoyage propre.

const (
	chainForward = "LABOSURF-FORWARD"
	chainNat     = "LABOSURF-POSTROUTING"
	nftTable     = "labosurf"
	nftChainFwd  = "forward"
	nftChainNat  = "postrouting"
)

// firewallBackend identifie l'outil de filtrage utilisé.
type firewallBackend int

const (
	backendNone firewallBackend = iota
	backendIPTables
	backendNFTables
)

// NetworkConfig contient les paramètres réseau du VPN.
type NetworkConfig struct {
	TUNName    string // "labosurf0"
	TUNAddress string // "10.77.0.1/24"
	VPNRange   string // "10.77.0.0/24"
	MTU        int    // 1500 par défaut
}

// DefaultNetworkConfig retourne la configuration par défaut.
func DefaultNetworkConfig() NetworkConfig {
	return NetworkConfig{
		TUNName:    "labosurf0",
		TUNAddress: "10.77.0.1/24",
		VPNRange:   "10.77.0.0/24",
		MTU:        1500,
	}
}

// NetworkState sauvegarde l'état avant modification pour restoration.
type NetworkState struct {
	ForwardingWasEnabled bool
	WANInterface         string
	ForwardingFilePath   string
	Backend              firewallBackend
}

// detectFirewallBackend choisit nftables si disponible, sinon iptables.
// nftables est privilégié car c'est le backend par défaut sur Ubuntu 22.04+,
// Debian 11+, et les systèmes modernes. iptables reste le fallback universel.
func detectFirewallBackend() firewallBackend {
	if _, err := exec.LookPath("nft"); err == nil {
		return backendNFTables
	}
	if _, err := exec.LookPath("iptables"); err == nil {
		return backendIPTables
	}
	return backendNone
}

// ConfigureNetwork configure le réseau complet pour le VPN :
//   - Interface TUN (adresse IP, UP, MTU)
//   - IPv4 forwarding
//   - NAT/MASQUERADE
//   - Règles FORWARD
//
// Retourne une fonction de nettoyage.
func ConfigureNetwork(cfg NetworkConfig) (cleanup func(), err error) {
	state := &NetworkState{}

	log.Println("[network] Configuration du réseau VPN...")

	// 1. Détecter l'interface WAN
	wan, err := detectWANInterface()
	if err != nil {
		return nil, fmt.Errorf("impossible de détecter l'interface WAN : %w", err)
	}
	state.WANInterface = wan
	log.Printf("[network] Interface WAN détectée : %s", wan)

	// 2. Configurer l'interface TUN
	if err := configureTUNInterface(cfg); err != nil {
		return nil, fmt.Errorf("configuration TUN : %w", err)
	}
	log.Printf("[network] Interface %s configurée : %s (MTU %d)", cfg.TUNName, cfg.TUNAddress, cfg.MTU)

	// 3. Activer l'IPv4 forwarding
	enabled, err := enableIPForwarding()
	if err != nil {
		return nil, fmt.Errorf("activation du forwarding : %w", err)
	}
	state.ForwardingWasEnabled = enabled
	state.ForwardingFilePath = "/proc/sys/net/ipv4/ip_forward"
	log.Println("[network] IPv4 forwarding activé")

	// Nettoyage utilisable aussi en cas d'échec pendant les étapes suivantes.
	cleanup = func() {
		log.Println("[network] Nettoyage du réseau VPN...")
		switch state.Backend {
		case backendNFTables:
			cleanupNFTables()
		case backendIPTables:
			cleanupIPTables()
		}
		if !state.ForwardingWasEnabled {
			disableIPForwarding()
		}
		log.Println("[network] Nettoyage terminé")
	}

	// 4. Configurer le firewall (nftables préféré, iptables en fallback)
	state.Backend = detectFirewallBackend()
	switch state.Backend {
	case backendNFTables:
		if err := setupNFTables(cfg, wan); err != nil {
			cleanup()
			return nil, fmt.Errorf("configuration nftables : %w", err)
		}
		log.Println("[network] Règles NAT/FORWARD configurées via nftables")
	case backendIPTables:
		if err := setupIPTables(cfg, wan); err != nil {
			cleanup()
			return nil, fmt.Errorf("configuration iptables : %w", err)
		}
		log.Println("[network] Règles NAT/FORWARD configurées via iptables")
	default:
		cleanup()
		return nil, fmt.Errorf("ni nftables ni iptables disponible sur ce système")
	}

	return cleanup, nil
}

// configureTUNInterface configure l'adresse IP, le masque et l'état UP
// de l'interface TUN via des appels ioctl Linux.
func configureTUNInterface(cfg NetworkConfig) error {
	// Ouvrir un socket pour les ioctl
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return fmt.Errorf("socket ioctl : %w", err)
	}
	defer syscall.Close(fd)

	// Parser l'adresse CIDR (ex: "10.77.0.1/24")
	ip, ipnet, err := net.ParseCIDR(cfg.TUNAddress)
	if err != nil {
		return fmt.Errorf("parse CIDR %s : %w", cfg.TUNAddress, err)
	}

	// Mettre l'interface UP
	if err := setInterfaceUp(fd, cfg.TUNName); err != nil {
		return fmt.Errorf("interface UP : %w", err)
	}

	// Assigner l'adresse IP
	if err := setInterfaceAddr(fd, cfg.TUNName, ip, ipnet.Mask); err != nil {
		return fmt.Errorf("adresse IP : %w", err)
	}

	// Configurer le MTU
	if cfg.MTU > 0 {
		if err := setInterfaceMTU(fd, cfg.TUNName, cfg.MTU); err != nil {
			return fmt.Errorf("MTU : %w", err)
		}
	}

	return nil
}

// setInterfaceUp met l'interface réseau en état UP via ioctl SIOCSIFFLAGS.
func setInterfaceUp(fd int, name string) error {
	var ifr ifreqWithFlags
	copy(ifr.Name[:], name)

	// Lire les flags actuels
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.SIOCGIFFLAGS),
		uintptr(unsafe.Pointer(&ifr)),
	)
	if errno != 0 {
		return fmt.Errorf("SIOCGIFFLAGS : %w", errno)
	}

	// Ajouter IFF_UP (0x1) et IFF_RUNNING (0x40)
	ifr.Flags |= syscall.IFF_UP | syscall.IFF_RUNNING

	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.SIOCSIFFLAGS),
		uintptr(unsafe.Pointer(&ifr)),
	)
	if errno != 0 {
		return fmt.Errorf("SIOCSIFFLAGS : %w", errno)
	}

	return nil
}

// setInterfaceAddr assigne une adresse IP et un masque via ioctl SIOCSIFADDR.
func setInterfaceAddr(fd int, name string, ip net.IP, mask net.IPMask) error {
	var ifr ifreqAddr
	copy(ifr.Name[:], name)

	// Adresse IP (IPv4)
	if ipv4 := ip.To4(); ipv4 != nil {
		var sockAddr sockaddrIn
		sockAddr.Family = syscall.AF_INET
		copy(sockAddr.Addr[:], ipv4)
		// Masque → stocké dans le champ Port (convention ioctl Linux)
		copy(sockAddr.Port[:], mask)
		ifr.Addr = sockAddr
	} else {
		return fmt.Errorf("seul IPv4 est supporté : %s", ip)
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.SIOCSIFADDR),
		uintptr(unsafe.Pointer(&ifr)),
	)
	if errno != 0 {
		return fmt.Errorf("SIOCSIFADDR : %w", errno)
	}

	// Assigner aussi le masque via SIOCSIFNETMASK
	var ifrMask ifreqAddr
	copy(ifrMask.Name[:], name)
	var sockAddrMask sockaddrIn
	sockAddrMask.Family = syscall.AF_INET
	copy(sockAddrMask.Addr[:], mask)
	ifrMask.Addr = sockAddrMask

	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.SIOCSIFNETMASK),
		uintptr(unsafe.Pointer(&ifrMask)),
	)
	if errno != 0 {
		return fmt.Errorf("SIOCSIFNETMASK : %w", errno)
	}

	return nil
}

// setInterfaceMTU configure le MTU via ioctl SIOCSIFMTU.
func setInterfaceMTU(fd int, name string, mtu int) error {
	var ifr ifreqMTU
	copy(ifr.Name[:], name)
	ifr.MTU = int32(mtu)

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.SIOCSIFMTU),
		uintptr(unsafe.Pointer(&ifr)),
	)
	if errno != 0 {
		return fmt.Errorf("SIOCSIFMTU : %w", errno)
	}

	return nil
}

// detectWANInterface détecte l'interface réseau utilisée pour la route
// par défaut (interface WAN).
func detectWANInterface() (string, error) {
	// Méthode 1 : lire /proc/net/route
	if iface, err := detectWANFromProc(); err == nil {
		return iface, nil
	}

	// Méthode 2 : ip route get 8.8.8.8
	if iface, err := detectWANFromIPRoute(); err == nil {
		return iface, nil
	}

	return "", fmt.Errorf("aucune interface WAN détectée")
}

// detectWANFromProc lit /proc/net/route pour trouver la route par défaut.
func detectWANFromProc() (string, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i == 0 {
			continue // skip header
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// Route par défaut : destination = 00000000
		if fields[1] == "00000000" {
			return fields[0], nil
		}
	}

	return "", fmt.Errorf("pas de route par défaut dans /proc/net/route")
}

// detectWANFromIPRoute utilise `ip route get` pour détecter l'interface.
func detectWANFromIPRoute() (string, error) {
	out, err := exec.Command("ip", "route", "get", "8.8.8.8").Output()
	if err != nil {
		return "", err
	}

	// Format: "8.8.8.8 via ... dev eth0 src ..."
	parts := strings.Fields(string(out))
	for i, part := range parts {
		if part == "dev" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}

	return "", fmt.Errorf("interface non trouvée dans ip route")
}

// enableIPForwarding active l'IPv4 forwarding et retourne si c'était déjà actif.
func enableIPForwarding() (wasEnabled bool, err error) {
	path := "/proc/sys/net/ipv4/ip_forward"

	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("lecture %s : %w", path, err)
	}

	// "1" = déjà actif, "0" = inactif
	currentValue := strings.TrimSpace(string(data))
	wasEnabled = currentValue == "1"

	if wasEnabled {
		return true, nil
	}

	if err := os.WriteFile(path, []byte("1"), 0644); err != nil {
		return false, fmt.Errorf("écriture %s : %w", path, err)
	}

	return false, nil
}

// disableIPForwarding désactive l'IPv4 forwarding.
func disableIPForwarding() {
	path := "/proc/sys/net/ipv4/ip_forward"
	if err := os.WriteFile(path, []byte("0"), 0644); err != nil {
		log.Printf("[network] Avertissement : impossible de désactiver le forwarding : %v", err)
	}
}

// setupIPTables crée les chaînes et règles iptables pour le VPN.
func setupIPTables(cfg NetworkConfig, wanIF string) error {
	// Créer la chaîne FORWARD dédiée
	runIPTables("-N", chainForward)
	// Ignorer l'erreur si la chaîne existe déjà

	// Créer la chaîne NAT dédiée
	runIPTables("-t", "nat", "-N", chainNat)

	// Vider les chaînes (nettoyage des règles précédentes)
	runIPTables("-F", chainForward)
	runIPTables("-t", "nat", "-F", chainNat)

	// Règles FORWARD
	// Accepter le trafic du VPN vers l'extérieur
	runIPTables("-A", chainForward, "-i", cfg.TUNName, "-o", wanIF, "-j", "ACCEPT")
	// Accepter le trafic retour (établi/connexions liées)
	runIPTables("-A", chainForward, "-i", wanIF, "-o", cfg.TUNName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")

	// Règle NAT/MASQUERADE
	runIPTables("-t", "nat", "-A", chainNat, "-s", cfg.VPNRange, "-o", wanIF, "-j", "MASQUERADE")

	// MSS clamping : le tunnel UDP encapsule chaque paquet IP dans un datagramme
	// avec 12 octets d'en-tête LABOSURF + 28 octets UDP/IP. Sans clamping, un
	// client qui envoie un paquet TCP de 1500 octets le verra fragmenté ou
	// perdu après encapsulation. On borne le MSS à MTU-120 (≈1380).
	mss := cfg.MTU - 120
	if mss < 1000 {
		mss = 1000
	}
	runIPTables("-t", "mangle", "-A", "POSTROUTING",
		"-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
		"-s", cfg.VPNRange,
		"-j", "TCPMSS", "--set-mss", fmt.Sprintf("%d", mss))

	// Relier la chaîne NAT dédiée à POSTROUTING.
	outNat, _ := exec.Command("iptables", "-t", "nat", "-L", "POSTROUTING", "-n", "--line-numbers").Output()
	if !strings.Contains(string(outNat), chainNat) {
		runIPTables("-t", "nat", "-I", "POSTROUTING", "1", "-j", chainNat)
	}

	// S'assurer que la chaîne FORWARD par défaut accepte notre chaîne
	// Ajouter un saut vers notre chaîne au début de la chaîne FORWARD
	// (seulement si pas déjà présent)
	out, _ := exec.Command("iptables", "-L", "FORWARD", "-n", "--line-numbers").Output()
	if !strings.Contains(string(out), chainForward) {
		runIPTables("-I", "FORWARD", "1", "-j", chainForward)
	}

	return nil
}

// cleanupIPTables supprime les règles et chaînes LABOSURF.
func cleanupIPTables() {
	// Supprimer les sauts vers nos chaînes
	runIPTables("-D", "FORWARD", "-j", chainForward)

	// Vider et supprimer la chaîne FORWARD
	runIPTables("-F", chainForward)
	runIPTables("-X", chainForward)

	// Supprimer le saut POSTROUTING vers notre chaîne NAT
	runIPTables("-t", "nat", "-D", "POSTROUTING", "-j", chainNat)

	// Vider et supprimer la chaîne NAT
	runIPTables("-t", "nat", "-F", chainNat)
	runIPTables("-t", "nat", "-X", chainNat)
}

// runIPTables exécute une commande iptables.
// Retourne une erreur si la commande échoue.
func runIPTables(args ...string) error {
	cmd := exec.Command("iptables", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[network] iptables %s : %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return err
}

// ============================================================
// NFTABLES
// ============================================================

// setupNFTables configure une table nftables dédiée pour le VPN.
// Avantages vs iptables : syntaxe atomique, pas de doublons, nettoyage simple.
func setupNFTables(cfg NetworkConfig, wanIF string) error {
	// Calculer le MSS pour le clamping TCP. Le tunnel ajoute 12 octets
	// d'en-tête à chaque paquet IP. Avec un MTU standard de 1500 sur la
	// route UDP sous-jacente, la charge utile IP max est ~1420-1450.
	// On clamp à 1380 pour laisser une marge confortable.
	mss := cfg.MTU - 120 // 1500 - 120 = 1380
	if mss < 1000 {
		mss = 1000
	}

	// Script nftables atomique : tout ou rien.
	script := fmt.Sprintf(`
table ip %s {
  chain %s {
    type filter hook forward priority 0; policy drop;
    iifname "%s" oifname "%s" accept
    iifname "%s" oifname "%s" ct state related,established accept
    tcp flags syn tcp option maxseg size set %d
  }
  chain %s {
    type nat hook postrouting priority 100; policy accept;
    ip saddr %s oifname "%s" masquerade
  }
}
`, nftTable, nftChainFwd, cfg.TUNName, wanIF, wanIF, cfg.TUNName, mss,
		nftChainNat, cfg.VPNRange, wanIF)

	// Supprimer la table existante pour garantir un état propre (idempotent).
	_ = runNFT("delete", "table", "ip", nftTable) // ignore l'erreur si absente

	// Appliquer le script via stdin (atomique : tout ou rien).
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft -f : %w (%s)", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// cleanupNFTables supprime la table nftables LABOSURF.
func cleanupNFTables() {
	if err := runNFT("delete", "table", "ip", nftTable); err != nil {
		log.Printf("[network] nettoyage nftables : %v", err)
	}
}

// runNFT exécute une commande nft.
func runNFT(args ...string) error {
	cmd := exec.Command("nft", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[network] nft %s : %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return err
}
