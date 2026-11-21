package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

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
	// For a marked link it is the OSC 8 target, never the visible text.
	URL string
	// Params are the OSC 8 parameters of a marked link, id= included. Two
	// runs with the same URL and an id= in the same Params are one link.
	Params string
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
	return l.WindowID == o.WindowID && l.URL == o.URL && l.Params == o.Params &&
		l.Y0 == o.Y0 && l.X0 == o.X0 && l.Y1 == o.Y1 && l.X1 == o.X1
}

func linksEnabled(s *config.Settings) bool { return s.Links != config.LinksOff }

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
	return bareLinkAt(window, x, y, maxX, s.LinkRules)
}

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
		Params:   want.Params,
		Marked:   true,
		Y0:       y0, X0: x0, Y1: y1, X1: x1,
		Row: y, Col: x,
	}, true
}

const linkWrapRows = 16

type linkCellRef struct{ X, Y int }

func bareLinkAt(window *terminal.Window, x, y, maxX int, rules *hints.LinkRules) (PaneLink, bool) {
	h := window.ContentHeight()

	top := y
	for top > y-linkWrapRows && paneRowWraps(window, top-1) {
		top--
	}

	var b strings.Builder
	var refs []linkCellRef
	var byteAt []int
	cursor := -1
	for row := top; row <= y+linkWrapRows; row++ {
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
		if !paneRowWraps(window, row) {
			break
		}
	}
	if cursor < 0 {
		return PaneLink{}, false
	}

	found, ok := hints.LinkAt(b.String(), cursor, rules)
	if !ok {
		return PaneLink{}, false
	}
	s, e := found.Start, found.End

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

	link := PaneLink{
		WindowID: window.ID,
		URL:      found.Target,
		Y0:       refs[first].Y, X0: refs[first].X,
		Y1: refs[last].Y, X1: refs[last].X,
		Row: y, Col: x,
	}
	if link.Y0 < 0 {
		link.Y0, link.X0 = 0, 0
	}
	if link.Y1 >= h {
		link.Y1, link.X1 = h-1, maxX-1
	}
	return link, true
}

func paneRowSource(window *terminal.Window, y int) (sbIdx, screenY int, ok bool) {
	if window.Terminal == nil {
		return -1, -1, false
	}
	offset := max(window.ScrollbackOffset, 0)
	if y >= offset {
		screenY = y - offset
		if screenY >= window.Terminal.Height() {
			return -1, -1, false
		}
		return -1, screenY, true
	}
	if offset == 0 && window.Terminal.IsAltScreen() {
		return -1, -1, false
	}
	n := window.ScrollbackLen()
	sbIdx = n - offset + y
	if sbIdx < 0 || sbIdx >= n {
		return -1, -1, false
	}
	return sbIdx, -1, true
}

func paneRowWraps(window *terminal.Window, y int) bool {
	sbIdx, screenY, ok := paneRowSource(window, y)
	if !ok {
		return false
	}
	var wrapped, known bool
	if sbIdx >= 0 {
		wrapped, known = window.Terminal.ScrollbackSoftWrapped(sbIdx)
	} else {
		wrapped, known = window.Terminal.RowSoftWrapped(screenY)
	}
	return known && wrapped
}

func paneCellAt(window *terminal.Window, x, y int) *uv.Cell {
	sbIdx, screenY, ok := paneRowSource(window, y)
	if !ok || x < 0 {
		return nil
	}
	if sbIdx >= 0 {
		line := window.ScrollbackLine(sbIdx)
		if x >= len(line) {
			return nil
		}
		return &line[x]
	}
	return window.Terminal.CellAt(x, screenY)
}

func paneRowText(window *terminal.Window, y, maxX int) (string, []int) {
	text, byteAt := appendPaneRowText(make([]byte, 0, maxX), make([]int, maxX), window, y, maxX)
	return string(text), byteAt
}

func appendPaneRowText(buf []byte, byteAt []int, window *terminal.Window, y, maxX int) ([]byte, []int) {
	byteAt = byteAt[:maxX]
	for i := range byteAt {
		byteAt[i] = -1
	}
	base := len(buf)
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
		byteAt[x] = len(buf) - base
		buf = append(buf, content...)
		x += width
	}
	return buf, byteAt
}
