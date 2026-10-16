package composer

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// gutter is the cells in front of each row: the sigil on the first row of the
// draft, a scroll mark on a row with more draft past it.
const gutter = 2

// blockMix is how far a block's ground is carried from the editor's ground
// toward the accent.
const blockMix = 0.18

// inks are the colours one frame is drawn with, worked out once per frame.
type inks struct {
	pal                  overlay.Palette
	surface, field       color.Color
	text, mute, dim, key color.Color
	block                color.Color
	blockText, blockNum  color.Color
	blockDim             color.Color
	selected             color.Color
	// blank is the gutter of a row with no mark, drawn once a frame.
	blank string
}

// newInks takes a frame's colours from the palette. surface is the ground
// around the editor, the panel's.
func newInks(pal overlay.Palette, surface color.Color) inks {
	in := inks{pal: pal, surface: surface, field: pal.Card}
	in.text = overlay.Readable(pal.Fg, in.field)
	in.mute = overlay.Readable(pal.FgMute, in.field)
	in.dim = overlay.Readable(pal.FgDim, in.field)
	in.key = overlay.Readable(pal.AccentBright, in.field)
	in.block = in.field
	if pal.Depth != overlay.Depth16 && !overlay.IsNoColor(in.field) {
		in.block = overlay.Shown(overlay.MixColors(in.field, pal.Accent, blockMix))
	}
	in.selected = pal.Ground(overlay.RowState{Cursor: true, Focused: true}, in.block)
	in.blockText = overlay.Readable(pal.Fg, in.block)
	in.blockNum = overlay.Readable(pal.AccentBright, in.block)
	in.blockDim = overlay.Readable(pal.FgDim, in.block)
	return in
}

// Render draws the editor at width cells, on surface, the ground of the panel
// it sits in. Every line it returns is width cells wide and carries its own
// colours. It is the editor's rows, then the notice when there is one; while
// a block is open it is the block instead.
func (m *Model) Render(width int, pal overlay.Palette, surface color.Color) []string {
	width = max(width, gutter+4)
	key := m.frameKey(width, pal, surface)
	if m.frame != nil && m.frameAt == key {
		return m.frame
	}
	m.frame, m.frameAt = m.render(width, pal, surface), key
	m.drawn++
	return m.frame
}

// Drawn counts the frames Render drew rather than took from its cache. A host
// that wraps the lines keeps its own copy until this changes.
func (m *Model) Drawn() uint64 { return m.drawn }

// render draws a frame the cache does not hold.
func (m *Model) render(width int, pal overlay.Palette, surface color.Color) []string {
	in := newInks(pal, surface)
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

	out := make([]string, 0, shown+2)
	sel := m.selectedBlock()
	in.blank = overlay.Style(in.field).Render("  ")
	for r := m.top; r < m.top+shown; r++ {
		var b strings.Builder
		b.WriteString(m.gutterMark(r, len(rows), shown, in))
		if len(m.cells) == 0 {
			b.WriteString(overlay.Cursor(" ", in.field, in.text))
			if m.opt.Placeholder != "" {
				ph := ansi.Truncate(m.opt.Placeholder, textW, "…")
				b.WriteString(overlay.Style(in.field).Foreground(in.mute).Render(ph))
			}
		} else {
			m.renderRow(&b, rows[r], r == cr, textW, sel, in)
		}
		out = append(out, overlay.Fill(b.String(), width, in.field))
	}
	if m.notice != "" {
		warn := overlay.Readable(pal.Warning, surface)
		for _, l := range wrapWords(m.notice, width-gutter) {
			out = append(out, overlay.Fill(overlay.Style(surface).Render(strings.Repeat(" ", gutter))+
				overlay.Style(surface).Foreground(warn).Render(l), width, surface))
		}
	}
	return out
}

// gutterMark is the two cells in front of row r: the sigil on the draft's
// first row, an arrow on the first or last row shown when more of the draft
// is past it.
func (m *Model) gutterMark(r, rows, shown int, in inks) string {
	style := overlay.Style(in.field)
	switch {
	case r == 0:
		return style.Foreground(in.key).Bold(true).Render(overlay.Sigil())
	case r == m.top && m.top > 0:
		return style.Foreground(in.mute).Render(arrow("↑", "^") + " ")
	case r == m.top+shown-1 && r < rows-1:
		return style.Foreground(in.mute).Render(arrow("↓", "v") + " ")
	}
	return in.blank
}

// arrow is glyph, or ascii where the terminal draws only ASCII.
func arrow(glyph, ascii string) string {
	if overlay.UseASCII() {
		return ascii
	}
	return glyph
}

// renderRow writes one row of the draft. Text is drawn in runs, so a row
// costs a few styled strings and not one per character.
func (m *Model) renderRow(b *strings.Builder, row vline, hasCursor bool, textW int, sel *block, in inks) {
	textStyle := overlay.Style(in.field).Foreground(in.text)
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
			b.WriteString(overlay.Cursor(cursorText(c), in.field, in.text))
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
		b.WriteString(overlay.Cursor(" ", in.field, in.text))
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
		text = overlay.Readable(in.pal.Fg, ground)
		num = overlay.Readable(in.pal.AccentBright, ground)
		dim = overlay.Readable(in.pal.FgDim, ground)
	}
	st := overlay.Style(ground)
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
		s = in.pal.Mark(s, overlay.RowState{Cursor: true})
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

// Hints are the keys the editor offers now, for the host's footer.
func (m *Model) Hints() []overlay.Hint {
	switch {
	case m.pager != nil:
		return []overlay.Hint{
			{Key: arrow("↑↓", "up/down"), Label: "scroll"},
			{Key: "ctrl+e", Label: "expand"},
			{Key: "backspace", Label: "remove"},
			{Key: "esc", Label: "back"},
		}
	case m.selectedBlock() != nil && m.selectedPaste() == nil:
		return []overlay.Hint{
			{Key: "backspace", Label: "remove"},
			{Key: "esc", Label: "close"},
		}
	case m.selectedBlock() != nil:
		return []overlay.Hint{
			{Key: "ctrl+o", Label: "open"},
			{Key: "ctrl+e", Label: "expand"},
			{Key: "backspace", Label: "remove"},
			{Key: "esc", Label: "close"},
		}
	}
	hints := []overlay.Hint{
		{Key: overlay.EnterKey(), Label: "send"},
		{Key: "ctrl+j", Label: "new line"},
	}
	if m.opt.Images {
		hints = append(hints, overlay.Hint{Key: "ctrl+v", Label: "paste image"})
	}
	return append(hints,
		overlay.Hint{Key: "ctrl+z", Label: "undo", Priority: overlay.HintOptional},
		overlay.Hint{Key: "esc", Label: "close"},
	)
}
