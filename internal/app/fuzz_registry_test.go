package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
)

// The registry has to name exactly the rules the oracle can break, and nothing
// keeps the two in step except this test. The failure it guards is quiet: a
// display matches a Violation's Rule against the registry, so a rule that can
// break under a name the registry does not carry renders as passing for the
// whole run, and a registered name nothing ever emits renders as a rule that is
// being checked when it is not. Both are the display telling the maintainer
// something untrue, which is worse than showing nothing.
//
// The check reads the oracle's source rather than running it, because the
// alternative is waiting for a fuzz run to happen to break each rule.

// oracleFiles are the files that construct Violations.
var oracleFiles = []string{"fuzz_oracle_test.go", "fuzz_oracle_scope_test.go"}

// emittedRules collects the first argument of every vio(...) call in the
// oracle. vio is the single constructor for a Violation in these files, which
// is what makes the scan complete.
func emittedRules(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, name := range oracleFiles {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
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
	}
	if len(out) == 0 {
		t.Fatal("found no vio calls, so this test is checking nothing")
	}
	return out
}

func TestFuzzRuleRegistryMatchesTheOracle(t *testing.T) {
	target, err := newFuzzTarget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	lister, ok := target.(fuzz.RuleLister)
	if !ok {
		t.Fatal("the fuzz target no longer lists its rules, so a display has no registry to draw")
	}

	registered := map[string]bool{}
	for _, r := range lister.Rules() {
		if registered[r.Name] {
			t.Errorf("rule %q is registered twice; a display would draw it twice and count it twice", r.Name)
		}
		registered[r.Name] = true
		if r.Family == "" {
			t.Errorf("rule %q has no family; the display groups by family and would put it in a nameless one", r.Name)
		}
		if r.Doc == "" {
			t.Errorf("rule %q has no doc; the fail callout would name it and say nothing about it", r.Name)
		}
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

// The order the registry declares is the order Check applies, and the engine
// leans on it: everything after the rule that broke is reported unrun. A
// registry sorted differently from the sweep would report rules as having run
// when they did not.
func TestFuzzRuleRegistryFollowsCheckOrder(t *testing.T) {
	var want []string
	for _, entry := range fuzzRules {
		for _, r := range entry.rules {
			want = append(want, r.Name)
		}
	}
	want = append(want, ruleNames(fuzzCheckRules)...)

	target, err := newFuzzTarget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	got := ruleNames(target.(fuzz.RuleLister).Rules())

	if len(got) != len(want) {
		t.Fatalf("Rules() returned %d names, the sweep declares %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rules()[%d] is %q, the sweep runs %q there", i, got[i], want[i])
		}
	}
}

func ruleNames(rs []fuzz.RuleInfo) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}
