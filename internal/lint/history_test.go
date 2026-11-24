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
// window's Terminal field, as in w.Terminal.ScrollbackLen(). A reader that
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
`
	if got := directHistoryReads(t, "x.go", []byte(src)); len(got) != 3 {
		t.Fatalf("the lint found %d reads, want the 3 direct ones (a, b and c):\n%s", len(got), strings.Join(got, "\n"))
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
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !historyMethods[method.Sel.Name] {
			return true
		}
		if field, ok := method.X.(*ast.SelectorExpr); ok && field.Sel.Name == "Terminal" {
			if line := fset.Position(call.Pos()).Line; !allowed[line] {
				out = append(out, fmt.Sprintf("%s:%d: Terminal.%s", name, line, method.Sel.Name))
			}
		}
		return true
	})
	return out
}
