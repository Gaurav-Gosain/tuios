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
// newest entries, or clamped past them. The viewer now moves a cursor through
// the shared list overlay, and these cases hold the keyboard to it: opening
// lands on the newest entry, g and end reach both ends, and the entry under the
// cursor is always one the renderer draws.
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
				if total == 0 {
					return
				}
				newest := len(o.LogMessages) - 1
				if o.LogSelected != newest {
					t.Fatalf("opening left the cursor on %d, want the newest %d", o.LogSelected, newest)
				}
				o.View() // the renderer clamps the scroll to the rows it draws
				if o.LogScrollOffset > o.LogSelected {
					t.Fatalf("the newest entry %d is above the first drawn row %d", o.LogSelected, o.LogScrollOffset)
				}

				out, _ := handleLogViewerKey(tea.KeyPressMsg{Code: 'g'}, o)
				if out.LogSelected != 0 || out.LogScrollOffset != 0 {
					t.Fatalf("g left the cursor on %d at offset %d, want 0 at 0", out.LogSelected, out.LogScrollOffset)
				}
				out, _ = handleLogViewerKey(tea.KeyPressMsg{Code: tea.KeyEnd}, o)
				if out.LogSelected != newest {
					t.Fatalf("end left the cursor on %d, want %d", out.LogSelected, newest)
				}
				out.View()
				if out.LogScrollOffset > newest {
					t.Fatalf("end scrolled past the newest entry: offset %d", out.LogScrollOffset)
				}
			})
		}
	}
}
