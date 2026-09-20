package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// A pane already knows where its links are. The emulator records OSC 8 on every
// cell it paints, so the address of a marked link is a field read away, and a
// bare URL is found by reading one row of text. What the pane did not do until
// now is say so: a link looked like every other run of characters and a click on
// it selected text.
//
// This file is the "say so". It resolves the run of cells under the pointer into
// a PaneLink, and everything else in the feature reads that one value: the
// render loop underlines it, the pointer turns into a hand over it, the label
// names its target, and a click acts on it.
//
// It runs on arriving motion and nowhere else. A frame never calls it, so a pane
// that nobody is pointing at costs exactly what it cost before.

// PaneLink is the link under the pointer, in the coordinates the render loop
// draws in.
//
// The run is viewport-relative and inclusive at both ends, in row-major order:
// a cell (x, y) is inside it when it is at or after (X0, Y0) and at or before
// (X1, Y1). A marked link wraps across rows, which is why the run needs two
// corners rather than one row and two columns.
type PaneLink struct {
	// WindowID is the pane the run belongs to. The pointer can only be over one
	// pane, and a run is meaningless against any other.
	WindowID string
	// URL is the address the link points at, exactly as the program wrote it.
	URL string
	// Marked says the address came from OSC 8, so the program declared it. A
	// bare URL found in plain text is not marked, and the two are treated
	// differently by nothing except this field and the config that finds them.
	Marked bool
	// The inclusive run, row-major, in viewport cells.
	Y0, X0, Y1, X1 int
	// Row and Col are the cell the pointer was actually on, which is what the
	// label is positioned against.
	Row, Col int
}

// Contains reports whether viewport cell (x, y) is inside the run.
func (l PaneLink) Contains(x, y int) bool {
	if y < l.Y0 || y > l.Y1 {
		return false
	}
	if y == l.Y0 && x < l.X0 {
		return false
	}
	if y == l.Y1 && x > l.X1 {
		return false
	}
	return true
}

// Same reports whether two resolutions describe the same run of the same pane.
// The pointer moving from one cell of a link to the next must not repaint the
// pane, and this is what tells the two apart.
func (l PaneLink) Same(o PaneLink) bool {
	return l.WindowID == o.WindowID && l.URL == o.URL &&
		l.Y0 == o.Y0 && l.X0 == o.X0 && l.Y1 == o.Y1 && l.X1 == o.X1
}

// linksEnabled reports whether the pointer picks links up at all.
func linksEnabled(s *config.Settings) bool { return s.Links != config.LinksOff }

// resolvePaneLink returns the link covering viewport cell (x, y) of window, or
// ok=false when there is none.
//
// The caller must not hold the window's I/O read lock; this takes it itself, and
// gives up rather than waiting. A pane printing thousands of lines a second
// holds the write side almost continuously, and blocking a mouse move on it
// would stall the whole input loop for a hover highlight. Motion arrives many
// times a second, so a skipped resolution costs one frame of a stale underline
// and nothing else.
func resolvePaneLink(window *terminal.Window, x, y int, s *config.Settings) (PaneLink, bool) {
	if window == nil || window.Terminal == nil || !linksEnabled(s) {
		return PaneLink{}, false
	}
	if !window.TryRLockIO() {
		return PaneLink{}, false
	}
	defer window.RUnlockIO()

	maxX := min(window.ContentWidth(), window.Terminal.Width())
	if x < 0 || y < 0 || x >= maxX || y >= window.ContentHeight() {
		return PaneLink{}, false
	}

	if link, ok := markedLinkAt(window, x, y, maxX); ok {
		return link, true
	}
	if s.Links != config.LinksAll {
		return PaneLink{}, false
	}
	return bareLinkAt(window, x, y, maxX)
}

// markedLinkAt finds the OSC 8 run covering (x, y).
//
// Two cells belong to the same marked link when they carry the same URL and the
// same parameters and nothing between them carries anything else. The id= a
// program may put in the parameters exists to join runs that are not adjacent,
// which would let a link split by a window's edge highlight as one thing; that
// is deliberately not done here, because the run this returns is also the run
// that gets underlined, and underlining cells the pointer is nowhere near reads
// as a bug rather than as a feature.
func markedLinkAt(window *terminal.Window, x, y, maxX int) (PaneLink, bool) {
	cell := paneCellAt(window, x, y)
	if cell == nil || cell.Link.URL == "" {
		return PaneLink{}, false
	}
	want := cell.Link

	same := func(cx, cy int) bool {
		c := paneCellAt(window, cx, cy)
		return c != nil && c.Link == want
	}

	// Walk backwards through the run, wrapping to the end of the row above.
	x0, y0 := x, y
	for {
		px, py := x0-1, y0
		if px < 0 {
			px, py = maxX-1, y0-1
		}
		if py < 0 || !same(px, py) {
			break
		}
		x0, y0 = px, py
	}

	// And forwards, wrapping to the start of the row below.
	x1, y1 := x, y
	h := window.ContentHeight()
	for {
		nx, ny := x1+1, y1
		if nx >= maxX {
			nx, ny = 0, y1+1
		}
		if ny >= h || !same(nx, ny) {
			break
		}
		x1, y1 = nx, ny
	}

	return PaneLink{
		WindowID: window.ID,
		URL:      want.URL,
		Marked:   true,
		Y0:       y0, X0: x0, Y1: y1, X1: x1,
		Row: y, Col: x,
	}, true
}

// linkWrapRows bounds how far a bare URL is followed across a wrap, in rows
// each way. A URL long enough to fill sixteen rows of a pane is not one anybody
// is about to click, and the bound is what keeps a screen of solid text from
// being joined end to end on every mouse move.
const linkWrapRows = 16

// linkCellRef is a viewport cell, used to map a byte in the joined text of a
// wrapped line back to the cell that drew it.
type linkCellRef struct{ X, Y int }

// bareLinkAt finds a plain-text URL covering (x, y), following it across a soft
// wrap.
//
// A guest that prints a URL wider than the pane gets it broken across rows with
// no character between the halves, and until now each half was scanned on its
// own: hovering the first half offered to open a truncated address, and the
// second half was not a link at all.
//
// The rows are joined when the row above is full, meaning its last column holds
// something other than a space. That is what a soft wrap leaves behind and what
// a line ending does not: a program that ends a line ends it where the text
// ends, which is almost never the last column exactly. The emulator does not
// record a wrap flag, so this is the signal available, and it is wrong only for
// a line that happens to fill the pane to its last cell and ends a URL exactly
// there, which is the same case a wrap produces.
func bareLinkAt(window *terminal.Window, x, y, maxX int) (PaneLink, bool) {
	h := window.ContentHeight()

	// Walk up to the first row of the wrapped line, then collect it and every
	// row the wrap carried it onto.
	top := y
	for top > 0 && top > y-linkWrapRows && paneRowIsFull(window, top-1, maxX) {
		top--
	}

	var b strings.Builder
	var refs []linkCellRef
	var byteAt []int
	cursor := -1
	for row := top; row < h && row <= y+linkWrapRows; row++ {
		text, rowBytes := paneRowText(window, row, maxX)
		base := b.Len()
		b.WriteString(text)
		for col, off := range rowBytes {
			if off < 0 {
				continue
			}
			if row == y && col == x {
				cursor = base + off
			}
			refs = append(refs, linkCellRef{X: col, Y: row})
			byteAt = append(byteAt, base+off)
		}
		// The line ends here unless this row is full, which is the wrap.
		if !paneRowIsFull(window, row, maxX) {
			break
		}
	}
	if cursor < 0 {
		return PaneLink{}, false
	}

	s, e, ok := ScanBareURL(b.String(), cursor)
	if !ok {
		return PaneLink{}, false
	}

	// Map the byte range back to the cells that drew it.
	first, last := -1, -1
	for i, off := range byteAt {
		if off >= s && off < e {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return PaneLink{}, false
	}

	return PaneLink{
		WindowID: window.ID,
		URL:      b.String()[s:e],
		Y0:       refs[first].Y, X0: refs[first].X,
		Y1: refs[last].Y, X1: refs[last].X,
		Row: y, Col: x,
	}, true
}

// paneRowIsFull reports whether the row's last column holds something other
// than a space, which is what a soft wrap leaves and a line ending does not.
//
// The caller must hold the window's I/O read lock.
func paneRowIsFull(window *terminal.Window, y, maxX int) bool {
	if y < 0 || maxX <= 0 {
		return false
	}
	text, _ := paneRowText(window, y, maxX)
	trimmed := strings.TrimRight(text, " ")
	return trimmed != "" && len(trimmed) == len(text)
}

// paneCellAt reads one viewport cell, from the scrollback ring when the pane is
// scrolled back and from the live screen otherwise. It is the same mapping the
// render loop walks, kept here so the cells the pointer lands on are the cells
// the user is looking at.
//
// The caller must hold the window's I/O read lock.
func paneCellAt(window *terminal.Window, x, y int) *uv.Cell {
	if window.ScrollbackOffset > 0 {
		if y < window.ScrollbackOffset {
			idx := window.ScrollbackLen() - window.ScrollbackOffset + y
			if idx < 0 || idx >= window.ScrollbackLen() {
				return nil
			}
			line := window.ScrollbackLine(idx)
			if x >= len(line) {
				return nil
			}
			return &line[x]
		}
		y -= window.ScrollbackOffset
	}
	if window.Terminal == nil || y < 0 || y >= window.Terminal.Height() {
		return nil
	}
	return window.Terminal.CellAt(x, y)
}

// paneRowText renders one viewport row as plain text and returns, per column,
// the byte offset of the cell that drew it. A column covered by the second half
// of a wide glyph, or past the end of the row, gets -1.
//
// The two are built together on purpose: a row holding a wide glyph or a
// combining mark has no fixed relationship between columns and bytes, and
// deriving one from the other afterwards is where an off-by-one in a URL's last
// character comes from.
//
// The caller must hold the window's I/O read lock.
func paneRowText(window *terminal.Window, y, maxX int) (string, []int) {
	var b strings.Builder
	b.Grow(maxX)
	byteAt := make([]int, maxX)
	for i := range byteAt {
		byteAt[i] = -1
	}

	for x := 0; x < maxX; {
		cell := paneCellAt(window, x, y)
		content := " "
		width := 1
		if cell != nil {
			if cell.Width > 1 {
				width = cell.Width
			}
			if cell.Content != "" {
				content = cell.Content
			}
		}
		byteAt[x] = b.Len()
		b.WriteString(content)
		x += width
	}
	return b.String(), byteAt
}
