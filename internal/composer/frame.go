package composer

import (
	"image/color"
)

type frameKey struct {
	ver                   uint64
	cur, top              int
	anchor                int
	width                 int
	notice                string
	info                  string
	pager                 *pager
	pagerTop              int
	ascii                 bool
	preview               PreviewMode
	previewV              uint64
	mentionAt, mentionSel int
	mentionV              uint64
	hopen                 bool
	hsearch               string
	hsel                  int
	plain                 bool
	sigil                 string
	colours               [10][4]uint32
	maxRows, room         int
	full                  bool
}

func (m *Model) frameKey(width int, st *Styles, surface color.Color) frameKey {
	k := frameKey{
		ver: m.ver, cur: m.cur, top: m.top, anchor: m.anchor, width: width, notice: m.notice, info: m.info,
		pager: m.pager, ascii: st.ASCII, plain: st.Plain, sigil: st.Sigil,
		maxRows: m.opt.MaxRows, room: m.room, full: m.full,
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
	for i, c := range [...]color.Color{surface, st.Field, st.Text, st.Muted, st.Dim, st.Key, st.Accent, st.Warning, st.Selected, st.Selection} {
		k.colours[i] = rgba(c)
	}
	return k
}

func rgba(c color.Color) [4]uint32 {
	if c == nil {
		return [4]uint32{}
	}
	r, g, b, a := c.RGBA()
	if !noColor(c) {
		a |= 1 << 31
	}
	return [4]uint32{r, g, b, a}
}
