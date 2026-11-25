package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const allowHistory = "//tuios:allow-history"

// historyDirs are the packages that read a pane's history to show or copy it.
var historyDirs = []string{"internal/app", "internal/input"}

// historyMethods are the emulator methods that read the main screen's history.
var historyMethods = map[string]bool{
	"ScrollbackLen":         true,
	"ScrollbackLine":        true,
	"ScrollbackRows":        true,
	"ScrollbackText":        true,
	"ScrollbackSoftWrapped": true,
	"ScrollbackPadded":      true,
	"ScrollbackGeneration":  true,
	"CopyScrollback":        true,
}

// TestPaneHistoryIsReadThroughTheWindow is the history lint. The client's
// emulator holds only the history it saw since it attached, so a pane's
// history is read through the window (Window.ScrollbackLine and the rest, or
// Window.Text and Window.PaneText), which is the one place where history kept
// elsewhere, such as in the daemon, is joined in. A reader that goes to the
// emulator directly would miss those lines.
//
// Rejected in internal/app and internal/input: a history method called on a
// window's Terminal field, as in w.Terminal.ScrollbackLen(), or in the same
// function on a name that holds it (term := w.Terminal) or on a vt.Terminal
// parameter. A reader that
// must count the client emulator's own rows, such as an image placement that
// is anchored to them, carries a comment on its line or the line above:
//
//	//tuios:allow-history <reason>
//
// It fails on an injected call; see TestHistoryLintCatchesADirectRead.
func TestPaneHistoryIsReadThroughTheWindow(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, dir := range historyDirs {
		files, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no Go files in %s, so the lint reads nothing", dir)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			found = append(found, directHistoryReads(t, dir+"/"+filepath.Base(file), src)...)
		}
	}
	if len(found) > 0 {
		t.Errorf("a pane's history is read from its emulator directly; read it through the window "+
			"(w.ScrollbackLine, w.Text, w.PaneText), or mark a reader of the emulator's own rows with %s <reason>:\n  %s",
			allowHistory, strings.Join(found, "\n  "))
	}
}

// TestHistoryLintCatchesADirectRead feeds the lint the forms it rejects and
// the forms it allows, so a lint that quietly stopped matching cannot pass
// the check above by finding nothing.
func TestHistoryLintCatchesADirectRead(t *testing.T) {
	src := `package x

func a(w *Window) int     { return w.Terminal.ScrollbackLen() }
func b(o *OS) any         { return o.Windows[0].Terminal.ScrollbackLine(3) }
func c(w *Window)         { w.Terminal.ScrollbackText(0, 1, nil) }
func d(w *Window) int     { return w.ScrollbackLen() }
func e(w *Window) any     { return w.Text().ScrollbackLine(1) }
func f(w *Window) int     { return w.Terminal.Height() }
func g(w *Window) int     { return w.Terminal.ScrollbackLen() } //tuios:allow-history a test of the escape
func h(w *Window) int {
	//tuios:allow-history the line above counts too
	return w.Terminal.ScrollbackLen()
}
func i(w *Window) int {
	term := w.Terminal
	return term.ScrollbackLen()
}
func j(t vt.Terminal) int { return t.ScrollbackLen() }
func k(w *Window) int {
	var term = w.Terminal
	return term.ScrollbackLen()
}
func l(g CopyGrid) int    { return g.ScrollbackLen() }
func m(w *Window) int {
	t := w.Text()
	return t.ScrollbackLen()
}
`
	if got := directHistoryReads(t, "x.go", []byte(src)); len(got) != 6 {
		t.Fatalf("the lint found %d reads, want the 6 direct ones (a, b, c, i, j and k):\n%s", len(got), strings.Join(got, "\n"))
	}
}

// directHistoryReads returns file:line for every history method called on a
// Terminal field in src, outside an allow marker.
func directHistoryReads(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	allowed := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, allowHistory) {
				line := fset.Position(c.Slash).Line
				allowed[line], allowed[line+1] = true, true
			}
		}
	}
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		aliases := emulatorNames(fn)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !historyMethods[method.Sel.Name] || !isEmulator(method.X, aliases) {
				return true
			}
			if line := fset.Position(call.Pos()).Line; !allowed[line] {
				out = append(out, fmt.Sprintf("%s:%d: Terminal.%s", name, line, method.Sel.Name))
			}
			return true
		})
	}
	return out
}

// emulatorNames returns the names in fn that hold a window's emulator: a
// vt.Terminal parameter, and a variable set from a Terminal field. It does not
// follow scopes: a name is counted in the whole function.
func emulatorNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	for _, field := range fn.Type.Params.List {
		if pkg, sel := selector(field.Type); pkg == "vt" && sel == "Terminal" {
			for _, n := range field.Names {
				names[n.Name] = true
			}
		}
	}
	bind := func(lhs []ast.Expr, rhs []ast.Expr) {
		if len(lhs) != len(rhs) {
			return
		}
		for i, r := range rhs {
			if id, ok := lhs[i].(*ast.Ident); ok && isEmulator(r, nil) {
				names[id.Name] = true
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			bind(s.Lhs, s.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(s.Names))
			for i, n := range s.Names {
				lhs[i] = n
			}
			bind(lhs, s.Values)
		}
		return true
	})
	return names
}

// isEmulator reports whether e is a Terminal field or one of names.
func isEmulator(e ast.Expr, names map[string]bool) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == "Terminal"
	case *ast.Ident:
		return names[x.Name]
	}
	return false
}
