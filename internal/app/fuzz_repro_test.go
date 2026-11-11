package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
)

// The corpus of shrunk repros, replayed on every ordinary test run.
//
// A finding that only lives in the fuzzer's seed set is a finding the suite
// rediscovers by luck: the campaigns walk pseudo-random sequences and a fix that
// regresses may not be caught for thousands of seeds. Each script here broke a
// named oracle rule once, so replaying it costs a few frames and pins the fix
// where it can be seen.
//
// To add one, drop the `--- script ---` block a campaign printed into
// testdata/fuzz-repros as a .txt file and name it after the rule it broke.
const fuzzReproDir = "testdata/fuzz-repros"

func TestFuzzRepros(t *testing.T) {
	dir := fuzzScratch(t)
	for _, path := range reproFiles(t, fuzzReproDir) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if res := replayRepro(t, dir, path); res.Failed {
				t.Fatalf("broke %s again\n%s", res.Violations[0].Rule, res.Repro())
			}
		})
	}
}

// fuzzKnownDir holds repros of product bugs the fuzzer found and nobody has
// fixed yet. Each must still break its rule: a fix that makes one pass moves
// it up into fuzzReproDir, where it guards the fix.
const fuzzKnownDir = fuzzReproDir + "/known"

func TestFuzzKnownBugs(t *testing.T) {
	dir := fuzzScratch(t)
	for _, path := range reproFiles(t, fuzzKnownDir) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if res := replayRepro(t, dir, path); !res.Failed {
				t.Errorf("%s passes now: the bug is fixed, so move it to %s", path, fuzzReproDir)
			}
		})
	}
}

// reproFiles lists the repro scripts in dir. A missing dir has none: git
// keeps no empty folder, so the known folder goes away with its last bug.
func reproFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".txt" {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func replayRepro(t *testing.T, dir, path string) fuzz.Result {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := fuzz.ParseScript(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	res, err := fuzz.Run(func() (fuzz.Target, error) { return newFuzzTarget(dir) },
		fuzz.Config{Actions: actions, NoShrink: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// maxFlakyRuns is how many runs of one test may break a rule without
// reproducing. The model reads the wall clock, so a replay can differ, and one
// such run in a test is tolerated; more says the harness or the model has
// stopped being replayable, and that must not hide behind the rule. It goes to
// zero once the model is handed its clock (R6).
const maxFlakyRuns = 1

// fuzzVerdict judges one fuzz run. A run that broke a rule fails the test
// unless its minimal script is one of the known bugs (fuzzKnownDir), which
// TestFuzzKnownBugs already holds, or the finding did not reproduce: then the
// model's replays differ, which a script cannot pin down, so it is logged and
// reported as flaky for the caller to count against maxFlakyRuns.
func fuzzVerdict(t *testing.T, label string, res fuzz.Result) (flaky bool) {
	t.Helper()
	if !res.Failed {
		return false
	}
	if res.Flaky {
		t.Logf("%s broke %s and did not reproduce: the model's replays differ\n%s", label, res.Violations[0].Rule, res.Repro())
		return true
	}
	if name, ok := knownBug(t, res.Actions); ok {
		t.Logf("%s broke %s, a known bug (%s)", label, res.Violations[0].Rule, name)
		return false
	}
	t.Errorf("%s broke %s\n%s", label, res.Violations[0].Rule, res.Repro())
	return false
}

// checkFlakyRuns fails tb when more runs than maxFlakyRuns did not reproduce.
func checkFlakyRuns(tb testing.TB, flaky int) {
	tb.Helper()
	if flaky > maxFlakyRuns {
		tb.Errorf("%d runs broke a rule and did not reproduce; at most %d may: the model's replays have stopped being repeatable", flaky, maxFlakyRuns)
	}
}

// knownBug reports which known repro, if any, is exactly this script.
func knownBug(t *testing.T, actions []fuzz.Action) (string, bool) {
	t.Helper()
	want := fuzz.Script(actions)
	for _, path := range reproFiles(t, fuzzKnownDir) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		known, err := fuzz.ParseScript(string(raw))
		if err == nil && fuzz.Script(known) == want {
			return filepath.Base(path), true
		}
	}
	return "", false
}
