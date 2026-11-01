package app

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The text half of quick look: the windows it reads, the position in them, and
// the keys that move through them.
//
// A text is read in windows, on demand, by byte offset (quickLookText), so a
// log of any size opens as fast as a small file. At most quickLookTextHeld bytes
// are held. No line is counted: a line number is exact only when the held text
// reaches back to the start of the file, and after a jump to the end the panel
// gives an estimate and says so.

const (
	// quickLookTextHeld bounds the bytes of a text the preview holds, as whole
	// windows of quickLookTextWindow bytes.
	quickLookTextHeld   = 768 << 10
	quickLookTextWindow = 256 << 10
	// quickLookLineMax cuts a longer line for the highlighter and the panel.
	quickLookLineMax = 64 << 10
)

// quickLookPart is one window of a text in a result.
type quickLookPart struct {
	lines int
	bytes int64
}

// quickLookMove says which window of a text a read is for.
type quickLookMove uint8

const (
	quickLookNoMove quickLookMove = iota
	// quickLookNext and quickLookPrev join the windows after and before the
	// held text. quickLookStart and quickLookEnd replace it.
	quickLookNext
	quickLookPrev
	quickLookStart
	quickLookEnd
)

// quickLookWindowMsg carries a finished window of a text back to the loop.
type quickLookWindowMsg struct {
	target quickLookTarget
	move   quickLookMove
	reply  quickLookWindowReply
	// failed says the read failed, and older that the machine has no windows.
	failed, older bool
	lines         []string
	spans         [][]diffview.Span
	cutLong       bool
}

// quickLookWindowReply is the part of a window's answer the preview reads.
type quickLookWindowReply struct {
	Size       int64  `json:"size"`
	Text       string `json:"text"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
}

// quickLookLines splits a text into lines for the panel, and highlights them.
func quickLookLines(path, text string, ascii bool) (lines []string, spans [][]diffview.Span, cutLong bool) {
	lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, l := range lines {
		if len(l) > quickLookLineMax {
			l, cutLong = strings.ToValidUTF8(l[:quickLookLineMax], ""), true
		}
		lines[i] = ckCodeText(strings.TrimSuffix(l, "\r"), ascii)
	}
	return lines, diffview.Highlight(path, lines), cutLong
}

// quickLookText reports whether r is a text the keys move through in windows.
func quickLookText(r *quickLookResult) bool { return r != nil && r.kind == "text" }

// quickLookPosition says where the top of the panel is in a text: the line, and
// how far through the file when the panel does not hold all of it. It is ""
// when there is nothing to say.
func quickLookPosition(r *quickLookResult, scroll, rows int) string {
	if !quickLookText(r) || len(r.lines) == 0 {
		return ""
	}
	scroll = max(min(scroll, len(r.lines)-rows), 0)
	whole := !r.more && r.startOff == 0
	if whole && len(r.lines) <= rows {
		return ""
	}
	var line string
	if r.firstLine > 0 {
		line = "Line " + strconv.Itoa(r.firstLine+scroll)
	} else {
		// Bytes held over lines held, for the lines above the held text.
		avg := max((r.endOff-r.startOff)/int64(len(r.lines)), 1)
		line = "Line about " + strconv.FormatInt(r.startOff/avg+int64(scroll)+1, 10)
	}
	if whole {
		return line + " of " + strconv.Itoa(len(r.lines))
	}
	return line + " · " + strconv.FormatInt(quickLookPercent(r, scroll), 10) + "%"
}

// quickLookPercent is how far through the file the line at scroll is.
func quickLookPercent(r *quickLookResult, scroll int) int64 {
	if r.size <= 0 || len(r.lines) == 0 {
		return 0
	}
	off := r.startOff + (r.endOff-r.startOff)*int64(scroll)/int64(len(r.lines))
	return off * 100 / r.size
}

// quickLookBar draws the bar on the panel's edge that says where the top of the
// panel is in the file, when the file does not fit.
func quickLookBar(c *ckCanvas, pal overlay.Palette, x, top, rows int, r *quickLookResult, scroll int) {
	if rows < 3 || r.size <= 0 || (!r.more && r.startOff == 0 && len(r.lines) <= rows) {
		return
	}
	at := int(quickLookPercent(r, scroll)) * rows / 100
	if !r.more && scroll+rows >= len(r.lines) {
		at = rows - 1
	}
	glyph, thumb := "│", "┃"
	if overlay.UseASCII() {
		glyph, thumb = "|", "#"
	}
	for i := range rows {
		seg := ckSeg{t: glyph, fg: pal.FgMute}
		if i == min(at, rows-1) {
			seg = ckSeg{t: thumb, fg: pal.AccentBright}
		}
		c.text(x, top+i, 0, seg)
	}
}

// quickLookPage is how many lines half a page is.
func (m *OS) quickLookPage() int {
	_, bodyH := m.quickLookSize()
	return max((bodyH-1)/2, 1)
}

// QuickLookScroll moves inside the file by lines. It reports false for a file
// with nothing to move through. The draw holds the position to what is held, and
// Update reads the next window.
func (m *OS) QuickLookScroll(lines int) bool {
	ql := &m.quickLook
	if !ql.open || !quickLookMoves(ql.res) {
		return false
	}
	ql.scroll += lines
	if !quickLookText(ql.res) || ql.res.startOff == 0 {
		ql.scroll = max(ql.scroll, 0)
	}
	ql.jump = quickLookNoMove
	return true
}

// QuickLookHalfPage moves half a page, forward or back.
func (m *OS) QuickLookHalfPage(forward bool) bool {
	n := m.quickLookPage()
	if !forward {
		n = -n
	}
	return m.QuickLookScroll(n)
}

// QuickLookLine moves one line, forward or back.
func (m *OS) QuickLookLine(forward bool) bool {
	if forward {
		return m.QuickLookScroll(1)
	}
	return m.QuickLookScroll(-1)
}

// QuickLookEdge goes to the start or the end of the file. A text whose start or
// end is not held asks for that window, and the next read fetches it.
func (m *OS) QuickLookEdge(end bool) bool {
	ql := &m.quickLook
	r := ql.res
	if !ql.open || !quickLookMoves(r) {
		return false
	}
	switch {
	case !end && (!quickLookText(r) || r.startOff == 0):
		ql.scroll, ql.jump = 0, quickLookNoMove
	case end && (!quickLookText(r) || !r.more):
		ql.scroll, ql.jump = 1<<30, quickLookNoMove
	case end:
		ql.jump = quickLookEnd
	default:
		ql.jump = quickLookStart
	}
	return true
}

// quickLookWindow reads the next window of a text when the position calls for
// one. Update asks after every message. It is nil unless a read is due, and only
// one read runs at a time, so a held key moves the position and the next read goes
// to where it ended.
func (m *OS) quickLookWindow() tea.Cmd {
	ql := &m.quickLook
	r := ql.res
	if !ql.open || ql.fetching || !quickLookText(r) || r.target != ql.target || r.older || r.stopped {
		return nil
	}
	if !r.more && r.startOff == 0 {
		return nil
	}
	if h := ql.target.host; h != "" && ql.hostOlder[h] {
		n := *r
		n.older = true
		ql.res = &n
		return nil
	}
	_, bodyH := m.quickLookSize()
	rows := max(bodyH-1, 1)
	move := ql.jump
	if move == quickLookNoMove {
		top := min(ql.scroll, len(r.lines)-rows)
		switch {
		case r.more && len(r.lines)-(ql.scroll+rows) < rows*2:
			move = quickLookNext
		case r.startOff > 0 && top < rows:
			move = quickLookPrev
		default:
			return nil
		}
	}
	var offset int64
	limit := quickLookTextWindow
	switch move {
	case quickLookNext:
		offset = r.endOff
	case quickLookPrev:
		offset = max(r.startOff-int64(limit), 0)
		limit = int(r.startOff - offset)
	case quickLookEnd:
		offset = max(r.size-int64(limit), 0)
	}
	ql.fetching = true
	t := ql.target
	var call func(host, verb string, params map[string]any) (json.RawMessage, error)
	ask := false
	if t.host != "" {
		call = m.verbOnHostCaller()
		ask = !ql.hostAsked[t.host]
	}
	ascii := overlay.UseASCII()
	return func() tea.Msg {
		msg := quickLookWindowMsg{target: t, move: move}
		var raw json.RawMessage
		var err error
		switch {
		case t.host == "":
			var out map[string]any
			if out, err = session.PreviewLocalWindow(t.path, offset, limit); err == nil {
				raw, err = json.Marshal(out)
			}
		default:
			if ask {
				msg.older = !quickLookHostHasWindows(call, t.host)
				if msg.older {
					return msg
				}
			}
			raw, err = call(t.host, "file-preview", map[string]any{"path": t.path, "offset": offset, "limit": limit})
		}
		if err != nil || json.Unmarshal(raw, &msg.reply) != nil {
			msg.failed = true
			return msg
		}
		msg.lines, msg.spans, msg.cutLong = quickLookLines(t.path, msg.reply.Text, ascii)
		return msg
	}
}

// quickLookJoinSpans joins the highlighting of two runs of lines, one entry per
// line. A file the lexer does not know has none, and stays with none.
func quickLookJoinSpans(a [][]diffview.Span, aLines int, b [][]diffview.Span, bLines int) [][]diffview.Span {
	if a == nil && b == nil {
		return nil
	}
	out := make([][]diffview.Span, aLines+bLines)
	copy(out, a)
	copy(out[aLines:], b)
	return out
}

// quickLookHostHasWindows asks a machine whether its file-preview verb takes
// offset. A daemon that does not know a param ignores it, so without asking, an
// older one would answer every window with the start of the file. A failed call
// counts as having them: the window read that follows says what is wrong.
func quickLookHostHasWindows(call func(host, verb string, params map[string]any) (json.RawMessage, error), host string) bool {
	raw, err := call(host, "list-verbs", map[string]any{"verb": "file-preview"})
	if err != nil {
		return true
	}
	var res struct {
		Verbs []struct {
			Verb   string `json:"verb"`
			Params []struct {
				Name string `json:"name"`
			} `json:"params"`
		} `json:"verbs"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return true
	}
	for _, v := range res.Verbs {
		if v.Verb != "file-preview" {
			continue
		}
		for _, p := range v.Params {
			if p.Name == "offset" {
				return true
			}
		}
		return false
	}
	return true
}

// applyQuickLookWindow joins a finished window to the held text. It makes a new
// result, so the kept panel sees the change.
func (m *OS) applyQuickLookWindow(msg quickLookWindowMsg) tea.Cmd {
	ql := &m.quickLook
	ql.fetching = false
	if !ql.open {
		return nil
	}
	if msg.target != ql.target {
		return m.quickLookShow(ql.target)
	}
	r := ql.res
	if !quickLookText(r) || r.target != msg.target {
		return nil
	}
	if msg.move == ql.jump {
		ql.jump = quickLookNoMove
	}
	if t := msg.target; t.host != "" && (msg.older || !msg.failed) {
		if ql.hostAsked == nil {
			ql.hostAsked = make(map[string]bool)
		}
		ql.hostAsked[t.host] = true
		if msg.older {
			if ql.hostOlder == nil {
				ql.hostOlder = make(map[string]bool)
			}
			ql.hostOlder[t.host] = true
		}
	}
	n := *r
	switch {
	case msg.older:
		n.older = true
		ql.res = &n
		return nil
	case msg.failed:
		// The text held stays, and the panel says the rest could not be read.
		n.stopped = true
		ql.res = &n
		return nil
	}
	w := msg.reply
	part := quickLookPart{lines: len(msg.lines), bytes: w.NextOffset - w.Offset}
	n.size = max(w.Size, r.size)
	n.cutLong = r.cutLong || msg.cutLong
	switch msg.move {
	case quickLookNext:
		n.lines = append(slices.Clone(r.lines), msg.lines...)
		n.spans = quickLookJoinSpans(r.spans, len(r.lines), msg.spans, len(msg.lines))
		n.parts = append(slices.Clone(r.parts), part)
		n.endOff = w.NextOffset
	case quickLookPrev:
		n.lines = append(slices.Clone(msg.lines), r.lines...)
		n.spans = quickLookJoinSpans(msg.spans, len(msg.lines), r.spans, len(r.lines))
		n.parts = append([]quickLookPart{part}, r.parts...)
		n.startOff = w.Offset
		ql.scroll += len(msg.lines)
		switch {
		case w.Offset == 0:
			n.firstLine = 1
		case r.firstLine > 0:
			n.firstLine = max(r.firstLine-len(msg.lines), 1)
		}
	default:
		n.lines, n.spans = msg.lines, msg.spans
		n.parts = []quickLookPart{part}
		n.startOff, n.endOff = w.Offset, w.NextOffset
		n.firstLine = 0
		if w.Offset == 0 {
			n.firstLine = 1
		}
		ql.scroll = 0
		if msg.move == quickLookEnd {
			ql.scroll = 1 << 30
		}
	}
	n.more = n.endOff < n.size
	// Let whole windows go from the far side of the position.
	for n.endOff-n.startOff > quickLookTextHeld && len(n.parts) > 1 {
		if msg.move == quickLookNext {
			p := n.parts[0]
			n.parts = n.parts[1:]
			n.lines = n.lines[p.lines:]
			if n.spans != nil {
				n.spans = n.spans[p.lines:]
			}
			n.startOff += p.bytes
			ql.scroll -= p.lines
			if n.firstLine > 0 {
				n.firstLine += p.lines
			}
			continue
		}
		p := n.parts[len(n.parts)-1]
		n.parts = n.parts[:len(n.parts)-1]
		n.lines = n.lines[:len(n.lines)-p.lines]
		if n.spans != nil {
			n.spans = n.spans[:len(n.lines)]
		}
		n.endOff -= p.bytes
		n.more = true
	}
	ql.res = &n
	return nil
}
