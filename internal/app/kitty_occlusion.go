package app

// What a window on top of an image leaves of it.
//
// A kitty image is not drawn by tuios. The host terminal paints it over the
// finished frame, so a pane drawn on top of it does not cover it the way a pane
// covers text: the cells are composited and the image is not. Until now the
// answer was to hide any image a higher window touched at all, which is correct
// and blunt. One cell of overlap took the whole picture away, and dragging a
// window near an image made it blink out.
//
// It does not have to. A placement can be cropped, and the machinery to crop
// one already exists for the screen edge and the layout box: a source rectangle
// and a cell count. So the question is not "is this image covered" but "what
// rectangle of it is still clear", and the answer is one the existing crop can
// carry.
//
// One rectangle is the limit of what a placement can show. A window over the
// middle of an image leaves a ring, which is four rectangles, and a window over
// a corner leaves an L, which is two. Those are drawn as the largest rectangle
// that is clear, so part of the picture stays rather than none of it. An image
// placed by Unicode placeholders has no such limit, because there the cells are
// text and the compositor has already done the work; see the placeholder notes
// in kitty_placeholder.go.

// cellRect is a rectangle of screen cells.
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

// clearOf returns the largest rectangle of r that blocker does not cover.
//
// The four candidates are the strips above, below, left and right of the
// blocker. Each is the whole of r on one axis and what is left on the other, so
// each is a rectangle, and the biggest of them is the most of the image that
// one placement can show. r itself is the answer when the blocker misses it.
func (r cellRect) clearOf(blocker cellRect) cellRect {
	if r.empty() || blocker.empty() || !r.overlaps(blocker) {
		return r
	}
	best := cellRect{}
	for _, c := range [...]cellRect{
		{r.X, r.Y, r.W, blocker.Y - r.Y},                                     // above
		{r.X, blocker.Y + blocker.H, r.W, r.Y + r.H - blocker.Y - blocker.H}, // below
		{r.X, r.Y, blocker.X - r.X, r.H},                                     // left
		{blocker.X + blocker.W, r.Y, r.X + r.W - blocker.X - blocker.W, r.H}, // right
	} {
		if c.area() > best.area() {
			best = c
		}
	}
	return best
}

// largestClearRect returns the biggest rectangle of r that none of the blockers
// cover, and whether anything is left.
//
// Blockers are taken one at a time against what the previous ones left. That is
// not the true largest rectangle for every arrangement of several blockers, and
// it does not need to be: each step only ever shrinks the answer, so the result
// is always clear of all of them, and the case this exists for is one window
// being dragged across one image.
func largestClearRect(r cellRect, blockers []cellRect) (cellRect, bool) {
	for _, b := range blockers {
		r = r.clearOf(b)
		if r.empty() {
			return cellRect{}, false
		}
	}
	return r, !r.empty()
}

// occludersAbove is the on-screen rectangle of every window drawn over this
// one. A window that is not visible cannot cover anything, whatever geometry it
// is still carrying from the last time it was.
func occludersAbove(
	windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) []cellRect {
	var out []cellRect
	for id, info := range allWindows {
		if id == excludeWindowID || !info.Visible || info.WindowZ <= windowZ {
			continue
		}
		out = append(out, cellRect{info.WindowX, info.WindowY, info.Width, info.Height})
	}
	return out
}
