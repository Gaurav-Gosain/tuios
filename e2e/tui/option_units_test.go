package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOptionsSayTheirUnit: an int option's number is shown with what it
// counts. list-options printed motion.layout_ms as "default 200" and the
// settings page as a bare 200, while older duration options read "120ms".
//
// It checks three kinds: a unit taken from the path (motion.layout_ms),
// a unit the entry names (appearance.gap, cells), and a percent
// (appearance.master_ratio). A plain count (appearance.master_count) must
// stay a bare number.
//
// How this could pass wrongly, written down first:
//   - The unit could be in the description. The test reads the default and
//     range lines and the JSON field, not the description.
//   - A unit on every int would pass the first three. master_count must have
//     none.
func TestOptionsSayTheirUnit(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-units", "--detach"); err != nil {
		t.Fatalf("new session: %v\n%s", err, out)
	}

	out, err := tuiosCLI(t, base, "list-options", "--json")
	if err != nil {
		t.Fatalf("list-options --json: %v\n%s", err, out)
	}
	var listed struct {
		Options []struct {
			Path string `json:"path"`
			Unit string `json:"unit"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("list-options --json is not JSON: %v", err)
	}
	units := map[string]string{}
	for _, o := range listed.Options {
		units[o.Path] = o.Unit
	}
	for path, want := range map[string]string{
		"motion.layout_ms":        "ms",
		"appearance.gap":          "cells",
		"appearance.master_ratio": "%",
		"appearance.master_count": "",
	} {
		if got := units[path]; got != want {
			t.Errorf("list-options --json gives %s the unit %q, want %q", path, got, want)
		}
	}

	human, err := tuiosCLI(t, base, "list-options", "motion.layout_ms")
	if err != nil {
		t.Fatalf("list-options motion.layout_ms: %v\n%s", err, human)
	}
	for _, want := range []string{" ms\n", "range: 0 to "} {
		if !strings.Contains(human, want) {
			t.Errorf("list-options motion.layout_ms does not say %q:\n%s", want, human)
		}
	}
	if !strings.Contains(human, "default ") || !strings.Contains(strings.SplitN(strings.SplitN(human, "default ", 2)[1], "\n", 2)[0], " ms") {
		t.Errorf("list-options motion.layout_ms does not give its default in ms:\n%s", human)
	}
	count, err := tuiosCLI(t, base, "list-options", "appearance.master_count")
	if err != nil {
		t.Fatalf("list-options appearance.master_count: %v\n%s", err, count)
	}
	line := strings.SplitN(strings.SplitN(count, "default ", 2)[1], "\n", 2)[0]
	if strings.TrimSpace(line) != strings.Fields(line)[0] {
		t.Errorf("a plain count has a unit after its default: %q", line)
	}
}
