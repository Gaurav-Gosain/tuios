package composer

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const pagerExtraRows = 4

type pager struct {
	block              *block
	viewCols, viewRows int
	top                int
	lines              []string
	width              int
}

// Open shows the selected block's text in place of the editor. It reports
// whether there was a block to open.
func (m *Model) Open() bool {
	b := m.selectedBlock()
	if b == nil {
		return false
	}
	m.pager = &pager{block: b}
	if b.img != nil {
		m.pager.viewCols, m.pager.viewRows = fitBox(b.img, m.cellAspect(), m.width+1, m.pagerRows(), false)
	}
	return true
}

// Opened reports whether a block is open.
func (m *Model) Opened() bool { return m.pager != nil }

func (m *Model) pagerRows() int { return m.opt.MaxRows + pagerExtraRows }

func (m *Model) pagerKey(msg tea.KeyPressMsg) Event {
	p := m.pager
	if p.block.img != nil {
		switch msg.String() {
		case "esc", "q", "ctrl+o", "enter":
			m.pager = nil
		case "o":
			if m.preview != nil && m.preview.CanOpen() {
				return EventOpenImage
			}
		case "backspace", "delete":
			m.Remove()
		}
		return EventNone
	}
	page := m.pagerRows()
	if delta, ok := m.listKey(msg.String(), page); ok {
		p.top += delta
		p.top = min(max(p.top, 0), max(len(p.lines)-page, 0))
		return EventNone
	}
	switch msg.String() {
	case "esc", "q", "ctrl+o", "enter":
		m.pager = nil
	case "b":
		p.top -= page
	case "space", "f":
		p.top += page
	case "ctrl+e":
		m.Expand()
	case "backspace", "delete":
		m.Remove()
	}
	if m.pager != nil {
		p.top = min(max(p.top, 0), max(len(p.lines)-page, 0))
	}
	return EventNone
}

func (m *Model) renderPager(width int, in inks) []string {
	p := m.pager
	textW := width - gutter
	if p.lines == nil || p.width != textW {
		p.lines, p.width = breakLines(p.block.text, textW), textW
	}
	page := m.pagerRows()
	p.top = min(max(p.top, 0), max(len(p.lines)-page, 0))
	end := min(p.top+page, len(p.lines))

	head := on(in.surface).Foreground(in.st.readable(in.st.Text, in.surface)).Bold(true).
		Render("Pasted text #" + strconv.Itoa(p.block.n))
	where := " · " + p.block.measureWords()
	if len(p.lines) > page {
		where += " · rows " + strconv.Itoa(p.top+1) + " to " + strconv.Itoa(end) + " of " + strconv.Itoa(len(p.lines))
	}
	head += on(in.surface).Foreground(in.st.readable(in.st.Dim, in.surface)).Render(where)
	out := make([]string, 0, page+1)
	out = append(out, fill(ansi.Truncate(head, width, "…"), width, in.surface))
	text := on(in.field).Foreground(in.text)
	pad := on(in.field).Render(strings.Repeat(" ", gutter))
	for _, l := range p.lines[p.top:end] {
		out = append(out, fill(pad+text.Render(l), width, in.field))
	}
	return out
}

func breakLines(text string, width int) []string {
	width = max(width, 1)
	text = strings.TrimRight(text, "\n")
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.ReplaceAll(line, "\t", strings.Repeat(" ", tabWidth))
		start, w := 0, 0
		for i := 0; i < len(line); {
			cluster, cw := ansi.FirstGraphemeCluster(line[i:], ansi.GraphemeWidth)
			if cluster == "" {
				break
			}
			if w+cw > width && i > start {
				out = append(out, line[start:i])
				start, w = i, 0
			}
			w += cw
			i += len(cluster)
		}
		line = line[start:]
		out = append(out, line)
	}
	return out
}

const pagerJump = 1 << 30

func (m *Model) listKey(key string, page int) (int, bool) {
	if m.opt.ListKey != nil {
		return m.opt.ListKey(key, page)
	}
	switch key {
	case "up", "k":
		return -1, true
	case "down", "j":
		return 1, true
	case "pgup":
		return -page, true
	case "pgdown":
		return page, true
	case "home", "g":
		return -pagerJump, true
	case "end", "G":
		return pagerJump, true
	}
	return 0, false
}
