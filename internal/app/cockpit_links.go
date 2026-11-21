package app

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type ckLinkRun struct {
	url       string
	y, x0, x1 int
}

func (m *OS) cockpitLinkAt(x, y int) (ckLinkRun, bool) {
	return m.cockpitLinkFound(x, y, pathAsk)
}

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

func (m *OS) cockpitLineLinkAt(line uv.Line, x int, how int) (string, int, int, bool) {
	if !linksEnabled(&m.Settings) || x < 0 || x >= len(line) {
		return "", 0, 0, false
	}
	if url, x0, x1, ok := canvasLinkAt(line, x, &m.Settings); ok {
		return url, x0, x1, true
	}
	return m.canvasPathAt(line, x, m.cockpitPathDir(), how)
}

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

func cellLinkAt(line uv.Line, x int) string {
	for x > 0 && line[x].Width == 0 && line[x].Content == "" {
		x--
	}
	return line[x].Link.URL
}

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

func (m *OS) cockpitClearLinkHover() {
	ck := &m.Inbox.ck
	if ck.linkHover.url == "" {
		return
	}
	ck.linkHover = ckLinkRun{}
	m.ResetPointerShape()
	m.cockpitTouch()
}

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

func (m *OS) cockpitLinkPress(x, y int) bool {
	run, ok := m.cockpitLinkFound(x, y, pathNow)
	m.Inbox.ck.linkPress = run.url
	return ok
}

// CockpitLinkCancel forgets the link of the last press. Every press in the
// view starts with it, so a press the view's click never sees (one in copy
// mode, or a drag) leaves no link to open on its release.
func (m *OS) CockpitLinkCancel() { m.Inbox.ck.linkPress = "" }

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
