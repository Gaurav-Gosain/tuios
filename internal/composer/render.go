package composer

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const gutter = 2

const blockMix = 0.18

type inks struct {
	st                   Styles
	surface, field       color.Color
	text, mute, dim, key color.Color
	block                color.Color
	blockText, blockNum  color.Color
	blockDim             color.Color
	selected             color.Color
	span, spanText       color.Color
	blank                string
}

func newInks(st Styles, surface color.Color) inks {
	in := inks{st: st, surface: surface, field: st.Field}
	in.text = st.readable(st.Text, in.field)
	in.mute = st.readable(st.Muted, in.field)
	in.dim = st.readable(st.Dim, in.field)
	in.key = st.readable(st.Key, in.field)
	in.block = in.field
	if st.Blend != nil && !noColor(in.field) {
		in.block = st.Blend(in.field, st.Accent, blockMix)
	}
	in.selected = st.Selected
	in.span = st.Selection
	if in.span == nil {
		in.span = st.Selected
	}
	in.spanText = st.readable(st.Text, in.span)
	in.blockText = st.readable(st.Text, in.block)
	in.blockNum = st.readable(st.Key, in.block)
	in.blockDim = st.readable(st.Dim, in.block)
	return in
}

// Render draws the editor at width cells in st, on surface, the ground of the
// panel it sits in. Every line it returns is width cells wide and carries its own
// colours. It is the editor's rows, then the notice when there is one; while
// a block is open it is the block instead.
func (m *Model) Render(width int, st Styles, surface color.Color) []string {
	width = max(width, gutter+4)
	key := m.frameKey(width, &st, surface)
	if m.frame != nil && m.frameAt == key {
		return m.frame
	}
	m.frame, m.frameAt = m.render(width, st, surface), key
	m.drawn++
	return m.frame
}

// Caret is a place in a frame: the line, counted from the frame's first, and
// the column, counted from its left edge.
type Caret struct{ Row, Col int }

// Caret is where the last frame drew the cursor cell. A host puts the
// terminal's own cursor there, hidden, so an input method opens its window
// next to the text being typed. ok is false when the frame drew no cursor.
func (m *Model) Caret() (c Caret, ok bool) { return m.caret, m.caretOK }

// FrameSize is the width and the line count of the last frame Render
// returned, for a host that maps the mouse onto it.
func (m *Model) FrameSize() (width, lines int) { return m.frameAt.width, len(m.frame) }

// Drawn counts the frames Render drew rather than took from its cache. A host
// that wraps the lines keeps its own copy until this changes.
func (m *Model) Drawn() uint64 { return m.drawn }

func (m *Model) render(width int, st Styles, surface color.Color) []string {
	if m.mention != nil {
		st.Field = surface
	}
	in := newInks(st, surface)
	m.places = m.places[:0]
	m.caretOK = false
	if m.pager != nil && m.pager.block.img != nil {
		return m.renderImageView(width, in)
	}
	if m.pager != nil {
		return m.renderPager(width, in)
	}
	textW := width - gutter - 1
	m.width = textW
	rows := m.rows(textW)
	cr := rowOf(rows, m.cur)
	out := m.renderStrip(width, in)
	limit := m.textRows(len(out))
	shown := min(len(rows), limit)
	m.top = min(max(m.top, cr-shown+1), cr)
	m.top = min(max(m.top, 0), len(rows)-shown)

	m.caret = Caret{Row: len(out) + cr - m.top, Col: gutter}
	if len(m.cells) > 0 {
		m.caret.Col += m.colOf(rows[cr], m.cur, textW)
	}
	m.caretOK = true
	m.rowLine = len(out)
	sel := m.selectedBlock()
	in.blank = on(in.field).Render("  ")
	for r := m.top; r < m.top+shown; r++ {
		var b strings.Builder
		b.WriteString(m.gutterMark(r, len(rows), shown, in))
		if len(m.cells) == 0 {
			b.WriteString(cursor(" ", in.field, in.text))
			if m.opt.Placeholder != "" {
				ph := ansi.Truncate(m.opt.Placeholder, textW, "…")
				b.WriteString(on(in.field).Foreground(in.mute).Render(ph))
			}
		} else {
			m.renderRow(&b, rows[r], r == cr, textW, sel, in)
		}
		out = append(out, fill(b.String(), width, in.field))
	}
	if m.full {
		for r := shown; r < limit; r++ {
			out = append(out, fill(in.blank, width, in.field))
		}
	}
	out = append(out, m.renderMentions(width, len(out), in)...)
	if m.hsearch != nil {
		out = append(out, m.renderHistorySearch(width, in)...)
	}
	line, ink := m.notice, st.Warning
	if line == "" {
		line, ink = m.info, st.Dim
	}
	if line != "" {
		fg := st.readable(ink, surface)
		for _, l := range wrapWords(line, width-gutter) {
			out = append(out, fill(on(surface).Render(strings.Repeat(" ", gutter))+
				on(surface).Foreground(fg).Render(l), width, surface))
		}
	}
	return out
}

func (m *Model) gutterMark(r, rows, shown int, in inks) string {
	style := on(in.field)
	switch {
	case r == 0:
		return style.Foreground(in.key).Bold(true).Render(in.st.Sigil + " ")
	case r == m.top && m.top > 0:
		return style.Foreground(in.mute).Render(in.st.arrow("↑", "^") + " ")
	case r == m.top+shown-1 && r < rows-1:
		return style.Foreground(in.mute).Render(in.st.arrow("↓", "v") + " ")
	}
	return in.blank
}

func arrowKeys(ascii bool) string {
	if ascii {
		return "up/down"
	}
	return "↑↓"
}

func (m *Model) renderRow(b *strings.Builder, row vline, hasCursor bool, textW int, sel *block, in inks) {
	plainStyle := on(in.field).Foreground(in.text)
	spanStyle := on(in.span).Foreground(in.spanText)
	if in.st.Plain {
		spanStyle = on(in.field).Foreground(in.text).Reverse(true)
	}
	lo, hi, selecting := m.Selection()
	textStyle, inRun := plainStyle, false
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			b.WriteString(textStyle.Render(run.String()))
			run.Reset()
		}
	}
	for i := row.start; i < row.end; i++ {
		c := m.cells[i]
		inSpan := selecting && i >= lo && i < hi
		if inSpan != inRun {
			flush()
			inRun, textStyle = inSpan, plainStyle
			if inSpan {
				textStyle = spanStyle
			}
		}
		if hasCursor && i == m.cur {
			flush()
			if inSpan {
				b.WriteString(spanStyle.Bold(true).Underline(true).Render(cursorText(c)))
			} else {
				b.WriteString(m.caretAt(cursorText(c), selecting, in))
			}
			if c.block != nil {
				b.WriteString(renderBlock(c.block, textW, false, true, in))
			}
			continue
		}
		if c.block != nil {
			flush()
			b.WriteString(renderBlock(c.block, textW, inSpan || c.block == sel, false, in))
			continue
		}
		if c.s == "\t" {
			run.WriteString(strings.Repeat(" ", tabWidth))
			continue
		}
		run.WriteString(c.s)
	}
	flush()
	if hasCursor && m.cur == row.end && (row.end == len(m.cells) || !row.soft) {
		b.WriteString(m.caretAt(" ", selecting, in))
	}
}

func cursorText(c cell) string {
	switch {
	case c.block != nil:
		return "["
	case c.s == "\t" || c.s == "\n" || c.w == 0:
		return " "
	}
	return c.s
}

func renderBlock(bl *block, maxW int, selected, skipOpen bool, in inks) string {
	ground, text, num, dim := in.block, in.blockText, in.blockNum, in.blockDim
	if selected {
		ground = in.selected
		text = in.st.readable(in.st.Text, ground)
		num = in.st.readable(in.st.Key, ground)
		dim = in.st.readable(in.st.Dim, ground)
	}
	st := on(ground)
	var s string
	if ansi.StringWidth(bl.label) > maxW {
		label := ansi.Truncate(bl.label, maxW, "…")
		if skipOpen {
			label = label[1:]
		}
		s = st.Foreground(text).Render(label)
	} else {
		head, tail, _ := strings.Cut(bl.label, "#")
		if skipOpen {
			head = head[1:]
		}
		numEnd := strings.IndexByte(tail, ' ')
		s = st.Foreground(text).Render(head) +
			st.Foreground(num).Bold(true).Render("#"+tail[:numEnd]) +
			st.Foreground(dim).Render(tail[numEnd:])
	}
	if selected {
		s = in.st.underline(s)
	}
	return s
}

func wrapWords(s string, width int) []string {
	width = max(width, 1)
	var out []string
	line, lineW := "", 0
	for word := range strings.FieldsSeq(s) {
		w := ansi.StringWidth(word)
		switch {
		case lineW == 0:
			line, lineW = word, w
		case lineW+1+w <= width:
			line += " " + word
			lineW += 1 + w
		default:
			out = append(out, line)
			line, lineW = word, w
		}
	}
	if line != "" {
		out = append(out, ansi.Truncate(line, width, "…"))
	}
	return out
}

// Hints are the keys the editor offers now, for the host's footer. ascii
// names the keys in ASCII, as Styles.ASCII draws the editor.
//
// The list is kept until the keys change, so a footer drawn every frame
// costs no allocation. It is shared: do not change it.
func (m *Model) Hints(ascii bool) []Hint {
	k := hintsKey{state: m.hintState(), ascii: ascii, full: m.full}
	if k.state == hintsImage && m.preview != nil {
		k.canOpen = m.preview.CanOpen()
	}
	if m.hints == nil || m.hintsAt != k {
		m.hints, m.hintsAt = m.opt.hints(k), k
	}
	return m.hints
}

const (
	hintsDraft = iota
	hintsPager
	hintsHistory
	hintsMention
	hintsImage
	hintsImageBlock
	hintsPasteBlock
)

type hintsKey struct {
	state                int
	ascii, canOpen, full bool
}

func (m *Model) hintState() int {
	switch {
	case m.pager != nil && m.pager.block.img == nil:
		return hintsPager
	case m.hsearch != nil:
		return hintsHistory
	case m.mention != nil:
		return hintsMention
	case m.pager != nil && m.pager.block.img != nil:
		return hintsImage
	case m.selectedBlock() != nil && m.selectedPaste() == nil:
		return hintsImageBlock
	case m.selectedBlock() != nil:
		return hintsPasteBlock
	}
	return hintsDraft
}

func (o Options) hints(k hintsKey) []Hint {
	ascii := k.ascii
	switch k.state {
	case hintsPager:
		move := arrowKeys(ascii)
		if o.ListKeyHint != nil {
			move = o.ListKeyHint()
		}
		return []Hint{
			{Key: move, Label: "scroll"},
			{Key: "ctrl+e", Label: "expand"},
			{Key: "backspace", Label: "remove"},
			{Key: "esc", Label: "back"},
		}
	case hintsHistory:
		return historyHints(ascii)
	case hintsMention:
		return mentionHints(ascii)
	case hintsImage:
		hints := []Hint{}
		if k.canOpen {
			hints = append(hints, Hint{Key: "o", Label: "open in viewer"})
		}
		return append(hints,
			Hint{Key: "backspace", Label: "remove"},
			Hint{Key: "esc", Label: "back"},
		)
	case hintsImageBlock:
		return []Hint{
			{Key: "ctrl+o", Label: "view"},
			{Key: "backspace", Label: "remove"},
			{Key: "esc", Label: "close"},
		}
	case hintsPasteBlock:
		return []Hint{
			{Key: "ctrl+o", Label: "open"},
			{Key: "ctrl+e", Label: "expand"},
			{Key: "backspace", Label: "remove"},
			{Key: "esc", Label: "close"},
		}
	}
	if o.SingleLine {
		return []Hint{{Key: enterKey(ascii), Label: "send"}, {Key: "esc", Label: "cancel"}}
	}
	hints := []Hint{
		{Key: enterKey(ascii), Label: "send"},
		{Key: "ctrl+j", Label: "new line"},
	}
	if o.Images {
		hints = append(hints, Hint{Key: "ctrl+v", Label: "paste image"})
	}
	full := "taller"
	if k.full {
		full = "shorter"
	}
	return append(hints,
		Hint{Key: FullKey, Label: full, Optional: true},
		Hint{Key: "ctrl+z", Label: "undo", Optional: true},
		Hint{Key: "esc", Label: "close"},
	)
}

func (m *Model) caretAt(ch string, selecting bool, in inks) string {
	if selecting {
		return on(in.field).Foreground(in.text).Bold(true).Underline(true).Render(ch)
	}
	return cursor(ch, in.field, in.text)
}
