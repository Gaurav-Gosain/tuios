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
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const (
	quickLookTextHeld   = 768 << 10
	quickLookTextWindow = 256 << 10
	quickLookLineMax    = 64 << 10
)

type quickLookPart struct {
	lines int
	bytes int64
}

type quickLookMove uint8

const (
	quickLookNoMove quickLookMove = iota
	quickLookNext
	quickLookPrev
	quickLookStart
	quickLookEnd
	quickLookSeek
	quickLookAt
)

type quickLookPromptKind uint8

const (
	quickLookNoPrompt quickLookPromptKind = iota
	quickLookFindPrompt
	quickLookLinePrompt
)

type quickLookWindowMsg struct {
	target        quickLookTarget
	move          quickLookMove
	reply         quickLookWindowReply
	failed, older bool
	lines         []string
	spans         [][]diffview.Span
	cutLong       bool
	seek          session.PreviewSeek
	sought        quickLookSought
}

type quickLookSought struct {
	Offset  int64 `json:"offset"`
	Reached int64 `json:"reached"`
	Counted int64 `json:"counted"`
	Size    int64 `json:"size"`
}

type quickLookWindowReply struct {
	Size       int64  `json:"size"`
	Text       string `json:"text"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
}

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

func quickLookText(r *quickLookResult) bool { return r != nil && r.kind == "text" }

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
		avg := max((r.endOff-r.startOff)/int64(len(r.lines)), 1)
		line = "Line about " + strconv.FormatInt(r.startOff/avg+int64(scroll)+1, 10)
	}
	if whole {
		return line + " of " + strconv.Itoa(len(r.lines))
	}
	return line + " · " + strconv.FormatInt(quickLookPercent(r, scroll), 10) + "%"
}

func quickLookPercent(r *quickLookResult, scroll int) int64 {
	if r.size <= 0 || len(r.lines) == 0 {
		return 0
	}
	off := r.startOff + (r.endOff-r.startOff)*int64(scroll)/int64(len(r.lines))
	return off * 100 / r.size
}

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
	if ql.quickLookOffMain() {
		ql.viewScroll = max(ql.viewScroll+lines, 0)
		return true
	}
	if quickLookPaged(ql.res) {
		if lines < 0 {
			return m.quickLookPageTo(ql.pageWant - 1)
		}
		return m.quickLookPageTo(ql.pageWant + 1)
	}
	if quickLookFilm(ql.res) {
		return m.quickLookFilmPercentBy(quickLookWheelPercent, lines > 0)
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
	if ql := &m.quickLook; ql.open && !ql.quickLookOffMain() && quickLookFilm(ql.res) {
		return m.quickLookFilmPercentBy(quickLookFilmPercent, forward)
	}
	n := m.quickLookPage()
	if !forward {
		n = -n
	}
	return m.QuickLookScroll(n)
}

// QuickLookLine moves one line, forward or back.
func (m *OS) QuickLookLine(forward bool) bool {
	if ql := &m.quickLook; ql.open && !ql.quickLookOffMain() && quickLookFilm(ql.res) {
		if forward {
			return m.quickLookFilmTo(ql.filmWant + 1000)
		}
		return m.quickLookFilmTo(ql.filmWant - 1000)
	}
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
	if ql.quickLookOffMain() {
		ql.viewScroll = 0
		if end {
			ql.viewScroll = 1 << 30
		}
		return true
	}
	if quickLookPaged(r) {
		if end {
			return m.quickLookPageTo(r.pages)
		}
		return m.quickLookPageTo(1)
	}
	if quickLookFilm(r) {
		if end {
			return m.quickLookFilmTo(r.durationMs)
		}
		return m.quickLookFilmTo(0)
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
	case quickLookAt:
		offset = min(max(ql.atOff, 0), r.size)
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
			if t.host == "" {
				var out map[string]any
				if out, err = session.PreviewLocalSeek(context.Background(), t.path, seek); err == nil {
					raw, err = jsonx.Marshal(out)
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
			if err != nil || jsonx.Unmarshal(raw, &msg.sought) != nil {
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
				raw, err = jsonx.Marshal(out)
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
		if err != nil || jsonx.Unmarshal(raw, &msg.reply) != nil {
			msg.failed = true
			return msg
		}
		msg.lines, msg.spans, msg.cutLong = quickLookLines(t.path, msg.reply.Text, ascii)
		return msg
	}
}

func quickLookJoinSpans(a [][]diffview.Span, aLines int, b [][]diffview.Span, bLines int) [][]diffview.Span {
	if a == nil && b == nil {
		return nil
	}
	out := make([][]diffview.Span, aLines+bLines)
	copy(out, a)
	copy(out[aLines:], b)
	return out
}

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
	if jsonx.Unmarshal(raw, &res) != nil {
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

func (m *OS) quickLookSearchRow() bool {
	ql := &m.quickLook
	return ql.prompt != quickLookNoPrompt || ql.query != "" || ql.status != ""
}

func quickLookDrawSearchRow(c *ckCanvas, pal overlay.Palette, ql *quickLookState, y, w int) {
	key := func(t string) ckSeg { return ckSeg{t: t, fg: pal.AccentBright, attrs: ckBold} }
	dim := func(t string) ckSeg { return ckSeg{t: t, fg: pal.FgDim} }
	switch {
	case ql.prompt == quickLookFindPrompt:
		c.text(1, y, w-1, append(append([]ckSeg{key("/ ")}, quickLookPromptSegs(ql, pal)...), dim("   Press enter to search."))...)
	case ql.prompt == quickLookLinePrompt:
		word := "line"
		if quickLookPaged(ql.res) {
			word = strings.ToLower(quickLookUnit(ql.res))
		}
		c.text(1, y, w-1, append(append([]ckSeg{key(": ")}, quickLookPromptSegs(ql, pal)...), dim("   Type a "+word+" number and press enter."))...)
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

func quickLookWrapsByDefault(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".text", ".rst", ".adoc", "":
		return true
	}
	return false
}

func quickLookWrapRows(line string, width int) int {
	width = max(width, 1)
	plain := true
	for i := 0; i < len(line); i++ {
		if line[i] < 0x20 || line[i] >= 0x7f {
			plain = false
			break
		}
	}
	if plain {
		return max((len(line)+width-1)/width, 1)
	}
	rows := 0
	for at := 0; at < len(line); rows++ {
		at, _ = quickLookWrapNext(line, at, width)
	}
	return max(rows, 1)
}

func quickLookLastWrapped(lines []string, rows, width int) int {
	used := 0
	for i := len(lines) - 1; i >= 0; i-- {
		used += quickLookWrapRows(lines[i], width)
		if used > rows {
			return min(i+1, len(lines)-1)
		}
	}
	return 0
}

func quickLookFold(q string) bool { return strings.ToLower(q) == q }

func quickLookMatch(line, q string) bool {
	if quickLookFold(q) {
		line = strings.ToLower(line)
	}
	return strings.Contains(line, q)
}

func quickLookLightMatches(c *ckCanvas, pal overlay.Palette, q, line string, x, y, width, xOff int) {
	if q == "" {
		return
	}
	hay := line
	if quickLookFold(q) {
		hay = strings.ToLower(line)
	}
	light := func(cell *uv.Cell) {
		cell.Style.Bg = pal.Accent
		cell.Style.Fg = pal.PillFg
	}
	p, w := 0, 0
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
		var col int
		if utf8.RuneStart(line[i]) {
			for p < i {
				g, gw := ansi.FirstGraphemeCluster(line[p:], ansi.GraphemeWidth)
				if g == "" || p+len(g) > i {
					break
				}
				p += len(g)
				w += gw
			}
			col = w + ansi.StringWidth(line[p:i]) - xOff
		} else {
			col = ansi.StringWidth(line[:i]) - xOff
		}
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
	if quickLookText(ql.res) && !ql.quickLookOffMain() {
		ql.prompt, ql.status = quickLookFindPrompt, ""
		ql.input.Set("")
	}
}

// QuickLookGoTo opens the prompt for a line number.
func (m *OS) QuickLookGoTo() {
	ql := &m.quickLook
	if quickLookText(ql.res) || quickLookPaged(ql.res) {
		ql.prompt, ql.status = quickLookLinePrompt, ""
		ql.input.Set("")
	}
}

// QuickLookWrap wraps long lines, or cuts them, for as long as the preview is open.
func (m *OS) QuickLookWrap() {
	ql := &m.quickLook
	if quickLookText(ql.res) {
		ql.wrap, ql.wrapSet = !ql.wrap, true
	}
}

// QuickLookPromptKey applies one key press to the prompt (see lineKey).
func (m *OS) QuickLookPromptKey(key, text string) {
	ql := &m.quickLook
	lineKey(&ql.input, key, text, ql.promptClean)
}

func (ql *quickLookState) promptClean(text string) string {
	text = cleanControl(text)
	if ql.prompt == quickLookLinePrompt && strings.Trim(text, "0123456789") != "" {
		return ""
	}
	if len(ql.input.String())+len(text) > quickLookPromptMax {
		return ""
	}
	return text
}

func (ql *quickLookState) inputAt() int {
	before, _, _ := ql.input.Parts()
	return len(before)
}

// QuickLookPromptCancel closes the prompt.
func (m *OS) QuickLookPromptCancel() { m.quickLook.prompt = quickLookNoPrompt }

// QuickLookPromptEnter runs what the prompt holds.
func (m *OS) QuickLookPromptEnter() {
	ql := &m.quickLook
	kind, text := ql.prompt, ql.input.String()
	ql.prompt = quickLookNoPrompt
	ql.input.Set("")
	switch kind {
	case quickLookFindPrompt:
		if text == "" {
			return
		}
		ql.query, ql.resume = text, 0
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

func quickLookNoMoreMatches(r *quickLookResult) string {
	if r.older {
		return "No more matches in what this machine sent."
	}
	return "No more matches."
}

func (m *OS) quickLookGoToLine(n int64) {
	ql := &m.quickLook
	r := ql.res
	if quickLookPaged(r) {
		if n > int64(r.pages) {
			ql.status, ql.statusLine = "The file has "+plural.Count(r.pages, strings.ToLower(quickLookUnit(r)))+".", true
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

func quickLookPromptSegs(ql *quickLookState, pal overlay.Palette) []ckSeg {
	before, at, after := ql.input.Parts()
	if at == "" {
		at = " "
	}
	return []ckSeg{{t: before, fg: pal.Fg}, {t: at, fg: pal.Canvas, bg: pal.Fg}, {t: after, fg: pal.Fg}}
}
