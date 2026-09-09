package app

import (
	"image"
	"slices"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The frame's cell buffer and the layers composed onto it.
//
// This used to be lipgloss's Canvas and Compositor, and the profile of a
// keystroke frame put more than half of the compose in the two of them doing
// work the frame did not need. The Compositor measured every layer's content
// twice per frame, once when the layer was added to its root and once when it
// flattened the tree, and a grapheme-width pass over a pane's whole body is
// not cheap. It then re-parsed every layer's string into cells on every frame,
// including the panes whose layer had not changed since the last one, which
// is most of them. And the Canvas underneath was a RenderBuffer, so every cell
// written paid a damage comparison and a touched-line update for a buffer
// that is cleared and rebuilt from scratch each frame anyway.
//
// What replaces them keeps the observable output byte for byte and drops the
// rest. frameCanvas is a plain uv.Buffer. composeLayers orders the layers the
// way the Compositor did and draws each one either straight from its string,
// as before, or from a cellLayer: the cells that string parsed to the last
// time it was seen. A pane's layer keeps its string between keystrokes in
// other panes, so its cells are parsed once per rebuild and copied thereafter.

// frameCanvas is the composed frame as cells. It stands in for the
// lipgloss.Canvas GetCanvas used to return and offers the same surface:
// CellAt hands back a pointer into the buffer, so the spotlight pass and the
// tests that inspect a cell keep working unchanged.
type frameCanvas struct {
	uv.Buffer
	renderer frameRenderer
	blank    uv.Line
}

// WidthMethod is the method a StyledString uses to cut the string into cells.
// lipgloss set its canvas to grapheme width, and the layers were drawn under
// it, so the same method here keeps every cluster on the same column.
func (c *frameCanvas) WidthMethod() uv.WidthMethod {
	return ansi.GraphemeWidth
}

// Render is the frame as the string bubbletea takes, with trailing blanks
// trimmed the way lipgloss trimmed them. See frame_render.go.
func (c *frameCanvas) Render() string {
	return c.renderer.render(c.Lines)
}

// cellLayer is a layer's string parsed to cells, kept so a layer that comes
// back unchanged on the next frame is copied rather than parsed again.
//
// The buffer is a few columns wider than the layer. A wide glyph on a layer's
// last column spills its second half past the layer's edge when drawn straight
// onto the canvas, because the clip in uv.Line.Set is against the line the
// cell lands on and not the layer's own bounds. Parsing into a buffer exactly
// the layer's width would turn that glyph into blanks and the two paths would
// disagree by a cell. The margin lets the head cell keep its width, and the
// copy hands only head cells to the canvas, whose Line.Set then spills or
// clips exactly as it did before.
type cellLayer struct {
	content string
	w, h    int
	buf     uv.Buffer
	// spill marks the rows holding a wide cell whose head is on the last
	// column, so its second half lands past the layer's edge. Those rows are
	// drawn cell by cell.
	spill []bool
	blank uv.Line
	gen   uint64
}

// wideMargin is the room past a layer's right edge that a head cell on the
// last column may need. Under grapheme width no cluster is wider than two
// cells; the margin is larger so a wcwidth fallback cannot reach it either.
const wideMargin = 8

// WidthMethod matches frameCanvas, so the cells parsed here are the cells the
// canvas would have parsed.
func (cl *cellLayer) WidthMethod() uv.WidthMethod {
	return ansi.GraphemeWidth
}

// update reparses the layer when its string changed and is a comparison
// otherwise. The comparison is a pointer check when the layer kept its
// string, which a cached pane layer does, and a memcmp when a producer built
// an equal string afresh, which is still far cheaper than parsing it.
//
// w and h are the layer's own measurements, taken once by lipgloss.NewLayer;
// the Compositor measured the string again, twice, on every frame.
func (cl *cellLayer) update(content string, w, h int) {
	if cl.content == content && cl.w >= 0 {
		return
	}
	cl.content = content
	cl.w, cl.h = w, h
	cl.buf.Resize(cl.w+wideMargin, cl.h)
	cl.blank = clearLines(cl.buf.Lines, cl.blank)
	uv.NewStyledString(content).Draw(&cellLayerScreen{cl}, uv.Rect(0, 0, cl.w, cl.h))
	cl.spill = slices.Grow(cl.spill[:0], cl.h)[:cl.h]
	for row, line := range cl.buf.Lines {
		cl.spill[row] = false
		for col := max(cl.w-wideMargin, 0); col < cl.w; col++ {
			if c := &line[col]; c.Width > 1 && col+c.Width > cl.w {
				cl.spill[row] = true
				break
			}
		}
	}
}

// clearLines sets every cell to the blank cell, a row at a time, copying
// from blank. It returns blank, grown to the widest line if it was shorter.
func clearLines(lines []uv.Line, blank uv.Line) uv.Line {
	w := 0
	for _, l := range lines {
		w = max(w, len(l))
	}
	if len(blank) < w {
		blank = make(uv.Line, w)
		for i := range blank {
			blank[i] = uv.EmptyCell
		}
	}
	for _, l := range lines {
		copy(l, blank[:len(l)])
	}
	return blank
}

// Clear blanks the canvas. uv.Buffer.Clear assigns cell by cell; a row copy
// is the same result in a fraction of the time.
func (c *frameCanvas) Clear() {
	c.blank = clearLines(c.Lines, c.blank)
}

// cellLayerScreen is the uv.Screen a layer is parsed onto. It is the layer's
// own buffer with the canvas's width method.
type cellLayerScreen struct {
	*cellLayer
}

func (s *cellLayerScreen) Bounds() uv.Rectangle         { return s.buf.Bounds() }
func (s *cellLayerScreen) CellAt(x, y int) *uv.Cell     { return s.buf.CellAt(x, y) }
func (s *cellLayerScreen) SetCell(x, y int, c *uv.Cell) { s.buf.SetCell(x, y, c) }
func (s *cellLayerScreen) WidthMethod() uv.WidthMethod  { return ansi.GraphemeWidth }

// blit copies the parsed cells onto the canvas at (x, y).
//
// What it reproduces is the sequence of writes a StyledString.Draw makes
// there: the layer's rectangle cleared to blanks, then each head cell set left
// to right, with uv.Line.Set splitting any wide cell it lands on and clipping
// at the canvas edge. A row whose edges meet no wide cell on either side is
// the same sequence collapsed to one copy, since every cell in it is then set
// exactly once to the value the source holds. The rest go through Line.Set a
// cell at a time.
func (cl *cellLayer) blit(canvas *frameCanvas, x, y int) {
	for row := range cl.h {
		cy := y + row
		if cy < 0 || cy >= len(canvas.Lines) {
			continue
		}
		line := canvas.Lines[cy]
		src := cl.buf.Lines[row]
		if x >= 0 && x+cl.w <= len(line) && !cl.spill[row] &&
			!isPlaceholder(&line[x]) && (x+cl.w == len(line) || !isPlaceholder(&line[x+cl.w])) {
			copy(line[x:x+cl.w], src[:cl.w])
			continue
		}
		for col := range cl.w {
			line.Set(x+col, nil)
		}
		for col := range cl.w {
			c := &src[col]
			if c.IsZero() {
				continue
			}
			line.Set(x+col, c)
		}
	}
}

// isPlaceholder reports whether a canvas cell is the continuation of a wide
// cell, which a write over it has to split.
func isPlaceholder(c *uv.Cell) bool {
	return c.Width == 0
}

// composedLayer is one layer with the bounds the compositor gives it.
type composedLayer struct {
	layer  *lipgloss.Layer
	bounds image.Rectangle
	cells  *cellLayer
}

// composeLayers draws layers onto the canvas in ascending z order.
//
// The order is the lipgloss Compositor's, reproduced exactly: its root layer
// takes part in the sort with a z of zero and empty bounds, and the sort is
// the same unstable one, so layers that share a z land in the order they
// always did. A layer with an id is drawn from its cellLayer, parsed the first
// time its string is seen and kept across frames under that id; a layer
// without one is parsed straight onto the canvas as before.
func (m *OS) composeLayers(canvas *frameCanvas, layers []*lipgloss.Layer) {
	m.composeGen++
	if m.layerCells == nil {
		m.layerCells = make(map[string]*cellLayer)
	}

	ordered := m.composeScratch[:0]
	// The Compositor's root: an empty layer at the origin that never draws
	// but does sort.
	ordered = append(ordered, composedLayer{bounds: image.Rect(0, 0, 0, 1)})
	for _, l := range layers {
		if l == nil {
			continue
		}
		entry := composedLayer{layer: l}
		if id := l.GetID(); id != "" {
			cl := m.layerCells[id]
			if cl == nil {
				cl = &cellLayer{w: -1}
				m.layerCells[id] = cl
			}
			cl.gen = m.composeGen
			entry.cells = cl
		}
		// The layer's own size, measured once when it was built. A layer here
		// has no children, so it is the size of the content and nothing else,
		// which is what the Compositor's bounds were.
		entry.bounds = image.Rect(l.GetX(), l.GetY(), l.GetX()+l.Width(), l.GetY()+l.Height())
		ordered = append(ordered, entry)
	}
	slices.SortFunc(ordered, func(a, b composedLayer) int {
		return layerZ(a.layer) - layerZ(b.layer)
	})

	area := canvas.Bounds()
	for _, cl := range ordered {
		if cl.layer == nil || !cl.bounds.Overlaps(area) {
			continue
		}
		if cl.cells != nil {
			// Parsed here, in draw order, and not when the layers were
			// collected: two layers on one frame that share an id share the
			// cellLayer too, and each has to hold its own cells at the moment
			// it is drawn. Nothing on the frame today shares an id, and
			// nothing enforces that either.
			cl.cells.update(cl.layer.GetContent(), cl.layer.Width(), cl.layer.Height())
			cl.cells.blit(canvas, cl.bounds.Min.X, cl.bounds.Min.Y)
			continue
		}
		uv.NewStyledString(cl.layer.GetContent()).Draw(canvas, cl.bounds)
	}

	// A cached layer that was not on this frame is dropped. A pane on another
	// workspace parses again when it comes back, which is the price of not
	// holding every pane's cells for as long as the pane lives.
	for id, cl := range m.layerCells {
		if cl.gen != m.composeGen {
			delete(m.layerCells, id)
		}
	}
	clear(ordered)
	m.composeScratch = ordered[:0]
}

// layerZ is a layer's z, with the compositor's root at zero.
func layerZ(l *lipgloss.Layer) int {
	if l == nil {
		return 0
	}
	return l.GetZ()
}
