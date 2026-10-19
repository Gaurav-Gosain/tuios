package app

import (
	"image/color"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// An agent's words in the thread are Markdown: internal/markdown reads them
// into blocks, and this lays the blocks out as card lines at the thread's
// width, in the look's colours. Headings, strong and emphasis are bold and
// italic in the body ink, code spans and links take the accent, a code block
// sits on the card ground highlighted by internal/diffview, a table is drawn
// with rules and cut to fit, and a link carries its address as OSC 8.
//
// The lines are built once per card: the thread keeps a card until its
// entry's text, the width or the look changes.

// ckCodeLexMax bounds the code of one message the lexer reads. A block past
// it is drawn plain.
const ckCodeLexMax = 8 << 10

// ckBullets are the list marks by depth, then by glyph set.
var ckBullets = [3]ckGlyph{{"•", "•", "-"}, {"◦", "◦", "*"}, {"▹", "▹", "+"}}

// ckTaskGlyphs are a task item's open and done marks, the plan card's.
var ckTaskGlyphs = [2]ckGlyph{ckGlyphIdle, ckGlyphReview}

// ckMD is the state of one message's layout.
type ckMD struct {
	m     *OS
	l     ckLook
	lines []ckLine
	// lexed is how many bytes of code the lexer has read.
	lexed int
}

// ckMarkdown lays out an agent's message as card lines of at most width
// cells.
func (m *OS) ckMarkdown(l ckLook, text string, width int) []ckLine {
	blocks, cut := markdown.Parse(strings.TrimSpace(text))
	md := &ckMD{m: m, l: l}
	md.blocks(blocks, nil, nil, width, l.pal.Fg, 0, false)
	if cut {
		md.lines = append(md.lines, ckLine{segs: []ckSeg{{t: overlay.Ellipsis() + " the rest is not shown", fg: l.pal.FgMute}}})
	}
	return md.lines
}

// blocks lays out bs. first is the prefix of the first line, such as a list
// mark, and rest the prefix of every other line. tight leaves out the blank
// line between blocks, as inside a list item. A heading sits on the block
// it heads, and the items of a list on each other.
func (md *ckMD) blocks(bs []markdown.Block, first, rest []ckSeg, width int, ink color.Color, list int, tight bool) {
	for i, b := range bs {
		pre := rest
		if i == 0 {
			pre = first
		} else if !tight && bs[i-1].Kind != markdown.Heading && (b.Kind != markdown.Item || bs[i-1].Kind != markdown.Item) {
			md.lines = append(md.lines, ckLine{segs: slicesClone(rest)})
		}
		md.block(b, pre, rest, width, ink, list)
	}
}

// slicesClone copies a prefix, so lines never share one backing array.
func slicesClone(s []ckSeg) []ckSeg {
	if len(s) == 0 {
		return nil
	}
	return append([]ckSeg(nil), s...)
}

// block lays out one block.
func (md *ckMD) block(b markdown.Block, first, rest []ckSeg, width int, ink color.Color, list int) {
	l := md.l
	pal := l.pal
	avail := max(width-ckSegsWidth(rest), 4)
	switch b.Kind {
	case markdown.Paragraph:
		md.text(md.runs(markdown.Inline(b.Text), ink, 0), first, rest, avail)
	case markdown.Heading:
		hink := pal.Fg
		switch {
		case b.Level == 1:
			hink = pal.AccentBright
		case b.Level > 2:
			hink = pal.FgDim
		}
		md.text(md.runs(markdown.Inline(b.Text), hink, ckBold), first, rest, avail)
	case markdown.Item:
		var mark string
		switch {
		case b.Task != 0:
			mark = l.g(ckTaskGlyphs[b.Task-1])
		case b.Ordered:
			mark = b.Number
		default:
			mark = l.g(ckBullets[list%len(ckBullets)])
		}
		markInk := pal.FgMute
		if b.Task == 2 {
			markInk = pal.Success
		}
		w := ckTextWidth(mark) + 1
		f := append(slicesClone(first), ckSeg{t: mark + " ", fg: markInk})
		r := rest
		if ckSegsWidth(rest)+w <= width/3 {
			r = append(slicesClone(rest), ckSeg{t: strings.Repeat(" ", w)})
		}
		if len(b.Children) == 0 {
			md.lines = append(md.lines, ckLine{segs: f})
			return
		}
		if b.Task == 2 {
			ink = pal.FgDim
		}
		md.blocks(b.Children, f, r, width, ink, list+1, true)
	case markdown.Quote:
		bar := ckSeg{t: "▎ ", fg: pal.FgMute}
		if l.ascii {
			bar.t = "| "
		}
		// Past a third of the width, a deeper quote or list draws no more
		// indent, so its text keeps room on a narrow thread.
		if ckSegsWidth(rest)+2 > width/3 {
			md.blocks(b.Children, first, rest, width, pal.FgDim, list, false)
			return
		}
		md.blocks(b.Children, append(slicesClone(first), bar), append(slicesClone(rest), bar), width, pal.FgDim, list, false)
	case markdown.Rule:
		md.lines = append(md.lines, ckLine{segs: append(slicesClone(first), ckSeg{t: strings.Repeat(hzGlyph(), avail), fg: l.edge})})
	case markdown.Code:
		md.code(b, first, rest, avail)
	case markdown.Table:
		md.table(b, first, rest, avail)
	}
}

// runs turns inline runs into segments in ink, each with attrs added.
func (md *ckMD) runs(rs []markdown.Run, ink color.Color, attrs uint8) []ckSeg {
	l := md.l
	pal := l.pal
	out := make([]ckSeg, 0, len(rs))
	for _, r := range rs {
		s := ckSeg{t: r.Text, fg: ink, attrs: attrs}
		if r.Style&markdown.Strong != 0 {
			s.attrs |= ckBold
		}
		if r.Style&markdown.Emphasis != 0 {
			s.attrs |= ckItalic
		}
		if r.Style&markdown.Strike != 0 {
			s.attrs |= ckStrike
			s.fg = pal.FgMute
		}
		if r.Style&markdown.CodeSpan != 0 {
			s.fg = pal.AccentBright
			if pal.Depth != overlay.Depth16 {
				s.bg = l.panel
			}
		}
		if r.Style&markdown.Link != 0 {
			s.fg = pal.Accent
			s.link = ckLinkTarget(r.URL)
		}
		out = append(out, s)
	}
	return out
}

// ckLinkTarget is the address a link opens: only web and mail addresses,
// with no control character and of a sane length. Any other link is drawn and opens nothing.
func ckLinkTarget(u string) string {
	if len(u) > 2048 {
		return ""
	}
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "mailto:") {
		return ""
	}
	for _, r := range u {
		// C0, space, DEL and C1: a C1 such as ST or CSI, written as UTF-8,
		// ends the OSC 8 string on some terminals.
		if r <= ' ' || r >= 0x7f && r < 0xa0 || r == utf8.RuneError {
			return ""
		}
	}
	return u
}

// text wraps segments to avail cells and adds them as lines, the first
// with first before it and the rest with rest.
func (md *ckMD) text(segs []ckSeg, first, rest []ckSeg, avail int) {
	for i, ln := range ckWrapSegs(segs, avail) {
		pre := rest
		if i == 0 {
			pre = first
		}
		md.lines = append(md.lines, ckLine{segs: append(slicesClone(pre), ln...)})
	}
}

// ckRuneWidth is the cells a rune takes as ckCanvas.text draws it: one
// rune at a time, controls dropped, a combining mark joined to the cell
// before it. The layout measures the same way, so a line it lays out fits
// the cells the canvas draws.
func ckRuneWidth(r rune) int {
	switch {
	case r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0:
		return 0
	case r < 0x80:
		return 1
	}
	var b [utf8.UTFMax]byte
	return ansi.StringWidth(string(b[:utf8.EncodeRune(b[:], r)]))
}

// ckTextWidth is the cells s takes, rune by rune.
func ckTextWidth(s string) int {
	n := 0
	for _, r := range s {
		n += ckRuneWidth(r)
	}
	return n
}

// ckSegsWidth is the cells segs take, rune by rune.
func ckSegsWidth(segs []ckSeg) int {
	n := 0
	for _, s := range segs {
		n += ckTextWidth(s.t)
	}
	return n
}

// ckCutText is the longest start of s at most w cells wide.
func ckCutText(s string, w int) string {
	n := 0
	for i, r := range s {
		rw := ckRuneWidth(r)
		if n+rw > w {
			return s[:i]
		}
		n += rw
	}
	return s
}

// ckWrapSegs breaks styled segments into lines of at most width cells, at
// spaces, and at "\n" (a hard break). A word wider than the line is cut.
// A word is kept as byte ranges of its segments and sliced once, so a long
// word costs its length, not its length squared.
func ckWrapSegs(segs []ckSeg, width int) [][]ckSeg {
	width = max(width, 1)
	// piece is part of a word: bytes start to end of segs[seg].
	type piece struct{ seg, start, end, w int }
	var (
		lines [][]ckSeg
		cur   []ckSeg
		word  []piece
		curW  int
		wordW int
		space bool
	)
	endLine := func() {
		lines = append(lines, cur)
		cur, curW = nil, 0
	}
	emit := func(si, start, end, w int) {
		if start < end {
			s := segs[si]
			s.t = s.t[start:end]
			cur = append(cur, s)
			curW += w
		}
	}
	endWord := func() {
		if wordW == 0 {
			word = word[:0]
			return
		}
		need := wordW
		if space && curW > 0 {
			need++
		}
		if curW > 0 && curW+need > width {
			endLine()
		}
		if space && curW > 0 {
			// The space takes the ink of the word before it, so the line
			// is written with fewer colour changes, and never its ground:
			// a code span's ground does not run into the gap.
			gap := ckSeg{t: " "}
			prev := cur[len(cur)-1]
			gap.fg = prev.fg
			// Inside one link, or one struck or underlined phrase, the
			// gap carries the look, so the line under it does not break.
			if first := segs[word[0].seg]; first.link == prev.link && first.attrs == prev.attrs {
				gap.link, gap.attrs = prev.link, prev.attrs
			}
			cur = append(cur, gap)
			curW++
		}
		for _, p := range word {
			if curW+p.w <= width {
				emit(p.seg, p.start, p.end, p.w)
				continue
			}
			// Cut a word too wide for any line, in one pass.
			t := segs[p.seg].t
			start, w := p.start, 0
			for i, r := range t[p.start:p.end] {
				rw := ckRuneWidth(r)
				if curW+w+rw > width && curW+w > 0 {
					emit(p.seg, start, p.start+i, w)
					endLine()
					start, w = p.start+i, 0
				}
				w += rw
			}
			emit(p.seg, start, p.end, w)
		}
		word, wordW = word[:0], 0
		space = false
	}
	for si, s := range segs {
		for i, r := range s.t {
			switch {
			case r == '\n':
				endWord()
				endLine()
				space = false
			case r == ' ' && s.bg == nil:
				endWord()
				space = true
			default:
				rw := ckRuneWidth(r)
				end := i + utf8.RuneLen(r)
				if r == utf8.RuneError {
					_, size := utf8.DecodeRuneInString(s.t[i:])
					end = i + size
				}
				if n := len(word); n > 0 && word[n-1].seg == si && word[n-1].end == i {
					word[n-1].end, word[n-1].w = end, word[n-1].w+rw
				} else {
					word = append(word, piece{seg: si, start: i, end: end, w: rw})
				}
				wordW += rw
			}
		}
	}
	endWord()
	if len(cur) > 0 || len(lines) == 0 {
		lines = append(lines, cur)
	}
	return lines
}

// code lays out a code block on the card ground, highlighted when the fence
// names a language the lexers know. Lines are cut at the width, not
// wrapped: wrapped code reads as other code.
func (md *ckMD) code(b markdown.Block, first, rest []ckSeg, avail int) {
	l := md.l
	pal := l.pal
	at := ckSegsWidth(rest)
	texts := make([]string, len(b.Lines))
	size := 0
	for i, s := range b.Lines {
		texts[i] = ckCodeText(s, l.ascii)
		size += len(texts[i]) + 1
	}
	var spans [][]diffview.Span
	if b.Lang != "" && md.lexed+size <= ckCodeLexMax {
		spans = diffview.HighlightLang(b.Lang, texts)
		md.lexed += size
	}
	dv := md.m.ckDiffTheme(l)
	codeW := max(avail-3, 4)
	head := ckLine{bg: l.panel, bgAt: at, segs: slicesClone(first)}
	if len(first) > 0 && ckSegsWidth(first) > at {
		// A list mark stays on the canvas, beside the block's ground.
		head.bgAt = ckSegsWidth(first)
	}
	if b.Lang != "" {
		head.right = []ckSeg{{t: ckTrunc(b.Lang, 16), fg: pal.FgMute}, {t: " "}}
	}
	md.lines = append(md.lines, head)
	for i, t := range texts {
		var sp []diffview.Span
		if i < len(spans) {
			sp = spans[i]
		}
		raw := dv.Code(t, sp, diffview.Range{}, diffview.Context, false, 0, codeW)
		md.lines = append(md.lines, ckLine{bg: l.panel, bgAt: at, indent: at + 2, raw: raw})
	}
	md.lines = append(md.lines, ckLine{bg: l.panel, bgAt: at, segs: slicesClone(rest)})
}

// table lays out a table: the header bold over a rule, columns set off by
// a line, each column as wide as its widest cell until the table is wider
// than the thread, then the widest columns give way and their cells are cut.
func (md *ckMD) table(b markdown.Block, first, rest []ckSeg, avail int) {
	l := md.l
	pal := l.pal
	cols := len(b.Header)
	row := func(cells []string, attrs uint8) [][]ckSeg {
		out := make([][]ckSeg, cols)
		for i := range cols {
			out[i] = md.runs(markdown.Inline(cells[i]), pal.Fg, attrs)
		}
		return out
	}
	rows := [][][]ckSeg{row(b.Header, ckBold)}
	for _, r := range b.Rows {
		rows = append(rows, row(r, 0))
	}
	widths := make([]int, cols)
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], ckSegsWidth(c))
		}
	}
	// The widest columns give way first: every column is cut to one cap,
	// the largest cap that fits, found by halving. No column goes under
	// three cells.
	fits := func(cap int) bool {
		n := 3 * (cols - 1)
		for _, w := range widths {
			n += min(w, cap)
		}
		return n <= avail
	}
	if !fits(math.MaxInt) {
		lo, hi := 3, 0
		for _, w := range widths {
			hi = max(hi, w)
		}
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if fits(mid) {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		for i := range widths {
			widths[i] = min(widths[i], lo)
		}
	}
	vert, cross, horiz := " │ ", "─┼─", "─"
	if l.ascii {
		vert, cross, horiz = " | ", "-+-", "-"
	}
	for ri, r := range rows {
		pre := rest
		if ri == 0 {
			pre = first
		}
		segs := slicesClone(pre)
		for i, c := range r {
			if i > 0 {
				segs = append(segs, ckSeg{t: vert, fg: l.edge})
			}
			segs = append(segs, ckAlignSegs(c, widths[i], b.Align[i])...)
		}
		md.lines = append(md.lines, ckLine{segs: segs})
		if ri == 0 {
			segs := slicesClone(rest)
			for i, w := range widths {
				if i > 0 {
					segs = append(segs, ckSeg{t: cross, fg: l.edge})
				}
				segs = append(segs, ckSeg{t: strings.Repeat(horiz, w), fg: l.edge})
			}
			md.lines = append(md.lines, ckLine{segs: segs})
		}
	}
}

// ckAlignSegs cuts or pads a cell to w cells, by its column's alignment.
func ckAlignSegs(segs []ckSeg, w int, align markdown.Align) []ckSeg {
	have := ckSegsWidth(segs)
	if have > w {
		var out []ckSeg
		left := w - 1
		for _, s := range segs {
			sw := ckTextWidth(s.t)
			if sw <= left {
				out = append(out, s)
				left -= sw
				continue
			}
			s.t = ckCutText(s.t, left)
			out = append(out, s)
			break
		}
		return append(out, ckSeg{t: overlay.Ellipsis()})
	}
	pad := w - have
	lead := 0
	switch align {
	case markdown.AlignRight:
		lead = pad
	case markdown.AlignCenter:
		lead = pad / 2
	}
	out := make([]ckSeg, 0, len(segs)+2)
	if lead > 0 {
		out = append(out, ckSeg{t: strings.Repeat(" ", lead)})
	}
	out = append(out, segs...)
	if pad-lead > 0 {
		out = append(out, ckSeg{t: strings.Repeat(" ", pad-lead)})
	}
	return out
}
