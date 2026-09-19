package app

import (
	"image/color"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	if m.copyFlash == nil {
		return
	}
	id := m.copyFlash.WindowID
	// Asked whether it is still running or has just this moment stopped, and
	// the pane is marked either way.
	//
	// The last frame of a sweep has light in it. Nothing was asking for a
	// frame after that, so that frame stayed on the screen until the pane
	// changed for some other reason: the block the sweep had been crossing sat
	// there lit, and clicking about produced another copy and another one
	// stuck behind it. The tick that finds the sweep finished is the one that
	// asks for the frame without it.
	m.CopyFlashActive()
	if w := m.windowByID(id); w != nil {
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

// copyFlashBand is the light on one frame: where its centre is along whichever
// axis the sweep runs, how far its glow reaches, and what it is made of.
type copyFlashBand struct {
	centre float64
	reach  float64
	// amp is the whole sweep's brightness on this frame, from 0 to 1. It is
	// what makes the light arrive and leave, rather than switch on at full
	// strength at one edge and off at the other.
	amp float64
	// slope is how far the light leans, in columns per row, and vertical makes
	// it travel down the rows instead of across the columns. Together they are
	// the four shapes; see copyFlashShape.
	slope    float64
	vertical bool

	tint color.Color
	ink  color.Color
}

// The shapes a sweep can take.
//
// They are a setting because which one reads best depends on what is usually
// being copied. A diagonal falls across a paragraph. A horizontal one crosses
// a single long line properly, where a diagonal barely leans at all over one
// row. A vertical one moves down a tall narrow block, where the other three
// cross it in an instant.
//
// The names live in config, with the option that names the set.

// copyFlashSlope is how far a diagonal leans, in columns per row.
//
// A character grid holds a diagonal exactly when its slope is a whole number
// of columns per row, which is the one thing a grid does better than a
// gradient: there is nothing to interpolate and nothing to alias.
const copyFlashSlope = 2

// position is where a cell sits along the axis the sweep runs.
//
// The row is subtracted rather than added, so a positive slope means each row
// down is lit further to the right: the light peaks where position equals the
// centre, which is at x = centre + row*slope. Added, the sign came out
// backwards and the diagonal leaned the wrong way, which is the opposite of
// the effect this is copying.
func (b copyFlashBand) position(x, row int) float64 {
	if b.vertical {
		return float64(row)
	}
	return float64(x) - float64(row)*b.slope
}

// intensity is how lit one cell is, from 0 to 1.
//
// The falloff is what makes it read as light passing over the text rather than
// a block sliding across it.
func (b copyFlashBand) intensity(x, row int) float64 {
	if b.reach <= 0 || b.amp <= 0 {
		return 0
	}
	d := b.position(x, row) - b.centre
	if d < 0 {
		d = -d
	}
	if d >= b.reach {
		return 0
	}
	t := 1 - d/b.reach
	return t * t * b.amp
}

// styleFor is how one cell of the sweep is drawn, and whether the light has
// reached it at all.
//
// Only lit cells are painted. An earlier version also painted the whole block
// in the selection colour for the length of the sweep, on the grounds that the
// effect this copies keeps its selection: there, the selection is still there
// because the app leaves it. Here a copy clears it, so painting it back put a
// block of colour on the screen that nobody had asked for and that read as the
// selection having come back rather than as an acknowledgement.
//
// Both halves of a lit cell move. The background is the pane's own ground
// carried toward the light, and a cell holding a character has its text
// carried toward the light as well, because a sweep that touched only the
// background would pass behind the words rather than over them.
func (b copyFlashBand) styleFor(x, row int, hasGlyph bool, bg color.Color) (lipgloss.Style, bool) {
	i := b.intensity(x, row)
	if i <= 0.02 {
		return lipgloss.Style{}, false
	}
	st := lipgloss.NewStyle().Background(overlay.MixColors(bg, b.tint, i))
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
// lit for a twentieth of the run and what reached the screen was a blink. The
// band is sized to the block instead, so the run takes the same time whatever
// was copied and the light is on the text for all of it.
func (m *OS) copyFlashBandFor(progress float64, box copyFlashBox) copyFlashBand {
	pal := theme.UI()
	band := copyFlashBand{
		amp:  copyFlashEnvelope(progress),
		tint: lipgloss.Color(m.Settings.CopyFlashColor),
		ink:  pal.Fg,
	}
	switch m.Settings.CopyFlashStyle {
	case config.CopyFlashHorizontal:
	case config.CopyFlashDiagonalReverse:
		band.slope = -copyFlashSlope
	case config.CopyFlashVertical:
		band.vertical = true
	default:
		band.slope = copyFlashSlope
	}

	// How far along its axis the block runs, from the extremes of the cells in
	// it. Taking it from the corners rather than assuming a rectangle is what
	// lets the same arithmetic serve all four shapes.
	lo, hi := band.axisRange(box)
	span := hi - lo
	band.reach = span * m.copyFlashReach()
	// A floor, or the light over a short block is one cell wide and reads as a
	// cursor rather than as a sweep. In rows rather than columns when the
	// sweep runs down, because a block is far shorter than it is wide.
	floor := 4.0
	if band.vertical {
		// In rows rather than columns, because a block is far shorter than it
		// is wide. Below one row the light would be thinner than the thing it
		// is crossing and a one-row block would never light at all.
		floor = 1.5
	}
	if band.reach < floor {
		band.reach = floor
	}
	// From fully off one end to fully off the other.
	band.centre = lo - band.reach + progress*(span+2*band.reach)
	return band
}

// axisRange is the lowest and highest position any cell of the block takes
// along the axis this sweep runs.
func (b copyFlashBand) axisRange(box copyFlashBox) (lo, hi float64) {
	if b.vertical {
		return float64(box.top), float64(box.bottom)
	}
	corners := []float64{
		b.position(box.left, box.top), b.position(box.right, box.top),
		b.position(box.left, box.bottom), b.position(box.right, box.bottom),
	}
	lo, hi = corners[0], corners[0]
	for _, c := range corners[1:] {
		lo, hi = min(lo, c), max(hi, c)
	}
	return lo, hi
}

// copyFlashBox is the block the sweep crosses, in the pane's own coordinates:
// the leftmost and rightmost lit columns, and the first and last lit rows.
//
// The rows are the pane's row numbers, not a count, and that is the whole
// point of them. They used to be a count, so the band's travel was worked out
// over rows zero to n while the cells were drawn at their real row numbers.
// For a block twenty rows down the diagonal was off by twenty times its slope
// and the light passed to one side of the text; the vertical sweep travelled
// over rows zero to n and never reached row twenty at all. Only the
// horizontal one worked, because it is the one shape with no row term.
type copyFlashBox struct {
	left   int
	right  int
	top    int
	bottom int
}

// rows is how many rows the block covers.
func (b copyFlashBox) rows() int { return b.bottom - b.top + 1 }

// copyFlashBoxOf measures the marked region, so the sweep can be sized to what
// was copied rather than to the pane it sits in.
func copyFlashBoxOf(grid *pool.HighlightGrid, maxY, maxX int) (copyFlashBox, bool) {
	box := copyFlashBox{left: maxX, right: -1, top: -1, bottom: -1}
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
			if box.top < 0 {
				box.top = y
			}
			box.bottom = y
		}
	}
	if box.right < 0 {
		return copyFlashBox{}, false
	}
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
