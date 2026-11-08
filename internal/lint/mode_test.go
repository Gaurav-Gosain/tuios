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

const allowMode = "//tuios:allow-mode"

// modeDirs are the packages that hold an *app.OS and change its mode.
var modeDirs = []string{"internal/app", "internal/input"}

// modeWriter is the one file that may write OS.Mode.
const modeWriter = "internal/app/mode.go"

// TestOnlyModeGoWritesTheClientMode is the mode lint. The client's input mode
// had 28 writers. Some told the tape recorder and some did not, so a replay
// ended in a different mode than the recording. Only the two switch methods
// ended nest mode. And each borrower saved the mode it saw, which could be a
// mode another borrower had taken: the hold key and the rail together handed
// back window mode to a person who was typing. internal/app/mode.go now holds
// every write: ChooseMode for a choice, SetBorrowedMode for a borrow.
//
// Rejected in internal/app and internal/input outside mode.go: an assignment
// to the Mode field of a receiver or parameter typed OS, an assignment of
// TerminalMode or WindowManagementMode to any Mode field, and an OS literal
// that sets Mode. Test files may set Mode to stage a state. A deliberate
// exception carries a comment on its line or the line above:
//
//	//tuios:allow-mode <reason>
//
// It fails on injected writes; see TestModeLintCatchesADirectWrite.
func TestOnlyModeGoWritesTheClientMode(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, dir := range modeDirs {
		files, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no Go files in %s, so the lint reads nothing", dir)
		}
		for _, file := range files {
			name := dir + "/" + filepath.Base(file)
			if strings.HasSuffix(file, "_test.go") || name == modeWriter {
				continue
			}
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			found = append(found, modeWrites(t, name, src)...)
		}
	}
	if len(found) > 0 {
		t.Errorf("the client mode is written outside %s; call ChooseMode for a choice or SetBorrowedMode "+
			"for a borrow, so the tape recorder and nest mode hear it, or mark a deliberate one with %s <reason>:\n  %s",
			modeWriter, allowMode, strings.Join(found, "\n  "))
	}
}

// TestModeLintCatchesADirectWrite feeds the lint the forms it rejects and the
// forms it allows, so a lint that quietly stopped matching cannot pass the
// check above by finding nothing.
func TestModeLintCatchesADirectWrite(t *testing.T) {
	src := `package x

func (m *OS) a()         { m.Mode = other }
func b(o *app.OS)        { o.Mode = app.TerminalMode }
func c(s *state)         { s.os.Mode = WindowManagementMode }
func d() *OS             { return &OS{Mode: TerminalMode} }
func e(o *app.OS)        { defer func() { o.Mode = x }() }
func f(v *vim)           { v.Mode = VimNormal }
func g(m *OS)            { m.TapeManager.Mode = TapeManagerList }
func h(m *OS)            { m.Mode = TerminalMode } //tuios:allow-mode a test of the escape
func i(m *OS) {
	//tuios:allow-mode the line above counts too
	m.Mode = TerminalMode
}
func j() any { return accent{Mode: compact} }
`
	got := modeWrites(t, "x.go", []byte(src))
	if len(got) != 5 {
		t.Fatalf("the lint found %d writes, want the 5 in a, b, c, d and e:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// modeWrites returns file:line for every write of the client mode in src.
func modeWrites(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	allowed := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, allowMode) {
				line := fset.Position(c.Slash).Line
				allowed[line], allowed[line+1] = true, true
			}
		}
	}
	var out []string
	report := func(n ast.Node, what string) {
		if line := fset.Position(n.Pos()).Line; !allowed[line] {
			out = append(out, fmt.Sprintf("%s:%d: %s", name, line, what))
		}
	}
	for _, decl := range f.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		holders := map[string]bool{}
		if isFunc {
			for _, fields := range []*ast.FieldList{fn.Recv, fn.Type.Params} {
				if fields == nil {
					continue
				}
				for _, field := range fields.List {
					if isOSType(field.Type) {
						for _, n := range field.Names {
							holders[n.Name] = true
						}
					}
				}
			}
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					sel, isSel := lhs.(*ast.SelectorExpr)
					if !isSel || sel.Sel.Name != "Mode" {
						continue
					}
					holder, isIdent := sel.X.(*ast.Ident)
					switch {
					case isIdent && holders[holder.Name]:
						report(n, holder.Name+".Mode =")
					case i < len(n.Rhs) && namesClientMode(n.Rhs[i]):
						report(n, "Mode = a client mode")
					}
				}
			case *ast.CompositeLit:
				if !isOSType(n.Type) {
					return true
				}
				for _, elt := range n.Elts {
					if kv, isKV := elt.(*ast.KeyValueExpr); isKV {
						if key, isIdent := kv.Key.(*ast.Ident); isIdent && key.Name == "Mode" {
							report(kv, "OS{Mode: ...}")
						}
					}
				}
			}
			return true
		})
	}
	return out
}

// isOSType reports whether expr is OS, *OS, app.OS or *app.OS.
func isOSType(expr ast.Expr) bool {
	if star, isStar := expr.(*ast.StarExpr); isStar {
		expr = star.X
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "OS"
	case *ast.SelectorExpr:
		pkg, isIdent := e.X.(*ast.Ident)
		return isIdent && pkg.Name == "app" && e.Sel.Name == "OS"
	}
	return false
}

// namesClientMode reports whether expr is one of the two client modes.
func namesClientMode(expr ast.Expr) bool {
	name := ""
	switch e := expr.(type) {
	case *ast.Ident:
		name = e.Name
	case *ast.SelectorExpr:
		name = e.Sel.Name
	}
	return name == "TerminalMode" || name == "WindowManagementMode"
}
