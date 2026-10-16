package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSSHSessionsDoNotWriteTheHostConfig pins that every TUI this package
// starts is built as an SSH client, which is what makes it ConfigReadOnly, and
// that it takes the server's --show-keys.
//
// A `tuios ssh` client is anyone holding a key in the authorized keys file,
// or anyone who can reach the port under --no-auth, and the session is chosen
// by the SSH username. The settings page inside it applies to that session
// and must not decide the contents of the host's config.toml on behalf of
// whoever else is attached, which is the same call the web client already
// made.
//
// Checked over the syntax tree rather than by starting a server, because the
// thing that has to hold is "every place that builds one sets it", and the
// failure this guards against is a fourth entrypoint written later that does
// not. There is no ssh.Session to hand a constructor in a unit test.
func TestSSHSessionsDoNotWriteTheHostConfig(t *testing.T) {
	// Every non-test file, whatever its build tags, so an entrypoint that
	// only builds on one platform is checked too.
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files[name] = file
	}

	found := 0
	for name, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "OSOptions" {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "app" {
				return true
			}
			found++
			pos := fset.Position(lit.Pos())
			where := fmt.Sprintf("%s:%d", filepath.Base(name), pos.Line)
			if !setsSelector(lit, "Client", "app", "ClientSSH") {
				t.Errorf("app.OSOptions at %s does not set Client: app.ClientSSH; "+
					"an SSH client would write the host's config file", where)
			}
			if !setsSelector(lit, "ShowKeys", "cfg", "ShowKeys") {
				t.Errorf("app.OSOptions at %s does not pass ShowKeys: cfg.ShowKeys; "+
					"`tuios ssh --show-keys` is registered and ignored", where)
			}
			return true
		})
	}
	if found == 0 {
		t.Fatal("no app.OSOptions literal found; this guard is no longer looking at anything")
	}
}

// setsSelector reports whether the literal assigns the named field the value
// pkg.name.
func setsSelector(lit *ast.CompositeLit, field, pkg, name string) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != field {
			continue
		}
		sel, ok := kv.Value.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != name {
			return false
		}
		x, ok := sel.X.(*ast.Ident)
		return ok && x.Name == pkg
	}
	return false
}

// themeFileWriters are the functions in internal/app that write or remove a
// file in the themes directory, or open the way to one. Each must read
// ConfigReadOnly: an SSH or web client must not change the host's theme
// files any more than its config.toml. A new writer is added here.
var themeFileWriters = []string{
	"ThemeEditorSave",      // the theme editor's ctrl+s
	"ThemePickerAskDelete", // the theme picker's ctrl+d, which the delete waits on
}

// TestThemeFileWritersHonourConfigReadOnly holds each named writer to a read
// of ConfigReadOnly in its own body. Checked over the syntax tree for the
// reason TestSSHSessionsDoNotWriteTheHostConfig gives: what has to hold is
// that every writer checks, including one written later.
func TestThemeFileWritersHonourConfigReadOnly(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("..", "app", "*.go"))
	if err != nil {
		t.Fatalf("listing internal/app: %v", err)
	}
	fset := token.NewFileSet()
	found := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !slices.Contains(themeFileWriters, fn.Name.Name) {
				continue
			}
			found[fn.Name.Name] = true
			reads := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "ConfigReadOnly" {
					reads = true
				}
				return !reads
			})
			if !reads {
				t.Errorf("%s in %s writes a theme file and never reads ConfigReadOnly; a served client would change the host's files",
					fn.Name.Name, filepath.Base(name))
			}
		}
	}
	for _, w := range themeFileWriters {
		if !found[w] {
			t.Errorf("%s is named as a theme file writer but internal/app has no such function; this guard is looking at nothing", w)
		}
	}
}

// themeFileWriteSites are the functions in internal/app that call a theme
// file writer, each with the gate in themeFileWriters that stands in front of
// it. ThemePickerConfirmDelete deletes only what ThemePickerAskDelete asked
// about, so the ask is its gate.
var themeFileWriteSites = map[string]string{
	"ThemeEditorSave":          "ThemeEditorSave",
	"ThemePickerConfirmDelete": "ThemePickerAskDelete",
}

// TestEveryThemeFileWriteIsGated finds every reach for a theme file writer in
// internal/app: theme.DeleteThemeFile, and a draft's Save or SaveOver. Each
// must sit in a function named in themeFileWriteSites, so a writer added
// later cannot skip the ConfigReadOnly check by not being on the list.
//
// Negative control: a call to st.draft.Save() in any other function fails
// this test with that function's name.
func TestEveryThemeFileWriteIsGated(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("..", "app", "*.go"))
	if err != nil {
		t.Fatalf("listing internal/app: %v", err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || !writesThemeFile(sel) {
					return true
				}
				gate, listed := themeFileWriteSites[fn.Name.Name]
				switch {
				case !listed:
					t.Errorf("%s in %s writes a theme file and is not in themeFileWriteSites; name it there with the gate that reads ConfigReadOnly",
						fn.Name.Name, fset.Position(sel.Pos()))
				case !slices.Contains(themeFileWriters, gate):
					t.Errorf("%s is gated by %s, which is not in themeFileWriters", fn.Name.Name, gate)
				}
				seen[fn.Name.Name] = true
				return true
			})
		}
	}
	for site := range themeFileWriteSites {
		if !seen[site] {
			t.Errorf("%s is named as a theme file write site but writes nothing; this guard is looking at nothing", site)
		}
	}
}

// writesThemeFile reports whether sel reaches for a theme file writer:
// theme.DeleteThemeFile, or Save or SaveOver on a draft.
func writesThemeFile(sel *ast.SelectorExpr) bool {
	switch sel.Sel.Name {
	case "DeleteThemeFile":
		x, ok := sel.X.(*ast.Ident)
		return ok && x.Name == "theme"
	case "Save", "SaveOver":
		x, ok := sel.X.(*ast.SelectorExpr)
		return ok && x.Sel.Name == "draft"
	}
	return false
}
