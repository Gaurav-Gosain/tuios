// Ported from pele src/diagrams/timeline/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The port reads UTF-8 bytes. Where pele steps one unit past a whitespace character
// that ends a keyword, the port steps the whole character.

package timeline

import (
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/util"
)

// Token types.
const (
	T_END = iota
	T_EOF
	T_INVALID
	T_NEWLINE
	T_timeline
	T_timeline_lr
	T_timeline_td
	T_title
	T_acc_title
	T_acc_title_value
	T_acc_descr
	T_acc_descr_value
	T_acc_descr_multiline_value
	T_section
	T_period
	T_event
)

var TOKEN_NAMES = [...]string{
	"$end",
	"EOF",
	"INVALID",
	"NEWLINE",
	"timeline",
	"timeline_lr",
	"timeline_td",
	"title",
	"acc_title",
	"acc_title_value",
	"acc_descr",
	"acc_descr_value",
	"acc_descr_multiline_value",
	"section",
	"period",
	"event",
}

// codeAt decodes the character at a byte offset: -1 past the end.
func codeAt(src string, q int) rune {
	if q < 0 || q >= len(src) {
		return -1
	}
	b := src[q]
	if b < utf8.RuneSelf {
		return rune(b)
	}
	r, _ := utf8.DecodeRuneInString(src[q:])
	return r
}

// sizeAt is the length in bytes of the character at q.
func sizeAt(src string, q int) int {
	if q >= len(src) || src[q] < utf8.RuneSelf {
		return 1
	}
	_, s := utf8.DecodeRuneInString(src[q:])
	return s
}

// spaceAt is the length in bytes of the whitespace character at q, or 0 if there is none.
func spaceAt(src string, q int) int {
	if util.IsSpace(codeAt(src, q)) {
		return sizeAt(src, q)
	}
	return 0
}

func tokenize(src string) common.ScanTokens {
	n := len(src)
	out := common.ScanTokens{}
	p := 0

	emit := func(typ, e int) {
		out.Types = append(out.Types, typ)
		out.Texts = append(out.Texts, src[p:e])
		out.Starts = append(out.Starts, p)
		p = e
	}

	for {
		p = common.SkipTrivia(src, p)
		if p >= n {
			break
		}
		c := codeAt(src, p)
		e := p + 1
		if c == 10 {
			for codeAt(src, e) == 10 {
				e++
			}
			emit(T_NEWLINE, e)
			continue
		}
		if common.KeywordAt(src, p, "timeline") {
			e = p + 8
			for codeAt(src, e) == 32 || codeAt(src, e) == 9 {
				e++
			}
			lr := common.KeywordAt(src, e, "lr")
			if e > p+8 && (lr || common.KeywordAt(src, e, "td")) && !util.IsWord(codeAt(src, e+2)) {
				if lr {
					emit(T_timeline_lr, e+2)
				} else {
					emit(T_timeline_td, e+2)
				}
				continue
			}
			if !util.IsWord(codeAt(src, p+8)) {
				emit(T_timeline, p+8)
				continue
			}
		}
		if common.KeywordAt(src, p, "title") {
			// Unlike Mermaid, ignore bare titles.
			e = p + 5
			for codeAt(src, e) != 10 && util.IsSpace(codeAt(src, e)) {
				e += sizeAt(src, e)
			}
			if e >= n || codeAt(src, e) == 10 {
				p = e
				continue
			}
			if s := spaceAt(src, p+5); s > 0 && codeAt(src, p+5) != 13 {
				emit(T_title, common.LineEnd(src, p+5+s))
				continue
			}
		}
		e = common.Accessibility(src, p, &out, T_acc_title)
		if e != p {
			p = e
			continue
		}
		// Like Mermaid, a bare section uses the next line as its name.
		if s := spaceAt(src, p+7); common.KeywordAt(src, p, "section") && s > 0 {
			from := p + 7 + s
			e = from
			for e < n && src[e] != ':' && src[e] != '\n' {
				e++
			}
			if e > from {
				emit(T_section, e)
				continue
			}
		}
		if c != 58 {
			e = p + sizeAt(src, p)
			for e < n && src[e] != '#' && src[e] != ':' && src[e] != '\n' {
				e++
			}
			emit(T_period, e)
			continue
		}
		// An event runs to the end of the line or to a colon that is followed by whitespace.
		if s := spaceAt(src, p+1); s > 0 {
			from := p + 1 + s
			for e = from; e < n; e++ {
				k := src[e]
				if k == '\n' || (k == ':' && spaceAt(src, e+1) > 0) {
					break
				}
			}
			if e > from {
				emit(T_event, e)
				continue
			}
		}
		emit(T_INVALID, p+1)
	}

	// Jison marks its lexer done when a rule runs on empty input, so an accessibility value that
	// matches nothing at the very end of the text is followed by no EOF token.
	if len(out.Starts) == 0 || out.Starts[len(out.Starts)-1] < n {
		emit(T_EOF, n)
	}
	emit(T_END, n)
	return out
}
