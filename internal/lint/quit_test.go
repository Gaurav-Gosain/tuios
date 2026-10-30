package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	allowQuit = "//tuios:allow-quit"
	bubbleTea = "charm.land/bubbletea/v2"
)

// quitDirs are the packages that decide the client should quit.
var quitDirs = []string{"internal/app", "internal/input"}

// TestEveryClientQuitGoesThroughQuitCmd is the quit lint. Closing the quit
// menu, or ending the prefix with Ctrl+B d, changes the keyboard flags the view
// asks the host for, and Bubble Tea sends a query (ESC [ ? u) with the next
// frame whenever they change. A quit cancels the input reader before it stops
// the renderer, so a frame flushed in between leaves the host's answer unread,
// and the shell the client returns to prints the end of it as "5u".
// OS.QuitCmd keeps the flags still once a quit is decided, so the last frame
// carries no change and no query. A quit that returns tea.Quit directly skips
// that.
//
// Rejected in internal/app and internal/input: tea.Quit, under any name
// Bubble Tea is imported as, except in QuitCmd itself. A deliberate exception
// carries a comment on its line or the line above:
//
//	//tuios:allow-quit <reason>
//
// It fails on an injected call; see TestQuitLintCatchesABareQuit.
func TestEveryClientQuitGoesThroughQuitCmd(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, dir := range quitDirs {
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
			found = append(found, bareQuits(t, dir+"/"+filepath.Base(file), src)...)
		}
	}
	if len(found) > 0 {
		t.Errorf("a quit returns tea.Quit directly; return OS.QuitCmd(), which keeps the keyboard "+
			"flags still so no query is sent as the client quits, or mark a deliberate one with %s <reason>:\n  %s",
			allowQuit, strings.Join(found, "\n  "))
	}
}

// TestQuitLintCatchesABareQuit feeds the lint a file holding the form it
// rejects and the forms it allows, so a lint that quietly stopped matching
// cannot pass the check above by finding nothing.
func TestQuitLintCatchesABareQuit(t *testing.T) {
	src := `package x

import tea "charm.land/bubbletea/v2"

func a() tea.Cmd { return tea.Quit }
func b() (int, tea.Cmd) { return 1, tea.Quit }
func c() tea.Msg { return tea.QuitMsg{} }
func d() tea.Cmd { return tea.Quit } //tuios:allow-quit a test of the escape
func e() tea.Cmd {
	//tuios:allow-quit the line above counts too
	return tea.Quit
}

func (m *OS) QuitCmd() tea.Cmd { return tea.Quit }
func (m *OS) Other() tea.Cmd   { return tea.Quit }
`
	if got := bareQuits(t, "x.go", []byte(src)); len(got) != 3 {
		t.Fatalf("the lint found %d quits, want the 3 bare ones (a, b and Other):\n%s", len(got), strings.Join(got, "\n"))
	}

	aliased := `package x

import bt "charm.land/bubbletea/v2"

func f() bt.Cmd { return bt.Quit }
func g() bt.Cmd { return tea.Quit } // tea here is not Bubble Tea
`
	if got := bareQuits(t, "x.go", []byte(aliased)); len(got) != 1 {
		t.Fatalf("the lint found %d quits under an aliased import, want 1:\n%s", len(got), strings.Join(got, "\n"))
	}

	none := `package x

func f() any { return tea.Quit } // no import of Bubble Tea, so tea is something else
`
	if got := bareQuits(t, "x.go", []byte(none)); len(got) != 0 {
		t.Fatalf("the lint found %d quits in a file that does not import Bubble Tea, want 0:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// bareQuits returns file:line for every tea.Quit in src outside QuitCmd.
func bareQuits(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	// The name Bubble Tea is imported under in this file. A file that does not
	// import it has no name for it, so an identifier called tea is not mistaken
	// for the package.
	local := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == bubbleTea {
			local = p[strings.LastIndex(p, "/")+1:]
			if local == "v2" {
				local = "tea"
			}
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
	}
	if local == "" {
		return nil
	}
	allowed := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, allowQuit) {
				line := fset.Position(c.Slash).Line
				allowed[line], allowed[line+1] = true, true
			}
		}
	}
	var out []string
	for _, decl := range f.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if isFunc && fn.Recv != nil && fn.Name.Name == "QuitCmd" {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if pkg, sel := selector(asExpr(n)); pkg == local && sel == "Quit" {
				if line := fset.Position(n.Pos()).Line; !allowed[line] {
					out = append(out, fmt.Sprintf("%s:%d: tea.Quit", name, line))
				}
			}
			return true
		})
	}
	return out
}

// asExpr is n as an expression, or nil.
func asExpr(n ast.Node) ast.Expr {
	e, _ := n.(ast.Expr)
	return e
}
