package app

import (
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
	entries, err := os.ReadDir(fuzzReproDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := fuzzScratch(t)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".txt" {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(fuzzReproDir, e.Name()))
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
			if res.Failed {
				t.Fatalf("broke %s again\n%s", res.Violations[0].Rule, res.Repro())
			}
		})
	}
}
