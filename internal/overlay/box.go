package overlay

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Box is a framed region inside a panel: the Dialog's rounded hairline, with
// its title set into the top border, drawn on the panel's own ground. A panel
// that shows more than one thing at once (a search line, a list and a
// preview) frames each one in a Box, so every region says what it is without
// a heading row of its own.
//
// Width is the inner width between the two border cells, so the box is
// Width+2 cells across and len(Lines)+2 rows tall. Lines are drawn as they
// are, padded to Width on bg and cut at it: a list row's highlight runs the
// whole inner width, so the box adds no padding of its own.
type Box struct {
	Title string
	// Note is set into the top border at the right: a count or a position.
	Note string
	// Footer is set into the bottom border at the right: what the region is
	// doing, such as a load that is still out.
	Footer string
	Width  int
	Lines  []string
	// Active says the region has the keyboard. Its frame and title take the
	// accent, so of the boxes on screen one says where a key goes.
	Active bool
}

// Render draws the box on bg and returns its rows.
func (b Box) Render(bg color.Color, pal Palette) []string {
	w := max(b.Width, 2)
	tl, tr, bl, br, h, v := dialogFrame()

	edgeInk, titleInk := pal.Edge, pal.FgDim
	if b.Active {
		edgeInk, titleInk = Readable(pal.Accent, bg), Readable(pal.Accent, bg)
	}
	edge := Style(bg).Foreground(edgeInk)
	rule := func(n int) string { return edge.Render(strings.Repeat(h, max(n, 0))) }
	pad := Style(bg).Render(" ")
	note := Style(bg).Foreground(pal.FgMute)

	// border sets a left label and a right label into one horizontal edge.
	// The right label gives way first: the title is what names the region.
	border := func(left, right string, lc, rc string) string {
		lw, rw := lipgloss.Width(left), lipgloss.Width(right)
		if lw > 0 && lw+4 > w {
			left = ansi.Truncate(left, max(w-4, 1), Ellipsis())
			lw = lipgloss.Width(left)
		}
		used := 0
		if lw > 0 {
			used = 1 + lw + 2
		}
		if rw > 0 && used+rw+3 > w {
			right, rw = "", 0
		}
		var s strings.Builder
		s.WriteString(edge.Render(lc))
		if lw > 0 {
			s.WriteString(rule(1) + pad + left + pad)
		}
		fill := w - used
		if rw > 0 {
			fill -= rw + 3
		}
		s.WriteString(rule(fill))
		if rw > 0 {
			s.WriteString(pad + right + pad + rule(1))
		}
		s.WriteString(edge.Render(rc))
		return s.String()
	}

	title := ""
	if b.Title != "" {
		title = Style(bg).Foreground(titleInk).Bold(true).Render(b.Title)
	}
	right := ""
	if b.Note != "" {
		right = note.Render(b.Note)
	}
	foot := ""
	if b.Footer != "" {
		foot = note.Italic(true).Render(b.Footer)
	}

	out := make([]string, 0, len(b.Lines)+2)
	out = append(out, border(title, right, tl, tr))
	side := edge.Render(v)
	for _, l := range b.Lines {
		if lipgloss.Width(l) > w {
			l = ansi.Truncate(l, w, "")
		}
		out = append(out, side+Fill(l, w, bg)+side)
	}
	out = append(out, border("", foot, bl, br))
	return out
}
