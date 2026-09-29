package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestUnknownStateDrawsItsMarkAndAgesItsEvidence: a pane set to unknown draws
// the unknown mark on its rail row, never idle's circle, and the three
// detection verbs report how old the evidence behind that state is. The frame
// is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The mark could be read from the pane's title bar or the dock, which also
//     draw it. The row is read inside the rail's columns, on the row that
//     names the pane.
//   - An age that is always 0, or a timestamp passed off as an age, would still
//     be a number. Two reads a known interval apart must differ by at least
//     that interval, and the first must be small.
//   - evidence_age_ms missing reads as nil after decoding, which is not a
//     number, so a build without the field fails the type check.
func TestUnknownStateDrawsItsMarkAndAgesItsEvidence(t *testing.T) {
	const cols, rows = 80, 24
	const pane = "probe"
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "unk", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "unk", "--name", pane); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "unk", "-w", pane, "unknown", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state unknown: %v\n%s", err, out)
	}
	setAt := time.Now()

	term := attachIn(t, base, "unk", startOpts{cols: cols, rows: rows, shippedLooks: true})
	railCol := cols - narrowRailWidth
	railRow := func(s tuitest.Screen) string {
		for y := range rows {
			var b strings.Builder
			for x := railCol; x < cols; x++ {
				b.WriteString(s.Cell(x, y).Content)
			}
			if row := b.String(); strings.Contains(row, pane) {
				return row
			}
		}
		return ""
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(railRow(s), "□") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never drew the unknown mark on the pane's row: %v\nrow: %q\n%s", err, railRow(term.Screen()), term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-unknown-80x24")
	if row := railRow(term.Screen()); strings.Contains(row, "○") {
		t.Errorf("ASSERTION: the unknown row wears idle's circle: %q", row)
	}

	age := func(args ...string) float64 {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, out)
		}
		var res map[string]any
		if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &res); err != nil {
			t.Fatalf("%s --json does not parse: %v\n%s", args[0], err, out)
		}
		if agents, ok := res["agents"].([]any); ok {
			res = nil
			for _, a := range agents {
				if row, _ := a.(map[string]any); row["name"] == pane {
					res = row
				}
			}
		}
		if res["state"] != "unknown" {
			t.Fatalf("%s: state %v, want unknown\n%s", args[0], res["state"], out)
		}
		ms, ok := res["evidence_age_ms"].(float64)
		if !ok {
			t.Fatalf("ASSERTION: %s has no numeric evidence_age_ms: %v\n%s", args[0], res["evidence_age_ms"], out)
		}
		return ms
	}
	reads := [][]string{
		{"get-agent-state", "-s", "unk", "-w", pane, "--json"},
		{"explain-agent-detect", "-s", "unk", "-w", pane, "--json"},
		{"list-agents", "-s", "unk", "--json"},
	}
	first := make([]float64, len(reads))
	for i, args := range reads {
		first[i] = age(args...)
		// The daemon measures the age on the wall clock and this test on the
		// monotonic clock. The two can drift by a millisecond, so allow one.
		if limit := float64(time.Since(setAt).Milliseconds()) + 1; first[i] > limit {
			t.Errorf("ASSERTION: %s reports evidence %vms old, older than the %vms since the report", args[0], first[i], limit)
		}
	}
	const gap = 1500 * time.Millisecond
	time.Sleep(gap)
	for i, args := range reads {
		if second := age(args...); second-first[i] < float64(gap.Milliseconds()) {
			t.Errorf("ASSERTION: %s evidence aged %vms over a %v wait (%v then %v)", args[0], second-first[i], gap, first[i], second)
		}
	}
}
