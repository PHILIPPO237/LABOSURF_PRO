package selfupdate

import "runtime"

// goosRuntime retourne le GOOS réel d'exécution. Isolé dans sa propre
// fonction pour rester substituable via currentGOOSFunc dans les tests.
func goosRuntime() string { return runtime.GOOS }
