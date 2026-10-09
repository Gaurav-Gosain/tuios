package transcriptview

import (
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
)

// The colours of a diff, for a client that draws them itself. They come
// from internal/diffview, so the phone and the desktop review agree: the
// same lexer for the same file name, and the same changed words for the same
// pair of lines. Offsets count UTF-16 code units, because that is what a
// string index means on the client (Kotlin), and a range is half open.

// spanLineMax is the longest line, in characters, that gets spans.
const spanLineMax = 2000

// spanBudget bounds the bytes of code the lexer reads for one reply. chroma's
// lexers are regular expressions and read about 0.5 MB a second, so a page
// of many large edits would spend seconds on colours. 16 KiB is about 35 ms. A hunk gets spans only
// when it fits in what is left, or when it is the first hunk the reply
// colours, so a reply costs at most the budget or its first hunk, whichever
// is more. A hunk past the budget gets its changed words and no spans, and
// its diff says so with Plain. The newest diffs of a newest or before read,
// and the oldest of an after read, are coloured first.
const spanBudget = 16 << 10

// Span is a run of one token class in a diff line. K is chroma's short CSS
// class name for the token type.
type Span struct {
	S int    `json:"s"`
	E int    `json:"e"`
	K string `json:"k"`
}

// Range is a run of a diff line that changed against the line it replaced.
type Range struct {
	S int `json:"s"`
	E int `json:"e"`
}

// style adds the spans and changed words to every line of d, once. It costs
// a lexer run over each side of each hunk, so it runs only for a diff that a
// page is about to carry, never for every diff decoded.
func (r *reader) style(d *Diff) {
	if d == nil || d.styled {
		return
	}
	d.styled = true
	for h := range d.Hunks {
		lines := d.Hunks[h].Lines
		spans := r.lexed == 0 || r.lexed+lexBytes(lines) <= spanBudget
		if !spans {
			d.Plain = true
		}
		r.lexed += styleHunk(d.File, lines, spans)
	}
}

// lexBytes is how many bytes the lexer reads for the spans of a hunk: a
// context line is read on both sides.
func lexBytes(lines []DiffLine) int {
	n := 0
	for _, l := range lines {
		n += len(l.Text) + 1
		if l.Op != "+" && l.Op != "-" {
			n += len(l.Text) + 1
		}
	}
	return n
}

// styleHunk adds words to the lines of one hunk, and spans when spans is
// set. It returns how many bytes the lexer read.
func styleHunk(file string, lines []DiffLine, spans bool) int {
	n := len(lines)
	if n == 0 {
		return 0
	}
	kinds := make([]diffview.Kind, n)
	for i, l := range lines {
		switch l.Op {
		case "+":
			kinds[i] = diffview.Add
		case "-":
			kinds[i] = diffview.Delete
		default:
			kinds[i] = diffview.Context
		}
	}
	units := make([]utf16Index, n)
	for i, l := range lines {
		units[i] = newUTF16Index(l.Text)
	}
	// Changed words: a removed line beside the added line that replaced it,
	// as the review lays them out side by side.
	for _, p := range diffview.Pairs(kinds) {
		if p.Left < 0 || p.Right < 0 || p.Left == p.Right {
			continue
		}
		o, w := diffview.Changed(lines[p.Left].Text, lines[p.Right].Text)
		if !o.Empty() {
			lines[p.Left].Words = []Range{{S: units[p.Left].at(o.Start), E: units[p.Left].at(o.End)}}
		}
		if !w.Empty() {
			lines[p.Right].Words = []Range{{S: units[p.Right].at(w.Start), E: units[p.Right].at(w.End)}}
		}
	}
	if !spans || !diffview.Enabled {
		return 0
	}
	lexed := 0
	// Spans: each side of the hunk as one text, the old side first, so a
	// line both sides have takes the new side's, as the review does.
	for _, skip := range []diffview.Kind{diffview.Add, diffview.Delete} {
		var idx []int
		var text []string
		size := 0
		for i := range lines {
			if kinds[i] != skip {
				idx = append(idx, i)
				text = append(text, lines[i].Text)
				size += len(lines[i].Text) + 1
			}
		}
		toks := diffview.Tokens(file, text, spanLineMax)
		if toks != nil {
			// A file type with no lexer costs nothing and counts nothing.
			lexed += size
		}
		for j, line := range toks {
			i := idx[j]
			if len(line) == 0 {
				lines[i].Spans = nil
				continue
			}
			spans := make([]Span, len(line))
			for k, t := range line {
				spans[k] = Span{S: units[i].at(t.Start), E: units[i].at(t.End), K: t.Class}
			}
			lines[i].Spans = spans
		}
	}
	return lexed
}

// utf16Index turns byte offsets of one string into UTF-16 offsets.
type utf16Index struct {
	// at16 holds the UTF-16 offset of each byte offset, nil when the string
	// is ASCII and the two are the same.
	at16 []int32
}

func newUTF16Index(s string) utf16Index {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return utf16Index{}
	}
	at := make([]int32, len(s)+1)
	u := int32(0)
	for i, r := range s {
		n := utf8.RuneLen(r)
		if n < 0 {
			// An invalid byte decodes as U+FFFD, one byte long.
			n = 1
		}
		for k := range n {
			at[i+k] = u
		}
		if r >= 0x10000 {
			u += 2
		} else {
			u++
		}
	}
	at[len(s)] = u
	return utf16Index{at16: at}
}

// at is the UTF-16 offset of byte offset b.
func (x utf16Index) at(b int) int {
	if x.at16 == nil {
		return b
	}
	return int(x.at16[min(max(b, 0), len(x.at16)-1)])
}
