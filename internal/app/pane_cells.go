package app

import (
	"image"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

// A lone pane that fills the content area, beside the rails or not, on a
// client with a screen is drawn from its emulator's cells straight into the
// canvas. The fast path the string frame
// takes for it builds the pane as text, and the screen would parse that text
// back into cells, which cost a lone pane about half a millisecond a frame.
// Here the pane's box is built around a blank body, which is the same string
// frame after frame, so the compositor keeps its parsed cells; the emulator's
// cells are then copied into the content rectangle.
//
// Only a pane whose cells are what the emulator holds takes this path: no
// marks, dim, link under the pointer, copy sweep, drawn cursor, open
// synchronized update or TML surface, no bare URL to link, no painted
// background, and no picture-in-picture view over it. Anything else is drawn by renderTerminal, as before.

// directPane is a lone pane being drawn from its emulator this frame. Its IO
// read lock is held from the choice until its cells are copied. held says the
// guest has a synchronized update open: the cells copied last time are drawn
// again, so a half-drawn screen is never shown.
type directPane struct {
	w      *terminal.Window
	locked bool
	held   bool
}

// paneHold is what the direct pane keeps between frames: the layer of its
// blank box, and the rectangle its cells were last drawn in, for a frame
// composed while its guest has a synchronized update open (DEC 2026). Those
// cells are read back from the frame the screen last wrote.
type paneHold struct {
	id    string
	rect  image.Rectangle
	drawn bool

	box      string
	boxLayer *lipgloss.Layer
}

// directBoxLayer is the layer of the direct pane's blank box, kept while the
// box and its place are the same.
func (m *OS) directBoxLayer(id, box string, x, y, z int) *lipgloss.Layer {
	h := &m.directHold
	if l := h.boxLayer; l != nil && h.box == box && l.GetID() == id && l.GetX() == x && l.GetY() == y && l.GetZ() == z {
		return l
	}
	h.box = box
	h.boxLayer = lipgloss.NewLayer(box).X(x).Y(y).Z(z).ID(id)
	return h.boxLayer
}

// chooseDirectPane picks the pane to draw from its emulator this frame, or
// none. A chosen pane's read lock is held: copyDirectPane releases it.
func (m *OS) chooseDirectPane() directPane {
	// The pane's cells are copied after every layer is composed, so a layer
	// drawn over the pane would be drawn under them. The overlays already keep
	// a lone pane off this path (loneWindow); the rails and the dock are
	// reserved regions beside the content area, which the pane fills and
	// does not cross; the picture-in-picture view is the one layer that can
	// sit on top of it, so it does too.
	if m.screen == nil || fastPathDisabled || m.fastPathPaints() || m.pipWanted() {
		return directPane{}
	}
	w, ok := m.loneWindow(true)
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
	if fake || m.findsLinksIn(screen, w) {
		d.release()
		return directPane{}
	}
	if screen.IsSyncActive() {
		// Mid-update: draw the last complete frame, if this path drew it.
		h := &m.directHold
		if !h.drawn || h.id != w.ID || h.rect != paneContentRect(w) || m.screen.Shown() == nil {
			d.release()
			return directPane{}
		}
		d.held = true
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
	h := &m.directHold
	if d.held {
		// The cells this rectangle had in the frame the screen last wrote.
		if shown := m.screen.Shown(); shown.Bounds().Eq(canvas.Bounds()) {
			for y := r.Min.Y; y < r.Max.Y; y++ {
				copy(canvas.Lines[y][r.Min.X:r.Max.X], shown.Lines[y][r.Min.X:r.Max.X])
			}
		}
		return
	}
	h.id, h.rect, h.drawn = w.ID, paneContentRect(w), true
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
