package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarMarqueeTimingIsAnOption: appearance.sidebar.marquee_pause_ms holds
// a hovered row still before its overflowing title starts to move, and
// marquee_step_ms is how long each step is held. With the default 900 ms pause
// the row has not moved 600 ms after the pointer lands; with a pause of 0 and a
// 20 ms step it has.
//
// Negative control: reading the fixed 900 ms pause again leaves the row still
// at 600 ms in the second run and fails it.
func TestSidebarMarqueeTimingIsAnOption(t *testing.T) {
	const name = "marquee-row-with-a-long-name-that-overflows-the-rail"
	for _, tc := range []struct {
		name, config string
		moves        bool
	}{
		{"default pause", "[appearance.sidebar]\nenabled = true\n", false},
		{"no pause", "[appearance.sidebar]\nenabled = true\nmarquee_pause_ms = 0\nmarquee_step_ms = 20\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, tc.config)
			killDaemon(t, base)
			if out, err := tuiosCLI(t, base, "new", "mq", "--detach"); err != nil {
				t.Fatalf("new: %v\n%s", err, out)
			}
			if out, err := tuiosCLI(t, base, "set-window", "-s", "mq", "--name", name); err != nil {
				t.Fatalf("name: %v\n%s", err, out)
			}
			term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "mq"}})
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(s.Text(), "marquee-row")
			}, bootTimeout); err != nil {
				t.Fatalf("the rail never showed the long name: %v\n%s", err, term.Snapshot())
			}
			// The name is on the pane's border too; the rail's row is the one
			// in the left-hand columns, where the rail is.
			row, col := -1, 0
			s := term.Screen()
			_, rows := s.Size()
			for r := 0; r < rows; r++ {
				if c := strings.Index(s.Line(r), "marquee-row"); c >= 0 && c < 30 {
					row, col = r, c
				}
			}
			if row < 0 {
				t.Fatalf("the rail has no row for the long name:\n%s", term.Snapshot())
			}
			rowText := func() string { return term.Screen().Line(row) }
			before := rowText()
			// A hover first shows the row whole (the ellipsis goes); the marquee has
			// moved once the title no longer starts at its own first letter.
			stillAtStart := func() bool { return strings.Contains(rowText(), "  marquee-row-with-a-long") }
			sendMouse(t, term, "hover", tuitest.MouseEvent{Col: col + 2, Row: row, Button: tuitest.MouseNone, Action: tuitest.MouseMove})
			// 600 ms is inside the default 900 ms pause and long past a pause of 0.
			if tc.moves {
				waitUntil(t, "the hovered row moves", 600*time.Millisecond, func() (bool, string) {
					return !stillAtStart(), fmt.Sprintf("before: %q after: %q", before, rowText())
				})
			} else {
				stayTrue(t, "the hovered row stays put", 600*time.Millisecond, func() (bool, string) {
					return stillAtStart(), fmt.Sprintf("before: %q after: %q", before, rowText())
				})
			}
		})
	}
}
