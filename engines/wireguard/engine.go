// Package wireguard fournit le moteur WireGuard pour la plateforme
// LABOSURF PRO.
//
// DÉCISION D'ARCHITECTURE (mission P2), à ne jamais oublier en modifiant ce
// paquet : contrairement à engines/xray, engines/tuic et engines/hysteria2
// (qui téléchargent et vérifient un binaire tiers OFFICIEL précompilé), ce
// moteur NE télécharge AUCUN binaire. Deux options ont été comparées avant
// d'écrire ce code :
//
//  A. Outils système (wg / wg-quick, module noyau WireGuard) — RETENUE.
//  B. Bibliothèque Go userspace officielle (golang.zx2c4.com/wireguard,
//     ex-wireguard-go) embarquée directement dans ce binaire.
//
// Fait vérifié (recherche réelle sur les dépôts officiels GitHub.com/
// WireGuard, API GitHub interrogée directement) : contrairement à
// apernet/hysteria et tuic-protocol/tuic, NI WireGuard/wireguard-go NI
// WireGuard/wireguard-tools ne publient de GitHub Release avec des binaires
// précompilés — la distribution officielle passe exclusivement par les
// paquets système (apt/dnf/pacman/apk — le module noyau est mainline depuis
// Linux 5.6) ou par la compilation depuis les sources. Le module Go
// golang.zx2c4.com/wireguard existe et se résout via le proxy Go officiel,
// mais : (1) il n'a aucun tag semver (versions horodatées uniquement, donc
// aucune garantie de stabilité d'API dans le temps), (2) son propre README
// officiel ne documente qu'un usage en LIGNE DE COMMANDE (binaire
// `wireguard-go <iface>`), aucune API Go publique d'intégration tierce
// n'est fournie ni garantie stable, et (3) ce même README affirme
// explicitement, pour Linux : « this will run on Linux; however you should
// instead use the kernel module, which is faster and better integrated
// into the OS » — le projet lui-même déconseille cette voie sur Linux, la
// cible de déploiement principale de LABOSURF PRO (VPS).
//
// Conséquence : ce moteur pilote les outils système `wg`/`wg-quick` en
// sous-processus (même modèle que l'invocation d'`openssl` déjà utilisée
// par internal/engineutil/certutil.go pour les certificats TLS des autres
// moteurs) plutôt que d'embarquer une bibliothèque à l'API non documentée
// et déconseillée par son propre auteur sur la plateforme cible. La
// génération/dérivation de clés WireGuard (Curve25519), elle, est faite en
// Go pur (internal/secret.X25519Keypair) — aucune dépendance nouvelle
// (golang.org/x/crypto est déjà une dépendance du module racine).
//
// LIMITE ASSUMÉE ET DOCUMENTÉE : contrairement aux autres moteurs de ce
// dépôt (xray/tuic/hysteria2/udp), ce choix ne fournit PAS de compatibilité
// Android/Termux — /dev/net/tun y est généralement inaccessible sans root
// hors application système signée, et `wg-quick`/le module noyau ne sont de
// toute façon pas empaquetés dans un environnement Termux standard. Ce
// moteur cible exclusivement un VPS Linux avec les outils système présents.
//
// Protocole : WireGuard (Noise_IK, Curve25519, ChaCha20Poly1305).
// Transport : UDP. Moteur VPN TERMINAL (RelaysTo vide) — jamais chaînable
// derrière DNSTT/SlowDNS/SSH avec l'architecture actuelle, aucun adaptateur
// développé ici.
package wireguard

import (
	"labosurf/internal/engine"
)

const (
	engineName = "wireguard"
	engineDesc = "WireGuard (module noyau + outils système wg/wg-quick, jamais un binaire tiers téléchargé — voir le commentaire de tête de ce paquet). Moteur VPN terminal."
	engineVer  = "1.0.0"
)

func New() (engine.Engine, error) {
	return NewWireGuardEngine()
}

func init() {
	engine.Register(engineName, New)
}
