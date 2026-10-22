// Ported from pele src/diagrams/journey/lexer.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
//
// The port reads UTF-8 bytes. Where pele steps one unit past a character, the port
// steps the whole character.

package journey

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
	T_journey
	T_title
	T_acc_title
	T_acc_title_value
	T_acc_descr
	T_acc_descr_value
	T_acc_descr_multiline_value
	T_section
	T_taskName
	T_taskData
	T_colon
)

var TOKEN_NAMES = [...]string{
	"$end",
	"EOF",
	"INVALID",
	"NEWLINE",
	"journey",
	"title",
	"acc_title",
	"acc_title_value",
	"acc_descr",
	"acc_descr_value",
	"acc_descr_multiline_value",
	"section",
	"taskName",
	"taskData",
	":",
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

// textEnd: `#`, a line break, and `;` end every kind of text; a colon also ends a task name or a section.
func textEnd(src string, from int, colon bool) int {
	n := len(src)
	e := from
	for ; e < n; e++ {
		c := src[e]
		if c == '#' || c == '\n' || c == ';' || (colon && c == ':') {
			break
		}
	}
	return e
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
		if common.KeywordAt(src, p, "journey") && !util.IsWord(codeAt(src, p+7)) {
			emit(T_journey, p+7)
			continue
		}
		// `\s` after a keyword also takes a line break, so `title` alone on a line takes the next line as its text.
		if s := spaceAt(src, p+5); common.KeywordAt(src, p, "title") && s > 0 {
			e = textEnd(src, p+5+s, false)
			if e > p+5+s {
				emit(T_title, e)
				continue
			}
		}
		e = common.Accessibility(src, p, &out, T_acc_title)
		if e != p {
			p = e
			continue
		}
		if s := spaceAt(src, p+7); common.KeywordAt(src, p, "section") && s > 0 {
			e = textEnd(src, p+7+s, true)
			if e > p+7+s {
				emit(T_section, e)
				continue
			}
		}
		if c == 58 {
			e = textEnd(src, p+1, false)
			if e > p+1 {
				emit(T_taskData, e)
			} else {
				emit(T_colon, p+1)
			}
		} else if c == 59 {
			emit(T_INVALID, p+1)
		} else {
			emit(T_taskName, textEnd(src, p+sizeAt(src, p), true))
		}
	}

	// Jison marks its lexer done when a rule runs on empty input, so an accessibility value that
	// matches nothing at the very end of the text is followed by no EOF token.
	if len(out.Starts) == 0 || out.Starts[len(out.Starts)-1] < n {
		emit(T_EOF, n)
	}
	emit(T_END, n)
	return out
}
