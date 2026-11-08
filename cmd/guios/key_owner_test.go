package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The key owner's phase has one writer, and a prefix action that runs on a
// later turn of the main thread has the owner keep the keys until it ran.
// Both are rules of the source, which the compiler accepts broken: a phase
// set in a second place, or an action posted without actStart, is the gap
// of design-G (G1) back, and shows only as a key lost now and then.

// guiosFiles parses the package's non-test files.
func guiosFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, p, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return fset, files
}

// TestKeyOwnerPhaseHasOneWriter: ko.phase is assigned in koSet alone.
func TestKeyOwnerPhaseHasOneWriter(t *testing.T) {
	fset, files := guiosFiles(t)
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "koSet" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, l := range as.Lhs {
					if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "phase" {
						t.Errorf("%s: %s sets the key owner's phase; only koSet may", fset.Position(as.Pos()), fn.Name.Name)
					}
				}
				return true
			})
		}
	}
}

// TestPrefixActionsHoldTheKeys: a function that posts a prefix action
// (postAction) calls actStart before it and actDone in what it posts.
func TestPrefixActionsHoldTheKeys(t *testing.T) {
	fset, files := guiosFiles(t)
	seen := 0
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var start, post, done token.Pos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := ""
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					name = fun.Name
				case *ast.SelectorExpr:
					name = fun.Sel.Name
				}
				switch name {
				case "actStart":
					if start == token.NoPos {
						start = call.Pos()
					}
				case "postAction":
					post = call.Pos()
				case "actDone":
					done = call.Pos()
				}
				return true
			})
			if post == token.NoPos {
				continue
			}
			seen++
			if start == token.NoPos || start > post {
				t.Errorf("%s: %s posts a prefix action with no actStart before it", fset.Position(post), fn.Name.Name)
			}
			if done == token.NoPos {
				t.Errorf("%s: %s posts a prefix action that never calls actDone", fset.Position(post), fn.Name.Name)
			}
		}
	}
	if seen == 0 {
		t.Error("no function posts a prefix action: the check reads nothing")
	}
}
