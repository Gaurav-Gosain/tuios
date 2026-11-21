package app

import (
	"image"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

type directPane struct {
	w      *terminal.Window
	locked bool
	held   bool
}

type paneHold struct {
	id    string
	rect  image.Rectangle
	drawn bool
	pure  bool
	saved *uv.Buffer

	box      string
	boxLayer *lipgloss.Layer
}

func (m *OS) directBoxLayer(id, box string, x, y, z int) *lipgloss.Layer {
	h := &m.directHold
	if l := h.boxLayer; l != nil && h.box == box && l.GetID() == id && l.GetX() == x && l.GetY() == y && l.GetZ() == z {
		return l
	}
	h.box = box
	h.boxLayer = lipgloss.NewLayer(box).X(x).Y(y).Z(z).ID(id)
	return h.boxLayer
}

func (m *OS) chooseDirectPane() directPane {
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
	native, _ := m.paneCursor()
	if !w.TryRLockIO() {
		if m.directHeld(w) {
			return directPane{w: w, held: true}
		}
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
		if !m.directHeld(w) {
			d.release()
			return directPane{}
		}
		d.held = true
	}
	return d
}

func (m *OS) directHeld(w *terminal.Window) bool {
	h := &m.directHold
	return h.drawn && h.pure && h.id == w.ID && h.rect == paneContentRect(w)
}

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

func (m *OS) copyDirectPane(canvas *frameCanvas, d *directPane, kept bool) {
	if d.w == nil {
		return
	}
	m.paneCells(canvas, d, kept, m.frameRows, m.heldSource(canvas, kept))
	d.w.CachedLayer = nil
	if d.held {
		return
	}
	w := d.w
	m.directHold.id, m.directHold.rect, m.directHold.drawn = w.ID, paneContentRect(w), true
	w.RenderedCols, w.RenderedRows = w.ContentWidth(), w.ContentHeight()
	w.CachedContent = ""
	w.CachedContentCols, w.CachedContentRows = 0, 0
	w.SyncHoldContent = ""
}

func (m *OS) paneCells(canvas *frameCanvas, d *directPane, kept bool, rows []bool, held *uv.Buffer) {
	w, screen := d.w, d.w.Terminal
	r := paneContentRect(w).Intersect(canvas.Bounds())
	mark := func(y int) {
		if y < len(rows) {
			rows[y] = true
		}
	}
	if d.held {
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
