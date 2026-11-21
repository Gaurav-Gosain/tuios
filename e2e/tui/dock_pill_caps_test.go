package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

var dockCapGlyphs = []string{"", "", "◖", "◗", "[", "]"}

func dockCapsIn(row string) []string {
	var found []string
	for _, r := range row {
		for _, g := range dockCapGlyphs {
			if string(r) == g {
				found = append(found, g)
			}
		}
	}
	return found
}

// TestDockPillCapsFollowTheSetting: with dock_pill_caps = false the dock row
// draws no cap on any pill, and with it true, or unset, the mode chip and the
// workspace pills are capped. Before #451 the mode chip and the workspace
// pills took their caps from accessors that ignored the setting, so false
// changed nothing a user could see. The default is rounded.
//
// Negative controls: build 1e0b4c89, before #451, and the off case fails
// with the mode chip's and the workspace pills' caps on the dock row. Build
// eced6152, where the default was flat, and the default case fails with no
// caps on the row.
func TestDockPillCapsFollowTheSetting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		capped bool
	}{
		{"off", "dock_pill_caps = false\n", false},
		{"on", "dock_pill_caps = true\n", true},
		{"default", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[appearance]\n"+tc.config)
			term := startIn(t, base, startOpts{cols: 120, rows: 30})
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(dockRow(s), "1:0")
			}, bootTimeout); err != nil {
				t.Fatalf("the dock never drew: %v\n%s", err, term.Snapshot())
			}
			row := dockRow(term.Screen())
			caps := dockCapsIn(row)
			if !tc.capped && len(caps) > 0 {
				t.Fatalf("%s: the dock row draws caps %q, want flat pills\n%s", tc.name, caps, row)
			}
			if tc.capped && len(caps) < 4 {
				t.Fatalf("%s: the dock row draws only caps %q, want caps on the mode chip and the workspace pills\n%s", tc.name, caps, row)
			}
		})
	}
}
