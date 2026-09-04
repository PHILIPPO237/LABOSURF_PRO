package main

// Ce fichier relie le moteur UDP au STORE CENTRAL partagé (labosurf/internal/store).
//
// Le store vit désormais à la racine du projet : un même compte logique peut
// être rattaché à plusieurs moteurs (grants). Le moteur UDP conserve les noms
// locaux historiques (Store, Account, Offer, UserConfig, LoadStore, …) via
// des alias de types et des fonctions wrapper, afin d'éviter toute réécriture
// des fichiers existants et de leurs tests.

import labostore "labosurf/internal/store"

// Les types suivants sont des ALIAS purs : il n'y a qu'une seule définition
// (dans internal/store), partagée par tous les moteurs.
type (
	Store      = labostore.Store
	Account    = labostore.Account
	Offer      = labostore.Offer
	UserConfig = labostore.UserConfig
)

// Constantes et erreurs du store central ré-exposées localement.
const (
	EngineUDP = labostore.EngineUDP
)

var (
	ErrAccountExists   = labostore.ErrAccountExists
	ErrAccountNotFound = labostore.ErrAccountNotFound
	ErrOfferExists     = labostore.ErrOfferExists
	ErrOfferNotFound   = labostore.ErrOfferNotFound
	ErrInvalidID       = labostore.ErrInvalidID
	ErrTokenNotFound   = labostore.ErrTokenNotFound
)

// Les fonctions suivantes sont des wrappers conservant la signature
// historique, mais délèguent à l'implémentation centrale.

// LoadStore charge (ou initialise) le store central depuis path.
func LoadStore(path string) (*Store, error) {
	return labostore.LoadStore(path)
}

// ClientLinkPath retourne le chemin du portail pour un token donné.
func ClientLinkPath(token string) string {
	return labostore.ClientLinkPath(token)
}

// expiryFromDays calcule une date d'expiration RFC3339 à partir de
// maintenant + days jours (compatibilité historique).
func expiryFromDays(days int) string {
	return labostore.ExpiryFromDays(days)
}

// normalizeID normalise un identifiant de compte (compatibilité historique).
func normalizeID(id string) string {
	return labostore.NormalizeID(id)
}