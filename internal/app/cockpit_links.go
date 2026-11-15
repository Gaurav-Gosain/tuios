package app

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Links in the agents view open on a click, as a link in a pane does.
//
// The cockpit draws its links as OSC 8 on the cells of its canvas, so the
// outer terminal can open them. A click inside the cockpit is tuios's own
// mouse event, so the outer terminal never sees it. This file reads the link
// back from the cell the click landed on, in the canvas the last frame drew,
// and hands it to OpenLink, which decides which machine opens it.
//
// A cell with no OSC 8 address may still hold a bare URL: a tool line or a
// command prints one as plain text. With links = "all" the row's text is
// scanned for one, with the same detector and link rules a pane uses.
//
// Nothing here runs on a frame that the pointer did not move over a link: the
// hover is resolved on motion, and the frame only restyles the cells of a run
// it already holds.

// ckLinkRun is a link on one row of the cockpit, in screen cells. x1 is the
// column after its last cell.
type ckLinkRun struct {
	url       string
	y, x0, x1 int
}

// cockpitLinkAt returns the link under screen cell x, y in the frame last
// drawn, if there is one.
func (m *OS) cockpitLinkAt(x, y int) (ckLinkRun, bool) {
	return m.cockpitLinkFound(x, y, pathAsk)
}

// cockpitLinkFound is cockpitLinkAt with a path found as how says
// (pathThere's): a press and its release ask the disk at once, as a press may
// come with no motion before it.
func (m *OS) cockpitLinkFound(x, y, how int) (ckLinkRun, bool) {
	ck := &m.Inbox.ck
	if !linksEnabled(&m.Settings) || ck.canvas == nil {
		return ckLinkRun{}, false
	}
	cy := y - ck.canvasTop
	if cy < 0 || cy >= ck.canvas.h {
		return ckLinkRun{}, false
	}
	url, x0, x1, ok := m.cockpitLineLinkAt(ck.canvas.buf.Lines[cy], x, how)
	return ckLinkRun{url: url, y: y, x0: x0, x1: x1}, ok
}

// cockpitLineLinkAt is the link covering column x of a line of the agents
// view: a link or a URL (canvasLinkAt), or else a file path that is there,
// relative ones taken from the agent's folder. how is pathThere's.
func (m *OS) cockpitLineLinkAt(line uv.Line, x int, how int) (string, int, int, bool) {
	if !linksEnabled(&m.Settings) || x < 0 || x >= len(line) {
		return "", 0, 0, false
	}
	if url, x0, x1, ok := canvasLinkAt(line, x, &m.Settings); ok {
		return url, x0, x1, true
	}
	return m.canvasPathAt(line, x, m.cockpitPathDir(), how)
}

// canvasLinkAt finds the link covering column x of a canvas line: an OSC 8
// run first, then, with links = "all", a bare URL in the line's text. It
// returns the link and the columns it covers, x1 the column after the last.
func canvasLinkAt(line uv.Line, x int, s *config.Settings) (url string, x0, x1 int, ok bool) {
	if x < 0 || x >= len(line) {
		return "", 0, 0, false
	}
	if url = cellLinkAt(line, x); url != "" {
		x0, x1 = x, x+1
		for x0 > 0 && cellLinkAt(line, x0-1) == url {
			x0--
		}
		for x1 < len(line) && cellLinkAt(line, x1) == url {
			x1++
		}
		return url, x0, x1, true
	}
	if s.Links != config.LinksAll {
		return "", 0, 0, false
	}
	text, byteAt := canvasRowText(line)
	at := byteAt[x]
	for i := x; at < 0 && i > 0; i-- {
		at = byteAt[i-1]
	}
	found, found2 := hints.LinkAt(text, max(at, 0), s.LinkRules)
	if !found2 || !hints.OpenableScheme(found.Target) {
		return "", 0, 0, false
	}
	x0 = -1
	for col, off := range byteAt {
		if off >= found.Start && off < found.End {
			if x0 < 0 {
				x0 = col
			}
			x1 = col + max(line[col].Width, 1)
		}
	}
	if x0 < 0 {
		return "", 0, 0, false
	}
	return found.Target, x0, min(x1, len(line)), true
}

// cellLinkAt is the OSC 8 address of the cell that draws column x: the cell
// itself, or the wide cell whose second half x is.
func cellLinkAt(line uv.Line, x int) string {
	for x > 0 && line[x].Width == 0 && line[x].Content == "" {
		x--
	}
	return line[x].Link.URL
}

// canvasRowText is one canvas row as plain text, and per column the byte
// offset of the cell that drew it, -1 for the second half of a wide cell.
func canvasRowText(line uv.Line) (string, []int) {
	byteAt := make([]int, len(line))
	b := make([]byte, 0, len(line))
	for x := range line {
		cell := line[x]
		if cell.Width == 0 && cell.Content == "" {
			byteAt[x] = -1
			continue
		}
		byteAt[x] = len(b)
		if cell.Content == "" {
			b = append(b, ' ')
		} else {
			b = append(b, cell.Content...)
		}
	}
	return string(b), byteAt
}

// cockpitLinkHover records the link under the pointer, and reports whether
// there is one. A change of run draws the frame again; a move along the same
// run does not.
func (m *OS) cockpitLinkHover(x, y int) bool {
	ck := &m.Inbox.ck
	run, ok := m.cockpitLinkAt(x, y)
	if run != ck.linkHover {
		ck.linkHover = run
		if ok {
			m.SetPointerShape(PointerPointer)
		} else {
			m.ResetPointerShape()
		}
		m.cockpitTouch()
	}
	ck.linkPointX, ck.linkPointY = x, y
	return ok
}

// cockpitClearLinkHover drops the hover, as when a menu or a sheet covers the
// link.
func (m *OS) cockpitClearLinkHover() {
	ck := &m.Inbox.ck
	if ck.linkHover.url == "" {
		return
	}
	ck.linkHover = ckLinkRun{}
	m.ResetPointerShape()
	m.cockpitTouch()
}

// drawCockpitLinkHover restyles the hovered run on the canvas just drawn, top
// being the canvas's first screen row. The run is found again on this canvas,
// so a thread that moved under a still pointer underlines what is there now.
func (m *OS) drawCockpitLinkHover(c *ckCanvas, top int) {
	ck := &m.Inbox.ck
	if ck.linkHover.url == "" {
		return
	}
	y := ck.linkPointY - top
	if y < 0 || y >= c.h {
		return
	}
	_, x0, x1, ok := m.cockpitLineLinkAt(c.buf.Lines[y], ck.linkPointX, pathPeekKnown)
	if !ok {
		return
	}
	ink := theme.LinkHover()
	c.restyle(x0, y, x1-x0, 1, func(cell *uv.Cell) {
		cell.Style.Fg = ink
		cell.Style.Underline = uv.UnderlineSingle
	})
}

// cockpitLinkPress takes a press on a link, if there is one under it. The
// link opens on the release (cockpitLinkRelease), so a drag that starts on a
// link selects text instead.
func (m *OS) cockpitLinkPress(x, y int) bool {
	run, ok := m.cockpitLinkFound(x, y, pathNow)
	m.Inbox.ck.linkPress = run.url
	return ok
}

// CockpitLinkCancel forgets the link of the last press. Every press in the
// view starts with it, so a press the view's click never sees (one in copy
// mode, or a drag) leaves no link to open on its release.
func (m *OS) CockpitLinkCancel() { m.Inbox.ck.linkPress = "" }

// cockpitLinkRelease opens the link a press was taken on, when the button
// goes up on the same link. A release elsewhere opens nothing. A drag's
// release does not reach it: the selection takes it first.
func (m *OS) cockpitLinkRelease(x, y int) (tea.Cmd, bool) {
	url := m.Inbox.ck.linkPress
	if url == "" {
		return nil, false
	}
	m.Inbox.ck.linkPress = ""
	if run, ok := m.cockpitLinkFound(x, y, pathNow); !ok || run.url != url {
		return nil, true
	}
	return m.OpenLink(url), true
}

// CockpitCopyLink returns the link under the copy mode cursor in the thread:
// the address a Markdown link carries, or a bare URL in the row's text. It is
// what gx opens.
func (m *OS) CockpitCopyLink() (string, bool) {
	q := m.threadCopy()
	if q == nil {
		return "", false
	}
	cm := &q.cm
	i := q.ScrollbackLen() - cm.ScrollOffset + cm.CursorY
	if i < 0 || i >= len(q.rows) {
		return "", false
	}
	url, _, _, ok := m.cockpitLineLinkAt(q.rows[i].cells, cm.CursorX, pathNow)
	return url, ok
}
