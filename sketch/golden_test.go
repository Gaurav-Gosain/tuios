package sketch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

// done lists the corpus files whose goldens must all match. A type joins it
// in the pull request that ports it.
var done = map[string]bool{
	"class":     true,
	"er":        true,
	"flowchart": true,
	"gantt":     true,
	"mindmap":   true,
	"pie":       true,
	"sequence":  true,
	"state":     true,
}

// goldenNow is the time gen.mjs renders with, for gantt.
var goldenNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// goldenZone is the time zone the gantt goldens were written in. pele reads dates in
// the process's zone, and gen.mjs ran in Asia/Dubai. The zone comes from the tzdata
// embedded in the test binary, so the test passes on a machine without the zone files.
var goldenZone = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Dubai")
	if err != nil {
		panic(err)
	}
	return loc
}()

type goldenCase struct {
	name   string // corpus file without -docs.json
	index  int
	source string
	want   []byte
}

func loadGoldens(t testing.TB) []goldenCase {
	t.Helper()
	files, err := filepath.Glob("testdata/corpus/*-docs.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	sort.Strings(files)
	var cases []goldenCase
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var sources []string
		if err := json.Unmarshal(data, &sources); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		name := strings.TrimSuffix(filepath.Base(f), "-docs.json")
		for i, src := range sources {
			want, err := os.ReadFile(fmt.Sprintf("testdata/golden/%s-%03d.svg", name, i))
			if err != nil {
				t.Fatalf("golden for %s #%d: %v", name, i, err)
			}
			cases = append(cases, goldenCase{name, i, src, want})
		}
	}
	return cases
}

// firstDifference shows where got and want part, from the element that holds the first differing byte.
func firstDifference(got, want []byte) string {
	n := min(len(got), len(want))
	at := 0
	for at < n && got[at] == want[at] {
		at++
	}
	start := strings.LastIndexByte(string(want[:at]), '<')
	if start < 0 {
		start = 0
	}
	window := func(b []byte) string {
		end := min(len(b), at+160)
		if start > len(b) {
			return ""
		}
		return string(b[start:end])
	}
	return fmt.Sprintf("first difference at byte %d (lengths got %d, want %d)\n got: %s\nwant: %s", at, len(got), len(want), window(got), window(want))
}

// TestGolden renders every source of pele's documentation corpus and compares the
// SVG byte for byte with what pele renders at the pinned commit. It reports how
// many match for each type, and fails for a type in `done` that does not match.
func TestGolden(t *testing.T) {
	cases := loadGoldens(t)
	type tally struct{ pass, fail, unsupported int }
	counts := map[string]*tally{}
	var order []string
	for _, c := range cases {
		tl := counts[c.name]
		if tl == nil {
			tl = &tally{}
			counts[c.name] = tl
			order = append(order, c.name)
		}
		out, err := Render(c.source, RenderOptions{Now: goldenNow, Location: goldenZone})
		if err != nil {
			if e, ok := err.(*Error); ok && e.Code == CodeUnsupported {
				tl.unsupported++
				continue
			}
			tl.fail++
			if done[c.name] {
				t.Errorf("%s-%03d: %v", c.name, c.index, err)
			}
			continue
		}
		if out.Data == string(c.want) {
			tl.pass++
			continue
		}
		tl.fail++
		if done[c.name] {
			t.Errorf("%s-%03d: %s", c.name, c.index, firstDifference([]byte(out.Data), c.want))
		}
	}
	var report strings.Builder
	total := 0
	passed := 0
	for _, name := range order {
		tl := counts[name]
		n := tl.pass + tl.fail + tl.unsupported
		total += n
		passed += tl.pass
		if tl.unsupported == n {
			continue
		}
		fmt.Fprintf(&report, "  %-14s %3d/%3d\n", name, tl.pass, n)
	}
	t.Logf("golden matches by type (types not ported yet are left out):\n%s  all            %3d/%3d", report.String(), passed, total)
}
