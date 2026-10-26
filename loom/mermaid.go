package loom

import (
	"errors"
	"strconv"
	"strings"

	"tuios.dev/sketch"
	sketchtext "tuios.dev/sketch/text"
)

// mermaidWidget is a Mermaid diagram drawn by tuios.dev/sketch as text
// cells, fitted to the width there is. A diagram taller than its box scrolls
// with the wheel. A source sketch cannot draw shows as code under a one-line
// note that says why.
type mermaidWidget struct {
	src  string
	code *codeWidget // the source, for a diagram sketch cannot draw

	content int // rows of the last drawing painted
	view    int // the rows it had to show them
	over    bool
}

func (s *Surface) buildMermaid(b *box, vars map[string]any) widget {
	code := s.buildCode(b, vars).(*codeWidget)
	return &mermaidWidget{src: strings.Join(code.lines, "\n"), code: code}
}

// mermaidDrawing is one drawing of a diagram at one width.
type mermaidDrawing struct {
	rows  [][]sketchtext.Cell
	w     int    // the widest row
	cut   bool   // the diagram is wider than the width: the rows run past it
	fail  string // why sketch cannot draw the diagram, "" when it can
	width int    // the width asked for
}

// sketchCache keeps the parsed diagrams and their drawings across renders,
// so a redraw, a state change elsewhere in the document or a measure at a
// width seen before does no layout. It is bounded: a document that keeps
// changing its diagrams starts the cache again.
type sketchCache struct {
	parsed   map[string]*parsedDiagram
	drawings map[sketchKey]*mermaidDrawing
}

type parsedDiagram struct {
	d   *sketch.Diagram
	err error
}

type sketchKey struct {
	src string
	w   int
}

// sketchCacheMax bounds each map of the cache.
const sketchCacheMax = 64

func (s *Surface) drawMermaid(src string, w int) *mermaidDrawing {
	w = max(w, 1)
	if s.sketches == nil {
		s.sketches = &sketchCache{parsed: map[string]*parsedDiagram{}, drawings: map[sketchKey]*mermaidDrawing{}}
	}
	c := s.sketches
	if d, ok := c.drawings[sketchKey{src, w}]; ok {
		return d
	}
	p, ok := c.parsed[src]
	if !ok {
		if len(c.parsed) >= sketchCacheMax {
			clear(c.parsed)
		}
		p = &parsedDiagram{}
		p.d, p.err = sketch.Parse(src, sketch.ParseOptions{})
		c.parsed[src] = p
	}
	out := &mermaidDrawing{width: w}
	if p.err != nil {
		out.fail = p.err.Error()
	} else {
		rows, err := sketchtext.DrawCells(p.d, sketchtext.Options{Cols: w})
		if errors.Is(err, sketchtext.ErrTooWide) {
			// Even the narrowest layout is too wide: draw it at its own
			// width and show what fits.
			rows, err = sketchtext.DrawCells(p.d, sketchtext.Options{})
			out.cut = err == nil
		}
		switch {
		case errors.Is(err, sketchtext.ErrUnsupported):
			out.fail = "this diagram type has no text drawing"
		case errors.Is(err, sketchtext.ErrTooLarge):
			out.fail = "the diagram is too large to draw"
		case err != nil:
			out.fail = err.Error()
		}
		out.rows = rows
		for _, r := range rows {
			out.w = max(out.w, len(r))
		}
	}
	if out.fail != "" {
		out.fail = oneLine(out.fail)
	}
	if len(c.drawings) >= sketchCacheMax {
		clear(c.drawings)
	}
	c.drawings[sketchKey{src, w}] = out
	return out
}

// oneLine shortens a parse error to one line: its first line, which says
// where, and its last, which says what was expected.
func oneLine(msg string) string {
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	first := strings.TrimSuffix(strings.TrimSpace(lines[0]), ":")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(lines) == 1 || last == "" {
		return first
	}
	return first + ". " + last
}

// note is the line over a diagram that is cut or not drawn, or "".
func (d *mermaidDrawing) note() (string, string) {
	switch {
	case d.fail != "":
		return "Cannot draw the diagram: " + d.fail, "warning"
	case d.cut:
		return "The diagram is wider than the pane. Make the pane wider to see all of it.", "mute"
	}
	return "", ""
}

func (w *mermaidWidget) measure(s *Surface, b *box, maxW int) (int, int) {
	d := s.drawMermaid(w.src, maxW)
	note, _ := d.note()
	if d.fail != "" {
		_, h := w.code.measure(s, b, maxW)
		return maxW, h + 1
	}
	h := len(d.rows)
	if note != "" {
		return maxW, h + 1
	}
	return min(d.w, maxW), h
}

func (w *mermaidWidget) paint(s *Surface, b *box, c *canvas, r Rect) {
	if r.Dx() <= 0 || r.Dy() <= 0 {
		return
	}
	d := s.drawMermaid(w.src, r.Dx())
	if note, role := d.note(); note != "" {
		c.drawLine(r.Min.X, r.Min.Y, r.Dx(), plainLine(note, style{fg: role}), alignLeft)
		r.Min.Y++
	}
	if d.fail != "" {
		w.content, w.over = 0, false
		w.code.paint(s, b, c, r)
		return
	}
	w.content, w.view = len(d.rows), r.Dy()
	w.over = w.content > r.Dy()
	draw := r
	off := 0
	if w.over {
		draw.Max.X--
		off, _ = lookup(s.view.Scroll, b)
		off = clampInt(off, 0, w.content-r.Dy())
		paintBar(s, c, r, w.content, off)
	}
	cc := c.withClip(draw)
	ascii := s.pal.Depth == ASCII
	grounded := s.pal.grounded()
	for i := 0; i < draw.Dy() && off+i < len(d.rows); i++ {
		row := d.rows[off+i]
		y := draw.Min.Y + i
		// Write runs of one style at a time.
		var run strings.Builder
		var runSt style
		runX := draw.Min.X
		flush := func(next int) {
			if run.Len() > 0 {
				cc.text(runX, y, run.String(), runSt, -1)
				run.Reset()
			}
			runX = next
		}
		for x, cell := range row {
			if cell.Rune == 0 {
				// Covered by the wide character before it.
				continue
			}
			if cell.Rune == ' ' && cell.Bg == sketchtext.RoleNone {
				flush(draw.Min.X + x + 1)
				continue
			}
			st := mermaidStyle(cell.Role)
			if cell.Bg != sketchtext.RoleNone && grounded {
				st.bg = mermaidStyle(cell.Bg).fg
			}
			if st != runSt {
				flush(draw.Min.X + x)
				runSt = st
			}
			ch := cell.Rune
			if ascii {
				ch = asciiRune(ch)
			}
			run.WriteRune(ch)
		}
		flush(0)
	}
}

// mermaidStyle is the theme role of a sketch cell role.
func mermaidStyle(r sketchtext.Role) style {
	switch r {
	case sketchtext.RoleLine:
		return style{fg: "dim"}
	case sketchtext.RoleBox:
		return style{fg: "accent"}
	case sketchtext.RoleNote:
		return style{fg: "warning"}
	case sketchtext.RoleFrame:
		return style{fg: "mute"}
	case sketchtext.RoleTitle, sketchtext.RoleHead:
		return style{fg: "fg", attrs: attrBold}
	case sketchtext.RoleCrit:
		return style{fg: "error"}
	}
	if r >= sketchtext.RoleSeries {
		return style{fg: "series" + strconv.Itoa(int(r-sketchtext.RoleSeries)%sketchtext.SeriesRoles+1)}
	}
	return style{fg: "fg"}
}

// asciiRune maps the box drawing, block and arrow characters of a drawing
// to ASCII, for the ASCII depth.
func asciiRune(r rune) rune {
	switch r {
	case '─', '━', '┄', '┅', '╌', '╍', '═', '╴', '╶', '╸', '╺', '▔', '▁':
		return '-'
	case '│', '┃', '┆', '┇', '╎', '╏', '║', '╵', '╷', '╹', '╻', '▏', '▕', '▎':
		return '|'
	case '╱':
		return '/'
	case '╲':
		return '\\'
	case '►', '▶', '▷':
		return '>'
	case '◄', '◀', '◁':
		return '<'
	case '▲', '△':
		return '^'
	case '▼', '▽':
		return 'v'
	case '●', '◉', '◆', '■':
		return '*'
	case '○', '◇', '□':
		return 'o'
	case '·':
		return '.'
	case '×':
		return 'x'
	}
	switch {
	case r >= 0x2500 && r <= 0x257F:
		return '+'
	case r >= 0x2580 && r <= 0x259F:
		return '#'
	case r == 0x2800:
		return ' '
	case r > 0x2800 && r <= 0x28FF:
		return '.'
	}
	return r
}
