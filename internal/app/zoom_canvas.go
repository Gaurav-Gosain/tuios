package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const zoomPeekMinCells = 2

type zoomCanvas struct {
	on         bool
	bounds     layout.Rect
	panX, panY int
}

func (c zoomCanvas) apply(r layout.Rect) layout.Rect {
	if !c.on {
		return r
	}
	r.X -= c.panX
	r.Y -= c.panY
	return r
}

func (c zoomCanvas) applySplit(s layout.SplitLine) layout.SplitLine {
	if !c.on {
		return s
	}
	if s.Vertical {
		s.Pos -= c.panX
		s.From -= c.panY
		s.To -= c.panY
		return s
	}
	s.Pos -= c.panY
	s.From -= c.panX
	s.To -= c.panX
	return s
}

func (m *OS) zoomCanvasBounds(zoomed *terminal.Window, tile layout.Rect) (layout.Rect, bool) {
	pct := m.zoomSize()
	region := layout.Rect{
		X: m.PaneLeft(), Y: m.PaneTop(),
		W: m.PaneWidth(), H: m.PaneHeight(),
	}
	if zoomed == nil || pct >= 100 || tile.W <= 0 || tile.H <= 0 || region.W <= 0 || region.H <= 0 {
		return region, false
	}
	if tile.W >= region.W && tile.H >= region.H {
		return region, false
	}

	wantW := min(float64(region.W)*float64(pct)/100, float64(region.W-2*zoomPeekMinCells))
	wantH := min(float64(region.H)*float64(pct)/100, float64(region.H-2*zoomPeekMinCells))
	if wantW < 1 || wantH < 1 {
		return region, false
	}
	boundsW := max(int(float64(region.W)*wantW/float64(tile.W)+0.5), region.W)
	boundsH := max(int(float64(region.H)*wantH/float64(tile.H)+0.5), region.H)
	if boundsW <= region.W && boundsH <= region.H {
		return region, false
	}
	return layout.Rect{X: region.X, Y: region.Y, W: boundsW, H: boundsH}, true
}

func (m *OS) zoomCanvasAt(bounds, zoomedRect layout.Rect) zoomCanvas {
	region := layout.Rect{
		X: m.PaneLeft(), Y: m.PaneTop(),
		W: m.PaneWidth(), H: m.PaneHeight(),
	}
	c := zoomCanvas{on: true, bounds: bounds}
	c.panX = clampInt(zoomedRect.X+zoomedRect.W/2-region.W/2-region.X, 0, max(bounds.W-region.W, 0))
	c.panY = clampInt(zoomedRect.Y+zoomedRect.H/2-region.H/2-region.Y, 0, max(bounds.H-region.H, 0))
	return c
}
