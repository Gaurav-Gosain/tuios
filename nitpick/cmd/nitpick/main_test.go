package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tuios.dev/nitpick"
)

// These tests build the nitpick binary and run it in scratch git
// repositories, the way a person or an agent runs it.

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nitpick-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin = filepath.Join(dir, "nitpick")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const (
	// fileA has one finding, on line 6.
	fileA = "package m\n\nimport \"os\"\n\nfunc A() error {\n\t_ = os.Chdir(\"a\")\n\treturn nil\n}\n"
	// fileB is fileA with a second finding, on line 11.
	fileB = fileA + "\nfunc B() error {\n\t_ = os.Chdir(\"b\")\n\treturn nil\n}\n"
	// fileC is fileB with a third finding, on line 16.
	fileC = fileB + "\nfunc C() error {\n\t_ = os.Chdir(\"c\")\n\treturn nil\n}\n"
)

// newRepo makes a module in a new git repository with no commit. The tests
// make no commit: git needs an author for that, and a tree from
// git write-tree is a base that needs none.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/m\n\ngo 1.26\n")
	gitRun(t, dir, "init", "-q")
	return dir
}

func writeFile(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// snapshot stages the working tree and returns its tree hash.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	return gitRun(t, dir, "write-tree")
}

type runOut struct {
	stdout, stderr string
	code           int
}

func nitpickIn(t *testing.T, dir string, args ...string) runOut {
	t.Helper()
	cmd := exec.Command(bin, args...)
	return runIn(t, cmd, dir)
}

// withSwallowed runs nitpick with swallowed_error, the opt-in rule that the
// fixtures trip, on as well.
func withSwallowed(t *testing.T, dir string, args ...string) runOut {
	t.Helper()
	args = append([]string{args[0], "--enable", "swallowed_error"}, args[1:]...)
	return nitpickIn(t, dir, args...)
}

func runIn(t *testing.T, cmd *exec.Cmd, dir string) runOut {
	t.Helper()
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return runOut{stdout.String(), stderr.String(), code}
}

func findings(t *testing.T, r runOut) result {
	t.Helper()
	var res result
	if err := json.Unmarshal([]byte(r.stdout), &res); err != nil {
		t.Fatalf("the output is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, r.stdout, r.stderr)
	}
	return res
}

func lines(res result) []int {
	var out []int
	for _, f := range res.Findings {
		out = append(out, f.Line)
	}
	return out
}

func TestCheckReportsEveryFinding(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.go", fileB)
	r := withSwallowed(t, dir, "check", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d, want 0\n%s", r.code, r.stderr)
	}
	res := findings(t, r)
	if got := lines(res); !slices.Equal(got, []int{6, 11}) {
		t.Fatalf("finding lines %v, want [6 11]", got)
	}
	rule, _ := nitpick.Lookup("swallowed_error")
	f := res.Findings[0]
	if f.File != "a.go" || f.Rule != "swallowed_error" || f.Column != 2 || f.Precision != rule.Precision {
		t.Errorf("finding %+v, want a.go column 2, swallowed_error, precision %v", f, rule.Precision)
	}
}

func TestDiffReportsChangedLinesOnly(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.go", fileA)
	base := snapshot(t, dir)
	writeFile(t, dir, "a.go", fileB)

	res := findings(t, withSwallowed(t, dir, "diff", "--base", base, "--json"))
	if got := lines(res); !slices.Equal(got, []int{11}) {
		t.Fatalf("diff finding lines %v, want [11]", got)
	}

	r := withSwallowed(t, dir, "diff", "--base", base)
	if !strings.HasPrefix(r.stdout, "a.go:11:2: swallowed_error: the error from os.Chdir is dropped") {
		t.Errorf("text output:\n%s", r.stdout)
	}
	if !strings.Contains(r.stderr, "1 finding on changed lines") {
		t.Errorf("summary:\n%s", r.stderr)
	}
}

func TestDiffStagedLeavesOutTheWorkingTree(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.go", fileA)
	base := snapshot(t, dir)
	writeFile(t, dir, "a.go", fileB)
	gitRun(t, dir, "add", "a.go")
	writeFile(t, dir, "a.go", fileC)

	staged := findings(t, withSwallowed(t, dir, "diff", "--staged", "--base", base, "--json"))
	if got := lines(staged); !slices.Equal(got, []int{11}) {
		t.Errorf("staged finding lines %v, want [11]", got)
	}
	all := findings(t, withSwallowed(t, dir, "diff", "--base", base, "--json"))
	if got := lines(all); !slices.Equal(got, []int{11, 16}) {
		t.Errorf("working tree finding lines %v, want [11 16]", got)
	}
}

func TestDiffCountsUntrackedFilesAsNew(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.go", fileB) // untracked, and HEAD has no commit
	res := findings(t, withSwallowed(t, dir, "diff", "--json"))
	if got := lines(res); !slices.Equal(got, []int{6, 11}) {
		t.Fatalf("finding lines %v, want [6 11]", got)
	}
}

func TestDiffWithNoGoChange(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.go", fileB)
	base := snapshot(t, dir)
	writeFile(t, dir, "notes.txt", "x\n")
	r := withSwallowed(t, dir, "diff", "--base", base)
	if r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "no findings on changed lines") {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
}

func TestGate(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.go", fileA)

	r := withSwallowed(t, dir, "check", "--gate", "0", "--json")
	if r.code != 1 || !findings(t, r).GateFailed {
		t.Errorf("--gate 0: exit %d, want 1 and gate_failed\n%s", r.code, r.stdout)
	}
	rule, _ := nitpick.Lookup("swallowed_error")
	if rule.Precision < 0.99 {
		above := fmt.Sprintf("%d%%", int(rule.Precision*100)+1)
		if r := withSwallowed(t, dir, "check", "--gate", above); r.code != 0 {
			t.Errorf("--gate %s: exit %d, want 0\n%s", above, r.code, r.stderr)
		}
	}
	if r := withSwallowed(t, dir, "check", "--gate", "2x"); r.code != 2 {
		t.Errorf("--gate 2x: exit %d, want 2", r.code)
	}
}

func TestErrorsExitWith2(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.go", "package m\n\nfunc A() { undefined() }\n")
	if r := nitpickIn(t, dir, "check"); r.code != 2 || !strings.Contains(r.stderr, "undefined") {
		t.Errorf("a type error: exit %d, want 2\n%s", r.code, r.stderr)
	}
	if r := nitpickIn(t, dir, "check", "--enable", "no_such_rule"); r.code != 2 || !strings.Contains(r.stderr, "is not a rule") {
		t.Errorf("an unknown rule: exit %d, want 2\n%s", r.code, r.stderr)
	}
	if r := nitpickIn(t, dir, "check", "--staged"); r.code != 2 {
		t.Errorf("check --staged: exit %d, want 2", r.code)
	}
}

func TestEnableAndDisable(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.go", "package m\n\nfunc foo() {}\n")
	if res := findings(t, nitpickIn(t, dir, "check", "--json")); len(res.Findings) != 0 {
		t.Errorf("junk_identifier is off by default, but: %+v", res.Findings)
	}
	res := findings(t, nitpickIn(t, dir, "check", "--json", "--enable", "all", "--disable", "deep_nesting"))
	if len(res.Findings) != 1 || res.Findings[0].Rule != "junk_identifier" {
		t.Errorf("--enable all: %+v", res.Findings)
	}
}

func TestRulesList(t *testing.T) {
	r := nitpickIn(t, t.TempDir(), "rules", "--json")
	var rows []struct {
		Name    string
		Default bool
	}
	if err := json.Unmarshal([]byte(r.stdout), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(nitpick.Rules) {
		t.Fatalf("%d rules listed, want %d", len(rows), len(nitpick.Rules))
	}
	text := nitpickIn(t, t.TempDir(), "rules").stdout
	for _, rule := range nitpick.Rules {
		if !strings.Contains(text, rule.Name()) {
			t.Errorf("nitpick rules does not list %s", rule.Name())
		}
	}
}

func TestParseDiff(t *testing.T) {
	top := t.TempDir()
	diff := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n" +
		"@@ -3 +3 @@ func a() {\n-old\n+new\n" +
		"@@ -10,2 +11,0 @@\n-gone\n-gone\n" +
		"@@ -20,0 +21,3 @@\n+a\n+b\n+c\n" +
		"diff --git a/y.go b/y.go\n--- a/y.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-y\n"
	c := parseDiff(diff, top)
	x := normPath(filepath.Join(top, "x.go"))
	want := []lineRange{{3, 3}, {21, 23}}
	if !slices.Equal(c[x], want) || len(c) != 1 {
		t.Fatalf("parseDiff: %v, want %s: %v", c, x, want)
	}
}

func TestParseArgs(t *testing.T) {
	for _, s := range []string{"0.9", "90", "90%"} {
		if v, err := parseGate(s); err != nil || v != 0.9 {
			t.Errorf("parseGate(%q) = %v, %v", s, v, err)
		}
	}
	if _, err := parseGate("150%"); err == nil {
		t.Error("parseGate(150%) has no error")
	}
}
