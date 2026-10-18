package composer

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// gutter is the cells in front of each row: the sigil on the first row of the
// draft, a scroll mark on a row with more draft past it.
const gutter = 2

// blockMix is how far a block's ground is carried from the editor's ground
// toward the accent.
const blockMix = 0.18

// inks are the colours one frame is drawn with, worked out once per frame.
type inks struct {
	st                   Styles
	surface, field       color.Color
	text, mute, dim, key color.Color
	block                color.Color
	blockText, blockNum  color.Color
	blockDim             color.Color
	selected             color.Color
	// blank is the gutter of a row with no mark, drawn once a frame.
	blank string
}

// newInks takes a frame's colours from the styles. surface is the ground
// around the editor, the panel's.
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

// Drawn counts the frames Render drew rather than took from its cache. A host
// that wraps the lines keeps its own copy until this changes.
func (m *Model) Drawn() uint64 { return m.drawn }

// render draws a frame the cache does not hold.
func (m *Model) render(width int, st Styles, surface color.Color) []string {
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
	shown := min(len(rows), m.opt.MaxRows)
	m.top = min(max(m.top, cr-shown+1), cr)
	m.top = min(max(m.top, 0), len(rows)-shown)

	out := m.renderStrip(width, in)
	m.caret = Caret{Row: len(out) + cr - m.top, Col: gutter}
	if len(m.cells) > 0 {
		m.caret.Col += m.colOf(rows[cr], m.cur, textW)
	}
	m.caretOK = true
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
	out = append(out, m.renderMentions(width, in)...)
	if m.hsearch != nil {
		out = append(out, m.renderHistorySearch(width, in)...)
	}
	if m.notice != "" {
		warn := st.readable(st.Warning, surface)
		for _, l := range wrapWords(m.notice, width-gutter) {
			out = append(out, fill(on(surface).Render(strings.Repeat(" ", gutter))+
				on(surface).Foreground(warn).Render(l), width, surface))
		}
	}
	return out
}

// gutterMark is the two cells in front of row r: the sigil on the draft's
// first row, an arrow on the first or last row shown when more of the draft
// is past it.
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

// arrowKeys names the up and down keys.
func arrowKeys(ascii bool) string {
	if ascii {
		return "up/down"
	}
	return "↑↓"
}

// renderRow writes one row of the draft. Text is drawn in runs, so a row
// costs a few styled strings and not one per character.
func (m *Model) renderRow(b *strings.Builder, row vline, hasCursor bool, textW int, sel *block, in inks) {
	textStyle := on(in.field).Foreground(in.text)
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			b.WriteString(textStyle.Render(run.String()))
			run.Reset()
		}
	}
	for i := row.start; i < row.end; i++ {
		c := m.cells[i]
		if hasCursor && i == m.cur {
			flush()
			b.WriteString(cursor(cursorText(c), in.field, in.text))
			if c.block != nil {
				// The cursor cell took the bracket. The rest of the
				// label follows it.
				b.WriteString(renderBlock(c.block, textW, false, true, in))
			}
			continue
		}
		if c.block != nil {
			flush()
			b.WriteString(renderBlock(c.block, textW, c.block == sel, false, in))
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
		b.WriteString(cursor(" ", in.field, in.text))
	}
}

// cursorText is what the cursor cell shows over c: the character itself, a
// space over a tab or a line break, and the bracket that opens a block.
func cursorText(c cell) string {
	switch {
	case c.block != nil:
		return "["
	case c.s == "\t" || c.s == "\n" || c.w == 0:
		return " "
	}
	return c.s
}

// renderBlock draws a block's label. The number is in the accent and the
// measure is quieter. The selected block, the one before the cursor, is drawn
// as a list draws its cursor row, so it reads as selected at every depth.
//
// skipOpen leaves out the opening bracket, which the cursor cell drew.
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
		// At 16 colours a selected block is underlined, not reversed: the
		// cursor right after it is drawn in reverse video, and two reversed
		// spans side by side read as one bar with no cursor in it.
		s = in.st.underline(s)
	}
	return s
}

// wrapWords breaks plain text onto lines of at most width cells, at spaces.
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
	k := hintsKey{state: m.hintState(), ascii: ascii}
	if k.state == hintsImage && m.preview != nil {
		k.canOpen = m.preview.CanOpen()
	}
	if m.hints == nil || m.hintsAt != k {
		m.hints, m.hintsAt = m.opt.hints(k), k
	}
	return m.hints
}

// The editor's states, as its keys tell them apart.
const (
	hintsDraft = iota
	hintsPager
	hintsHistory
	hintsMention
	hintsImage
	hintsImageBlock
	hintsPasteBlock
)

// hintsKey is what the hints depend on.
type hintsKey struct {
	state          int
	ascii, canOpen bool
}

// hintState is the state the editor is in, for its keys.
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

// hints are the keys of one state.
func (o Options) hints(k hintsKey) []Hint {
	ascii := k.ascii
	switch k.state {
	case hintsPager:
		return []Hint{
			{Key: arrowKeys(ascii), Label: "scroll"},
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
	return append(hints,
		Hint{Key: "ctrl+z", Label: "undo", Optional: true},
		Hint{Key: "esc", Label: "close"},
	)
}
