package engine

import (
	"fmt"
	"sort"
	"sync"
)

var (
	regMu     sync.RWMutex
	registry  = make(map[string]Factory)
	order     []string
)

// Factory construit une instance de moteur à partir de son nom.
// Le moteur est construit à chaque demande d'instance ; il ne doit
// pas être partagé entre plusieurs Start() simultanés.
type Factory func() (Engine, error)

// Register enregistre une factory de moteur dans le registre global.
// Cette fonction doit être appelée au démarrage du programme (dans
// une fonction init() par exemple). Elle échoue (panic) en cas de nom
// vide ou déjà enregistré.
func Register(name string, factory Factory) {
	regMu.Lock()
	defer regMu.Unlock()

	if name == "" {
		panic("engine: nom de moteur vide")
	}

	if _, exists := registry[name]; exists {
		panic("engine: moteur déjà enregistré : " + name)
	}

	registry[name] = factory
	order = append(order, name)
	sort.Strings(order)
}

// Get retourne une nouvelle instance du moteur nommé.
func Get(name string) (Engine, error) {
	regMu.RLock()
	factory, ok := registry[name]
	regMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("moteur inconnu : %s", name)
	}

	return factory()
}

// Has indique si un moteur du nom donné est enregistré.
func Has(name string) bool {
	regMu.RLock()
	defer regMu.RUnlock()

	_, ok := registry[name]
	return ok
}

// Names retourne la liste triée des noms de moteurs enregistrés.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()

	names := make([]string, len(order))
	copy(names, order)
	return names
}

// Remove retire un moteur du registre (utilisé pour supprimer un hybride
// composé). Retourne faux si le moteur n'existe pas.
func Remove(name string) bool {
	regMu.Lock()
	defer regMu.Unlock()

	if _, ok := registry[name]; ok {
		delete(registry, name)
		order = order[:0]
		for n := range registry {
			order = append(order, n)
		}
		sort.Strings(order)
		return true
	}
	return false
}
