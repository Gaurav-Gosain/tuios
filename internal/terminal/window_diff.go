package terminal

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// DiffCell is a minimal cell representation for the screen diff protocol.
// Avoids importing the session package (which would create a cycle).
type DiffCell struct {
	Row, Col int
	Content  string
	Width    int
	Fg, Bg   uint32
	Attrs    uint16
	UlColor  uint32
	UlStyle  uint8
}

// ApplyScreenDiff writes changed cells from a daemon screen diff directly
// into the terminal emulator's screen buffer. This bypasses the VT parser
// entirely: no raw bytes, no escape sequences, just cell data. Used by
// the event-based screen diff protocol to update daemon windows without
// risk of byte-stream corruption.
func (w *Window) ApplyScreenDiff(cells []DiffCell, cursorX, cursorY int, cursorHidden, isAltScreen bool) {
	if w.Terminal == nil {
		return
	}

	w.ioMu.Lock()
	for _, c := range cells {
		cell := &uv.Cell{
			Content: c.Content,
			Width:   c.Width,
			Style: uv.Style{
				Fg:             unpackColor(c.Fg),
				Bg:             unpackColor(c.Bg),
				Attrs:          unpackDiffAttrs(c.Attrs),
				Underline:      uv.Underline(c.UlStyle),
				UnderlineColor: unpackColor(c.UlColor),
			},
		}
		w.Terminal.SetCell(c.Col, c.Row, cell)
	}
	w.ioMu.Unlock()

	w.SetAltScreen(isAltScreen)

	w.HasNewOutput.Store(true)
	w.MarkContentDirty()
	if w.PTYDataChan != nil {
		select {
		case w.PTYDataChan <- struct{}{}:
		default:
		}
	}
}

// unpackColor converts a packed RGBA uint32 to a color.Color.
// 0 means "default terminal color" (nil).
func unpackColor(rgba uint32) color.Color {
	if rgba == 0 {
		return nil
	}
	return color.RGBA{
		R: uint8(rgba >> 24),
		G: uint8(rgba >> 16),
		B: uint8(rgba >> 8),
		A: uint8(rgba),
	}
}

// unpackDiffAttrs converts DiffCell attrs bitmask to ultraviolet's uint8 Attrs.
func unpackDiffAttrs(attrs uint16) uint8 {
	// DiffCell bitmask matches ultraviolet's AttrBold..AttrStrikethrough order
	return uint8(attrs & 0xFF)
}

// UpdateThemeColors updates the terminal colors when the theme changes
func (w *Window) UpdateThemeColors() {
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
		// Mark the window as dirty to trigger a redraw
		w.Dirty = true
		w.ContentDirty = true
	}
}
