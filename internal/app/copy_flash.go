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

// copyFlashBand is the shape of the light: where its centre is across the
// region, and how far its glow reaches.
type copyFlashBand struct {
	centre float64
	reach  float64
	tint   color.Color
	ground color.Color
}

// copyFlashIntensity is how lit one column is, from 0 to 1.
//
// A soft falloff rather than a hard edge, which is what makes it read as light
// passing over the text rather than as a block sliding across it. The band
// starts off the left edge and ends off the right, so the first and last
// columns are lit on the way past instead of the sweep appearing to begin and
// end inside the text.
func (b copyFlashBand) intensity(x int) float64 {
	if b.reach <= 0 {
		return 0
	}
	d := float64(x) - b.centre
	if d < 0 {
		d = -d
	}
	if d >= b.reach {
		return 0
	}
	// Smooth at both ends: 1 at the centre, 0 at the reach, with no corner.
	t := 1 - d/b.reach
	return t * t
}

// copyFlashBandFor builds the band for this frame.
//
// The sweep crosses the full width of the pane rather than the width of the
// region, because a selection of three characters and a selection of a whole
// line should take the same time to cross: light moving at a speed that
// depends on how much was copied reads as a progress bar, which it is not.
func (m *OS) copyFlashBandFor(progress float64, width int) copyFlashBand {
	pal := theme.UI()
	reach := float64(width) * m.copyFlashReach()
	if reach < 3 {
		reach = 3
	}
	// From fully off one edge to fully off the other.
	span := float64(width) + 2*reach
	return copyFlashBand{
		centre: -reach + progress*span,
		reach:  reach,
		tint:   lipgloss.Color(m.Settings.CopyFlashColor),
		ground: pal.Canvas,
	}
}

// styleFor is the style one column of the sweep is drawn in, and whether the
// light has reached it at all. A column the light has not reached is left to
// the ordinary cell path, so the sweep costs nothing outside its own band.
func (b copyFlashBand) styleFor(x int) (lipgloss.Style, bool) {
	i := b.intensity(x)
	if i <= 0.02 {
		return lipgloss.Style{}, false
	}
	return lipgloss.NewStyle().Background(overlay.MixColors(b.ground, b.tint, i)), true
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
