package app

import (
	"image"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
)

// applyDropZone shades where the held pane would land if the button came up
// now: the whole target for a swap, the half an edge drop gives it. It is a
// pass over the canvas right after the held pane's own layer, so it reads over
// that pane and the target alike and under any chrome drawn above them.
//
// The cells keep their text. The rectangle is outlined in the accent and
// filled with its tint; at 16 colours, where the tint is no colour at all, the
// outline is reverse video and the inside is left alone.
func (m *OS) applyDropZone(canvas *frameCanvas, id string) {
	held := m.LiveWindowDrag()
	if m.dropZone == nil || held == nil || held.ID != id {
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
	pal := theme.UI()
	reverse := theme.Depth() == overlay.Depth16
	for y := clip.Min.Y; y < clip.Max.Y && y < len(canvas.Lines); y++ {
		row := canvas.Lines[y]
		for x := clip.Min.X; x < clip.Max.X && x < len(row); x++ {
			ring := x == rect.Min.X || x == rect.Max.X-1 || y == rect.Min.Y || y == rect.Max.Y-1
			st := &row[x].Style
			switch {
			case ring && reverse:
				st.Fg, st.Bg = nil, nil
				st.Attrs |= uv.AttrReverse
			case ring:
				st.Fg, st.Bg = pal.PillFg, pal.Accent
			case !reverse:
				st.Bg = pal.AccentTint
			}
		}
	}
}
