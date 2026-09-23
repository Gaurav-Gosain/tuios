package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// TestFullyVisibleWindowBoxFitsItsRectangle is the contract GetCanvas relies
// on when it hands a window inside the viewport to the compositor without
// clipping it: the box renderWindowBox draws is never wider or taller than the
// window's own rectangle. If it were, the unclipped box would draw over the
// pane beside it.
//
// The titles are the input that has broken width contracts before. A guest can
// put any bytes in an OSC title, and ansi measures malformed UTF-8 differently
// from how it truncates it (see TestClipNeverExceedsViewportWidth). Both body
// paths run: the pre-shaped one the emulator lays out to the pane, and the
// lipgloss one that fitToContentBox trims.
//
// Negative control: laying the title bar out one cell wider in
// layoutBorderRow fails every bordered case here.
func TestFullyVisibleWindowBoxFitsItsRectangle(t *testing.T) {
	prevZen, prevPreShaped := config.Global.ZenMode, preShapedDisabled
	t.Cleanup(func() { config.Global.ZenMode, preShapedDisabled = prevZen, prevPreShaped })

	titles := []string{
		"",
		"a plain title",
		strings.Repeat("long title ", 20),
		"世世世世世\xe4\xb800\xb80",
		strings.Repeat("\xe4\xb8", 40),
		"ab\xffcd\xfe" + strings.Repeat("世", 30),
	}
	bodies := []string{
		"$ ls\r\n",
		strings.Repeat("x", 300) + "\r\n" + strings.Repeat("世", 200),
		"\x1b[38;2;200;100;50m" + strings.Repeat("colour ", 40) + "\x1b[0m",
	}
	sawTitle := false
	for _, noPreShape := range []bool{false, true} {
		preShapedDisabled = noPreShape
		for _, zen := range []string{config.ZenModeDisabled, config.ZenModeAlways} {
			config.Global.ZenMode = zen
			for _, size := range []struct{ w, h int }{{12, 4}, {40, 10}, {61, 17}} {
				for ti, title := range titles {
					for bi, body := range bodies {
						for _, focused := range []bool{true, false} {
							name := fmt.Sprintf("preshaped=%v zen=%s %dx%d title=%d body=%d focused=%v",
								!noPreShape, zen, size.w, size.h, ti, bi, focused)
							win := newTestWindow(t, fmt.Sprintf("fit-%d-%d", ti, bi), size.w, size.h)
							win.X, win.Y = 3, 2
							win.SetTitle(title)
							win.LockIO()
							_, _ = win.Terminal.Write([]byte(body))
							win.UnlockIO()
							win.MarkContentDirty()
							m := newTestOS(win)
							m.Width, m.Height = 120, 40

							box := m.renderWindowBox(win, 0, focused, theme.BorderFocusedWindow())
							if ti == 1 && strings.Contains(ansi.Strip(box), "a plain") {
								sawTitle = true
							}
							rows := strings.Split(box, "\n")
							if len(rows) > win.Height {
								t.Errorf("%s: box is %d rows in a %d-row window", name, len(rows), win.Height)
							}
							for i, row := range rows {
								if w := ansi.StringWidth(row); w > win.Width {
									t.Errorf("%s: row %d is %d cells in a %d-cell window", name, i, w, win.Width)
								}
							}
						}
					}
				}
			}
		}
	}
	if !sawTitle {
		t.Error("setup: no box drew its title, so the titles tested nothing")
	}
}
