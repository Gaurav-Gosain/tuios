package app

import (
	"image/color"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The mark a copy leaves behind: a band of light that crosses what was copied,
// once, and then it is gone.
//
// Copying is the one gesture in a terminal with no result to look at. The text
// stays exactly as it was, the selection usually disappears, and the only
// feedback was a line of words in the dock saying how many characters went.
// That says it happened; it does not say what went. The sweep runs over the
// cells that were taken, so the answer is in the same place the question was.
//
// It is drawn rather than animated in any real sense: there is no state
// machine and nothing to cancel. A copy writes down the region and the time,
// every frame until the time runs out draws the band where the clock says it
// is, and after that the region is forgotten. A client that renders no frames
// in between simply misses it, which is the correct amount of machinery for a
// flourish.

// copyFlash is what was copied and when, for as long as the sweep lasts.
type copyFlash struct {
	// WindowID is the pane the text came from. The sweep is drawn there and
	// nowhere else, even though a copy can be made while another pane is
	// focused.
	WindowID string
	// Start and End are the region in the pane's absolute coordinates, the
	// same ones copy mode's visual selection uses, so the sweep covers exactly
	// the cells that were taken.
	Start terminal.Position
	End   terminal.Position
	// At is when the copy happened.
	At time.Time
}

// NoteCopyFlash records a copy so the next frames can sweep over it.
//
// It takes the region from the pane's live selection, which is still there at
// the moment of the copy and usually gone immediately after: the sweep outlives
// the selection it describes, which is the whole point.
func (m *OS) NoteCopyFlash(window *terminal.Window) {
	if window == nil || !m.Settings.CopyFlash || m.copyFlashDuration() <= 0 {
		return
	}
	if !window.HasSelection() || window.CopyMode == nil {
		return
	}
	start, end := window.CopyMode.VisualStart, window.CopyMode.VisualEnd
	if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
		start, end = end, start
	}
	m.copyFlash = &copyFlash{WindowID: window.ID, Start: start, End: end, At: time.Now()}
	// Nothing in the pane changed, so nothing else is going to ask for a
	// frame. The first one is asked for here and the work tick keeps them
	// coming while the sweep runs; see tickNeedsWork.
	window.ContentDirty = true
}

// markCopyFlashPane asks the pane a sweep is crossing to draw another frame.
//
// It is called from the maintenance tick, and it is what makes the sweep move
// at all. A pane is drawn from its cached frame unless something marks it, a
// copy changes nothing in the pane, and nothing else was marking it, so the
// light was computed every tick and painted into a frame that was thrown away.
// One frame reached the screen: the one the copy itself asked for, which is
// the frame where the light has not arrived yet.
func (m *OS) markCopyFlashPane() {
	if m.copyFlash == nil || !m.CopyFlashActive() {
		return
	}
	if w := m.windowByID(m.copyFlash.WindowID); w != nil {
		w.ContentDirty = true
	}
}

// copyFlashDuration is how long one sweep takes.
func (m *OS) copyFlashDuration() time.Duration {
	return time.Duration(m.Settings.CopyFlashMs) * time.Millisecond
}

// copyFlashProgress is how far through the sweep this frame is, from 0 to 1,
// and whether there is a sweep to draw for this pane at all.
//
// The flash is dropped as soon as it has run its course, so an idle client
// holds nothing and asks for no frames on its account.
func (m *OS) copyFlashProgress(windowID string) (float64, bool) {
	if m.copyFlash == nil {
		return 0, false
	}
	elapsed := time.Since(m.copyFlash.At)
	total := m.copyFlashDuration()
	if total <= 0 || elapsed >= total {
		m.copyFlash = nil
		return 0, false
	}
	if m.copyFlash.WindowID != windowID {
		return 0, false
	}
	return float64(elapsed) / float64(total), true
}

// CopyFlashActive reports whether a sweep is still running, so the render loop
// knows to keep asking for frames while it does.
func (m *OS) CopyFlashActive() bool {
	if m.copyFlash == nil {
		return false
	}
	if time.Since(m.copyFlash.At) >= m.copyFlashDuration() {
		m.copyFlash = nil
		return false
	}
	return true
}

// copyFlashBand is the light on one frame: where its centre is, how far its
// glow reaches, how bright it is overall, and what it is made of.
type copyFlashBand struct {
	centre float64
	reach  float64
	// amp is the whole sweep's brightness on this frame, from 0 to 1. It is
	// what makes the light arrive and leave, rather than switch on at full
	// strength at one edge and off at the other.
	amp  float64
	tint color.Color
	ink  color.Color
	// ground is the pane's own background, and sel is the selection the sweep
	// passes over. The selection is painted for as long as the sweep runs,
	// because a copy clears it and light crossing nothing reads as a glitch
	// rather than as an acknowledgement of what was taken.
	ground color.Color
	sel    color.Color
}

// copyFlashSlope is how far the light leans, in columns per row.
//
// A vertical band crossing a paragraph looks like a wipe. A diagonal one looks
// like light falling across it, which is the thing worth having, and a
// character grid can hold a diagonal exactly as long as its slope is a whole
// number of columns per row.
const copyFlashSlope = 2

// intensity is how lit one cell is, from 0 to 1.
//
// The falloff is what makes it read as light passing over the text rather than
// a block sliding across it, and the row offset is what makes it a diagonal:
// each row's band sits that much further along than the one above it.
func (b copyFlashBand) intensity(x, row int) float64 {
	if b.reach <= 0 || b.amp <= 0 {
		return 0
	}
	d := float64(x) - (b.centre + float64(row*copyFlashSlope))
	if d < 0 {
		d = -d
	}
	if d >= b.reach {
		return 0
	}
	// Smooth at both ends: 1 at the centre, 0 at the reach, with no corner.
	t := 1 - d/b.reach
	return t * t * b.amp
}

// styleFor is how one cell of the sweep is drawn, and whether it is part of
// the sweep at all.
//
// Both halves move. The background is the selection carried toward the light,
// and a cell holding a character has its text carried toward the light too,
// because a sweep that touched only the background would pass behind the words
// rather than over them.
func (b copyFlashBand) styleFor(x, row int, hasGlyph bool) (lipgloss.Style, bool) {
	if b.amp <= 0 {
		return lipgloss.Style{}, false
	}
	i := b.intensity(x, row)
	if i <= 0.02 {
		// Inside the block but outside the light, so the selection shows.
		return lipgloss.NewStyle().Background(b.sel), true
	}
	st := lipgloss.NewStyle().Background(overlay.MixColors(b.sel, b.tint, i))
	if hasGlyph {
		st = st.Foreground(overlay.MixColors(b.ink, b.tint, i))
	}
	return st, true
}

// copyFlashEnvelope is the sweep's brightness over its life: it ramps in,
// holds, and fades. Without it the light appears at full strength at one edge
// and vanishes at the other, which reads as a wipe rather than as something
// passing over.
func copyFlashEnvelope(progress float64) float64 {
	const (
		rampIn  = 0.15
		rampOut = 0.25
	)
	switch {
	case progress <= 0 || progress >= 1:
		return 0
	case progress < rampIn:
		return progress / rampIn
	case progress > 1-rampOut:
		return (1 - progress) / rampOut
	default:
		return 1
	}
}

// copyFlashBandFor builds the band for this frame.
//
// It crosses the copied block, not the pane.
//
// It used to cross the pane, on the reasoning that three characters and a
// whole line should take the same time so the sweep could not be read as a
// progress bar. That was wrong in the way that matters: the light is only
// visible while it is over the block, so a short selection on a wide pane was
// lit for a twentieth of the time the sweep was running and the whole thing
// went past in a blink. Crossing the block means the light is on the text for
// the whole duration, whatever was copied.
//
// box is the block's bounds in columns and its height in rows.
func (m *OS) copyFlashBandFor(progress float64, box copyFlashBox) copyFlashBand {
	pal := theme.UI()
	width := max(box.right-box.left+1, 1)
	reach := float64(width) * m.copyFlashReach()
	// A floor, or the light on a narrow block is one cell wide and reads as a
	// cursor rather than as a sweep.
	if reach < 4 {
		reach = 4
	}
	// From fully off one edge to fully off the other, and far enough past the
	// end for the lowest row's band, which leans furthest along, to leave too.
	lean := float64(box.rows * copyFlashSlope)
	span := float64(width) + lean + 2*reach
	return copyFlashBand{
		centre: float64(box.left) - reach + progress*span,
		reach:  reach,
		amp:    copyFlashEnvelope(progress),
		tint:   lipgloss.Color(m.Settings.CopyFlashColor),
		ink:    pal.Fg,
		ground: pal.Canvas,
		sel:    lipgloss.Color(m.Settings.SelectionBg),
	}
}

// copyFlashBox is the block the sweep crosses: its leftmost and rightmost lit
// columns, and how many rows it covers.
type copyFlashBox struct {
	left  int
	right int
	rows  int
}

// copyFlashBoxOf measures the marked region, so the sweep can be sized to what
// was copied rather than to the pane it sits in.
func copyFlashBoxOf(grid *pool.HighlightGrid, maxY, maxX int) (copyFlashBox, bool) {
	box := copyFlashBox{left: maxX, right: -1}
	top, bottom := -1, -1
	for y := range maxY {
		for x := range maxX {
			if !grid.Get(y, x) {
				continue
			}
			if x < box.left {
				box.left = x
			}
			if x > box.right {
				box.right = x
			}
			if top < 0 {
				top = y
			}
			bottom = y
		}
	}
	if box.right < 0 {
		return copyFlashBox{}, false
	}
	box.rows = bottom - top + 1
	return box, true
}

// fillPaneRegion marks the cells of a pane region on a grid, mapping the
// pane's absolute coordinates onto the rows currently on screen.
//
// It is the mapping copy mode's visual selection does, lifted out so the copy
// sweep covers exactly the same cells rather than a second implementation of
// the same arithmetic that could disagree with it.
func fillPaneRegion(grid *pool.HighlightGrid, start, end terminal.Position,
	scrollbackLen, scrollbackOffset, maxY, maxX int,
) {
	if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
		start, end = end, start
	}
	for absY := start.Y; absY <= end.Y; absY++ {
		var viewportY int
		if absY < scrollbackLen {
			if scrollbackOffset <= 0 || absY < scrollbackLen-scrollbackOffset {
				continue
			}
			viewportY = absY - (scrollbackLen - scrollbackOffset)
		} else {
			screenY := absY - scrollbackLen
			viewportY = screenY
			if scrollbackOffset > 0 {
				viewportY = scrollbackOffset + screenY
			}
		}
		if viewportY < 0 || viewportY >= maxY {
			continue
		}
		startX, endX := 0, maxX-1
		if absY == start.Y {
			startX = start.X
		}
		if absY == end.Y {
			endX = end.X
		}
		for x := startX; x <= endX && x < maxX; x++ {
			grid.Set(viewportY, x)
		}
	}
}

// copyFlashReach is the share of the pane's width the glow spans, as a
// fraction. A wider band on a wider pane, so the sweep looks the same on a
// narrow pane and a full-screen one.
func (m *OS) copyFlashReach() float64 { return 0.18 }
