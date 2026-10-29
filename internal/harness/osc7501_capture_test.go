package harness

import (
	"os"
	"strings"
	"testing"

	"tuios.dev/progstatus"
)

// TestCapturedClaudeCode7501 parses the bytes Claude Code 2.1.312 wrote, kept
// under testdata/osc7501 with their provenance. It checks progstatus against a
// real emitter, not a hand-written example.
func TestCapturedClaudeCode7501(t *testing.T) {
	body := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile("testdata/osc7501/" + name)
		if err != nil {
			t.Fatal(err)
		}
		b, ok := strings.CutPrefix(strings.TrimSuffix(string(raw), "\a"), "\x1b]7501;")
		if !ok {
			t.Fatalf("%s is not OSC 7501: %q", name, raw)
		}
		return b
	}
	if q := body("claude-code-2.1.312-query.bin"); !progstatus.IsQuery([]byte(q)) {
		t.Errorf("query capture is not the feature query: %q", q)
	}
	b := body("claude-code-2.1.312-idle.bin")
	r, ok := progstatus.Parse([]byte(b), progstatus.SequenceLen(len("7501;")+len(b), true))
	if !ok || r.State != progstatus.Idle || r.App != "claude-code" || r.Progress != progstatus.NoProgress {
		t.Fatalf("parsed %+v, ok=%v", r, ok)
	}
}
