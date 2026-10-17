package composer

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// pagerExtraRows is how many more rows the open block takes than the editor
// grows to.
const pagerExtraRows = 4

// pager is an open block: a paste's text, read only, with a scroll, or an
// image's view.
type pager struct {
	block *block
	// viewCols and viewRows are an image view's picture box as last drawn.
	viewCols, viewRows int
	top                int
	// lines is the text broken for width, kept until the width changes.
	lines []string
	width int
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
		// The view's box as the next frame draws it, so the host can
		// prepare the picture before that frame.
		m.pager.viewCols, m.pager.viewRows = fitBox(b.img, m.cellAspect(), m.width+1, m.pagerRows(), false)
	}
	return true
}

// Opened reports whether a block is open.
func (m *Model) Opened() bool { return m.pager != nil }

// pagerRows is how many rows of text the open block shows.
func (m *Model) pagerRows() int { return m.opt.MaxRows + pagerExtraRows }

// pagerKey handles a key while a block is open: the arrows and the page keys
// scroll, ctrl+e expands the block, backspace removes it, and esc, q or
// ctrl+o go back to the draft.
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
	switch msg.String() {
	case "esc", "q", "ctrl+o", "enter":
		m.pager = nil
	case "up", "k":
		p.top--
	case "down", "j":
		p.top++
	case "pgup", "b":
		p.top -= page
	case "pgdown", "space", "f":
		p.top += page
	case "home", "g":
		p.top = 0
	case "end", "G":
		p.top = len(p.lines)
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

// renderPager draws the open block: a heading with where the view is, and
// the block's text broken to the width.
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

// breakLines splits text into rows of at most width cells: at each line
// break, and inside a line too long for the width. Tabs are drawn as spaces.
func breakLines(text string, width int) []string {
	width = max(width, 1)
	text = strings.TrimRight(text, "\n")
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.ReplaceAll(line, "\t", strings.Repeat(" ", tabWidth))
		// One pass over the line, so a megabyte on one line is not read
		// again for every row it makes.
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
