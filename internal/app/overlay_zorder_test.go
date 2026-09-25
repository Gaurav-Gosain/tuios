package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// openOverlayKindsSource reads the body of openOverlayKinds out of this
// package's own source and returns every kind it can put in the set.
//
// It reads the source instead of calling the function because the gates are not
// all reachable from a test: one is a method, one is a field of another struct,
// and the rest are plain flags. The first version of this guard turned on the
// flags it could think of by hand, which is the same kind of hand-kept list the
// bug is about, and it missed two kinds for exactly that reason. A parse of the
// function cannot miss one: a new overlay has to write its key here to open at
// all, and that is the line this reads.
func openOverlayKindsSource(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package source: %v", err)
	}
	fset := token.NewFileSet()
	consts := map[string]string{}
	var fn *ast.FuncDecl
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				// String constants, so a key written as a named constant
				// (overlayKindShot) resolves to the kind it stands for.
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, id := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						if s, ok := stringLit(vs.Values[i]); ok {
							consts[id.Name] = s
						}
					}
				}
			case *ast.FuncDecl:
				if d.Name.Name == "openOverlayKinds" && d.Body != nil {
					fn = d
				}
			}
		}
	}
	if fn == nil {
		t.Fatal("openOverlayKinds not found in the package source; this guard reads it and cannot check anything without it")
	}

	// Every `open[key] = ...` in the body. The key is the overlay kind.
	var kinds []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			ix, ok := lhs.(*ast.IndexExpr)
			if !ok {
				continue
			}
			if id, ok := ix.X.(*ast.Ident); !ok || id.Name == "" {
				continue
			}
			switch key := ix.Index.(type) {
			case *ast.BasicLit:
				if s, ok := stringLit(key); ok {
					kinds = append(kinds, s)
					continue
				}
				t.Errorf("openOverlayKinds writes a key this guard cannot read: %s", fset.Position(key.Pos()))
			case *ast.Ident:
				if s, ok := consts[key.Name]; ok {
					kinds = append(kinds, s)
					continue
				}
				t.Errorf("openOverlayKinds writes the key %q, which is not a string constant this guard can resolve: %s",
					key.Name, fset.Position(key.Pos()))
			default:
				t.Errorf("openOverlayKinds writes a key this guard cannot read: %s", fset.Position(ix.Index.Pos()))
			}
		}
		return true
	})
	if len(kinds) == 0 {
		t.Fatal("read no kinds out of openOverlayKinds; the guard is not looking at what it thinks it is")
	}
	slices.Sort(kinds)
	return kinds
}

// stringLit returns the value of an untyped string literal.
func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// TestEveryOverlayKindHasAPlaceInTheStack is the guard for the bug the colour
// picker found and the effect picker found again: a kind was in the set of
// overlays that can be open and not in the order they are stacked in, so it
// never entered the z-order at all and sat on the base index. Two panels on the
// base index tie, and the tie goes to whichever was recorded first, which is how
// clicks inside a picker landed on the settings row behind it.
//
// Both sides come from the code: the kinds from a parse of openOverlayKinds,
// the order from overlayKindOrder itself. Neither is retyped here, so an
// eleventh overlay cannot repeat this quietly.
func TestEveryOverlayKindHasAPlaceInTheStack(t *testing.T) {
	inOrder := map[string]bool{}
	for _, k := range overlayKindOrder {
		if inOrder[k] {
			t.Errorf("%q appears twice in overlayKindOrder", k)
		}
		inOrder[k] = true
	}

	canOpen := map[string]bool{}
	for _, kind := range openOverlayKindsSource(t) {
		canOpen[kind] = true
		if !inOrder[kind] {
			t.Errorf("%q can be open but is not in overlayKindOrder, so it never gets a z-index and a click inside it reaches the panel behind", kind)
		}
	}
	for _, kind := range overlayKindOrder {
		if !canOpen[kind] {
			t.Errorf("%q is in overlayKindOrder but openOverlayKinds never opens it, so the entry is dead", kind)
		}
	}
}
