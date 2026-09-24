package terminal

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// applyTheme sets the active theme's colors on an emulator, or clears them to
// the terminal defaults when theming is disabled.
func applyTheme(t vt.Terminal) {
	if theme.IsEnabled() {
		t.SetThemeColors(
			theme.TerminalFg(),
			theme.TerminalBg(),
			theme.TerminalCursor(),
			theme.GetANSIPalette(),
		)
	} else {
		t.SetThemeColors(nil, nil, nil, [16]color.Color{})
	}
}

// SetReportColors tells the emulator what the pane is really drawn on, so an
// OSC 10 or OSC 11 query from the program is answered with it. key names the
// pair, and a pair already given is not given again, which is what lets the
// renderer ask on every frame: with the pane background off the key is empty
// and so is the one held here.
func (w *Window) SetReportColors(fg, bg color.Color, key string) {
	if w.reportKey == key {
		return
	}
	w.reportKey = key
	w.ioMu.Lock()
	if w.Terminal != nil {
		w.Terminal.SetReportColors(fg, bg)
	}
	w.ioMu.Unlock()
}

// UpdateThemeColors pushes the active theme's palette into the emulator so
// already-rendered SGR indexed colors resolve to the new theme on the next
// render. SetThemeColors mutates the emulator's color table, which the PTY
// reader goroutine reads under ioMu inside Terminal.Write, so it is taken here
// (this runs on the UI goroutine) to avoid a torn interface-value read.
func (w *Window) UpdateThemeColors() {
	w.ioMu.Lock()
	if w.Terminal != nil {
		applyTheme(w.Terminal)
	}
	w.ioMu.Unlock()

	// Mark dirty and drop the cached render: the palette changed, so both the
	// cached content string and the cached styled layer are stale.
	w.Dirty = true
	w.ContentDirty = true
	w.InvalidateCache()
}
