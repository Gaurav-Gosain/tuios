package review

import "strings"

// AnchorWindow is how far from its line a note's quote is looked for first.
const AnchorWindow = 50

// Anchor finds the line a note on line, quoting quote, sits on now in lines
// (numbered from 1). The quote is looked for within AnchorWindow lines of
// line, nearest first and above before below on a tie; then anywhere in the
// file, nearest first. A note with no quote stays on its line while the file
// still has it. ok is false when the line cannot be found: the note is
// outdated.
func Anchor(lines []string, line int, quote string) (int, bool) {
	if quote == "" {
		return line, line >= 1 && line <= len(lines)
	}
	want := quoteKey(quote)
	match := func(n int) bool {
		return n >= 1 && n <= len(lines) && quoteKey(lines[n-1]) == want
	}
	for d := 0; d <= AnchorWindow; d++ {
		if match(line - d) {
			return line - d, true
		}
		if d > 0 && match(line+d) {
			return line + d, true
		}
	}
	best, bestDist := 0, -1
	for n := 1; n <= len(lines); n++ {
		if !match(n) {
			continue
		}
		dist := n - line
		if dist < 0 {
			dist = -dist
		}
		if bestDist < 0 || dist < bestDist {
			best, bestDist = n, dist
		}
	}
	return best, bestDist >= 0
}

// AnchorHunk finds the hunk a note on a whole hunk sits on now: the hunk
// with the same header, else the one whose range on side holds line. ok is
// false when neither is there: the note is outdated.
func AnchorHunk(hunks []Hunk, side, header string, line int) (Hunk, bool) {
	for _, h := range hunks {
		if h.Header == header {
			return h, true
		}
	}
	for _, h := range hunks {
		start, count := h.NewStart, h.NewLines
		if side == SideOld {
			start, count = h.OldStart, h.OldLines
		}
		if line >= start && line < start+max(count, 1) {
			return h, true
		}
	}
	return Hunk{}, false
}

// quoteKey is a line or a quote as Anchor compares them: cleaned the way a
// quote is kept (CleanQuote), then normalized. A quote is stored cleaned and
// cut to TextMax bytes, so a file line is cut the same way before it is
// compared, and a line longer than TextMax, or one holding a control
// character, still finds its note. CleanQuote gives its own output back
// unchanged, so a stored quote keys the same as the line it came from.
func quoteKey(s string) string {
	if len(s) > TextMax || hasControl(s) {
		s = CleanQuote(s)
	}
	return normalizeLine(s)
}

// hasControl reports whether s holds a byte CleanQuote would change other
// than white space: a control character, or anything that is not ASCII, since
// C1 controls and invalid UTF-8 are left out too.
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 0x20 && c != '\t' && c != '\n' && c != '\r') || c >= 0x7f {
			return true
		}
	}
	return false
}

// normalizeLine is a line as quotes are compared: white space at either end
// does not count and a run of it inside counts as one space, so a quote kept
// as one clean line matches its indented line, and a line that was only
// re-indented is still found.
func normalizeLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
