package app

import "strings"

// Bare URLs in a terminal are text, not markup. OSC 8 is the spec for a real
// hyperlink and it is what the emulator records per cell, but almost nothing a
// person reads in a pane went through it: a URL in a log line, a git remote, a
// stack trace or a chat message is plain characters that no program ever marked
// up. A link feature that only sees OSC 8 therefore looks broken, because the
// links the user can see are the ones it cannot.
//
// So this file finds the other kind. The rules are deliberately narrow, because
// a false positive here is worse than a miss: it underlines ordinary prose and
// then offers to open it.
//
//   - Only the schemes below start a match. No bare host names, no "www.", no
//     guessing at "example.com" in the middle of a sentence.
//   - A scheme must be followed by at least one more character, so the word
//     "https" alone, or "http://" at the end of a line, is not a link.
//   - The match ends at the first character a pasted URL never contains: a
//     space, a control character, a quote, an angle bracket, a backtick.
//   - Trailing punctuation is then given back to the sentence, and brackets are
//     balanced, so "see https://example.com/a." and "(https://example.com/b)"
//     both end where a reader would say they end.
//
// One row is scanned at a time, and only when the pointer moves to a new cell,
// so the cost is a few hundred byte comparisons per mouse move and nothing at
// all per frame. Nothing in this file runs during a render.

// linkSchemes are the prefixes a bare match may start with. http and https are
// what people paste; file is what a shell prints for a local path and what OSC 8
// carries for the same thing. Every other scheme (mailto, ssh, an application's
// own) is left to OSC 8, where the program has said outright that it meant a
// link.
var linkSchemes = []string{"https://", "http://", "file://"}

// linkTerminator reports whether c ends a bare URL. RFC 3986 allows more than
// this, but a terminal line is prose as often as it is data, and stopping at the
// characters that never appear inside a pasted URL is what keeps a sentence from
// being swallowed whole.
func linkTerminator(c byte) bool {
	switch c {
	case ' ', '\t', '"', '\'', '<', '>', '`', '|', '^', '{', '}', '\\':
		return true
	}
	return c < 0x20 || c == 0x7f
}

// linkTrailing are the characters trimmed from the end of a match, because at
// the end of a line of text they are far more likely to be the sentence's than
// the URL's. A URL may legally end in any of them; one that does loses its last
// character here, which is the trade this makes on purpose.
const linkTrailing = ".,;:!?'\""

// linkWordByte reports whether c may not sit immediately before a scheme. It
// stops "xhttps://a" and "foo.http://b" from reading as links with a stray
// character in front.
func linkWordByte(c byte) bool {
	return c == '_' || c == '-' || c == '.' || c == '/' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ScanBareURL returns the byte range of the bare URL covering col in s, or
// ok=false when col is not inside one. col is a byte offset into s.
//
// s is one row's plain text. The caller maps cell columns to byte offsets,
// because a row may hold wide glyphs and combining marks and this function has
// no business knowing about either.
func ScanBareURL(s string, col int) (start, end int, ok bool) {
	if col < 0 || col >= len(s) {
		return 0, 0, false
	}
	for i := 0; i <= col; i++ {
		schLen := schemeAt(s, i)
		if schLen == 0 {
			continue
		}
		e := bareURLEnd(s, i, schLen)
		if e <= i+schLen {
			// A scheme with nothing after it is the word, not a link.
			continue
		}
		if col < e {
			return i, e, true
		}
		// The pointer is past this match, so keep looking: one row may hold
		// more than one URL.
		i = e - 1
	}
	return 0, 0, false
}

// schemeAt returns the length of the scheme starting at i, or 0 when no scheme
// starts there or when the byte before it makes this the middle of a word.
func schemeAt(s string, i int) int {
	if i > 0 && linkWordByte(s[i-1]) {
		return 0
	}
	for _, sch := range linkSchemes {
		if strings.HasPrefix(s[i:], sch) {
			return len(sch)
		}
	}
	return 0
}

// bareURLEnd finds where the match starting at i, whose scheme is schLen bytes
// long, ends: first at the terminating character, then after trailing sentence
// punctuation is handed back and unopened brackets are dropped.
func bareURLEnd(s string, i, schLen int) int {
	e := i + schLen
	for e < len(s) && !linkTerminator(s[e]) {
		e++
	}
	// Trim repeatedly: "example.com/a)." loses the stop and then the bracket.
	for e > i+schLen {
		last := s[e-1]
		if strings.IndexByte(linkTrailing, last) >= 0 {
			e--
			continue
		}
		// A closing bracket that was never opened inside the URL belongs to the
		// text around it. A matched one is kept, which is what lets a link with
		// a "(disambiguation)" suffix survive.
		if opener := bracketOpener(last); opener != 0 && !bracketsBalanced(s[i:e], opener, last) {
			e--
			continue
		}
		break
	}
	return e
}

// bracketOpener returns the opening bracket matching c, or 0 when c is not a
// closing bracket.
func bracketOpener(c byte) byte {
	switch c {
	case ')':
		return '('
	case ']':
		return '['
	}
	return 0
}

// bracketsBalanced reports whether sub holds at least as many openers as
// closers, which is the test for whether the closer at its end was opened
// inside the URL.
func bracketsBalanced(sub string, open, closed byte) bool {
	depth := 0
	for i := range len(sub) {
		switch sub[i] {
		case open:
			depth++
		case closed:
			depth--
		}
	}
	return depth >= 0
}
