package composer

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// frameKey is everything a frame depends on. Render keeps the last frame and
// its key, so a frame with nothing changed since the last one costs a
// comparison and no drawing. The host draws the editor every frame, and most
// frames come from something else on screen.
type frameKey struct {
	ver      uint64
	cur, top int
	width    int
	notice   string
	pager    *pager
	pagerTop int
	ascii    bool
	preview  PreviewMode
	previewV uint64
	// The @ list: open, its selection, and the completer's version.
	mentionAt, mentionSel int
	mentionV              uint64
	depth                 overlay.Depth
	colours               [10][4]uint32
}

// frameKey is the key of a frame drawn now at width.
func (m *Model) frameKey(width int, pal overlay.Palette, surface color.Color) frameKey {
	k := frameKey{
		ver: m.ver, cur: m.cur, top: m.top, width: width, notice: m.notice,
		pager: m.pager, ascii: overlay.UseASCII(), depth: pal.Depth,
	}
	if m.pager != nil {
		k.pagerTop = m.pager.top
	}
	if m.preview != nil {
		k.preview, k.previewV = m.preview.Mode(), m.preview.Version()
	}
	k.mentionAt = -1
	if m.mention != nil {
		k.mentionAt, k.mentionSel = m.mention.at, m.mention.sel
		k.mentionV = m.completer.Version()
	}
	// Colours are compared by value: a palette's fields are interfaces, and
	// comparing them with == can panic on a type that is not comparable.
	for i, c := range [...]color.Color{surface, pal.Card, pal.Fg, pal.FgMute, pal.FgDim, pal.AccentBright, pal.Accent, pal.Warning, pal.RowSel, pal.RowSelQuiet} {
		k.colours[i] = rgba(c)
	}
	return k
}

// rgba is c's channels, zero for no colour.
func rgba(c color.Color) [4]uint32 {
	if c == nil {
		return [4]uint32{}
	}
	r, g, b, a := c.RGBA()
	// NoColor and a transparent black both read as zero; the alpha bit
	// tells a real colour from none.
	if !overlay.IsNoColor(c) {
		a |= 1 << 31
	}
	return [4]uint32{r, g, b, a}
}
