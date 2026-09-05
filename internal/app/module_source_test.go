package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleSource parses every non-test Go file in the module. The guards that
// hold the entry points to one construction read the tree rather than call
// into cmd/tuios, cmd/tuios-web and internal/server, because the thing that
// has to hold is "every place that builds one does it the same way", and the
// failure they guard against is a site written later that does not.
//
// Directories that are not this module's source are skipped: the ghostty
// checkout, node modules, the docs.
func moduleSource(t testing.TB) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	root := moduleRoot(t)
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	skip := map[string]bool{".git": true, ".ghostty-vt": true, "node_modules": true, "vendor": true, "docs": true, ".claude": true}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[filepath.ToSlash(rel)] = f
		return nil
	})
	if err != nil {
		t.Fatalf("parse the module: %v", err)
	}
	if len(files) < 100 {
		t.Fatalf("parsed %d files; this guard is no longer looking at the module", len(files))
	}
	return fset, files
}

// moduleRoot walks up from the package directory to the go.mod.
func moduleRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package directory")
		}
		dir = parent
	}
}

// selectorCall reports the package and name of a call written as pkg.Name(...),
// and false for any other call.
func selectorCall(call *ast.CallExpr) (pkg, name string, ok bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return ident.Name, sel.Sel.Name, true
}

// enclosingFuncs walks a file and calls visit for every function declaration
// and literal with its body, innermost last.
func forEachFuncBody(file *ast.File, visit func(name string, body *ast.BlockStmt)) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				visit(fn.Name.Name, fn.Body)
			}
		case *ast.FuncLit:
			visit("func literal", fn.Body)
		}
		return true
	})
}

// directCalls lists the pkg.Name calls in a body that are not inside a nested
// function literal, in source order.
func directCalls(body *ast.BlockStmt) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			out = append(out, call)
		}
		return true
	})
	return out
}
