package app

import "github.com/Gaurav-Gosain/tuios/internal/overlay"

// hitSurface names a surface that records where it drew its mouse targets
// into the frame's hit record.
type hitSurface uint8

const (
	// hitMenu is the context menu. Its first rect is the whole panel with
	// ref -1, and each drawn row follows it with the row's item index.
	hitMenu hitSurface = iota
	hitSurfaceCount
)

// frameHit is one mouse target drawn this frame: where, at which z, and a
// small ref the surface reads back (a row index, an item index).
type frameHit struct {
	rect overlay.Rect
	z    int
	ref  int
}

// hitSlot is the targets one surface drew this frame. seq is the order the
// surface first drew in, which breaks a z tie the way the compositor does: of
// two layers at one z, the one appended later is drawn on top.
type hitSlot struct {
	on   bool
	seq  int
	hits []frameHit
}

// frameHits is the frame's hit record: every surface on it puts the
// rectangles it drew, at the z it drew them, while the frame is composed.
// Hit answers from the top, so the target the mouse finds is the one drawn
// over the others, and the two cannot disagree the way a second list of z
// ranks can.
//
// The slots are fixed, one per surface, and keep their capacity, so a frame
// that puts the same targets as the last one allocates nothing, which is what
// the frame a typed key costs needs (TestKeystrokeFrameBudget).
type frameHits struct {
	slots [hitSurfaceCount]hitSlot
	seq   int
}

// begin starts a frame: no surface has drawn a target yet.
func (f *frameHits) begin() {
	for i := range f.slots {
		f.slots[i].on = false
		f.slots[i].hits = f.slots[i].hits[:0]
	}
	f.seq = 0
}

// put empties a surface's slot for the targets it is drawing now. A surface
// that draws again in the same frame keeps its place in the draw order.
func (f *frameHits) put(s hitSurface) *hitSlot {
	slot := &f.slots[s]
	if !slot.on {
		slot.on = true
		slot.seq = f.seq
		f.seq++
	}
	slot.hits = slot.hits[:0]
	return slot
}

// add records one target in the slot.
func (s *hitSlot) add(r overlay.Rect, z, ref int) {
	s.hits = append(s.hits, frameHit{rect: r, z: z, ref: ref})
}

// above reports whether a target at z in a slot first drawn at seq, at index
// i within it, is drawn over the one held in best.
func above(z, seq, i int, best frameHit, bestSeq, bestI int, found bool) bool {
	if !found || z != best.z {
		return !found || z > best.z
	}
	if seq != bestSeq {
		return seq > bestSeq
	}
	return i > bestI
}

// at returns the target drawn on top at screen (x, y) and its surface.
func (f *frameHits) at(x, y int) (hitSurface, frameHit, bool) {
	var (
		best           frameHit
		bestS          hitSurface
		bestSeq, bestI int
		found          bool
	)
	for s := range f.slots {
		slot := &f.slots[s]
		if !slot.on {
			continue
		}
		for i := range slot.hits {
			h := &slot.hits[i]
			if h.rect.Contains(x, y) && above(h.z, slot.seq, i, best, bestSeq, bestI, found) {
				best, bestS, bestSeq, bestI, found = *h, hitSurface(s), slot.seq, i, true
			}
		}
	}
	return bestS, best, found
}

// in returns the top target of one surface at screen (x, y), whatever another
// surface drew over it.
func (f *frameHits) in(s hitSurface, x, y int) (frameHit, bool) {
	slot := &f.slots[s]
	var best frameHit
	bestI, found := 0, false
	for i := range slot.hits {
		h := &slot.hits[i]
		if h.rect.Contains(x, y) && above(h.z, 0, i, best, 0, bestI, found) {
			best, bestI, found = *h, i, true
		}
	}
	return best, found
}

// rect returns where a surface drew the target with the given ref this frame.
func (f *frameHits) rect(s hitSurface, ref int) (overlay.Rect, bool) {
	for _, h := range f.slots[s].hits {
		if h.ref == ref {
			return h.rect, true
		}
	}
	return overlay.Rect{}, false
}
