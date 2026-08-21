package terminal

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// UpdateThemeColors pushes the active theme's palette into the emulator so
// already-rendered SGR indexed colors resolve to the new theme on the next
// render. SetThemeColors mutates the emulator's color table, which the PTY
// reader goroutine reads under ioMu inside Terminal.Write, so it is taken here
// (this runs on the UI goroutine) to avoid a torn interface-value read.
func (w *Window) UpdateThemeColors() {
	w.ioMu.Lock()
	if w.Terminal != nil {
		if theme.IsEnabled() {
			w.Terminal.SetThemeColors(
				theme.TerminalFg(),
				theme.TerminalBg(),
				theme.TerminalCursor(),
				theme.GetANSIPalette(),
			)
		} else {
			w.Terminal.SetThemeColors(nil, nil, nil, [16]color.Color{})
		}
	}
	w.ioMu.Unlock()

	// Mark dirty and drop the cached render: the palette changed, so both the
	// cached content string and the cached styled layer are stale.
	w.Dirty = true
	w.ContentDirty = true
	w.InvalidateCache()
}
