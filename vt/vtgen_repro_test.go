package vt_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"tuios.dev/vt/vtgen"
)

const vtgenReproDir = "testdata/vtgen-repros"

const budgetPasses = 3

type vtgenRepro struct {
	Why           string       `json:"why"`
	SplitSeed     uint64       `json:"split_seed"`
	BudgetMS      int          `json:"budget_ms,omitempty"`
	BudgetAllocMB uint64       `json:"budget_alloc_mb,omitempty"`
	Script        vtgen.Script `json:"script"`
}

func pinnable(s vtgen.Script, seed uint64, broken string) string {
	b, err := json.MarshalIndent(vtgenRepro{Why: broken, SplitSeed: seed, Script: s}, "", "  ")
	if err != nil {
		return fmt.Sprintf("(cannot render the repro: %v)", err)
	}
	var back vtgenRepro
	if err := json.Unmarshal(b, &back); err != nil || !reflect.DeepEqual(back.Script, s) {
		return fmt.Sprintf("(the repro does not read back as the same script, so pinning it would guard something else: %v)\n%s", err, b)
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
			oracles := []struct {
				name string
				run  func() string
			}{
				{"invariants", func() string { return replay(r.Script) }},
				{"invariants, split", func() string { return replaySplit(r.Script, r.SplitSeed) }},
				{"split equivalence", func() string { return splitEquivalence(r.Script, r.SplitSeed) }},
				{"render round trip", func() string { return renderRoundTrip(r.Script) }},
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			start := time.Now()
			for _, o := range oracles {
				if broken := o.run(); broken != "" {
					t.Errorf("%s (%s): %s\n\n%s", o.name, r.Why, broken, r.Script)
				}
			}
			took := time.Since(start)
			runtime.ReadMemStats(&after)
			if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; r.BudgetAllocMB > 0 && mb > r.BudgetAllocMB {
				t.Errorf("the oracles allocated %d MB, over the %d MB budget (%s)", mb, r.BudgetAllocMB, r.Why)
			}
			if r.BudgetMS > 0 && !raceEnabled {
				budget := time.Duration(r.BudgetMS) * time.Millisecond
				passes := 1
				for ; passes < budgetPasses && took > budget; passes++ {
					start := time.Now()
					for _, o := range oracles {
						o.run()
					}
					took = min(took, time.Since(start))
				}
				if took > budget {
					t.Errorf("the fastest of %d passes of the oracles took %s, over the %dms budget (%s)", passes, took, r.BudgetMS, r.Why)
				}
			}
		})
	}
}
