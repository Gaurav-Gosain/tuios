package app

import (
	"image/color"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

// An agent's words in the thread are Markdown: internal/markdown reads them
// into blocks, and this lays the blocks out as card lines at the thread's
// width, in the look's colours. Headings, strong and emphasis are bold and
// italic in the body ink, code spans and links take the accent, a code block
// sits on the card ground highlighted by internal/diffview, a table is drawn
// with rules and keeps every cell, and a link carries its address as OSC 8.
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
	// fold is the hit that opens a long code block, "" for a layout that
	// shows every line, and open says the message's blocks are open.
	fold string
	open bool
	// wrap breaks a code line wider than the block onto more rows, each
	// after the first with a continuation mark. Without it the line is cut,
	// with a cut mark at the block's right edge.
	wrap bool
	// t is the thread the message is in, nil outside a thread: it keeps how
	// far each code block is scrolled sideways. id is the message's entry,
	// and codes counts its code blocks so far, for each block's key.
	t     *agentThread
	id    string
	codes int
	// hook lays out a block kind a reader draws itself, such as a picture,
	// and reports whether it did. A reader plugs a kind in here; nil draws
	// every block the usual way. See quicklook_markdown.go.
	hook func(md *ckMD, b markdown.Block, first, rest []ckSeg, width int) bool
	// heads draws a second level heading in the accent too, and a third in
	// the body ink, for a document read as a page: quick look's Markdown.
	heads bool
	// headAt is the line each heading starts on, and headLevel its level,
	// for a layout with heads: quick look marks them on its ruler.
	headAt, headLevel []int
	// lead says the next runs start a paragraph, whose first bold run is
	// its lead label. See runs.
	lead bool
	// blockScope names the layout's rich blocks: the thread and entry, or the
	// file, "" for a layout that draws them as code. nblocks counts them so
	// far, for each block's key. See cockpit_blocks.go.
	blockScope string
	nblocks    int
	// pics takes the pictures of Mermaid blocks, nil for a layout that
	// draws them as text. See cockpit_mermaid_pic.go.
	pics *ckPics
}

// ckCodeFold is how many lines of a code block a closed message shows.
const ckCodeFold = 12

// ckMarkdownFold is ckMarkdown for a thread's message: a code block longer
// than ckCodeFold lines shows that many and a line that opens it, unless
// open.
// The thread t keeps the sideways scroll of its code blocks.
func (m *OS) ckMarkdownFold(l ckLook, text string, width int, id string, open bool, t *agentThread) []ckLine {
	md := &ckMD{m: m, l: l, fold: "fold:" + id, open: open, wrap: m.Inbox.ck.codeWrap, t: t, id: id}
	if t != nil {
		md.blockScope = t.key + "#" + id
		md.pics = m.threadPics(t)
	}
	return md.layout(text, width, markdown.Options{})
}

// ckMarkdown lays out an agent's message as card lines of at most width
// cells.
func (m *OS) ckMarkdown(l ckLook, text string, width int) []ckLine {
	return m.ckMarkdownWith(l, text, width, markdown.Options{})
}

// ckMarkdownWith is ckMarkdown with the parser's options.
func (m *OS) ckMarkdownWith(l ckLook, text string, width int, opt markdown.Options) []ckLine {
	return (&ckMD{m: m, l: l}).layout(text, width, opt)
}

// layout lays out a message's text.
func (md *ckMD) layout(text string, width int, opt markdown.Options) []ckLine {
	l := md.l
	blocks, cut := markdown.ParseWith(strings.TrimSpace(text), opt)
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

// ckAlert is a GitHub alert's label and ink.
func ckAlert(pal overlay.Palette, kind string) (string, color.Color) {
	switch kind {
	case "TIP":
		return "Tip", pal.Success
	case "IMPORTANT":
		return "Important", pal.AccentBright
	case "WARNING":
		return "Warning", pal.Warning
	case "CAUTION":
		return "Caution", pal.Warn
	}
	return "Note", pal.Accent
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
	if md.hook != nil && md.hook(md, b, first, rest, width) {
		return
	}
	switch b.Kind {
	case markdown.Paragraph:
		// A bold label at the start of a paragraph is its lead label.
		md.lead = true
		md.text(md.runs(markdown.Inline(b.Text), ink, 0), first, rest, avail)
	case markdown.Heading:
		hink := pal.Fg
		switch {
		case b.Level == 1:
			hink = pal.AccentBright
		case b.Level == 2 && md.heads:
			hink = pal.Accent
		case b.Level > 2 && !md.heads, b.Level > 3:
			hink = pal.FgDim
		}
		segs := md.runs(markdown.Inline(b.Text), hink, ckBold)
		if icon := ckIconFor(ckLabelIcons, ckSegsText(segs)); icon != "" && l.nerd {
			segs = append([]ckSeg{{t: icon, fg: hink, attrs: ckBold}, {t: " "}}, segs...)
		}
		if md.heads {
			md.headAt, md.headLevel = append(md.headAt, len(md.lines)), append(md.headLevel, b.Level)
		}
		md.text(segs, first, rest, avail)
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
		// A list's marks take the accent, so its items stand out.
		markInk := pal.Accent
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
		if b.Alert != "" {
			name, ink := ckAlert(pal, b.Alert)
			bar.fg = ink
			md.lines = append(md.lines, ckLine{segs: append(slicesClone(first), bar, ckSeg{t: name, fg: ink, attrs: ckBold})})
			first = append(slicesClone(rest), bar)
			md.blocks(b.Children, first, append(slicesClone(rest), bar), width, pal.Fg, list, false)
			return
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
		if !md.richBlock(b, first, rest, avail) {
			md.code(b, first, rest, avail)
		}
	case markdown.Table:
		md.table(b, first, rest, avail)
	case markdown.Image:
		// A reader with no hook for pictures shows the words.
		md.text([]ckSeg{{t: "Image: " + b.Text, fg: pal.FgMute}}, first, rest, avail)
	}
}

// runs turns inline runs into segments in ink, each with attrs added.
func (md *ckMD) runs(rs []markdown.Run, ink color.Color, attrs uint8) []ckSeg {
	l := md.l
	pal := l.pal
	out := make([]ckSeg, 0, len(rs))
	lead := md.lead
	md.lead = false
	for i, r := range rs {
		if ckMarkerRun(r) {
			var a uint8
			if r.Style&markdown.Emphasis != 0 {
				a = ckItalic
			}
			out = append(out, md.pill(r.Text, attrs|a)...)
			continue
		}
		s := ckSeg{t: r.Text, fg: ink, attrs: attrs}
		if r.Style&markdown.Strong != 0 {
			s.attrs |= ckBold
			if attrs&ckBold == 0 {
				// Bold in running text is an anchor term, in the link ink.
				s.fg = pal.Accent
			}
			// A bold lead label the list knows, at the start of a
			// paragraph, takes its icon in the bright accent. A lead-in
			// that names an action takes its icon with its colon anywhere.
			icon := ""
			if lead && i == 0 {
				if icon = ckIconFor(ckLabelIcons, r.Text); icon != "" {
					s.fg = pal.AccentBright
				}
			}
			if icon == "" && strings.HasSuffix(strings.TrimSpace(r.Text), ":") {
				icon = ckIconFor(ckActionIcons, r.Text)
			}
			if icon != "" && l.nerd && r.Style&markdown.CodeSpan == 0 {
				out = append(out, ckSeg{t: icon, fg: s.fg, attrs: s.attrs}, ckSeg{t: " "})
			}
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
		if r.Style&(markdown.CodeSpan|markdown.Link|markdown.Strike) == 0 {
			out = append(out, md.marks(s)...)
			continue
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
	if ckHasTree(segs) {
		md.treeText(segs, first, rest, avail)
		return
	}
	lines := ckWrapSegs(segs, avail)
	for i, ln := range lines {
		pre := rest
		if i == 0 {
			pre = first
		}
		md.lines = append(md.lines, ckLine{segs: append(slicesClone(pre), ln...), wrap: i < len(lines)-1})
	}
}

// ckHasTree reports whether segs hold a row of a tree an agent drew.
func ckHasTree(segs []ckSeg) bool {
	for _, s := range segs {
		if strings.ContainsAny(s.t, "├└") {
			return true
		}
	}
	return false
}

// ckTreeGuide is a tree row's lead as its wrapped rows show it: a branch
// that goes on below (├) is a guide (│), and the rest of the branch is
// blank, so the row's text lines up under its first line.
var ckTreeGuide = strings.NewReplacer("├", "│", "└", " ", "─", " ")

// treeText is text for a paragraph that holds tree rows. Each row is wrapped
// on its own, with a hanging indent: a row that runs past the width goes on
// under its text, after the branch, and the guides of the rows around it go
// on beside it.
func (md *ckMD) treeText(segs []ckSeg, first, rest []ckSeg, avail int) {
	for i, row := range ckSplitRows(segs) {
		pre := rest
		if i == 0 {
			pre = first
		}
		lead, body := ckTreeLead(row)
		hang := ckSegsWidth(lead)
		if hang == 0 || hang > avail/2 {
			lines := ckWrapSegs(row, avail)
			for k, ln := range lines {
				p := rest
				if k == 0 {
					p = pre
				}
				md.lines = append(md.lines, ckLine{segs: append(slicesClone(p), ln...), wrap: k < len(lines)-1})
			}
			continue
		}
		guide := make([]ckSeg, len(lead))
		for k, s := range lead {
			s.t = ckTreeGuide.Replace(s.t)
			guide[k] = s
		}
		lines := ckWrapSegs(body, avail-hang)
		for k, ln := range lines {
			p, l := append(slicesClone(rest), guide...), ln
			if k == 0 {
				p = append(slicesClone(pre), lead...)
			}
			md.lines = append(md.lines, ckLine{segs: append(p, l...), wrap: k < len(lines)-1})
		}
	}
}

// ckSplitRows cuts segments at their hard breaks into rows.
func ckSplitRows(segs []ckSeg) [][]ckSeg {
	var rows [][]ckSeg
	var cur []ckSeg
	for _, s := range segs {
		for {
			i := strings.IndexByte(s.t, '\n')
			if i < 0 {
				break
			}
			if i > 0 {
				p := s
				p.t = s.t[:i]
				cur = append(cur, p)
			}
			rows = append(rows, cur)
			cur = nil
			s.t = s.t[i+1:]
		}
		if s.t != "" {
			cur = append(cur, s)
		}
	}
	return append(rows, cur)
}

// ckTreeLead cuts a row at the end of its tree lead: the spaces, guides and
// branch before its text, and the space after the branch. lead is empty for a
// row that is not a tree row.
func ckTreeLead(row []ckSeg) (lead, body []ckSeg) {
	n, branch := 0, false
	for _, s := range row {
		for _, r := range s.t {
			switch r {
			case '├', '└':
				branch = true
			case '│', '─', ' ':
			default:
				goto done
			}
			n += utf8.RuneLen(r)
		}
	}
done:
	if !branch {
		return nil, row
	}
	for _, s := range row {
		switch {
		case n <= 0:
			body = append(body, s)
		case n >= len(s.t):
			lead = append(lead, s)
			n -= len(s.t)
		default:
			a, b := s, s
			a.t, b.t = s.t[:n], s.t[n:]
			lead, body = append(lead, a), append(body, b)
			n = 0
		}
	}
	return lead, body
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
			case r == ' ' && s.bg == nil && s.attrs&uv.AttrReverse == 0:
				// A space on a ground of its own, or in reverse as a pill
				// is at 16 colours, is part of the word.
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
// names a language the lexers know. A line wider than the block is cut, with
// a cut mark in the mute ink in its last cell, or, with wrap on (W in the
// thread), broken onto more rows, each with a continuation mark in the
// gutter, so a wrapped row does not read as another line.
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
	if md.t != nil {
		// A click on the header copies the block.
		head.right = append([]ckSeg{{t: "copy", fg: pal.FgMute}, {t: "  "}}, head.right...)
		if b.Lang == "" {
			head.right = append(head.right, ckSeg{t: " "})
		}
	}
	start := len(md.lines)
	md.lines = append(md.lines, head)
	more := 0
	if md.fold != "" && !md.open && len(texts) > ckCodeFold {
		more = len(texts) - ckCodeFold
	}
	shown := texts[:len(texts)-more]
	key, xOff := md.codeScroll(shown, codeW)
	if key != "" {
		md.lines[start].hit = ckCodeCopyHit + key
		if md.t.code == nil {
			md.t.code = make(map[string]ckCode)
		}
		md.t.code[key] = ckCode{text: strings.Join(b.Lines, "\n"), open: b.Open}
	}
	for i, t := range shown {
		var sp []diffview.Span
		if i < len(spans) {
			sp = spans[i]
		}
		md.codeLine(dv, t, sp, at, codeW, xOff)
	}
	if more > 0 {
		md.lines = append(md.lines, ckLine{bg: l.panel, bgAt: at, indent: at + 2, hit: md.fold,
			segs: []ckSeg{{t: l.g(ckGlyphFold) + " " + plural.CountAs(more, "more line", "more lines"), fg: pal.FgMute}}})
	}
	md.lines = append(md.lines, ckLine{bg: l.panel, bgAt: at, segs: slicesClone(rest)})
	if key != "" {
		for i := start; i < len(md.lines); i++ {
			md.lines[i].code = key
		}
	}
}

// codeScroll is the next code block's key and how far it is scrolled
// sideways, in cells, held to what its widest line shown leaves past
// codeW. The key is "" outside a thread. It writes that bound to the
// thread, for the scroll.
func (md *ckMD) codeScroll(texts []string, codeW int) (string, int) {
	if md.t == nil {
		return "", 0
	}
	key := md.id + "/" + strconv.Itoa(md.codes)
	md.codes++
	room := 0
	if !md.wrap {
		for _, s := range texts {
			room = max(room, ckTextWidth(s)-codeW)
		}
	}
	if md.t.codeMax == nil {
		md.t.codeMax = make(map[string]int)
	}
	md.t.codeMax[key] = room
	return key, min(md.t.codeX[key], room)
}

// CockpitCodeWrap is W: the long lines of the thread's code blocks wrap, or
// are cut again. It holds for every thread until the client ends.
func (m *OS) CockpitCodeWrap() {
	ck := &m.Inbox.ck
	ck.codeWrap = !ck.codeWrap
	// The cards are kept by look: a new one draws every card again.
	ck.lookGen++
	word := "Code lines are cut at the edge."
	if ck.codeWrap {
		word = "Code lines wrap."
	}
	m.ShowNotification(word, "info", m.Settings.NotificationDuration)
	m.cockpitTouch()
}

// ckCodeCutMark and ckCodeWrapMark are a code row's marks: the line goes on
// past the block's edge (the right one, or the left one when the block is
// scrolled sideways), and the row goes on with the line above.
var (
	ckCodeCutMark  = ckGlyph{"…", "…", ">"}
	ckCodeWrapMark = ckGlyph{"↳", "↳", "+"}
)

// codeLine lays out one line of a code block at column at, in codeW cells:
// whole, cut with the cut mark, or wrapped. A cut line starts xOff cells
// in, with the cut mark in the gutter when that leaves some of it out.
func (md *ckMD) codeLine(dv *diffview.Theme, text string, spans []diffview.Span, at, codeW, xOff int) {
	l := md.l
	// The ink of the block's language label, on the same ground.
	ink := l.pal.FgMute
	lmark := ""
	if !md.wrap && xOff > 0 && text != "" {
		lmark = l.g(ckCodeCutMark)
		s := len(ckCutText(text, xOff))
		text, spans = text[s:], ckSpansIn(spans, s, len(text))
	}
	if ckTextWidth(text) <= codeW {
		md.lines = append(md.lines, ckLine{bg: l.panel, bgAt: at, indent: at + 2, lmark: lmark, markInk: ink,
			raw: dv.Code(text, spans, diffview.Range{}, diffview.Context, false, 0, codeW)})
		return
	}
	if !md.wrap {
		mark := l.g(ckCodeCutMark)
		mw := ckTextWidth(mark)
		n := len(ckCutText(text, max(codeW-mw, 1)))
		md.lines = append(md.lines, ckLine{bg: l.panel, bgAt: at, indent: at + 2, mark: mark, markAt: at + 2 + codeW - mw, markInk: ink, lmark: lmark,
			raw: dv.Code(text[:n], ckSpansIn(spans, 0, n), diffview.Range{}, diffview.Context, false, 0, codeW)})
		return
	}
	for a, first := 0, true; a < len(text); first = false {
		b := a + len(ckCutText(text[a:], codeW))
		if b == a {
			// A character wider than the block: it goes alone.
			_, size := utf8.DecodeRuneInString(text[a:])
			b = a + size
		}
		if b < len(text) {
			// A row never ends on a space, so copy mode, which trims
			// each row's end, joins the rows back to the line.
			k := len(strings.TrimRight(text[a:b], " "))
			if k > 0 {
				b = a + k
			}
		}
		ln := ckLine{bg: l.panel, bgAt: at, indent: at + 2, cont: !first,
			raw: dv.Code(text[a:b], ckSpansIn(spans, a, b), diffview.Range{}, diffview.Context, false, 0, codeW)}
		if !first {
			ln.mark, ln.markAt, ln.markInk = l.g(ckCodeWrapMark), at+1, ink
		}
		md.lines = append(md.lines, ln)
		a = b
	}
}

// ckSpansIn is the spans of bytes a to b of a line, moved to start at 0.
func ckSpansIn(spans []diffview.Span, a, b int) []diffview.Span {
	var out []diffview.Span
	for _, sp := range spans {
		if sp.End <= a || sp.Start >= b {
			continue
		}
		sp.Start, sp.End = max(sp.Start, a)-a, min(sp.End, b)-a
		out = append(out, sp)
	}
	return out
}

// ckTableMinCol is the narrowest column a table wraps its cells in.
// Narrower than that, the table is drawn as one record a row.
const ckTableMinCol = 6

// table lays out a table and keeps every cell, in the thread, quick look and
// every other reader. The header is bold over a rule, and the columns are set
// off by a line, each as wide as its widest cell. A table wider than the room
// gives its widest columns one cap, the largest that fits, and a cell wraps in
// its column. A row of more than one line has a rule under it, so the rows
// stay apart. A table that does not fit at ckTableMinCol cells a column is
// drawn as records: each row its cells under the header's names.
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
	fits := func(cap int) bool {
		n := 3 * (cols - 1)
		for _, w := range widths {
			n += min(w, cap)
		}
		return n <= avail
	}
	if !fits(ckTableMinCol) {
		md.tableRecords(rows, first, rest, avail)
		return
	}
	if !fits(math.MaxInt) {
		lo, hi := ckTableMinCol, 0
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
	rule := func() {
		segs := slicesClone(rest)
		for i, w := range widths {
			if i > 0 {
				segs = append(segs, ckSeg{t: cross, fg: l.edge})
			}
			segs = append(segs, ckSeg{t: strings.Repeat(horiz, w), fg: l.edge})
		}
		md.lines = append(md.lines, ckLine{segs: segs})
	}
	wrapped := make([][][][]ckSeg, len(rows))
	tall := false
	for ri, r := range rows {
		wrapped[ri] = make([][][]ckSeg, cols)
		for i, c := range r {
			wrapped[ri][i] = ckWrapSegs(c, widths[i])
			tall = tall || len(wrapped[ri][i]) > 1
		}
	}
	for ri, r := range wrapped {
		h := 1
		for _, c := range r {
			h = max(h, len(c))
		}
		for k := range h {
			pre := rest
			if ri == 0 && k == 0 {
				pre = first
			}
			segs := slicesClone(pre)
			for i, c := range r {
				if i > 0 {
					segs = append(segs, ckSeg{t: vert, fg: l.edge})
				}
				var part []ckSeg
				if k < len(c) {
					part = c[k]
				}
				segs = append(segs, ckAlignSegs(part, widths[i], b.Align[i])...)
			}
			md.lines = append(md.lines, ckLine{segs: segs})
		}
		if ri == 0 || tall && ri < len(wrapped)-1 {
			rule()
		}
	}
}

// tableRecords lays out a table too wide for its columns as records: for
// each row, each header's name and the row's cell under it, and a blank line
// between the rows.
func (md *ckMD) tableRecords(rows [][][]ckSeg, first, rest []ckSeg, avail int) {
	pal := md.l.pal
	for ri, r := range rows[1:] {
		if ri > 0 {
			md.lines = append(md.lines, ckLine{segs: slicesClone(rest)})
		}
		for i, c := range r {
			name := make([]ckSeg, 0, len(rows[0][i])+1)
			for _, s := range rows[0][i] {
				s.fg = pal.FgDim
				name = append(name, s)
			}
			name = append(name, ckSeg{t: ": ", fg: pal.FgDim})
			pre := rest
			if ri == 0 && i == 0 {
				pre = first
			}
			md.text(append(name, c...), pre, rest, avail)
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
