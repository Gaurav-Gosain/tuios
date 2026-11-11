package lint

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

// globalSettingsReader is the one production file that may read
// config.Global: app.NewOS falls back to it when it is given no Settings.
const globalSettingsReader = "internal/app/factory.go"

// TestNoProductionReadOfGlobalSettings is the Settings lint. config.Global was
// the process's Settings, filled by whichever entrypoint ran: the local client
// and the servers applied the config file to it, the daemon never did, so a
// daemon verb that read it (a screenshot's window title, the glyphs a set
// draws) answered with the built-in defaults whatever the file said. Every
// entrypoint now builds its Settings with config.Resolve and passes them to
// app.NewOS, and the daemon reads a session's options.
//
// Rejected outside test files and internal/app/factory.go: a read or a write
// of config.Global, in internal/, cmd/ and pkg/. Tests still set it as the
// seed for a model built without Settings.
func TestNoProductionReadOfGlobalSettings(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, dir := range []string{"internal", "cmd", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			name, _ := filepath.Rel(root, path)
			name = filepath.ToSlash(name)
			if name == globalSettingsReader {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found = append(found, globalSettingsUses(t, name, src)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Errorf("config.Global is read outside %s; build Settings with config.Resolve and pass them to app.NewOS, "+
			"or read the session's options in the daemon:\n  %s", globalSettingsReader, strings.Join(found, "\n  "))
	}
}

// TestGlobalSettingsLintCatchesAUse feeds the lint the forms it rejects and
// the forms it allows.
func TestGlobalSettingsLintCatchesAUse(t *testing.T) {
	src := `package x

func a() string { return config.Global.BorderStyle }
func b()        { config.Global.Motion = "none" }
func c() int    { s := config.Global; return s.PaneGap }
func d() int    { s := config.Resolve(nil, config.Overrides{}); return s.PaneGap }
func e() int    { return other.Global.PaneGap }
`
	got := globalSettingsUses(t, "x.go", []byte(src))
	if len(got) != 3 {
		t.Fatalf("the lint found %d uses, want 3 (a, b, c):\n%s", len(got), strings.Join(got, "\n"))
	}
}

// globalSettingsUses lists each config.Global selector in src.
func globalSettingsUses(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Global" {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "config" {
			found = append(found, fset.Position(sel.Pos()).String())
		}
		return true
	})
	return found
}
