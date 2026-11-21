package app

import (
	"image"
	"image/color"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

func (m *OS) applyDropZone(canvas *frameCanvas, id string) {
	held := m.LiveWindowDrag()
	if !m.dropZoneShown || m.dropZone == nil || held == nil || held.ID != id {
		return
	}
	land := m.dropZone.Land
	rect := image.Rect(land.X, land.Y, land.X+land.W, land.Y+land.H)
	area := canvas.Bounds()
	if v := m.sessionView; v.on {
		rect = rect.Add(image.Pt(v.dx, v.dy))
		area = area.Intersect(v.clip)
	}
	clip := rect.Intersect(area)
	if clip.Empty() {
		return
	}
	if theme.Depth() == overlay.Depth16 {
		drawDropRingReverse(canvas, rect, clip)
		return
	}
	look := m.dropZoneLook()
	fill := m.Settings.DropPreview != config.DropPreviewOutline
	border := dropRingBorder(&m.Settings)
	ring := uv.Cell{Width: 1, Style: uv.Style{Fg: look.ring, Bg: look.ringBg}}
	if fill {
		ring.Style.Bg = look.tint
	}
	for y := clip.Min.Y; y < clip.Max.Y && y < len(canvas.Lines); y++ {
		row := canvas.Lines[y]
		for x := clip.Min.X; x < clip.Max.X && x < len(row); x++ {
			if g := dropRingGlyph(&border, rect, x, y); g != "" {
				ring.Content = g
				ring.Link = uv.Link{}
				row.Set(x, &ring)
				continue
			}
			if fill {
				m.washDropCell(&row[x], look)
			}
		}
	}
}

func drawDropRingReverse(canvas *frameCanvas, rect, clip image.Rectangle) {
	for y := clip.Min.Y; y < clip.Max.Y && y < len(canvas.Lines); y++ {
		row := canvas.Lines[y]
		for x := clip.Min.X; x < clip.Max.X && x < len(row); x++ {
			if x == rect.Min.X || x == rect.Max.X-1 || y == rect.Min.Y || y == rect.Max.Y-1 {
				st := &row[x].Style
				st.Fg, st.Bg = nil, nil
				st.Attrs |= uv.AttrReverse
			}
		}
	}
}

func dropRingBorder(s *config.Settings) lipgloss.Border {
	b := s.GetBorderForStyle()
	if b.Top == "" || b.Top == " " || b.Left == "" || b.Left == " " {
		if s.UseASCIIOnly {
			return lipgloss.ASCIIBorder()
		}
		return lipgloss.RoundedBorder()
	}
	return b
}

func dropRingGlyph(b *lipgloss.Border, rect image.Rectangle, x, y int) string {
	left, right := x == rect.Min.X, x == rect.Max.X-1
	top, bottom := y == rect.Min.Y, y == rect.Max.Y-1
	switch {
	case !left && !right && !top && !bottom:
		return ""
	case rect.Dx() == 1:
		return b.Left
	case rect.Dy() == 1:
		return b.Top
	case top && left:
		return b.TopLeft
	case top && right:
		return b.TopRight
	case bottom && left:
		return b.BottomLeft
	case bottom && right:
		return b.BottomRight
	case top:
		return b.Top
	case bottom:
		return b.Bottom
	}
	if left {
		return b.Left
	}
	return b.Right
}

type dropLook struct {
	key                     [6]uint32
	tint, ink, ring, ringBg color.Color
	fg, bg                  color.Color
	wash                    [dropWashSize]dropWashEntry
}

type dropWashEntry struct {
	key uint64
	out color.Color
}

const (
	dropWashBits   = 6
	dropWashSize   = 1 << dropWashBits
	dropWashProbes = 4
)

const dropWashMix = 0.85

func (m *OS) dropZoneLook() *dropLook {
	pal := theme.UI()
	fg, bg := pal.Fg, pal.Surface
	if theme.CurrentThemeID() != "" {
		if f, b := theme.TerminalFg(), theme.TerminalBg(); !isNilColor(f) && !isNilColor(b) {
			fg, bg = f, b
		}
	}
	var ground color.Color
	if g := m.paneGround(); g.on() {
		ground, bg = g.bg, g.bg
		if !isNilColor(g.fg) {
			fg = g.fg
		}
	}
	key := [6]uint32{
		1<<31 | packColor8(pal.AccentTint), packColor8(pal.Accent),
		packColor8(fg), packColor8(bg), packColor8(ground), packColor8(pal.FgMute),
	}
	if l := &m.dropLook; l.key != key || isNilColor(l.tint) {
		*l = dropLook{key: key, tint: pal.AccentTint, fg: fg, bg: bg, ringBg: ground}
		l.ink = theme.ReadableAt(pal.FgMute, pal.AccentTint, theme.MarkFloor)
		ink := theme.ReadableAt(pal.Accent, pal.AccentTint, theme.MarkFloor)
		l.ring = theme.ReadableAt(ink, bg, theme.MarkFloor)
	}
	return &m.dropLook
}

func (m *OS) washDropCell(c *uv.Cell, look *dropLook) {
	st := &c.Style
	if vt.IsKittyPlaceholder(c.Content) {
		st.Bg = look.tint
		return
	}
	if solidGlyph(c.Content) {
		st.Fg, st.Bg = look.tint, look.tint
		st.UnderlineColor = nil
		st.Attrs &^= uv.AttrReverse | uv.AttrFaint | uv.AttrBlink | uv.AttrRapidBlink | uv.AttrConceal
		return
	}
	src := st.Fg
	if st.Attrs&uv.AttrReverse != 0 {
		src = st.Bg
		if isNilColor(src) {
			src = look.bg
		}
	} else if isNilColor(src) {
		src = look.fg
	}
	st.Fg = m.dropWash(src, look)
	st.Bg = look.tint
	st.UnderlineColor = nil
	st.Attrs &^= uv.AttrReverse | uv.AttrFaint | uv.AttrBlink | uv.AttrRapidBlink | uv.AttrConceal
}

func (m *OS) dropWash(c color.Color, look *dropLook) color.Color {
	key := 1<<63 | uint64(packColor8(c))
	home := (key * 0x9e3779b97f4a7c15) >> (64 - dropWashBits)
	slot := home
	for p := range uint64(dropWashProbes) {
		e := &look.wash[(home+p)%dropWashSize]
		if e.key == key {
			return e.out
		}
		if e.key == 0 {
			slot = (home + p) % dropWashSize
			break
		}
	}
	out := theme.ReadableAt(overlay.MixColors(c, look.ink, dropWashMix), look.tint, theme.MarkFloor)
	look.wash[slot] = dropWashEntry{key: key, out: out}
	return out
}

func solidGlyph(s string) bool {
	if len(s) < 3 || s[0] < 0xe2 {
		return false
	}
	switch s[0] {
	case 0xe2:
		return s[1] == 0x96 && s[2] >= 0x80 && s[2] <= 0x9f
	case 0xee:
		return (s[1] == 0x82 && s[2] >= 0xb0) || (s[1] == 0x83 && s[2] <= 0x97)
	}
	return false
}
