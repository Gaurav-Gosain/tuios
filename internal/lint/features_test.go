package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// featuresFile declares every handshake feature.
const featuresFile = "internal/session/features.go"

// TestFeatureNamesAreDeclaredInFeaturesGo is the feature lint. A handshake
// feature (session.Feature) is on for a connection only when both sides
// listed it, so a check for a name that this build does not list is always
// false, and the code behind it never runs. Each name is a constant in
// internal/session/features.go, with a doc line that says what it changes on
// the wire.
//
// Rejected under internal/ and cmd/: a Has or HasFeature call whose argument
// is a string literal, or a Feature constant that features.go does not
// declare. It fails on injected calls; see TestFeatureLintCatchesAnUnknownName.
func TestFeatureNamesAreDeclaredInFeaturesGo(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, featuresFile))
	if err != nil {
		t.Fatal(err)
	}
	declared := featureConsts(t, src)
	var found []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			found = append(found, undeclaredFeatures(t, rel, src, declared)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Errorf("a feature check names a feature that %s does not declare; add the constant there, "+
			"with a doc line, and to knownFeatures:\n  %s", featuresFile, strings.Join(found, "\n  "))
	}
}

// TestFeatureLintCatchesAnUnknownName feeds the lint the forms it rejects and
// the forms it allows, so a lint that quietly stopped matching cannot pass the
// check above by finding nothing.
func TestFeatureLintCatchesAnUnknownName(t *testing.T) {
	decl := featureConsts(t, []byte(`package session

type Feature string

const (
	// FeatureA is a test feature.
	FeatureA Feature = "a"
)
`))
	if !decl["FeatureA"] || len(decl) != 1 {
		t.Fatalf("the lint read %v from the declarations, want FeatureA only", decl)
	}
	src := `package x

func f(c *TUIClient, s FeatureSet, o other) {
	_ = c.HasFeature(session.FeatureA)
	_ = s.Has(FeatureA)
	_ = c.HasFeature("a")
	_ = s.Has(FeatureB)
	_ = c.HasFeature(session.FeatureB)
	_ = o.Has("not a feature set")
	_ = o.Has(key)
}
`
	if got := undeclaredFeatures(t, "x.go", []byte(src), decl); len(got) != 3 {
		t.Fatalf("the lint found %d checks, want the 3 bad ones (a literal, FeatureB twice):\n%s", len(got), strings.Join(got, "\n"))
	}
}

// featureConsts returns the names of the Feature constants declared in src.
func featureConsts(t *testing.T, src []byte) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), featuresFile, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", featuresFile, err)
	}
	out := map[string]bool{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "Feature" {
				for _, n := range vs.Names {
					out[n.Name] = true
				}
			}
		}
	}
	return out
}

// undeclaredFeatures returns file:line for every feature check in src that
// names a feature not in declared. A HasFeature call is a feature check
// always. A Has call is one when its argument is named Feature*, since Has is
// a common method name.
func undeclaredFeatures(t *testing.T, name string, src []byte, declared map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (method.Sel.Name != "Has" && method.Sel.Name != "HasFeature") {
			return true
		}
		arg := ""
		switch a := call.Args[0].(type) {
		case *ast.Ident:
			arg = a.Name
		case *ast.SelectorExpr:
			arg = a.Sel.Name
		case *ast.BasicLit:
			if method.Sel.Name == "HasFeature" {
				out = append(out, fmt.Sprintf("%s:%d: %s(%s)", name, fset.Position(call.Pos()).Line, method.Sel.Name, a.Value))
			}
			return true
		}
		if strings.HasPrefix(arg, "Feature") && !declared[arg] || method.Sel.Name == "HasFeature" && !declared[arg] {
			out = append(out, fmt.Sprintf("%s:%d: %s(%s)", name, fset.Position(call.Pos()).Line, method.Sel.Name, arg))
		}
		return true
	})
	return out
}
