package overlay

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// A framed overlay is filled with the host's pushed ground (see FrameColors).
// Its body is drawn by the host on the palette's surface before the overlay
// sees it, so the overlay moves the whole rendered string onto the ground at
// the end: every cell whose background is the surface takes the ground
// instead. Cells on any other background keep it, which is what keeps a
// selected or hovered row, an input field and a pill their own colours.

// onGround returns s with every background of colour from set to to instead, a
// nil to being the terminal's default background. Only the SGR sequences in s
// change.
func onGround(s string, from, to color.Color) string {
	want := bgParams(from)
	if want == "" || want == bgParams(to) || !strings.Contains(s, want) {
		return s
	}
	repl := bgParams(to)
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := i + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j >= len(s) {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+2])
		if s[j] == 'm' {
			b.WriteString(swapBg(s[i+2:j], want, repl))
		} else {
			b.WriteString(s[i+2 : j])
		}
		b.WriteByte(s[j])
		s = s[j+1:]
	}
}

// bgParams is the SGR parameters that set c as the background, as lipgloss
// writes them: "49" for nil.
func bgParams(c color.Color) string {
	seq := ansi.Style{}.BackgroundColor(c).String()
	return strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m")
}

// swapBg replaces the background want with repl in one SGR parameter list. An
// extended colour is read whole, so the digits of a foreground cannot be
// mistaken for a background.
func swapBg(params, want, repl string) string {
	toks := strings.Split(params, ";")
	out := make([]string, 0, len(toks))
	for i := 0; i < len(toks); {
		n := 1
		if t := toks[i]; (t == "38" || t == "48" || t == "58") && i+1 < len(toks) {
			switch toks[i+1] {
			case "5":
				n = 3
			case "2":
				n = 5
			}
		}
		n = min(n, len(toks)-i)
		run := strings.Join(toks[i:i+n], ";")
		if run == want {
			run = repl
		}
		out = append(out, run)
		i += n
	}
	return strings.Join(out, ";")
}

// MeasuredOn returns p with its text inks lifted, where they miss their floor,
// to read on ground as well: the ground a framed overlay is filled with. The
// palette's own grounds are measured again after it, so an ink still clears
// them wherever the two pull apart.
func (p Palette) MeasuredOn(ground color.Color) Palette {
	if ground == nil || p.Depth == Depth16 {
		return p
	}
	p.Fg = ReadableAt(p.Fg, ground, ContrastFloor)
	p.FgDim = ReadableAt(p.FgDim, ground, ContrastFloor)
	p.FgMute = ReadableAt(p.FgMute, ground, MarkFloor)
	p.checkContrast()
	return p
}
