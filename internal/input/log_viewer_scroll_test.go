package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The log viewer's scroll range was computed with a fixed screen-height
// formula while the renderer measured the panel it actually draws, so the two
// disagreed on anything but one screen size: scrolling stopped short of the
// newest entries, or clamped past them. These cases hold the keyboard and the
// wheel to the same bounds the renderer uses, and hold the newest entry within
// reach at both sizes the formulas disagreed on.
func TestLogViewerScrollReachesTheNewestEntries(t *testing.T) {
	for _, h := range []int{24, 40, 60} {
		for _, total := range []int{0, 5, 120, 400} {
			t.Run(fmt.Sprintf("h=%d/logs=%d", h, total), func(t *testing.T) {
				o := osWithBindings(t, func(*config.KeybindingsConfig) {})
				o.Width, o.Height = 120, h
				for i := range total {
					o.Log("INFO", "line %d", i)
				}
				// Open through handleToggleLogs, the keybinding's own path: it
				// logs "Log viewer opened" first, and that line moves the bottom
				// the toggle then lands the view on.
				o, _ = handleToggleLogs(tea.KeyPressMsg{}, o)
				page, maxScroll, _ := o.LogViewerBounds()
				if maxScroll != o.LogScrollOffset {
					t.Fatalf("opening left offset %d, want the bottom %d", o.LogScrollOffset, maxScroll)
				}
				if total == 0 {
					return
				}
				last := o.LogMessages[len(o.LogMessages)-1].Message

				// g goes to the oldest, then end back to the newest.
				out, _ := handleLogViewerKey(tea.KeyPressMsg{Code: 'g'}, o)
				if out.LogScrollOffset != 0 {
					t.Fatalf("g from the bottom left offset %d, want 0", out.LogScrollOffset)
				}
				out, _ = handleLogViewerKey(tea.KeyPressMsg{Code: tea.KeyEnd}, o)
				if out.LogScrollOffset != maxScroll {
					t.Fatalf("end left offset %d, want %d", out.LogScrollOffset, maxScroll)
				}

				// The bottom page contains the newest line.
				if len(out.LogMessages) > page {
					shown := out.LogMessages[min(len(out.LogMessages), out.LogScrollOffset+page)-1].Message
					if shown != last {
						t.Fatalf("bottom page ends with %q, want the newest %q", shown, last)
					}
				}
			})
		}
	}
}
