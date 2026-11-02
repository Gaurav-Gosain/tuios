package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The text half of quick look: the windows it reads, the position in them, and
// the keys that move through them.
//
// A text is read in windows, on demand, by byte offset (quickLookText), so a
// log of any size opens as fast as a small file. At most quickLookTextHeld bytes
// are held. No line is counted: a line number is exact only when the held text
// reaches back to the start of the file, and after a jump to the end the panel
// gives an estimate and says so.
//
// A search looks through the held text first, and then asks the machine with the
// file to look on from there. It reads one chunk at a time for at most 5 s and
// says how far it got, so n goes on from there. A count to a line number works the
// same way. Neither keeps anything.

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
	// held text. quickLookStart and quickLookEnd replace it, and quickLookSeek
	// looks for ql.seek first and replaces it with what it found.
	quickLookNext
	quickLookPrev
	quickLookStart
	quickLookEnd
	quickLookSeek
)

// quickLookPromptKind is what the row at the bottom of a text is asking for.
type quickLookPromptKind uint8

const (
	quickLookNoPrompt quickLookPromptKind = iota
	quickLookFindPrompt
	quickLookLinePrompt
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
	// seek is what a quickLookSeek read looked for, and sought what it found.
	seek   session.PreviewSeek
	sought quickLookSought
}

// quickLookSought is the part of a find or line answer the preview reads.
type quickLookSought struct {
	Offset  int64 `json:"offset"`
	Reached int64 `json:"reached"`
	Counted int64 `json:"counted"`
	Size    int64 `json:"size"`
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
	rows := bodyH - 1
	if m.quickLookSearchRow() {
		rows--
	}
	return max(rows/2, 1)
}

// QuickLookScroll moves inside the file by lines. It reports false for a file
// with nothing to move through. The draw holds the position to what is held, and
// Update reads the next window.
func (m *OS) QuickLookScroll(lines int) bool {
	ql := &m.quickLook
	if !ql.open || !quickLookMoves(ql.res) {
		return false
	}
	if quickLookPaged(ql.res) {
		if lines < 0 {
			return m.quickLookPageTo(ql.pageWant - 1)
		}
		return m.quickLookPageTo(ql.pageWant + 1)
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
	ql.status = ""
	if quickLookPaged(r) {
		if end {
			return m.quickLookPageTo(r.pages)
		}
		return m.quickLookPageTo(1)
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
	if !r.more && r.startOff == 0 && ql.jump != quickLookSeek {
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
	seek := ql.seek
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
		msg := quickLookWindowMsg{target: t, move: move, seek: seek}
		var raw json.RawMessage
		var err error
		if move == quickLookSeek {
			// A search or a count first. The window read follows from what it found.
			if t.host == "" {
				var out map[string]any
				if out, err = session.PreviewLocalSeek(context.Background(), t.path, seek); err == nil {
					raw, err = json.Marshal(out)
				}
			} else {
				if ask {
					msg.older = !quickLookHostHasParam(call, t.host, "find")
					if msg.older {
						return msg
					}
				}
				raw, err = call(t.host, "file-preview", map[string]any{"path": t.path, "find": seek.Find, "from": seek.From, "back": seek.Back, "line": seek.Line})
			}
			if err != nil || json.Unmarshal(raw, &msg.sought) != nil {
				msg.failed = true
				return msg
			}
			if msg.sought.Offset < 0 {
				return msg
			}
			offset = msg.sought.Offset
		}
		switch {
		case t.host == "":
			var out map[string]any
			if out, err = session.PreviewLocalWindow(t.path, offset, limit); err == nil {
				raw, err = json.Marshal(out)
			}
		default:
			if ask && move != quickLookSeek {
				msg.older = !quickLookHostHasParam(call, t.host, "offset")
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

// quickLookHostHasParam asks a machine whether its file-preview verb takes a
// param. A daemon that does not know a param ignores it, so without asking, an
// older one would answer every window with the start of the file. A failed call
// counts as having it: the read that follows says what is wrong.
func quickLookHostHasParam(call func(host, verb string, params map[string]any) (json.RawMessage, error), host, param string) bool {
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
			if p.Name == param {
				return true
			}
		}
		return false
	}
	return true
}

// quickLookSeekMissed says why a search or a count found nothing.
func quickLookSeekMissed(seek session.PreviewSeek, s quickLookSought) string {
	pct := func() string {
		if s.Size <= 0 {
			return "0"
		}
		return strconv.FormatInt(s.Reached*100/s.Size, 10)
	}
	if seek.Line > 0 {
		if s.Reached < s.Size {
			return "tuios counted " + strconv.FormatInt(s.Counted, 10) + " lines in 5 seconds and stopped."
		}
		return "The file has " + strconv.FormatInt(s.Counted, 10) + " lines."
	}
	if seek.Back && s.Reached > 0 || !seek.Back && s.Reached < s.Size {
		return "Searched to " + pct() + "% in 5 seconds. Press n to go on."
	}
	return "No more matches."
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
		if msg.move == quickLookSeek {
			ql.status = "This machine runs an older tuios. It can search only what it sent."
			ql.statusLine = msg.seek.Line > 0
		}
		return nil
	case msg.failed:
		// The text held stays, and the panel says the rest could not be read.
		n.stopped = true
		ql.res = &n
		return nil
	case msg.move == quickLookSeek && msg.sought.Offset < 0:
		ql.status = quickLookSeekMissed(msg.seek, msg.sought)
		ql.statusLine = msg.seek.Line > 0
		ql.resume = 0
		if msg.seek.Find != "" {
			ql.resume = msg.sought.Reached
		}
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
		if msg.move == quickLookSeek {
			ql.resume = 0
			if msg.seek.Line > 0 {
				n.firstLine = int(msg.seek.Line)
			}
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

// quickLookSearchRow says the bottom row of a text is taken by a prompt, a search
// or what the last search said.
func (m *OS) quickLookSearchRow() bool {
	ql := &m.quickLook
	return ql.prompt != quickLookNoPrompt || ql.query != "" || ql.status != ""
}

// quickLookDrawSearchRow draws that row.
func quickLookDrawSearchRow(c *ckCanvas, pal overlay.Palette, ql *quickLookState, y, w int) {
	key := func(t string) ckSeg { return ckSeg{t: t, fg: pal.AccentBright, attrs: ckBold} }
	dim := func(t string) ckSeg { return ckSeg{t: t, fg: pal.FgDim} }
	switch {
	case ql.prompt == quickLookFindPrompt:
		c.text(1, y, w-1, key("/ "), ckSeg{t: ql.input + "█", fg: pal.Fg}, dim("   Press enter to search."))
	case ql.prompt == quickLookLinePrompt:
		word := "line"
		if quickLookPaged(ql.res) {
			word = "page"
		}
		c.text(1, y, w-1, key(": "), ckSeg{t: ql.input + "█", fg: pal.Fg}, dim("   Type a "+word+" number and press enter."))
	case ql.status != "":
		segs := []ckSeg{}
		if ql.query != "" && !ql.statusLine {
			segs = append(segs, key("/ "), ckSeg{t: ql.query + "   ", fg: pal.Fg})
		}
		c.text(1, y, w-1, append(segs, dim(ql.status))...)
	default:
		c.text(1, y, w-1, key("/ "), ckSeg{t: ql.query + "   ", fg: pal.Fg}, key("n"), dim(" next  "), key("N"), dim(" back"))
	}
}

// quickLookWrapsByDefault says a file reads as prose, so long lines wrap until the
// person says otherwise. Code and logs line up by column, and stay cut.
func quickLookWrapsByDefault(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".text", ".rst", ".adoc", "":
		return true
	}
	return false
}

// quickLookWrapRows is how many rows a line takes when it wraps.
func quickLookWrapRows(line string, width int) int {
	return max((ansi.StringWidth(line)+width-1)/max(width, 1), 1)
}

// quickLookLastWrapped is the last top line that still fills the rows, walking up
// from the end of the held text, so the end of a file comes wholly into view.
func quickLookLastWrapped(lines []string, rows, width int) int {
	used := 0
	for i := len(lines) - 1; i >= 0; i-- {
		used += quickLookWrapRows(lines[i], width)
		if used > rows {
			return i + 1
		}
	}
	return 0
}

// quickLookFold says a search ignores case: it holds no capital.
func quickLookFold(q string) bool { return strings.ToLower(q) == q }

// quickLookMatch reports whether a held line holds the search.
func quickLookMatch(line, q string) bool {
	if quickLookFold(q) {
		line = strings.ToLower(line)
	}
	return strings.Contains(line, q)
}

// quickLookLightMatches lights the matches in one drawn row. xOff is the column
// of the line the row starts at.
func quickLookLightMatches(c *ckCanvas, pal overlay.Palette, q, line string, x, y, width, xOff int) {
	if q == "" {
		return
	}
	hay := line
	if quickLookFold(q) {
		hay = strings.ToLower(line)
	}
	// ToLower can change a line's length on a rare rune. The search then lights
	// the wrong cells on that line, and finds the right lines still.
	light := func(cell *uv.Cell) {
		cell.Style.Bg = pal.Accent
		cell.Style.Fg = pal.PillFg
	}
	for at := 0; ; {
		i := strings.Index(hay[at:], q)
		if i < 0 {
			return
		}
		i += at
		end := min(i+len(q), len(line))
		if i >= len(line) {
			return
		}
		col := ansi.StringWidth(line[:i]) - xOff
		cols := ansi.StringWidth(line[i:end])
		if col >= width {
			return
		}
		if col+cols > 0 {
			from := max(col, 0)
			c.restyle(x+from, y, min(col+cols, width)-from, 1, light)
		}
		at = i + len(q)
	}
}

// QuickLookPromptOpen reports whether the row at the bottom of the text is being
// typed in.
func (m *OS) QuickLookPromptOpen() bool {
	return m.quickLook.open && m.quickLook.prompt != quickLookNoPrompt
}

// QuickLookFind opens the search prompt.
func (m *OS) QuickLookFind() {
	ql := &m.quickLook
	if quickLookText(ql.res) {
		ql.prompt, ql.input, ql.status = quickLookFindPrompt, "", ""
	}
}

// QuickLookGoTo opens the prompt for a line number.
func (m *OS) QuickLookGoTo() {
	ql := &m.quickLook
	if quickLookText(ql.res) || quickLookPaged(ql.res) {
		ql.prompt, ql.input, ql.status = quickLookLinePrompt, "", ""
	}
}

// QuickLookWrap wraps long lines, or cuts them, for as long as the preview is open.
func (m *OS) QuickLookWrap() {
	ql := &m.quickLook
	if quickLookText(ql.res) {
		ql.wrap, ql.wrapSet = !ql.wrap, true
	}
}

// QuickLookPromptType adds typed text to the prompt. A line number takes digits
// only.
func (m *OS) QuickLookPromptType(text string) {
	ql := &m.quickLook
	if ql.prompt == quickLookLinePrompt && strings.Trim(text, "0123456789") != "" {
		return
	}
	if len(ql.input)+len(text) <= quickLookPromptMax {
		ql.input += text
	}
}

// QuickLookPromptBack takes the last character off the prompt.
func (m *OS) QuickLookPromptBack() {
	ql := &m.quickLook
	if _, n := utf8.DecodeLastRuneInString(ql.input); n > 0 {
		ql.input = ql.input[:len(ql.input)-n]
	}
}

// QuickLookPromptCancel closes the prompt.
func (m *OS) QuickLookPromptCancel() { m.quickLook.prompt = quickLookNoPrompt }

// QuickLookPromptEnter runs what the prompt holds.
func (m *OS) QuickLookPromptEnter() {
	ql := &m.quickLook
	kind, text := ql.prompt, ql.input
	ql.prompt, ql.input = quickLookNoPrompt, ""
	switch kind {
	case quickLookFindPrompt:
		if text == "" {
			return
		}
		ql.query, ql.resume = text, 0
		// The search is counted from the line at the top, which it may be on.
		ql.scroll--
		m.QuickLookFindMove(true)
	case quickLookLinePrompt:
		if n, err := strconv.ParseInt(text, 10, 64); err == nil && n > 0 {
			m.quickLookGoToLine(n)
		}
	}
}

// QuickLookFindMove goes to the next match, or the one before: in the held text
// when it is there, and otherwise it asks the machine to look on from where the
// held text ends.
func (m *OS) QuickLookFindMove(forward bool) {
	ql := &m.quickLook
	r := ql.res
	if !quickLookText(r) || ql.query == "" {
		return
	}
	ql.status, ql.statusLine = "", false
	top := max(ql.scroll, -1)
	if forward {
		for i := top + 1; i < len(r.lines); i++ {
			if quickLookMatch(r.lines[i], ql.query) {
				ql.scroll = i
				return
			}
		}
		ql.scroll = max(top, 0)
		if !r.more || r.older || r.stopped {
			ql.status = quickLookNoMoreMatches(r)
			return
		}
		ql.seek = session.PreviewSeek{Find: ql.query, From: max(r.endOff, ql.resume)}
		ql.jump = quickLookSeek
		return
	}
	for i := min(top-1, len(r.lines)-1); i >= 0; i-- {
		if quickLookMatch(r.lines[i], ql.query) {
			ql.scroll = i
			return
		}
	}
	ql.scroll = max(top, 0)
	if r.startOff == 0 || r.older || r.stopped {
		ql.status = quickLookNoMoreMatches(r)
		return
	}
	from := r.startOff
	if ql.resume > 0 {
		from = min(from, ql.resume)
	}
	ql.seek = session.PreviewSeek{Find: ql.query, From: from, Back: true}
	ql.jump = quickLookSeek
}

// quickLookNoMoreMatches is the search coming to the end of what it can read.
func quickLookNoMoreMatches(r *quickLookResult) string {
	if r.older {
		return "No more matches in what this machine sent."
	}
	return "No more matches."
}

// quickLookGoToLine goes to a line number, in the held text when it is there.
func (m *OS) quickLookGoToLine(n int64) {
	ql := &m.quickLook
	r := ql.res
	if quickLookPaged(r) {
		if n > int64(r.pages) {
			ql.status, ql.statusLine = "The file has "+plural.Count(r.pages, "page")+".", true
			return
		}
		m.quickLookPageTo(int(n))
		return
	}
	if !quickLookText(r) {
		return
	}
	if r.firstLine > 0 {
		if i := n - int64(r.firstLine); i >= 0 && i < int64(len(r.lines)) {
			ql.scroll = int(i)
			return
		}
	}
	if r.older || r.stopped {
		ql.status, ql.statusLine = "This machine cannot go to that line.", true
		return
	}
	ql.seek = session.PreviewSeek{Line: n}
	ql.jump = quickLookSeek
}
