package apptarget

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// The whole binary runs against a throwaway XDG tree, so nothing here can reach
// the developer's own directories. See testutil.RunIsolated for why this cannot
// be a per-test helper.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }

// A short campaign from a clean start finds nothing. The point is not coverage,
// it is that the target itself is sound: a Reset that inherits the last run's
// globals, or a rule stated too strongly, shows up here as a failure on the
// first seed rather than as noise in somebody's campaign.
func TestShortRunPasses(t *testing.T) {
	dir := t.TempDir()
	for _, seed := range []uint64{1, 2, 3} {
		res, err := fuzz.Run(func() (fuzz.Target, error) { return New(dir) },
			fuzz.Config{Seed: seed, Steps: 120})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if res.Failed {
			t.Fatalf("seed %d broke a rule from a clean start:\n%s", seed, res.Repro())
		}
	}
}

// The registry is what a display draws, so a name that appears twice is a rule
// counted twice, and a family or doc left empty is a row the display can only
// label with an identifier.
func TestRulesAreWellFormed(t *testing.T) {
	target, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	seen := map[string]bool{}
	for _, r := range target.Rules() {
		if seen[r.Name] {
			t.Errorf("rule %q is registered twice; a display would draw it twice and count it twice", r.Name)
		}
		seen[r.Name] = true
		if r.Family == "" {
			t.Errorf("rule %q has no family; the display groups by family and would put it in a nameless one", r.Name)
		}
		if r.Doc == "" {
			t.Errorf("rule %q has no doc; the fail callout would name it and say nothing about it", r.Name)
		}
	}
}

// The registry has to name exactly the rules the oracle can break, and nothing
// keeps the two in step except this test. The failure it guards is quiet: a
// display matches a Violation's Rule against the registry, so a rule that can
// break under a name the registry does not carry renders as passing for the
// whole run, and a registered name nothing ever emits renders as a rule that is
// being checked when it is not.
//
// The check reads the oracle's source rather than running it, because the
// alternative is waiting for a fuzz run to happen to break each rule.
func TestRegistryMatchesTheOracle(t *testing.T) {
	target, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	registered := map[string]bool{}
	for _, r := range target.Rules() {
		registered[r.Name] = true
	}

	emitted := emittedRules(t)
	for rule, pos := range emitted {
		if !registered[rule] {
			t.Errorf("the oracle raises %q at %s but the registry does not list it, so a display would show it passing while it fails",
				rule, pos)
		}
	}
	for rule := range registered {
		if _, ok := emitted[rule]; !ok {
			t.Errorf("the registry lists %q but nothing raises it, so a display would show a rule that is not checked", rule)
		}
	}
}

// emittedRules collects the first argument of every vio(...) call in the oracle.
// vio is the single constructor for a Violation in this package, which is what
// makes the scan complete.
func emittedRules(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "oracle.go", nil, 0)
	if err != nil {
		t.Fatalf("parse oracle.go: %v", err)
	}
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "vio" || len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Errorf("%s: vio called with a non-literal rule name; the registry cannot be checked against it",
				fset.Position(call.Pos()))
			return true
		}
		rule, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("%s: %v", fset.Position(lit.Pos()), err)
		}
		out[rule] = fset.Position(lit.Pos()).String()
		return true
	})
	if len(out) == 0 {
		t.Fatal("found no vio calls, so this test is checking nothing")
	}
	return out
}
