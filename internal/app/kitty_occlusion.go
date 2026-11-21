package app

import (
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type cellRect struct {
	X, Y, W, H int
}

func (r cellRect) empty() bool { return r.W <= 0 || r.H <= 0 }
func (r cellRect) area() int {
	if r.empty() {
		return 0
	}
	return r.W * r.H
}

func (r cellRect) overlaps(o cellRect) bool {
	return rectsOverlap(r.X, r.Y, r.W, r.H, o.X, o.Y, o.W, o.H)
}

func (r cellRect) clearOf(blocker cellRect) cellRect {
	if r.empty() || blocker.empty() || !r.overlaps(blocker) {
		return r
	}
	best := cellRect{}
	for _, c := range [...]cellRect{
		{r.X, r.Y, r.W, blocker.Y - r.Y},
		{r.X, blocker.Y + blocker.H, r.W, r.Y + r.H - blocker.Y - blocker.H},
		{r.X, r.Y, blocker.X - r.X, r.H},
		{blocker.X + blocker.W, r.Y, r.X + r.W - blocker.X - blocker.W, r.H},
	} {
		if c.area() > best.area() {
			best = c
		}
	}
	return best
}

const maxVisibleSlices = 6

func subtractRect(r, b cellRect) []cellRect {
	if r.empty() {
		return nil
	}
	if b.empty() || !r.overlaps(b) {
		return []cellRect{r}
	}
	var out []cellRect
	if b.Y > r.Y {
		out = append(out, cellRect{r.X, r.Y, r.W, b.Y - r.Y})
	}
	if bBottom := b.Y + b.H; bBottom < r.Y+r.H {
		out = append(out, cellRect{r.X, bBottom, r.W, r.Y + r.H - bBottom})
	}
	midTop := max(r.Y, b.Y)
	midBottom := min(r.Y+r.H, b.Y+b.H)
	if midBottom > midTop {
		if b.X > r.X {
			out = append(out, cellRect{r.X, midTop, b.X - r.X, midBottom - midTop})
		}
		if bRight := b.X + b.W; bRight < r.X+r.W {
			out = append(out, cellRect{bRight, midTop, r.X + r.W - bRight, midBottom - midTop})
		}
	}
	return out
}

func clearRegion(dst []cellRect, r cellRect, blockers []cellRect, limit int) ([]cellRect, bool) {
	dst = append(dst[:0], r)
	for _, b := range blockers {
		if len(dst) == 0 {
			return dst, true
		}
		n := len(dst)
		for i := range n {
			for _, part := range subtractRect(dst[i], b) {
				if len(dst)-n >= limit {
					return nil, false
				}
				dst = append(dst, part)
			}
		}
		dst = dst[:copy(dst, dst[n:])]
	}
	return dst, true
}

func largestClearRect(r cellRect, blockers []cellRect) (cellRect, bool) {
	for _, b := range blockers {
		r = r.clearOf(b)
		if r.empty() {
			return cellRect{}, false
		}
	}
	return r, !r.empty()
}

func occludersAboveInto(
	dst []cellRect,
	windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) []cellRect {
	for id, info := range allWindows {
		if id == excludeWindowID || !info.Visible || info.WindowZ <= windowZ {
			continue
		}
		dst = append(dst, cellRect{info.WindowX, info.WindowY, info.Width, info.Height})
	}
	return dst
}

func chromeRectsAbovePanes(dst []cellRect, layers []*lipgloss.Layer) []cellRect {
	for _, l := range layers {
		if l == nil || l.GetZ() <= config.ZIndexFloatingTop || l.Width() <= 0 || l.Height() <= 0 {
			continue
		}
		dst = append(dst, cellRect{l.GetX(), l.GetY(), l.Width(), l.Height()})
	}
	return dst
}

func (kp *KittyPassthrough) occludersAboveInto(
	dst []cellRect,
	windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) []cellRect {
	return occludersAboveInto(dst, windowZ, allWindows, excludeWindowID)
}
