package nitpick_test

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"tuios.dev/nitpick"
)

// TestRules runs each rule on its testdata package, which marks the true
// findings with want comments and holds near misses with none.
func TestRules(t *testing.T) {
	for _, r := range nitpick.Rules {
		t.Run(r.Name(), func(t *testing.T) {
			analysistest.Run(t, analysistest.TestData(), r.Analyzer, r.Name()+"/...")
		})
	}
}

// TestPrecisionTable keeps the table in PRECISION.md and Rules equal.
func TestPrecisionTable(t *testing.T) {
	doc, err := os.ReadFile("PRECISION.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`(?m)^\| ` + "`" + `(\w+)` + "`" + ` \| (on|off) \| (\d+) \| (\d+) \| (\d+)% \|`)
	seen := map[string]bool{}
	for _, m := range row.FindAllStringSubmatch(string(doc), -1) {
		r, ok := nitpick.Lookup(m[1])
		if !ok {
			t.Errorf("PRECISION.md names %s, which is not a rule", m[1])
			continue
		}
		seen[m[1]] = true
		sample, _ := strconv.Atoi(m[3])
		correct, _ := strconv.Atoi(m[4])
		pct, _ := strconv.Atoi(m[5])
		if (m[2] == "on") != r.Default {
			t.Errorf("%s: PRECISION.md says %s, Rules says Default=%v", m[1], m[2], r.Default)
		}
		if sample != r.Sample {
			t.Errorf("%s: PRECISION.md has a sample of %d, Rules has %d", m[1], sample, r.Sample)
		}
		if sample > 0 && (pct != int(float64(correct)*100/float64(sample)+0.5) || pct != int(r.Precision*100+0.5)) {
			t.Errorf("%s: %d of %d is not %d%%, or Rules has precision %v", m[1], correct, sample, pct, r.Precision)
		}
	}
	for _, r := range nitpick.Rules {
		if !seen[r.Name()] {
			t.Errorf("PRECISION.md has no row for %s", r.Name())
		}
	}
}
