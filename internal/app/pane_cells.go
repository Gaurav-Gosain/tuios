package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

// A lone fullscreen pane on a client with a screen is drawn from its
// emulator's cells straight into the canvas. The fast path the string frame
// takes for it builds the pane as text, and the screen would parse that text
// back into cells, which cost a lone pane about half a millisecond a frame.
// Here the pane's box is built around a blank body, which is the same string
// frame after frame, so the compositor keeps its parsed cells; the emulator's
// cells are then copied into the content rectangle.
//
// Only a pane whose cells are what the emulator holds takes this path: no
// marks, dim, link under the pointer, copy sweep, drawn cursor, open
// synchronized update or TML surface, no bare URL to link, and no painted
// background. Anything else is drawn by renderTerminal, as before.

// directPane is a lone pane being drawn from its emulator this frame. Its IO
// read lock is held from the choice until its cells are copied.
type directPane struct {
	w      *terminal.Window
	locked bool
}

// chooseDirectPane picks the pane to draw from its emulator this frame, or
// none. A chosen pane's read lock is held: copyDirectPane releases it.
func (m *OS) chooseDirectPane() directPane {
	if m.screen == nil || fastPathDisabled || m.fastPathPaints() {
		return directPane{}
	}
	w, ok := m.fullscreenFastWindow()
	if !ok || w.Terminal == nil || m.DrawsTML(w) || w.CopyMode != nil || w.ScrollbackOffset > 0 {
		return directPane{}
	}
	focused := m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) && m.Windows[m.FocusedWindow] == w
	if paneDim(focused || m.MultifocusUndimmed(w.ID), &m.Settings) > 0 || m.MultiCopyParked(w.ID) {
		return directPane{}
	}
	if m.copyFlashFor(w.ID) != nil {
		return directPane{}
	}
	if _, hover := m.linkHoverFor(w); hover {
		return directPane{}
	}
	// renderTerminal draws a cursor of its own when the terminal's cannot be
	// placed; paneCursor takes the pane's read lock, so it is asked first.
	native, _ := m.paneCursor()
	if !w.TryRLockIO() {
		return directPane{}
	}
	d := directPane{w: w, locked: true}
	screen := w.Terminal
	fake := native == nil && !m.Resizing && focused && m.Mode == TerminalMode && !screen.IsCursorHidden()
	if fake || screen.IsSyncActive() || m.findsLinksIn(screen, w) {
		d.release()
		return directPane{}
	}
	return d
}

func (d *directPane) release() {
	if d.locked {
		d.w.RUnlockIO()
		d.locked = false
	}
}

// findsLinksIn reports whether renderTerminal would turn text on the pane's
// screen into links: bare URLs (links = all) or link rules.
func (m *OS) findsLinksIn(screen vt.Terminal, w *terminal.Window) bool {
	if m.Settings.Links != config.LinksAll {
		return false
	}
	if !m.Settings.LinkRules.Empty() {
		return true
	}
	rows, cols := min(w.ContentHeight(), screen.Height()), min(w.ContentWidth(), screen.Width())
	for y := range rows {
		prev2, prev1 := "", ""
		for x := range cols {
			c := screen.CellAt(x, y)
			if c == nil {
				prev2, prev1 = prev1, ""
				continue
			}
			if prev2 == ":" && prev1 == "/" && c.Content == "/" {
				return true
			}
			prev2, prev1 = prev1, c.Content
		}
	}
	return false
}

// blankBody is a pane body of spaces, w by h, kept while the size holds: the
// body the direct pane's box is built around.
func (m *OS) blankBody(w, h int) string {
	if m.blank.w != w || m.blank.h != h {
		row := strings.Repeat(" ", w)
		rows := make([]string, h)
		for i := range rows {
			rows[i] = row
		}
		m.blank.s, m.blank.w, m.blank.h = strings.Join(rows, "\n"), w, h
	}
	return m.blank.s
}

// copyDirectPane copies the pane's emulator cells into its content rectangle
// on the canvas and lets go of its lock. The pane's text cache is dropped, so
// a frame drawn the usual way reads the emulator again.
func (m *OS) copyDirectPane(canvas *frameCanvas, d *directPane) {
	if d.w == nil {
		return
	}
	defer d.release()
	w, screen := d.w, d.w.Terminal
	r := paneContentRect(w).Intersect(canvas.Bounds())
	rows, cols := min(r.Dy(), screen.Height()), min(r.Dx(), screen.Width())
	for y := range rows {
		line := canvas.Lines[r.Min.Y+y]
		for x := range cols {
			if c := screen.CellAt(x, y); c != nil {
				line[r.Min.X+x] = *c
			} else {
				line[r.Min.X+x] = uv.EmptyCell
			}
		}
	}
	w.RenderedCols, w.RenderedRows = w.ContentWidth(), w.ContentHeight()
	w.CachedContent = ""
	w.CachedContentCols, w.CachedContentRows = 0, 0
	w.CachedLayer = nil
}
