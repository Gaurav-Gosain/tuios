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

// applyDropZone shows where the held pane would land if the button came up
// now: the whole target for a swap, the half an edge drop gives it. It is a
// pass over the canvas right after the held pane's own layer, so it reads over
// that pane and the target alike and under any chrome drawn above them.
//
// The preview is drawn as the pane it becomes: a frame in the panes' own
// border glyphs, in the accent. With appearance.drop_preview = "fill" the
// inside is washed as well: one tinted ground, and every cell's text carried
// most of the way to one muted ink, so what is under the zone reads as behind
// it rather than poking through it in its own colours. Glyphs that are drawn as
// solid shapes (powerline caps and arrows, block elements) are drawn in the
// tint itself, since a shape in any ink reads as a coloured blob on the wash. At 16 colours, where the tint is no colour at
// all, the frame is reverse video and the inside is left alone.
func (m *OS) applyDropZone(canvas *frameCanvas, id string) {
	held := m.LiveWindowDrag()
	if !m.dropZoneShown || m.dropZone == nil || held == nil || held.ID != id {
		return
	}
	land := m.dropZone.Land
	rect := image.Rect(land.X, land.Y, land.X+land.W, land.Y+land.H)
	area := canvas.Bounds()
	// In a view of a larger session the panes are drawn shifted and clipped,
	// and the highlight goes with them. See pane_view.go.
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
				// Set rather than a write in place: it blanks the other half
				// of a wide character the frame cuts through, which would
				// otherwise be left as half a glyph.
				row.Set(x, &ring)
				continue
			}
			if fill {
				m.washDropCell(&row[x], look)
			}
		}
	}
}

// drawDropRingReverse is the frame at 16 colours: the zone's edge cells in
// reverse video, their text kept. The inside is left alone, since the tint is
// not a colour at this depth and any slot would fight the pane's own text.
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

// dropRingBorder is the border the frame is drawn in: the one the panes use,
// so the preview looks like the frame the pane will get. A hidden border draws
// no frame at all, so the preview takes the rounded one (or ASCII in ASCII
// mode) instead of vanishing.
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

// dropRingGlyph is the border glyph the frame draws at (x, y), or "" for a
// cell inside it. A zone one cell wide is a vertical rule and one a single row
// high a horizontal rule: a corner there would close a box with no inside.
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

// dropLook is the preview's colours for one palette and pane ground. They are
// kept on the OS and worked out again only when what they are made from
// changes, so a drag's frames measure nothing and box no colour.
type dropLook struct {
	key [6]uint32
	// tint is the ground of the filled zone. ink is the muted text colour
	// the wash carries every cell's text toward. ring is the frame's ink, ringBg
	// its ground when the zone is not filled: the pane's painted ground, or
	// nil for the terminal's own.
	tint, ink, ring, ringBg color.Color
	// fg and bg stand in for a cell that names no colour of its own.
	fg, bg color.Color
	wash   [dropWashSize]dropWashEntry
}

type dropWashEntry struct {
	key uint64
	out color.Color
}

const (
	dropWashBits = 6
	dropWashSize = 1 << dropWashBits
	// dropWashProbes is how many slots a lookup tries.
	dropWashProbes = 4
)

// dropWashMix is how far a cell's text is carried toward the muted ink. Far
// enough that a prompt's segments, an icon and an ls listing's colours all read
// as the one ink, with a trace of their hue left so the words stay apart.
const dropWashMix = 0.85

// dropZoneLook returns the preview's colours for this frame.
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
		// The text under the wash is held to the mark floor, not the text
		// floor: it is there to be recognised, not read.
		l.ink = theme.ReadableAt(pal.FgMute, pal.AccentTint, theme.MarkFloor)
		// The frame is a mark, held to the mark floor on the ground it is
		// drawn on: the tint when the zone is filled, the pane's ground when
		// it is not. One ink serves both, so it is measured on the harder.
		ink := theme.ReadableAt(pal.Accent, pal.AccentTint, theme.MarkFloor)
		l.ring = theme.ReadableAt(ink, bg, theme.MarkFloor)
	}
	return &m.dropLook
}

// washDropCell puts one cell inside the zone behind the preview: its ground
// becomes the tint, whatever ground it had, and its text is carried toward the
// muted ink and held to the mark floor on the tint. A solid-shape glyph takes
// the tint as its ink and vanishes into the wash. The attributes that would bring
// the text forward again or hide it are dropped with the colours.
func (m *OS) washDropCell(c *uv.Cell, look *dropLook) {
	st := &c.Style
	// A kitty placeholder's colour is the image's id, not a colour. See
	// dimCell.
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

// dropWash is a cell's text colour washed into the tint, behind a memo on the
// OS: a pane's text repeats a handful of colours, so a drag measures each once,
// and a hit hands back the colour already boxed. The memo probes a few slots
// from the hashed one, so two colours that hash alike do not evict each other
// on every other cell.
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

// solidGlyph reports whether a cell's glyph is drawn as a filled shape rather
// than as a letter: the powerline separators and caps a prompt draws its
// segments with (U+E0B0 to U+E0D7), and the block elements (U+2580 to
// U+259F). Both are three bytes in UTF-8, and a check on the lead bytes keeps
// the common case, a one-byte letter, to one comparison.
func solidGlyph(s string) bool {
	if len(s) < 3 || s[0] < 0xe2 {
		return false
	}
	switch s[0] {
	case 0xe2: // U+2580..U+259F is E2 96 80..E2 96 9F
		return s[1] == 0x96 && s[2] >= 0x80 && s[2] <= 0x9f
	case 0xee: // U+E0B0..U+E0D7 is EE 82 B0..EE 83 97
		return (s[1] == 0x82 && s[2] >= 0xb0) || (s[1] == 0x83 && s[2] <= 0x97)
	}
	return false
}
