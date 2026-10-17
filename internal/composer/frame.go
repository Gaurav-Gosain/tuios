package composer

import (
	"image/color"
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
	// The ctrl+r search: open, its query and its selection.
	hopen   bool
	hsearch string
	hsel    int
	plain   bool
	sigil   string
	colours [9][4]uint32
}

// frameKey is the key of a frame drawn now at width.
//
// The functions of st are not in the key. A host that changes what they do
// changes a colour or a flag of st with them.
func (m *Model) frameKey(width int, st *Styles, surface color.Color) frameKey {
	k := frameKey{
		ver: m.ver, cur: m.cur, top: m.top, width: width, notice: m.notice,
		pager: m.pager, ascii: st.ASCII, plain: st.Plain, sigil: st.Sigil,
	}
	if m.pager != nil {
		k.pagerTop = m.pager.top
	}
	if m.preview != nil {
		k.preview, k.previewV = m.preview.Mode(), m.preview.Version()
	}
	if m.hsearch != nil {
		k.hsearch, k.hsel = m.hsearch.query, m.hsearch.sel
		k.hopen = true
	}
	k.mentionAt = -1
	if m.mention != nil {
		k.mentionAt, k.mentionSel = m.mention.at, m.mention.sel
		k.mentionV = m.completer.Version()
	}
	// Colours are compared by value: they are interfaces, and comparing them
	// with == can panic on a type that is not comparable.
	for i, c := range [...]color.Color{surface, st.Field, st.Text, st.Muted, st.Dim, st.Key, st.Accent, st.Warning, st.Selected} {
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
	if !noColor(c) {
		a |= 1 << 31
	}
	return [4]uint32{r, g, b, a}
}
