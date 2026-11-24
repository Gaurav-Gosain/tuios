package app

import (
	"image"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
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
// guest has a synchronized update open, or its output holds the lock: the
// cells copied last time are drawn again, so a half-drawn screen is never
// shown. A held pane may have no lock, since it does not read the emulator.
type directPane struct {
	w      *terminal.Window
	locked bool
	held   bool
}

// paneHold is what the direct pane keeps between frames: the layer of its
// blank box, and the rectangle its cells were last drawn in, for a frame
// composed while its guest has a synchronized update open (DEC 2026) or its
// output holds the lock. A held frame draws the cells the last frame left in
// that rectangle of the canvas: a kept canvas still has them, and one that is
// cleared has them saved first (saved).
//
// drawn says the last frame composed drew this pane from its emulator or
// held it, and pure that nothing was drawn over its rectangle in that frame,
// so the rectangle holds the pane's cells and nothing else. Any other frame
// drops the hold: a frame of another layout, or one with a layer over the
// pane, left other cells there.
type paneHold struct {
	id    string
	rect  image.Rectangle
	drawn bool
	pure  bool
	saved *uv.Buffer

	box      string
	boxLayer *lipgloss.Layer

	// fresh is when the pane was last drawn from its emulator.
	fresh time.Time
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
		// The guest's output is being read into the emulator. The cells the
		// last frame drew are drawn again, as for an open update: the pane drawn
		// the usual way has no text of its own to shed to (copyDirectPane
		// dropped it), so it would wait for the lock, or show the text it
		// had before this path drew it.
		if !m.directHeld(w) {
			return directPane{}
		}
		// Under a flood the writer takes the lock again as soon as it lets
		// go of a chunk, so the try fails frame after frame and the screen
		// stood still for up to seconds. Once the pane has not been drawn
		// fresh for a frame period, the frame waits for the read lock, which
		// a blocked reader gets at the end of the writer's chunk.
		if time.Since(m.directHold.fresh) < m.framePeriod() {
			return directPane{w: w, held: true}
		}
		w.RLockIO()
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
		if !m.directHeld(w) {
			d.release()
			return directPane{}
		}
		d.held = true
	}
	return d
}

// directHeld reports whether the canvas holds w's cells as the last frame
// left them, so this frame can draw them again.
func (m *OS) directHeld(w *terminal.Window) bool {
	h := &m.directHold
	return h.drawn && h.pure && h.id == w.ID && h.rect == paneContentRect(w)
}

// saveHeldPane keeps the held pane's rectangle before the canvas is cleared,
// for paneCells to write back.
func (m *OS) saveHeldPane(canvas *frameCanvas, r image.Rectangle) {
	r = r.Intersect(canvas.Bounds())
	h := &m.directHold
	if h.saved == nil || h.saved.Width() != canvas.Width() || h.saved.Height() != canvas.Height() {
		h.saved = uv.NewBuffer(canvas.Width(), canvas.Height())
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(h.saved.Lines[y][r.Min.X:r.Max.X], canvas.Lines[y][r.Min.X:r.Max.X])
	}
}

// heldSource is where a held pane's cells come from this frame: the canvas
// itself when it was kept, the rectangle saved before it was cleared
// otherwise.
func (m *OS) heldSource(canvas *frameCanvas, kept bool) *uv.Buffer {
	if kept {
		return &canvas.Buffer
	}
	return m.directHold.saved
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
// on the canvas. composeLayers calls it right after the pane's box layer is
// drawn; the lock is let go when GetCanvas returns. The pane's text cache is
// dropped, so a frame drawn the usual way reads the emulator again.
//
// On a frame that kept the canvas the rectangle holds the cells copied last
// time, so a cell is written only where it differs, and each row written is
// marked in frameRows for the screen.
func (m *OS) copyDirectPane(canvas *frameCanvas, d *directPane, kept bool) {
	if d.w == nil {
		return
	}
	m.paneCells(canvas, d, kept, m.frameRows, m.heldSource(canvas, kept))
	// The pane's layer is its blank box. A frame that draws the pane the
	// usual way while the guest has an update open holds the cached layer,
	// and that would draw the pane blank.
	d.w.CachedLayer = nil
	if d.held {
		return
	}
	w := d.w
	m.directHold.id, m.directHold.rect, m.directHold.drawn = w.ID, paneContentRect(w), true
	m.directHold.fresh = time.Now()
	w.RenderedCols, w.RenderedRows = w.ContentWidth(), w.ContentHeight()
	w.CachedContent = ""
	w.CachedContentCols, w.CachedContentRows = 0, 0
	// The text a frame drawn the usual way holds while the guest has an
	// update open is older than these cells. Shown after them it put the
	// pane back to what it held before this path drew it (#2157).
	w.SyncHoldContent = ""
}

// paneCells writes the direct pane's cells into its content rectangle on
// canvas: the emulator's, or for a held pane the ones in held (heldSource).
// With kept a cell is written only where it differs. Each row written is
// marked in rows when rows is not nil. It changes nothing else, so the frame
// check (frame_check.go) draws the pane with it too.
func (m *OS) paneCells(canvas *frameCanvas, d *directPane, kept bool, rows []bool, held *uv.Buffer) {
	w, screen := d.w, d.w.Terminal
	r := paneContentRect(w).Intersect(canvas.Bounds())
	mark := func(y int) {
		if y < len(rows) {
			rows[y] = true
		}
	}
	if d.held {
		// The cells the last frame left in this rectangle.
		if held != nil && held.Bounds().Eq(canvas.Bounds()) {
			for y := r.Min.Y; y < r.Max.Y; y++ {
				dst, src := canvas.Lines[y][r.Min.X:r.Max.X], held.Lines[y][r.Min.X:r.Max.X]
				if kept && termio.SameLine(dst, src) {
					continue
				}
				copy(dst, src)
				mark(y)
			}
		}
		return
	}
	n, cols := min(r.Dy(), screen.Height()), min(r.Dx(), screen.Width())
	for y := range n {
		line := canvas.Lines[r.Min.Y+y]
		changed := false
		for x := range cols {
			src := screen.CellAt(x, y)
			if src == nil {
				src = &uv.EmptyCell
			}
			dst := &line[r.Min.X+x]
			if kept && termio.SameCell(dst, src) {
				continue
			}
			*dst = *src
			changed = true
		}
		if changed {
			mark(r.Min.Y + y)
		}
	}
}
