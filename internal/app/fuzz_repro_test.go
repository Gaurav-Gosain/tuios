package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
)

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

const maxFlakyRuns = 1

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

func checkFlakyRuns(tb testing.TB, flaky int) {
	tb.Helper()
	if flaky > maxFlakyRuns {
		tb.Errorf("%d runs broke a rule and did not reproduce; at most %d may: the model's replays have stopped being repeatable", flaky, maxFlakyRuns)
	}
}

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
