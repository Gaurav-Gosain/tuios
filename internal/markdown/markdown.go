// Package markdown reads the Markdown that coding agents write into blocks
// and styled runs, for a terminal surface to lay out and draw. It draws
// nothing itself.
//
// It reads the part of CommonMark and GitHub's extensions that agent replies
// use: ATX and setext headings, paragraphs, bullet, ordered and task lists,
// block quotes, fenced and indented code, thematic breaks, pipe tables, and
// inline emphasis, strong, strikethrough, code spans, links, autolinks and
// bare URLs. HTML, reference links and footnotes stay as text.
//
// It is small on purpose. goldmark with its GFM extensions adds about 480 KB
// to the tuios binary, and glamour, which also links every chroma lexer,
// about 8.8 MB. This package and its layout cost about 94 KB, and code blocks are
// highlighted by internal/diffview, which the binary already carries.
//
// Every bound is fixed: input past MaxBytes is cut at a line, nesting past
// maxDepth is read as text, and the closer searches remember a failure, so
// the cost of a text is linear in its size.
package markdown

import (
	"strings"
)

// MaxBytes is the most of a text that is read. The rest is cut at the last
// line break before it, and Parse reports the cut.
const MaxBytes = 32 << 10

// maxDepth bounds the nesting of quotes and lists, and of inline styles.
const maxDepth = 8

// Bounds on a table: past maxTableCols columns the lines are read as a
// paragraph, and rows stop once the table holds maxTableCells cells. A short
// row is padded to the header's width, so without them a wide header and
// many one-pipe rows would grow a few KB of text into millions of cells.
const (
	maxTableCols  = 64
	maxTableCells = 4096
)

// Kind is what a block is.
type Kind uint8

const (
	// Paragraph is running text. Text holds its inline source.
	Paragraph Kind = iota
	// Heading is a heading of Level 1 to 6. Text holds its inline source.
	Heading
	// Item is a list item. Children hold its blocks.
	Item
	// Quote is a block quote. Children hold its blocks.
	Quote
	// Code is a code block. Lines hold its text and Lang its language.
	Code
	// Rule is a thematic break.
	Rule
	// Table is a pipe table. Header, Rows and Align hold its cells.
	Table
)

// Align is the alignment of a table column.
type Align uint8

const (
	// AlignNone is a column with no colon in its delimiter.
	AlignNone Align = iota
	// AlignLeft is ":---".
	AlignLeft
	// AlignCenter is ":---:".
	AlignCenter
	// AlignRight is "---:".
	AlignRight
)

// Block is one block of a text.
type Block struct {
	Kind Kind
	// Level is a heading's level.
	Level int
	// Text is the inline source of a paragraph or heading. A line break in
	// the source is kept as "\n"; Inline reads it.
	Text string
	// Ordered and Number describe a list item: an ordered item's number as
	// written, such as "3.".
	Ordered bool
	Number  string
	// Task is 0 for an item with no check box, 1 for "[ ]" and 2 for "[x]".
	Task int8
	// Children are the blocks of an item or a quote.
	Children []Block
	// Lang and Lines are a code block's language, as the fence names it,
	// and its lines. Open says the fence was never closed: the text is
	// still being written.
	Lang  string
	Lines []string
	Open  bool
	// Header, Rows and Align are a table's cells, as inline source.
	Header []string
	Rows   [][]string
	Align  []Align
}

// Parse reads src into blocks. cut reports that src was longer than
// MaxBytes and only its start was read.
func Parse(src string) (blocks []Block, cut bool) {
	if len(src) > MaxBytes {
		end := strings.LastIndexByte(src[:MaxBytes], '\n')
		if end < 0 {
			end = MaxBytes
		}
		src, cut = src[:end], true
	}
	// Invalid bytes become U+FFFD first: a pair of delimiters taken out
	// from between two stray bytes would join them into a rune the text
	// never held.
	src = strings.ToValidUTF8(src, "\uFFFD")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if strings.IndexByte(l, '\t') >= 0 {
			lines[i] = expandTabs(l)
		}
	}
	return parseBlocks(lines, 0), cut
}

// expandTabs turns tabs into spaces to the next stop of four, which is how
// CommonMark measures indentation.
func expandTabs(s string) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := 4 - col%4
			b.WriteString("    "[:n])
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// indent is the number of spaces a line starts with.
func indent(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

// blank reports a line of spaces only.
func blank(s string) bool { return indent(s) == len(s) }

// fence reads an opening code fence: its character, its length and its
// info string.
func fence(s string) (ch byte, n int, info string, ok bool) {
	i := indent(s)
	if i > 3 || i >= len(s) || s[i] != '`' && s[i] != '~' {
		return 0, 0, "", false
	}
	ch = s[i]
	for j := i; j < len(s) && s[j] == ch; j++ {
		n++
	}
	if n < 3 {
		return 0, 0, "", false
	}
	info = strings.TrimSpace(s[i+n:])
	if ch == '`' && strings.IndexByte(info, '`') >= 0 {
		return 0, 0, "", false
	}
	return ch, n, info, true
}

// closes reports whether s closes a fence of ch at least n long.
func closes(s string, ch byte, n int) bool {
	i := indent(s)
	if i > 3 {
		return false
	}
	j := i
	for j < len(s) && s[j] == ch {
		j++
	}
	return j-i >= n && blank(s[j:])
}

// heading reads an ATX heading.
func heading(s string) (level int, text string, ok bool) {
	i := indent(s)
	if i > 3 {
		return 0, "", false
	}
	j := i
	for j < len(s) && s[j] == '#' {
		j++
	}
	level = j - i
	if level < 1 || level > 6 || j < len(s) && s[j] != ' ' {
		return 0, "", false
	}
	text = strings.TrimSpace(s[j:])
	// A closing run of #, set off by a space, is not part of the text.
	if k := strings.TrimRight(text, "#"); k != text && (k == "" || strings.HasSuffix(k, " ")) {
		text = strings.TrimSpace(k)
	}
	return level, text, true
}

// rule reports a thematic break: three or more of one of - * _, with
// spaces between them allowed.
func rule(s string) bool {
	if indent(s) > 3 {
		return false
	}
	var ch byte
	n := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == ' ':
		case (c == '-' || c == '*' || c == '_') && (ch == 0 || c == ch):
			ch = c
			n++
		default:
			return false
		}
	}
	return n >= 3
}

// setext reads a setext underline: its heading level, or 0.
func setext(s string) int {
	if indent(s) > 3 {
		return 0
	}
	t := strings.TrimSpace(s)
	if t == "" {
		return 0
	}
	if strings.Trim(t, "=") == "" {
		return 1
	}
	if strings.Trim(t, "-") == "" {
		return 2
	}
	return 0
}

// listItem reads a list marker: whether it is ordered, the number as
// written, and the column the item's content starts at.
func listItem(s string) (ordered bool, number string, content int, ok bool) {
	i := indent(s)
	if i > 3 || i >= len(s) {
		return false, "", 0, false
	}
	j := i
	switch s[i] {
	case '-', '*', '+':
		j++
	default:
		for j < len(s) && j-i < 9 && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i || j >= len(s) || s[j] != '.' && s[j] != ')' {
			return false, "", 0, false
		}
		ordered, number = true, s[i:j+1]
		j++
	}
	if j < len(s) && s[j] != ' ' {
		return false, "", 0, false
	}
	// The content starts after one to four spaces; a run of five or more
	// is code inside the item, which starts after one.
	sp := indent(s[j:])
	if sp == 0 || sp > 4 || j+sp == len(s) {
		sp = 1
	}
	return ordered, number, j + sp, true
}

// quote reports a block quote line and returns it without its marker.
func quote(s string) (string, bool) {
	i := indent(s)
	if i > 3 || i >= len(s) || s[i] != '>' {
		return "", false
	}
	return strings.TrimPrefix(s[i+1:], " "), true
}

// interrupts reports whether s starts a block that ends a paragraph.
func interrupts(s string) bool {
	if _, _, _, ok := fence(s); ok {
		return true
	}
	if _, _, ok := heading(s); ok {
		return true
	}
	if _, ok := quote(s); ok {
		return true
	}
	if rule(s) {
		return true
	}
	// A list item ends a paragraph when it has text, and an ordered one
	// only when it starts at 1.
	if ordered, number, content, ok := listItem(s); ok && content < len(s) && !blank(s[content:]) {
		return !ordered || number == "1." || number == "1)"
	}
	return false
}

// parseBlocks reads lines into blocks. depth is the nesting of quotes and
// lists the lines are in.
func parseBlocks(lines []string, depth int) []Block {
	var out []Block
	for i := 0; i < len(lines); {
		s := lines[i]
		if blank(s) {
			i++
			continue
		}
		nest := depth < maxDepth
		if ch, n, info, ok := fence(s); ok {
			ind := indent(s)
			b := Block{Kind: Code, Open: true}
			b.Lang, _, _ = strings.Cut(info, " ")
			i++
			for ; i < len(lines); i++ {
				if closes(lines[i], ch, n) {
					b.Open = false
					i++
					break
				}
				// Content keeps its indent past the fence's own.
				l := lines[i]
				b.Lines = append(b.Lines, l[min(ind, indent(l)):])
			}
			out = append(out, b)
			continue
		}
		if indent(s) >= 4 {
			b := Block{Kind: Code}
			for ; i < len(lines) && (blank(lines[i]) || indent(lines[i]) >= 4); i++ {
				b.Lines = append(b.Lines, lines[i][min(4, len(lines[i])):])
			}
			for len(b.Lines) > 0 && blank(b.Lines[len(b.Lines)-1]) {
				b.Lines = b.Lines[:len(b.Lines)-1]
			}
			out = append(out, b)
			continue
		}
		if level, text, ok := heading(s); ok {
			out = append(out, Block{Kind: Heading, Level: level, Text: text})
			i++
			continue
		}
		if rule(s) {
			out = append(out, Block{Kind: Rule})
			i++
			continue
		}
		if _, ok := quote(s); ok && nest {
			var inner []string
			for ; i < len(lines); i++ {
				if q, ok := quote(lines[i]); ok {
					inner = append(inner, q)
					continue
				}
				// A lazy line continues the quote's paragraph.
				if blank(lines[i]) || interrupts(lines[i]) || len(inner) == 0 || blank(inner[len(inner)-1]) {
					break
				}
				inner = append(inner, lines[i])
			}
			out = append(out, Block{Kind: Quote, Children: parseBlocks(inner, depth+1)})
			continue
		}
		if ordered, number, content, ok := listItem(s); ok && nest {
			inner := []string{s[min(content, len(s)):]}
			i++
			for i < len(lines) {
				l := lines[i]
				if blank(l) {
					// Blank lines stay in the item only when an indented
					// line follows them.
					j := i
					for j < len(lines) && blank(lines[j]) {
						j++
					}
					if j == len(lines) || indent(lines[j]) < content {
						break
					}
					for ; i < j; i++ {
						inner = append(inner, "")
					}
					continue
				}
				if indent(l) >= content {
					inner = append(inner, l[content:])
					i++
					continue
				}
				// A lazy line continues the item's paragraph.
				if last := inner[len(inner)-1]; blank(last) || interrupts(l) {
					break
				}
				if _, _, _, item := listItem(l); item {
					break
				}
				inner = append(inner, strings.TrimLeft(l, " "))
				i++
			}
			b := Block{Kind: Item, Ordered: ordered, Number: number}
			if t := inner[0]; len(t) >= 3 && t[0] == '[' && t[2] == ']' && (len(t) == 3 || t[3] == ' ') {
				switch t[1] {
				case ' ':
					b.Task = 1
				case 'x', 'X':
					b.Task = 2
				}
				if b.Task != 0 {
					inner[0] = strings.TrimLeft(t[3:], " ")
				}
			}
			b.Children = parseBlocks(inner, depth+1)
			out = append(out, b)
			continue
		}
		if i+1 < len(lines) && strings.IndexByte(s, '|') >= 0 {
			if align, ok := delimiterRow(lines[i+1]); ok {
				header := cells(s)
				if len(header) == len(align) && len(header) <= maxTableCols {
					b := Block{Kind: Table, Header: header, Align: align}
					for i += 2; i < len(lines) && (len(b.Rows)+2)*len(header) <= maxTableCells && !blank(lines[i]) && strings.IndexByte(lines[i], '|') >= 0 && !interrupts(lines[i]); i++ {
						row := cells(lines[i])
						// A row has the header's width: short rows are
						// filled, long ones cut.
						for len(row) < len(header) {
							row = append(row, "")
						}
						b.Rows = append(b.Rows, row[:len(header)])
					}
					out = append(out, b)
					continue
				}
			}
		}
		// A paragraph runs to a blank line or a block that interrupts it.
		para := []string{strings.TrimLeft(s, " ")}
		level := 0
		for i++; i < len(lines); i++ {
			l := lines[i]
			if blank(l) {
				break
			}
			if level = setext(l); level != 0 {
				i++
				break
			}
			if interrupts(l) {
				break
			}
			para = append(para, strings.TrimLeft(l, " "))
		}
		text := strings.TrimRight(strings.Join(para, "\n"), " ")
		if level != 0 {
			out = append(out, Block{Kind: Heading, Level: level, Text: text})
		} else {
			out = append(out, Block{Kind: Paragraph, Text: text})
		}
	}
	return out
}

// delimiterRow reads a table's delimiter row: one alignment per column.
func delimiterRow(s string) ([]Align, bool) {
	if strings.IndexByte(s, '-') < 0 || strings.Count(s, "|") > maxTableCols+1 {
		return nil, false
	}
	var out []Align
	for _, c := range cells(s) {
		c = strings.TrimSpace(c)
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		c = strings.Trim(c, ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return nil, false
		}
		switch {
		case left && right:
			out = append(out, AlignCenter)
		case left:
			out = append(out, AlignLeft)
		case right:
			out = append(out, AlignRight)
		default:
			out = append(out, AlignNone)
		}
	}
	return out, len(out) > 0
}

// cells splits a table row at its unescaped pipes outside code spans.
func cells(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var out []string
	start, tick := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '`':
			n := 1
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			switch tick {
			case 0:
				tick = n
			case n:
				tick = 0
			}
			i += n - 1
		case '|':
			if tick == 0 {
				out = append(out, strings.TrimSpace(strings.ReplaceAll(s[start:i], `\|`, "|")))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(strings.ReplaceAll(s[start:], `\|`, "|")))
}
