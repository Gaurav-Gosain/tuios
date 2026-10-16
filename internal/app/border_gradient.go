package app

import (
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The focused pane's border as a static gradient
// (appearance.border_focused_gradient). The frame is drawn in one colour as
// before, and a pass over the canvas, right after the focused pane's layer is
// drawn, recolours the border cells that carry that colour. Cells in another
// colour, the title badge's ink and the window buttons, keep theirs.
//
// The colours step: a cell takes one of gradientLevels colours, so a run of
// border cells shares one SGR and a full redraw over SSH grows by a few
// sequences per edge, not one per cell. The levels are blended once per
// change of the stops, the theme or the depth. At 16 colours there is no
// gradient: the terminal's own slots have no blend.

// gradientLevels is how many colours the gradient steps through.
const gradientLevels = 8

// borderGradient is the gradient's state on this client.
type borderGradient struct {
	// key is what levels were blended from.
	setting string
	gen     uint64
	depth   overlay.Depth
	ok      bool
	levels  [gradientLevels]color.Color
	// target is the colour the focused border was drawn in on this frame,
	// and rect the cells its frame runs on, in screen cells. id is the layer
	// that draws it. Set by the render pass, read by the compose pass.
	target color.Color
	rect   image.Rectangle
	id     string
	on     bool
}

// gradientOn reports whether the focused border draws a gradient on this
// frame: one is configured and the depth can show one.
func (m *OS) gradientOn() bool {
	if m.Settings.BorderFocusedGradient == "" || theme.Depth() == overlay.Depth16 {
		return false
	}
	return m.gradientLevels()
}

// gradientLevels blends the levels when an input changed, and reports whether
// there are levels: two stops or more, every one a colour.
func (m *OS) gradientLevels() bool {
	g := &m.gradient
	setting, gen, depth := m.Settings.BorderFocusedGradient, theme.Generation(), theme.Depth()
	if g.setting == setting && g.gen == gen && g.depth == depth {
		return g.ok
	}
	g.setting, g.gen, g.depth, g.ok = setting, gen, depth, false
	values := config.GradientStops(setting)
	if len(values) < 2 {
		return false
	}
	stops := make([]color.Color, 0, len(values))
	for _, v := range values {
		c := theme.ConfigColor(v)
		if c == nil || overlay.IsNoColor(c) {
			return false
		}
		stops = append(stops, c)
	}
	for i := range g.levels {
		t := float64(i) / float64(gradientLevels-1)
		pos := t * float64(len(stops)-1)
		j := min(int(pos), len(stops)-2)
		c := overlay.MixColors(stops[j], stops[j+1], pos-float64(j))
		if depth == overlay.Depth256 {
			c = overlay.To256(c)
		}
		g.levels[i] = c
	}
	g.ok = true
	return true
}

// markGradient records the focused pane's border for the compose pass: the
// colour it was drawn in, the cells its frame runs on and the layer that draws
// them. It is called once per frame by whichever path draws the frame.
func (m *OS) markGradient(id string, rect image.Rectangle, target color.Color) {
	m.gradient.id, m.gradient.rect, m.gradient.target, m.gradient.on = id, rect, target, true
}

// gradientLayer reports whether the layer id draws the focused border: the
// focused pane's own layer, or for a tiled pane a rule of the border grid.
func (m *OS) gradientLayer(id string) bool {
	if m.gradient.id != "" {
		return id == m.gradient.id
	}
	return strings.HasPrefix(id, "sep-")
}

// applyBorderGradient recolours the focused border's cells inside region, a
// rectangle of the canvas a layer was just drawn on. off is where the layer
// was moved by the session view.
func (m *OS) applyBorderGradient(canvas *frameCanvas, region image.Rectangle, off image.Point) {
	g := &m.gradient
	if !g.on || g.target == nil {
		return
	}
	rect := g.rect.Add(off)
	w, h := rect.Dx(), rect.Dy()
	if w < 2 || h < 2 {
		return
	}
	region = region.Intersect(rect).Intersect(canvas.Bounds())
	if region.Empty() {
		return
	}
	tr, tg, tb, _ := g.target.RGBA()
	same := func(c color.Color) bool {
		if c == nil {
			return false
		}
		r, gg, b, _ := c.RGBA()
		return r == tr && gg == tg && b == tb
	}
	// The direction, in the rectangle's own units so 45 degrees runs corner
	// to corner whatever its shape, and the span of the projection over the
	// four corners, which maps the projection onto 0..1.
	rad := float64(m.Settings.BorderGradientAngle) * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	lo := math.Min(0, cos) + math.Min(0, sin)
	span := math.Abs(cos) + math.Abs(sin)
	level := func(x, y int) color.Color {
		u := float64(x-rect.Min.X) / float64(w-1)
		v := float64(y-rect.Min.Y) / float64(h-1)
		t := (u*cos + v*sin - lo) / span
		i := int(math.Round(t * float64(gradientLevels-1)))
		return g.levels[max(0, min(i, gradientLevels-1))]
	}
	paint := func(x, y int) {
		if !(image.Point{x, y}).In(region) {
			return
		}
		cell := canvas.CellAt(x, y)
		if cell == nil || !same(cell.Style.Fg) {
			return
		}
		cell.Style.Fg = level(x, y)
	}
	top, bottom := rect.Min.Y, rect.Max.Y-1
	for x := region.Min.X; x < region.Max.X; x++ {
		paint(x, top)
		paint(x, bottom)
	}
	left, right := rect.Min.X, rect.Max.X-1
	for y := max(region.Min.Y, top+1); y < min(region.Max.Y, bottom); y++ {
		paint(left, y)
		paint(right, y)
	}
}
