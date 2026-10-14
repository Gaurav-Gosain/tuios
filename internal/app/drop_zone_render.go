package app

import (
	"image"
	"image/color"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// applyDropZone shows where the held pane would land if the button came up
// now: the whole target for a swap, the half an edge drop gives it. It is a
// pass over the canvas right after the held pane's own layer, so it reads over
// that pane and the target alike and under any chrome drawn above them.
//
// The preview is drawn as the pane it becomes: a frame in the panes' own
// border glyphs, in the accent. With appearance.drop_preview = "fill" the
// inside is washed as well: one tinted ground, and every cell's text carried
// toward it, so what is under the zone reads as behind it rather than poking
// through it in its own colours. At 16 colours, where the tint is no colour at
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
	key [5]uint32
	// tint is the ground of the filled zone. ring is the frame's ink, ringBg
	// its ground when the zone is not filled: the pane's painted ground, or
	// nil for the terminal's own.
	tint, ring, ringBg color.Color
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

// dropWashMix is how far a cell's text is carried toward the tint. Enough that
// a prompt's segments and an ls listing's colours fall back to one family,
// while the words keep enough of their own colour to be told apart.
const dropWashMix = 0.6

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
	key := [5]uint32{
		1<<31 | packColor8(pal.AccentTint), packColor8(pal.Accent),
		packColor8(fg), packColor8(bg), packColor8(ground),
	}
	if l := &m.dropLook; l.key != key || isNilColor(l.tint) {
		*l = dropLook{key: key, tint: pal.AccentTint, fg: fg, bg: bg, ringBg: ground}
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
// tint and then held to the mark floor on it. The attributes that would bring
// the text forward again or hide it are dropped with the colours.
func (m *OS) washDropCell(c *uv.Cell, look *dropLook) {
	st := &c.Style
	// A kitty placeholder's colour is the image's id, not a colour. See
	// dimCell.
	if vt.IsKittyPlaceholder(c.Content) {
		st.Bg = look.tint
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
	out := theme.ReadableAt(overlay.MixColors(c, look.tint, dropWashMix), look.tint, theme.MarkFloor)
	look.wash[slot] = dropWashEntry{key: key, out: out}
	return out
}
