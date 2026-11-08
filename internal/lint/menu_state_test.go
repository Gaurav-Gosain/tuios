package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const allowMenu = "//tuios:allow-menu"

// menuDirs are the packages that open and close the leader's menus.
var menuDirs = []string{"internal/app", "internal/input", "internal/learn"}

// menuWriter is the one file that may write the leader's menu state.
const menuWriter = "internal/app/menu_state.go"

// menuFields are the fields of the leader's menu state.
var menuFields = []string{"PrefixActive", "SubMenu", "Menu", "LastPrefixTime"}

// TestOnlyMenuStateWritesTheLeader is the leader lint. The leader, its
// sub-menu and its declared menu were three fields with nine writers, most of
// them writing one field alone. The leader pressed twice cleared the leader
// and left the workspace sub-menu open, so the next key typed for the shell
// switched the workspace. internal/app/menu_state.go now holds every write:
// ArmLeader, OpenSubMenu, OpenMenu, HoldMenu, KeepLeaderPending, CloseMenus
// and SetMenuState, each writing all of them.
//
// Rejected in internal/app, internal/input and internal/learn outside
// menu_state.go: an assignment to a field named PrefixActive, SubMenu, Menu
// or LastPrefixTime. Test files may set them to stage a state. A deliberate
// exception carries a comment on its line or the line above:
//
//	//tuios:allow-menu <reason>
//
// It fails on injected writes; see TestMenuLintCatchesADirectWrite.
func TestOnlyMenuStateWritesTheLeader(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, dir := range menuDirs {
		files, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no Go files in %s, so the lint reads nothing", dir)
		}
		for _, file := range files {
			name := dir + "/" + filepath.Base(file)
			if strings.HasSuffix(file, "_test.go") || name == menuWriter {
				continue
			}
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			found = append(found, menuWrites(t, name, src)...)
		}
	}
	if len(found) > 0 {
		t.Errorf("the leader's menu state is written outside %s; call ArmLeader, OpenSubMenu, OpenMenu, HoldMenu, "+
			"KeepLeaderPending or CloseMenus, or mark a deliberate one with %s <reason>:\n  %s",
			menuWriter, allowMenu, strings.Join(found, "\n  "))
	}
}

// TestMenuLintCatchesADirectWrite feeds the lint the forms it rejects and
// the forms it allows.
func TestMenuLintCatchesADirectWrite(t *testing.T) {
	src := `package x

func a(o *app.OS) { o.PrefixActive = false }
func b(o *app.OS) { o.SubMenu = app.SubMenuWorkspace }
func c(m *OS)     { m.Menu, m.PrefixActive = "resize", true }
func d(o *app.OS) { o.LastPrefixTime = time.Now() }
func e(o *app.OS) { o.PrefixActive = true } //tuios:allow-menu a test of the escape
func f(o *app.OS) { _ = o.PrefixActive == true }
func g(s *state)  { s.MenuOpen = true }
`
	got := menuWrites(t, "x.go", []byte(src))
	if len(got) != 5 {
		t.Fatalf("the lint found %d writes, want the 5 in a, b, c (two) and d:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// menuWrites returns file:line for every write of a menu field in src.
func menuWrites(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	allowed := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, allowMenu) {
				line := fset.Position(c.Slash).Line
				allowed[line], allowed[line+1] = true, true
			}
		}
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok || !slices.Contains(menuFields, sel.Sel.Name) {
				continue
			}
			if line := fset.Position(sel.Pos()).Line; !allowed[line] {
				out = append(out, fmt.Sprintf("%s:%d: %s", name, line, sel.Sel.Name))
			}
		}
		return true
	})
	return out
}
