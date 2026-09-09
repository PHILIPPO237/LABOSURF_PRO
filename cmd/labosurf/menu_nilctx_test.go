package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMenuNeverPassesNilContext est un test de non-régression : le menu
// interactif appelait auparavant e.Start(nil), e.Restart(nil), e.Install(nil, ...)
// et e.Configure(nil, ...) — un context.Context nil qui fait paniquer tous
// les moteurs (ils appellent context.WithCancel(ctx) dans Start()), ce qui
// plantait le menu d'administration dès qu'on démarrait/redémarrait/
// installait/configurait un moteur. Ce test analyse statiquement menu.go et
// échoue si un appel à l'une de ces méthodes de l'interface engine.Engine
// reçoit à nouveau un littéral nil comme premier argument.
func TestMenuNeverPassesNilContext(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "menu.go", nil, 0)
	if err != nil {
		t.Fatalf("parse menu.go : %v", err)
	}

	guarded := map[string]bool{
		"Start":     true,
		"Restart":   true,
		"Install":   true,
		"Configure": true,
	}

	found := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !guarded[sel.Sel.Name] {
			return true
		}
		found++
		if len(call.Args) == 0 {
			return true
		}
		if ident, ok := call.Args[0].(*ast.Ident); ok && ident.Name == "nil" {
			pos := fset.Position(call.Pos())
			t.Errorf(
				"%s:%d : appel à %s() avec un context.Context nil — utiliser context.Background() (régression du deadlock/panic déjà corrigé)",
				pos.Filename, pos.Line, sel.Sel.Name,
			)
		}
		return true
	})

	if found == 0 {
		t.Fatal("aucun appel Start/Restart/Install/Configure trouvé dans menu.go — le test ne vérifie plus rien, à revoir")
	}
}
