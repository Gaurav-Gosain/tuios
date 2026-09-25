package vt_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen"
)

// Pinned generator findings, replayed on every ordinary test run.
//
// A corpus entry under testdata/fuzz is the input bytes of a vtgen target, and
// those bytes mean a script only through the generator that decoded them. Any
// change to vtgen (a new sequence family, a reweighted draw) decodes every
// entry to a different script, and a regression seed that guarded a bug
// silently stops guarding it while still passing. That happened to the two
// FuzzEmulatorRenderRoundTrip entries when the generator grew.
//
// A file here is the script itself, the steps with their bytes, so it means
// the same thing whatever the generator becomes. Every oracle the vtgen
// targets have runs over it. The fuzz targets print this form on failure; to
// pin a finding, save the printed JSON under testdata/vtgen-repros with a name
// that says what it broke and a why line.
const vtgenReproDir = "testdata/vtgen-repros"

// vtgenRepro is one pinned script.
type vtgenRepro struct {
	// Why says what the script broke and which commit fixed it.
	Why string `json:"why"`
	// SplitSeed is the seed the split-write oracles cut the bytes with.
	SplitSeed uint64 `json:"split_seed"`
	// BudgetMS, when set, is how long all the oracles together may take. It
	// pins a performance fix, so it is set well above the fixed time and well
	// below the time the bug took.
	BudgetMS int          `json:"budget_ms,omitempty"`
	Script   vtgen.Script `json:"script"`
}

// pinnable renders a failing script in the form a file here takes.
func pinnable(s vtgen.Script, seed uint64, broken string) string {
	b, err := json.MarshalIndent(vtgenRepro{Why: broken, SplitSeed: seed, Script: s}, "", "  ")
	if err != nil {
		return fmt.Sprintf("(cannot render the repro: %v)", err)
	}
	return "to pin it, save as " + vtgenReproDir + "/<what-it-broke>.json:\n" + string(b)
}

func TestVTGenRepros(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(vtgenReproDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no pinned scripts found; the directory moved or the glob is wrong")
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".json"), func(t *testing.T) {
			raw, err := os.ReadFile(f) //nolint:gosec // fixed test data path
			if err != nil {
				t.Fatal(err)
			}
			var r vtgenRepro
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if len(r.Script) == 0 || r.Why == "" {
				t.Fatalf("%s: a pinned script needs steps and a why", f)
			}
			start := time.Now()
			for _, o := range []struct {
				name string
				run  func() string
			}{
				{"invariants", func() string { return replay(r.Script) }},
				{"invariants, split", func() string { return replaySplit(r.Script, r.SplitSeed) }},
				{"split equivalence", func() string { return splitEquivalence(r.Script, r.SplitSeed) }},
				{"render round trip", func() string { return renderRoundTrip(r.Script) }},
			} {
				if broken := o.run(); broken != "" {
					t.Errorf("%s (%s): %s\n\n%s", o.name, r.Why, broken, r.Script)
				}
			}
			if took := time.Since(start); r.BudgetMS > 0 && took > time.Duration(r.BudgetMS)*time.Millisecond {
				t.Errorf("the oracles took %s, over the %dms budget (%s)", took, r.BudgetMS, r.Why)
			}
		})
	}
}
