package kitchen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Which Telegram types package the kitchen reuses internally is an
// implementation detail: the wire contract is Bot API JSON, and a caller must
// never have to import a library to use the toolbox. UpdateProcessor is the one
// exception, since its whole job is to have the shape a library's own entry
// point already has.
const borrowedTypesAllowedIn = "UpdateProcessor"

var libraryPackages = map[string]bool{"bot": true, "models": true}

func TestLibraryTypesStayInside(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package: %v", err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			for _, found := range borrowedTypesIn(decl) {
				t.Errorf("%s: exported %s carries %s; keep the library's types behind the boundary",
					name, found.where, found.what)
			}
		}
	}
}

type borrowed struct{ where, what string }

func borrowedTypesIn(decl ast.Decl) []borrowed {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if !d.Name.IsExported() || !onAnExportedType(d) {
			return nil
		}
		return selectorsIn("func "+d.Name.Name, d.Type)

	case *ast.GenDecl:
		var found []borrowed
		for _, spec := range d.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || !ts.Name.IsExported() || ts.Name.Name == borrowedTypesAllowedIn {
				continue
			}
			// Only what a caller can reach: an unexported field is nobody's business.
			if fields, ok := ts.Type.(*ast.StructType); ok {
				for _, field := range fields.Fields.List {
					if reachable(field) {
						found = append(found, selectorsIn("type "+ts.Name.Name, field.Type)...)
					}
				}
				continue
			}
			found = append(found, selectorsIn("type "+ts.Name.Name, ts.Type)...)
		}
		return found
	}
	return nil
}

func selectorsIn(where string, node ast.Node) []borrowed {
	var found []borrowed
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && libraryPackages[pkg.Name] {
			found = append(found, borrowed{where, pkg.Name + "." + sel.Sel.Name})
		}
		return true
	})
	return found
}

func onAnExportedType(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return true
	}
	receiver := d.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	named, ok := receiver.(*ast.Ident)
	return ok && named.IsExported()
}

// An embedded field is named after its own type, so it is always reachable.
func reachable(field *ast.Field) bool {
	if len(field.Names) == 0 {
		return true
	}
	for _, name := range field.Names {
		if name.IsExported() {
			return true
		}
	}
	return false
}
