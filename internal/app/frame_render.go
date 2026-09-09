package app

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The composed frame's cells becoming the string bubbletea takes.
//
// ultraviolet's Lines.Render does this generically: per cell it compares the
// cell to the blank cell and its style to the pen through colour-by-RGBA
// equality, and per style change it computes the SGR diff afresh, which
// builds an ansi.Style slice and joins it into a string. Then lipgloss trims
// the result with uv.TrimSpace, which splits the frame into lines, trims each,
// and joins them again: two more copies of the frame. Under a flood the diff's
// allocations were near half of every object the client allocated per frame.
//
// renderFrame writes the same bytes. Each step of Lines.Render is kept in the
// same order with the same conditions, so the test that compares the two over
// random cell buffers is the definition of done here; what changed is only
// how each step is answered. Equality is asked of the structs first and of
// the colours' RGBA only when the structs differ, which is the same answer
// because equal values have equal RGBA. The diff for a pair of styles is
// remembered, since a frame repeats the same few transitions thousands of
// times. And each line is trimmed as it is written, on the bytes it just
// produced, so the frame is built once.

// frameRenderer holds the buffer and memo the frame is rendered with, reused
// across frames.
type frameRenderer struct {
	out   []byte
	diffs map[[2]uv.Style]string
}

// maxDiffMemo bounds the memo. A frame uses a few dozen transitions; a memo
// past this size means the palette is churning and remembering it is not
// paying, so it starts over.
const maxDiffMemo = 4096

// diff is the SGR sequence that takes the pen from one style to the other,
// remembered by the pair.
func (r *frameRenderer) diff(from, to *uv.Style) string {
	key := [2]uv.Style{*from, *to}
	if s, ok := r.diffs[key]; ok {
		return s
	}
	if r.diffs == nil || len(r.diffs) >= maxDiffMemo {
		r.diffs = make(map[[2]uv.Style]string, 64)
	}
	s := uv.StyleDiff(from, to)
	r.diffs[key] = s
	return s
}

// render is uv.TrimSpace(uv.Lines(lines).Render()) byte for byte.
func (r *frameRenderer) render(lines []uv.Line) string {
	out := r.out[:0]
	for i, l := range lines {
		start := len(out)
		out = r.renderLine(out, l)
		out = trimLineRight(out, start)
		if i < len(lines)-1 {
			out = append(out, '\n')
		}
	}
	r.out = out
	return string(out)
}

// trimLineRight is uv.TrimSpace's treatment of one line, applied in place to
// the bytes from start: trailing blanks go, and a carriage return at the very
// end survives them.
func trimLineRight(out []byte, start int) []byte {
	end := len(out)
	hasCR := end > start && out[end-1] == '\r'
	if hasCR {
		end--
	}
	for end > start && out[end-1] == ' ' {
		end--
	}
	if hasCR {
		out[end] = '\r'
		end++
	}
	return out[:end]
}

// styleIsZero is Style.IsZero.
func styleIsZero(s *uv.Style) bool {
	return *s == uv.Style{}
}

// styleEqual is Style.Equal, answered by the structs when they match and by
// the colours' RGBA when they do not.
func styleEqual(a, b *uv.Style) bool {
	return *a == *b || a.Equal(b)
}

// isEmptyCell is Cell.Equal(&EmptyCell): a one-column space with no style and
// no link.
func isEmptyCell(c *uv.Cell) bool {
	return c.Width == 1 && c.Content == " " && styleIsZero(&c.Style) && c.Link == uv.Link{}
}

// renderLine is ultraviolet's renderLine, appending to out.
func (r *frameRenderer) renderLine(out []byte, l uv.Line) []byte {
	var pen uv.Style
	var link uv.Link
	pending := 0

	for i := range l {
		c := &l[i]
		if c.IsZero() {
			continue
		}
		if isEmptyCell(c) {
			if !styleIsZero(&pen) {
				out = append(out, ansi.ResetStyle...)
				pen = uv.Style{}
			}
			if link != (uv.Link{}) {
				out = append(out, resetHyperlink...)
				link = uv.Link{}
			}
			pending++
			continue
		}

		for ; pending > 0; pending-- {
			out = append(out, ' ')
		}

		if styleIsZero(&c.Style) && !styleIsZero(&pen) {
			out = append(out, ansi.ResetStyle...)
			pen = uv.Style{}
		}
		if !styleEqual(&c.Style, &pen) {
			out = append(out, r.diff(&pen, &c.Style)...)
			pen = c.Style
		}

		if c.Link != link && link.URL != "" {
			out = append(out, resetHyperlink...)
			link = uv.Link{}
		}
		if c.Link != link {
			out = append(out, ansi.SetHyperlink(c.Link.URL, c.Link.Params)...)
			link = c.Link
		}

		out = append(out, c.Content...)
	}

	for ; pending > 0; pending-- {
		out = append(out, ' ')
	}
	if link.URL != "" {
		out = append(out, resetHyperlink...)
	}
	if !styleIsZero(&pen) {
		out = append(out, ansi.ResetStyle...)
	}
	return out
}

// resetHyperlink is ansi.ResetHyperlink(), which builds the same string on
// every call.
var resetHyperlink = ansi.ResetHyperlink()
