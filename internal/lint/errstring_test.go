package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

const errfmtPath = "github.com/Gaurav-Gosain/tuios/internal/errfmt"

// TestErrfmtStringsFollowST1005 holds errfmt.Errorf to staticcheck's ST1005.
// ST1005 checks the strings handed to errors.New and fmt.Errorf only, by name,
// so moving the calls to errfmt.Errorf took them out of its reach. go vet
// still checks the format against the arguments, because it learns that
// errfmt.Errorf forwards to fmt.Errorf; ST1005 has no such step.
//
// The rule is ST1005's, from staticcheck's stylecheck/st1005: an error string
// does not end in '.', ':', '!' or a newline, and its first word does not
// start with a capital unless the word holds another capital or a digit (an
// initialism), a '(' (a call), or is a type or function of the package. A
// //nolint:staticcheck on the line skips the call, as it skips ST1005. A
// format that is not built from string literals is not checked, where
// staticcheck would also follow a named constant.
//
// It fails on an injected string; see TestErrStringLintCatchesAString.
func TestErrfmtStringsFollowST1005(t *testing.T) {
	root := repoRoot(t)
	byDir := map[string][]string{}
	for _, dir := range outlineDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var found []string
	checked := 0
	for _, paths := range byDir {
		files := map[string][]byte{}
		for _, path := range paths {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			rel, _ := filepath.Rel(root, path)
			files[filepath.ToSlash(rel)] = src
		}
		got, n := errStringFindings(t, files)
		found = append(found, got...)
		checked += n
	}
	if checked < 500 {
		t.Fatalf("the lint checked %d errfmt.Errorf strings, so it reads the wrong folders", checked)
	}
	if len(found) > 0 {
		t.Errorf("an errfmt.Errorf string breaks staticcheck's ST1005, which does not see these calls:\n  %s",
			strings.Join(found, "\n  "))
	}
}

// TestErrStringLintCatchesAString feeds the lint the strings ST1005 rejects
// and the ones it allows.
func TestErrStringLintCatchesAString(t *testing.T) {
	src := `package x

import "github.com/Gaurav-Gosain/tuios/internal/errfmt"

type Config struct{}

func bad() {
	_ = errfmt.Errorf("ends in a stop.")
	_ = errfmt.Errorf("ends in a colon:")
	_ = errfmt.Errorf("Starts with a capital %d", 1)
	_ = errfmt.Errorf("Joined " + "from parts")
}

func good() {
	_ = errfmt.Errorf("read %s: %w", "a", nil)
	_ = errfmt.Errorf("SSH host %s is down", "h")
	_ = errfmt.Errorf("P384 curve is missing")
	_ = errfmt.Errorf("Config holds %d keys", 1)
	_ = errfmt.Errorf("Single")
	_ = errfmt.Errorf("runs ./...") //nolint:staticcheck // ST1005: a package pattern
}
`
	got, n := errStringFindings(t, map[string][]byte{"x.go": []byte(src)})
	if n != 9 || len(got) != 4 {
		t.Fatalf("the lint checked %d strings and found %d, want 9 and 4:\n%s", n, len(got), strings.Join(got, "\n"))
	}
}

// errStringFindings checks the errfmt.Errorf strings in the files of one
// package. It returns a file:line for each finding and the number of strings
// it checked.
func errStringFindings(t *testing.T, files map[string][]byte) ([]string, int) {
	t.Helper()
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}
	names := map[string]bool{}
	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed[name] = f
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				names[d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						names[ts.Name.Name] = true
					}
				}
			}
		}
	}
	var out []string
	checked := 0
	for name, f := range parsed {
		local := ""
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == errfmtPath {
				local = "errfmt"
				if imp.Name != nil {
					local = imp.Name.Name
				}
			}
		}
		if local == "" {
			continue
		}
		lines := strings.Split(string(files[name]), "\n")
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if pkg, sel := selector(call.Fun); pkg != local || sel != "Errorf" {
				return true
			}
			s, ok := literalString(call.Args[0])
			if !ok {
				return true
			}
			line := fset.Position(call.Pos()).Line
			if text := lines[line-1]; strings.Contains(text, "//nolint:") && strings.Contains(text[strings.Index(text, "//nolint:"):], "staticcheck") {
				return true
			}
			checked++
			if why := st1005(s, names); why != "" {
				out = append(out, fmt.Sprintf("%s:%d: %s: %q", name, line, why, s))
			}
			return true
		})
	}
	return out, checked
}

// literalString returns the value of a string literal, or of a sum of them.
func literalString(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return literalString(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok := literalString(e.X)
		if !ok {
			return "", false
		}
		y, ok := literalString(e.Y)
		return x + y, ok
	}
	return "", false
}

// st1005 is staticcheck's ST1005 on one error string. names are the types and
// functions of the package, which a string may start with.
func st1005(s string, names map[string]bool) string {
	if s == "" {
		return ""
	}
	switch s[len(s)-1] {
	case '.', ':', '!', '\n':
		return "error strings should not end with punctuation or newlines"
	}
	word, _, ok := strings.Cut(s, " ")
	if !ok {
		return ""
	}
	first, n := utf8.DecodeRuneInString(word)
	if !unicode.IsUpper(first) {
		return ""
	}
	for _, c := range word[n:] {
		if unicode.IsUpper(c) || unicode.IsDigit(c) {
			return ""
		}
	}
	if strings.ContainsRune(word, '(') {
		return ""
	}
	if names[strings.TrimRightFunc(word, unicode.IsPunct)] {
		return ""
	}
	return "error strings should not be capitalized"
}
