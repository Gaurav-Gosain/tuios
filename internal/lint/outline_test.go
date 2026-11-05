package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// outlineDirs are the trees whose packages the tuios binary links.
var outlineDirs = []string{"internal", "cmd/tuios", "pkg"}

// outlineSkip are folders inside outlineDirs that are not part of the binary,
// and the wrappers themselves.
var outlineSkip = []string{"internal/errfmt", "internal/jsonx", "internal/harness/herdrconv", "internal/lint"}

// outlined maps each standard library call the compiler inlines at every
// call site to the out-of-line copy to call instead.
var outlined = map[[2]string]string{
	{"fmt", "Errorf"}:              "errfmt.Errorf",
	{"encoding/json", "Marshal"}:   "jsonx.Marshal",
	{"encoding/json", "Unmarshal"}: "jsonx.Unmarshal",
}

// TestInlinedCallsGoOutOfLine is the out-of-line lint. The compiler inlines
// fmt.Errorf (with the errors.New inside it), json.Marshal and json.Unmarshal
// at every call site: about 100 bytes of binary per call, for about 2,400
// calls. errfmt and jsonx hold the same functions kept out of line, so every
// call shares one copy. Moving the calls to them took 272 KiB off the
// linux/amd64 binary.
//
// Rejected in the non-test files of the packages the binary links: those
// three calls, under any name their package is imported as. A file built only
// by go run (//go:build ignore) is not part of the binary and may use them.
//
// It fails on an injected call; see TestOutlineLintCatchesACall.
func TestInlinedCallsGoOutOfLine(t *testing.T) {
	root := repoRoot(t)
	var found []string
	read := 0
	for _, dir := range outlineDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if slices.Contains(outlineSkip, rel) || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			read++
			found = append(found, inlinedCalls(t, rel, src)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if read < 100 {
		t.Fatalf("the lint read %d files, so it reads the wrong folders", read)
	}
	if len(found) > 0 {
		t.Errorf("a call that the compiler copies into every caller; "+
			"call the out-of-line copy named, which works the same way:\n  %s",
			strings.Join(found, "\n  "))
	}
}

// TestOutlineLintCatchesACall feeds the lint a file holding the forms it
// rejects and the forms it allows, so a lint that quietly stopped matching
// cannot pass the check above by finding nothing.
func TestOutlineLintCatchesACall(t *testing.T) {
	src := `package x

import (
	"encoding/json"
	"fmt"
	f2 "fmt"
)

func a() error { return fmt.Errorf("a %d", 1) }
func b() error { return f2.Errorf("b") }
func c() string { return fmt.Sprintf("c") }
func d() error { return errfmt.Errorf("d") }
func e() ([]byte, error) { return json.Marshal(1) }
func f(b []byte) error { var v int; return json.Unmarshal(b, &v) }
func g() ([]byte, error) { return json.MarshalIndent(1, "", " ") }
`
	if got := inlinedCalls(t, "x.go", []byte(src)); len(got) != 4 {
		t.Fatalf("the lint found %d calls, want 4 (a, b, e and f):\n%s", len(got), strings.Join(got, "\n"))
	}

	ignored := "//go:build ignore\n\npackage main\n\nimport \"fmt\"\n\nfunc main() { _ = fmt.Errorf(\"x\") }\n"
	if got := inlinedCalls(t, "gen.go", []byte(ignored)); len(got) != 0 {
		t.Fatalf("the lint found %d calls in a go run file, want 0:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// inlinedCalls returns file:line for every call in src that outlined names.
func inlinedCalls(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.TrimSpace(c.Text) == "//go:build ignore" {
				return nil
			}
		}
	}
	// The import path of each name a package is imported under here.
	paths := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		local := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		paths[local] = p
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		pkg, sel := selector(asExpr(n))
		if instead, ok := outlined[[2]string{paths[pkg], sel}]; ok && pkg != "" {
			out = append(out, fmt.Sprintf("%s:%d: %s.%s, call %s", name, fset.Position(n.Pos()).Line, pkg, sel, instead))
		}
		return true
	})
	return out
}
