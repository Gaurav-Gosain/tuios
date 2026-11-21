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

const gradientLevels = 8

type borderGradient struct {
	setting string
	gen     uint64
	depth   overlay.Depth
	ok      bool
	levels  [gradientLevels]color.Color
	target  color.Color
	rect    image.Rectangle
	id      string
	on      bool
}

func (m *OS) gradientOn() bool {
	if m.Settings.BorderFocusedGradient == "" || theme.Depth() == overlay.Depth16 {
		return false
	}
	return m.gradientLevels()
}

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

func (m *OS) markGradient(id string, rect image.Rectangle, target color.Color) {
	m.gradient.id, m.gradient.rect, m.gradient.target, m.gradient.on = id, rect, target, true
}

func (m *OS) gradientLayer(id string) bool {
	if m.gradient.id != "" {
		return id == m.gradient.id
	}
	return strings.HasPrefix(id, "sep-")
}

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
	capL, capR := m.Settings.GetWindowPillLeft(), m.Settings.GetWindowPillRight()
	paint := func(x, y int) {
		if !(image.Point{x, y}).In(region) {
			return
		}
		cell := canvas.CellAt(x, y)
		if cell == nil || !same(cell.Style.Fg) || cell.Content == capL || cell.Content == capR {
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
