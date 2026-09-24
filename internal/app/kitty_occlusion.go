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

// maxVisibleSlices caps how many placements one image may be broken into.
//
// Subtracting one rectangle from another leaves at most four, and each extra
// blocker can split those again, so an unbounded decomposition is an unbounded
// number of escape sequences on every frame of a drag. Past the cap the image
// falls back to the single largest clear rectangle, which is worse looking and
// cheap, and which nothing short of several overlapping windows can reach.
const maxVisibleSlices = 6

// subtractRect returns the parts of r that b does not cover: nothing when b
// covers r, r itself when they do not meet, and otherwise up to four rectangles
// (a band above, a band below, and the left and right pieces of what is
// between them).
func subtractRect(r, b cellRect) []cellRect {
	if r.empty() {
		return nil
	}
	if b.empty() || !r.overlaps(b) {
		return []cellRect{r}
	}
	var out []cellRect
	// The band above the blocker keeps r's full width.
	if b.Y > r.Y {
		out = append(out, cellRect{r.X, r.Y, r.W, b.Y - r.Y})
	}
	// The band below it, likewise.
	if bBottom := b.Y + b.H; bBottom < r.Y+r.H {
		out = append(out, cellRect{r.X, bBottom, r.W, r.Y + r.H - bBottom})
	}
	// What is left of the middle band, to the left and right of the blocker.
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

// clearRegion returns the exact visible part of r as a set of rectangles, which
// is what lets an image keep the whole of an L rather than the larger of its
// two strips.
//
// dst is reused across frames. The answer is empty when the image is covered,
// and nil with ok false when the decomposition ran past limit pieces, which
// tells the caller to fall back to the single largest rectangle. limit is
// maxVisibleSlices unless chrome over the panes adds blockers of its own.
func clearRegion(dst []cellRect, r cellRect, blockers []cellRect, limit int) ([]cellRect, bool) {
	dst = append(dst[:0], r)
	for _, b := range blockers {
		if len(dst) == 0 {
			return dst, true
		}
		// The pieces this blocker leaves are appended after the current ones
		// and then moved down. Writing them over the current pieces in place
		// is not safe: one piece can split into several, which overwrote the
		// pieces after it before they were read, and the region came back
		// with some parts lost and others repeated.
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
	return occludersAboveInto(nil, windowZ, allWindows, excludeWindowID)
}

// occludersAboveInto appends into dst, which a caller reuses across frames so a
// refresh during a drag allocates nothing.
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

func (kp *KittyPassthrough) occludersAboveInto(
	dst []cellRect,
	windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) []cellRect {
	return occludersAboveInto(dst, windowZ, allWindows, excludeWindowID)
}

// sameSlices reports whether two slice lists describe the same drawing, so a
// pass that changed nothing emits nothing.
func sameSlices(a, b []placementSlice) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
