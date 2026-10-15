package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"github.com/charmbracelet/x/ansi"
)

// TestDockCenterStaysPutDuringNotifications reads the real client's cells:
// popups must draw and dismiss without shifting or covering the center clock.
func TestDockCenterStaysPutDuringNotifications(t *testing.T) {
	for _, fixture := range []struct {
		name, component, clock, position string
	}{
		{"custom-top", "custom/clock", "CLOCK 12:34", "top"},
		{"builtin-bottom", "clock", "CLOCK", "bottom"},
	} {
		for _, cols := range []int{80, 120, 180} {
			t.Run(fmt.Sprintf("%s/%dcols", fixture.name, cols), func(t *testing.T) {
				base := t.TempDir()
				writeConfig(t, base, fmt.Sprintf(`
[startup]
open_default_window = true
tiled = true

[appearance]
dockbar_position = %q
show_clock = true

[notifications]
duration = 2

[dock]
left = ["mode", "workspaces"]
center = [%q, "windows"]
right = ["notifications", "session-controls"]

[dock.clock]
format = "CLOCK"

[dock.custom.clock]
command = "printf 'CLOCK 12:34'"
refresh = "once"
`, fixture.position, fixture.component))

				term := startIn(t, base, startOpts{
					cols: cols, rows: 28, shippedLooks: true,
					args: []string{"new", "dock-center"},
				})
				if err := term.WaitForText(fixture.clock, bootTimeout); err != nil {
					t.Fatalf("clock did not draw: %v\n%s", err, term.Snapshot())
				}
				row := 0
				if fixture.position == "bottom" {
					row = 27
				}
				dismiss := func() {
					t.Helper()
					if err := term.SendKeys(tuitest.Esc); err != nil {
						t.Fatal(err)
					}
					// Let a bare Esc resolve before the next key can become Alt+key.
					time.Sleep(200 * time.Millisecond)
					if err := term.WaitFor(func(s tuitest.Screen) bool {
						line := s.Line(row)
						return strings.Contains(line, fixture.clock) && !strings.Contains(line, "CENTER") &&
							!strings.Contains(line, "Window created") && !strings.Contains(line, "Tiling") &&
							!strings.Contains(line, "\uf057") && !strings.Contains(line, "\uf05a")
					}, uiTimeout); err != nil {
						t.Fatalf("notification did not dismiss: %v\n%s", err, term.Snapshot())
					}
				}
				column := func() int {
					t.Helper()
					line := term.Screen().Line(row)
					i := strings.Index(line, fixture.clock)
					if i < 0 {
						t.Fatalf("clock clipped or covered: %q\n%s", line, term.Snapshot())
					}
					return ansi.StringWidth(line[:i])
				}
				dismiss()
				initial := column()
				longMessageShot(t, term, "before")
				for i, message := range []string{"Tiling off", "Tiling on"} {
					if err := term.SendKeys("t"); err != nil {
						t.Fatal(err)
					}
					if err := term.WaitForText(message, uiTimeout); err != nil {
						t.Fatalf("popup did not draw: %v\n%s", err, term.Snapshot())
					}
					longMessageShot(t, term, fmt.Sprintf("popup-%d", i))
					if got := column(); got != initial {
						t.Errorf("popup moved the clock from column %d to %d", initial, got)
					}
					if got, want := column(), (cols-len(fixture.clock))/2; got != want {
						t.Errorf("clock at column %d, want physical center column %d", got, want)
					}
					dismiss()
					if got := column(); got != initial {
						t.Errorf("dismissal moved the clock from column %d to %d", initial, got)
					}
				}

				// Named minimized panes must not be shortened by a popup, or use
				// so much of the dock that its message can no longer be opened.
				for i, name := range []string{"EDITOR-WITH-A-LONG-NAME", "SERVER-WITH-A-LONG-NAME"} {
					if i > 0 {
						if err := term.SendKeys("n"); err != nil {
							t.Fatal(err)
						}
						if err := term.WaitForText("$ ", uiTimeout); err != nil {
							t.Fatalf("new pane did not draw: %v\n%s", err, term.Snapshot())
						}
					}
					renameWindow(t, term, name)
					if err := term.SendKeys("m"); err != nil {
						t.Fatal(err)
					}
					if err := term.WaitForText(fmt.Sprintf("%d:", i+1), uiTimeout); err != nil {
						t.Fatalf("minimized pane did not reach the dock: %v\n%s", err, term.Snapshot())
					}
					dismiss()
					initial = column()
					out, err := tuiosCLI(t, base, "run-command", "--session", "dock-center", "ShowNotification", "CENTER "+strings.Repeat("long notification ", 8)+"END-CENTER", "error")
					if err != nil {
						t.Fatalf("show long notification: %v: %s", err, out)
					}
					// A crowded dock may show only the error mark and an ellipsis.
					if err := term.WaitForText("\uf057", uiTimeout); err != nil {
						t.Fatalf("long popup did not draw: %v\n%s", err, term.Snapshot())
					}
					longMessageShot(t, term, fmt.Sprintf("popup-with-%d-minimized", i+1))
					if got := column(); got != initial {
						t.Errorf("popup resized the center contents and moved the clock from %d to %d", initial, got)
					}
					line := term.Screen().Line(row)
					mark := strings.Index(line, "\uf057")
					if !strings.Contains(line, " ▊ \uf057") {
						t.Errorf("the popup has no gap before its cap: %q", line)
					}
					leftClick(t, term, ansi.StringWidth(line[:mark]), row)
					if err := term.WaitForText("END-CENTER", uiTimeout); err != nil {
						t.Fatalf("clicking the truncated popup did not open its full message: %v\n%s", err, term.Snapshot())
					}
					longMessageShot(t, term, fmt.Sprintf("full-message-%d", i+1))
					dismiss()
					if got := column(); got != initial {
						t.Errorf("dismissal moved the center contents from %d to %d", initial, got)
					}
				}
				line := term.Screen().Line(row)
				entry := strings.Index(line, "1:")
				if entry < 0 {
					t.Fatalf("no minimized entry to restore: %q", line)
				}
				leftClick(t, term, ansi.StringWidth(line[:entry]), row)
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return !strings.Contains(s.Line(row), "EDITOR") && strings.Contains(s.Text(), "EDITOR-WITH-A-LONG-NAME")
				}, uiTimeout); err != nil {
					t.Fatalf("clicking the minimized pane did not restore it: %v\n%s", err, term.Snapshot())
				}
				longMessageShot(t, term, "after-restore")
			})
		}
	}
}
