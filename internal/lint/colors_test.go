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

// renderDirs are the packages whose non-test files are render code.
var renderDirs = []string{"internal/app", "internal/overlay"}

// tokenFiles are the files in renderDirs that define the tokens, and so are
// where colour literals belong.
var tokenFiles = map[string]bool{
	"internal/overlay/palette.go":  true,
	"internal/overlay/contrast.go": true,
	"internal/overlay/depth.go":    true,
	"internal/overlay/oklab.go":    true,
}

const allowColor = "//tuios:allow-color"

// TestNoHardCodedColoursInRenderCode is the colour lint. What it rejects:
//
//   - lipgloss.Color with a constant argument: lipgloss.Color("#7dcfff").
//   - A color.RGBA or color.NRGBA literal whose fields are all constants.
//   - color.Black, color.White and any charmtone colour.
//   - ansi.BasicColor or ansi.IndexedColor of a constant.
//
// A colour computed from something (a setting, a theme, a blend) passes: the
// lint is about colours nothing can reach, not about the types.
//
// It fails on an injected literal; see TestColourLintCatchesEachForm.
func TestNoHardCodedColoursInRenderCode(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, dir := range renderDirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := dir + "/" + name
			if tokenFiles[rel] {
				continue
			}
			src, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatal(err)
			}
			found = append(found, colourLiterals(t, rel, src)...)
		}
	}
	if len(found) > 0 {
		t.Errorf("hard-coded colours in render code; take them from overlay.Palette or the theme package, "+
			"or mark a deliberate one with %s <reason>:\n  %s", allowColor, strings.Join(found, "\n  "))
	}
}

// TestColourLintCatchesEachForm feeds the lint one file holding each form it
// rejects, and each allowed form, so a lint that quietly stopped matching
// cannot pass the check above by finding nothing.
func TestColourLintCatchesEachForm(t *testing.T) {
	src := `package x

func f() {
	_ = lipgloss.Color("#7dcfff")
	_ = color.RGBA{R: 1, G: 2, B: 3, A: 255}
	_ = color.White
	_ = charmtone.Charple
	_ = ansi.BasicColor(3)
	_ = ansi.IndexedColor(17)

	_ = lipgloss.Color(setting)
	_ = color.RGBA{R: uint8(r >> 8), A: 255}
	_ = color.RGBA{}
	_ = lipgloss.Color("#000000") //tuios:allow-color a test of the escape
	//tuios:allow-color the line above counts too
	_ = color.Black
}
`
	got := colourLiterals(t, "x.go", []byte(src))
	if len(got) != 6 {
		t.Fatalf("the lint found %d literals, want the 6 rejected forms:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// colourLiterals returns file:line: form for every rejected colour in src.
func colourLiterals(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	allowed := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, allowColor) {
				line := fset.Position(c.Slash).Line
				allowed[line], allowed[line+1] = true, true
			}
		}
	}
	var out []string
	report := func(n ast.Node, what string) {
		line := fset.Position(n.Pos()).Line
		if !allowed[line] {
			out = append(out, fmt.Sprintf("%s:%d: %s", name, line, what))
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			pkg, fn := selector(v.Fun)
			switch {
			case pkg == "lipgloss" && fn == "Color", pkg == "ansi" && (fn == "BasicColor" || fn == "IndexedColor"):
				if len(v.Args) == 1 && constant(v.Args[0]) {
					report(v, pkg+"."+fn+" of a constant")
				}
			}
		case *ast.CompositeLit:
			pkg, typ := selector(v.Type)
			if pkg == "color" && (typ == "RGBA" || typ == "NRGBA") && len(v.Elts) > 0 {
				all := true
				for _, e := range v.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						e = kv.Value
					}
					all = all && constant(e)
				}
				if all {
					report(v, "color."+typ+" literal")
				}
			}
		case *ast.SelectorExpr:
			pkg, sel := selector(v)
			if pkg == "charmtone" || (pkg == "color" && (sel == "Black" || sel == "White")) {
				report(v, pkg+"."+sel)
			}
		}
		return true
	})
	return out
}

// selector splits pkg.Name, or returns empty strings for anything else.
func selector(e ast.Expr) (pkg, name string) {
	s, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	id, ok := s.X.(*ast.Ident)
	if !ok {
		return "", ""
	}
	return id.Name, s.Sel.Name
}

// constant reports whether e is a literal or built only from literals.
func constant(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return constant(v.X)
	case *ast.UnaryExpr:
		return constant(v.X)
	case *ast.BinaryExpr:
		return constant(v.X) && constant(v.Y)
	}
	return false
}

// repoRoot is the directory holding go.mod, two levels above this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: %v", dir, err)
	}
	return dir
}
