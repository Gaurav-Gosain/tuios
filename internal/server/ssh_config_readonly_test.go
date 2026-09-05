package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSHSessionsDoNotWriteTheHostConfig pins that every TUI this package
// starts is built as an SSH client, which is what makes it ConfigReadOnly, and
// that it takes the server's --show-keys.
//
// `tuios ssh` authenticates no client: the session is chosen by the SSH
// username, and with --host 0.0.0.0 anyone who can reach the port gets a
// session. The settings page inside it applies to that session and must not
// decide the contents of the host's config.toml on behalf of whoever else is
// attached, which is the same call the web client already made.
//
// Checked over the syntax tree rather than by starting a server, because the
// thing that has to hold is "every place that builds one sets it", and the
// failure this guards against is a fourth entrypoint written later that does
// not. There is no ssh.Session to hand a constructor in a unit test.
func TestSSHSessionsDoNotWriteTheHostConfig(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}

	found := 0
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
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
