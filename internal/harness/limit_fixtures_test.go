package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLimitScreensToday records what the bundled manifests do with the text
// harnesses show at a usage limit. Each fixture names the answer today, so a
// manifest that learns limits must update its fixture in the same change.
func TestLimitScreensToday(t *testing.T) {
	r := testRegistry(t)
	paths, err := filepath.Glob(filepath.Join("testdata", "limits", "*.txt"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no limit fixtures: %v", err)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for n, chunk := range strings.Split(string(data), fixtureSeparator) {
			head, body, ok := strings.Cut(chunk, "\n---\n")
			if !ok {
				t.Fatalf("%s#%d: no --- line", p, n+1)
			}
			meta := map[string]string{}
			for line := range strings.SplitSeq(head, "\n") {
				k, v, _ := strings.Cut(strings.TrimPrefix(line, "# "), ":")
				meta[k] = strings.TrimSpace(v)
			}
			switch meta["source"] {
			case "from source", "from user report", "captured":
			default:
				t.Errorf("%s#%d: source %q", p, n+1, meta["source"])
			}
			if meta["ref"] == "" {
				t.Errorf("%s#%d: no ref", p, n+1)
			}
			t.Run(filepath.Base(p)+"#"+string(rune('0'+n+1)), func(t *testing.T) {
				id := meta["harness"]
				if r.Lookup(id) == nil {
					t.Fatalf("no manifest %q", id)
				}
				tail := fixtureTail(strings.Split(body, "\n"), r.ScreenLines(id))
				state, _, ok := r.Classify(id, tail)
				got := "none"
				if ok {
					got = state
				}
				if got != meta["today"] {
					t.Errorf("classified as %s, want %s", got, meta["today"])
				}
			})
		}
	}
}
