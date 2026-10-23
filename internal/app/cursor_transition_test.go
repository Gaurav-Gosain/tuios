package app

import (
	"testing"

	"tuios.dev/vt"
)

// cursorTransitionTerminal models a guest showing its cursor between the
// ownership query and the locked cell walk. The first visibility sample is
// hidden; later samples see the visible emulator, without scheduler timing.
type cursorTransitionTerminal struct {
	vt.Terminal
	hideNext bool
}

func (term *cursorTransitionTerminal) IsCursorHidden() bool {
	if term.hideNext {
		term.hideNext = false
		return true
	}
	return term.Terminal.IsCursorHidden()
}

// A hidden native cursor must retain ownership of rendering. Otherwise a
// hidden-to-visible transition paints a software cursor into the content cache,
// and the independently sampled native cursor leaves that mark behind on moving.
func TestNativeCursorVisibilityTransition(t *testing.T) {
	for _, tc := range []struct {
		name       string
		hidden     bool
		transition bool
	}{
		{name: "visible"},
		{name: "hidden", hidden: true},
		{name: "hidden-to-visible", transition: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			win := newTestWindow(t, "cursor-transition", 40, 8)
			win.WriteOutput([]byte("abcdefghijklmnop\x1b[1;6H"))
			if tc.hidden {
				win.WriteOutput([]byte("\x1b[?25l"))
			}
			if tc.transition {
				win.LockIO()
				win.Terminal = &cursorTransitionTerminal{Terminal: win.Terminal, hideNext: true}
				win.UnlockIO()
			}
			m := newTestOS(win)
			m.Mode = TerminalMode

			frame := m.renderTerminal(win, true, true)
			if n := countFakeCursors(frame); n != 0 {
				t.Errorf("render painted %d software cursors while the native cursor owns rendering", n)
			}
			if win.CachedContent != frame || win.ContentDirty {
				t.Fatal("fixture did not cache the rendered text")
			}

			// Cursor-only output can reach the emulator before the UI consumes
			// its dirty notification. Keep the content cache and sample the
			// native cursor independently, just as View does.
			win.LockIO()
			_, err := win.Terminal.Write([]byte("\x1b[1;11H"))
			win.UnlockIO()
			if err != nil {
				t.Fatal(err)
			}
			cur := m.getRealCursor()
			if tc.hidden {
				if cur != nil {
					t.Errorf("hidden guest has a visible native cursor: %v", cur)
				}
			} else if cur == nil || cur.X != 11 || cur.Y != 1 {
				t.Errorf("native cursor did not move to screen (11, 1): %v", cur)
			}
			if cached := m.renderTerminal(win, true, true); cached != frame || countFakeCursors(cached) != 0 {
				t.Error("cached text retained a software cursor after the native cursor moved")
			}

			// Without native ownership, the same guest still needs a software cursor.
			win.WriteOutput([]byte("\x1b[?25h"))
			m.ShowScrollbackBrowser = true
			win.ContentDirty = true
			win.CachedContent = ""
			if countFakeCursors(m.renderTerminal(win, true, true)) == 0 {
				t.Fatal("software fallback drew no cursor without native ownership")
			}
			t.Log("software fallback rendered without native ownership")
		})
	}
}
