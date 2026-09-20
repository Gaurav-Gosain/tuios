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
	m.copyFlashStep = -1
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
	// Only when the step changes, plus the one that finds it finished.
	//
	// The fade has six of them, so one copy costs six repaints whatever the
	// frame rate is. Marking on every tick redrew the pane sixty times a
	// second to show six colours, and because the sweep it replaced gave every
	// cell a different background, none of those repaints could coalesce a run
	// of cells into one escape sequence either.
	step := copyFlashStepAt(m.copyFlashProgressAt(time.Now()))
	if step == m.copyFlashStep && step >= 0 {
		return
	}
	m.copyFlashStep = step
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

// CancelCopyFlash drops a sweep that is still running, and asks its pane for
// the frame without it.
//
// Anything the user does supersedes it. The sweep is a 550ms acknowledgement
// of a copy, and once they have pressed a key or clicked somewhere they are
// no longer looking at what was copied: leaving the light running meant it
// carried on painting a region whose text had moved underneath it, which is
// what leaving copy mode mid-sweep looked like.
func (m *OS) CancelCopyFlash() {
	if m.copyFlash == nil {
		return
	}
	id := m.copyFlash.WindowID
	m.copyFlash = nil
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
// copyFlashProgressAt is how far through the fade the given moment is, with no
// side effects, for a caller that only wants to know which step is showing.
func (m *OS) copyFlashProgressAt(now time.Time) float64 {
	if m.copyFlash == nil {
		return 1
	}
	total := m.copyFlashDuration()
	if total <= 0 {
		return 1
	}
	return float64(now.Sub(m.copyFlash.At)) / float64(total)
}

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

// copyFlashSteps is how many distinct frames a fade has.
//
// Not one per display frame. Fourteen frames over the duration would be
// fourteen colours differing by under two percent luminance each, which is
// below what a terminal and a display resolve between them: fourteen repaints
// for the appearance of six. Six steps at forty milliseconds each is long
// enough for a step to read as a state rather than a transition, and short
// enough that six of them read as continuous.
const copyFlashSteps = 6

// copyFlashLevels is how strongly each step is mixed toward the peak. It
// starts at full and decays: the acknowledgement appears at the instant of the
// keypress, because a ramp-in reads as lag rather than as arrival.
var copyFlashLevels = [copyFlashSteps]float64{1.00, 0.70, 0.45, 0.28, 0.15, 0.06}

// copyFlashBand is the fade on one frame.
//
// It has no position. The first version of this swept a band of light across
// the copied block, modelled frame by frame on a graphical app's shimmer, and
// every part of that translation was wrong for a character grid.
//
// A grid cannot move anything by less than a whole cell, so the band crept on
// a short copy and teleported on a wide one, with no duration that suited
// both. A gradient across cells is a handful of whole-cell steps, which is
// banding rather than light. A diagonal over a block that is five rows tall
// and two hundred columns wide leans two degrees, so the shape the setting
// promised was a vertical wipe in the common case. And motion is the loudest
// thing a terminal can do: it is the one stimulus that pulls the eye, which is
// the opposite of what an acknowledgement wants, since the person already
// knows what they did and is usually looking elsewhere.
//
// What is left is the part that carried the meaning: the region. The shape of
// what was taken, ragged right margin and all, is the whole message.
type copyFlashBand struct {
	// level is how far toward peak this frame sits, from 1 down to 0.
	level float64
	// peak is the colour the ground is carried toward, derived from the pane's
	// own background rather than fixed. See copyFlashPeak.
	peak color.Color
}

// styleFor is how one cell of the region is drawn on this frame.
//
// The foreground is never set, and that is the fix for the worst fault the
// sweep had. It mixed the text toward the same colour as the ground, so at the
// centre of the band the two were equal and the characters were simply gone:
// eleven to one down to one to one. What reads as an effect behaving strangely
// is text disappearing and coming back, which is a far louder event than a
// tint. It also threw away the colours the program had written, because it
// mixed from the interface's own foreground rather than the cell's.
//
// So only the background moves, and it moves from whatever the cell already
// had, which is what makes a line with its own background brighten rather than
// be replaced.
func (b copyFlashBand) styleFor(bg color.Color) (lipgloss.Style, bool) {
	if b.level <= 0 {
		return lipgloss.Style{}, false
	}
	return lipgloss.NewStyle().Background(overlay.MixColors(bg, b.peak, b.level)), true
}

// copyFlashCeiling is the most the ground may be lifted, as a contrast ratio.
//
// Calibrated against the selection colour, which is the most familiar "this
// region is marked" signal in the product and measures about 1.8 to 1 against
// a dark ground. An acknowledgement should land just under that: the same
// order, read as weaker.
const copyFlashCeiling = 1.6

// copyFlashFloor keeps the fade perceptible on a theme with so little contrast
// that the ceiling cannot be spent.
const copyFlashFloor = 1.15

// copyFlashPeak is the colour the ground is carried toward.
//
// Derived rather than fixed. The setting used to be a hex literal, and the
// same literal measured fourteen to one against a dark ground and one point
// oh three to one against a light one: a strobe on one theme and invisible on
// the other. What matters is the change relative to the ground, not the
// colour, so the ground is lifted by a ratio and the direction is whichever
// one has headroom, which is what ContrastText answers by measuring.
//
// The lift is capped so it never takes the pane's text below the floor the
// rest of the interface holds its marks to. A high-contrast theme spends the
// whole ceiling; a low-contrast one spends what it has.
func copyFlashPeak(bg, fg color.Color) color.Color {
	ratio := overlay.ContrastRatio(fg, bg) / overlay.MarkFloor
	ratio = min(max(ratio, copyFlashFloor), copyFlashCeiling)

	// Then measured and backed off until it actually clears the floor.
	//
	// The arithmetic above says where the lift may stop, and a colour is eight
	// bits a channel, so what Tone can return is a colour near that and not
	// the colour itself. On a low-contrast theme, where the whole budget is
	// spent, rounding landed at 2.99 against a floor of 3.00. Measuring the
	// answer costs a few multiplications once per fade and means the floor is
	// a fact rather than an intention.
	peak := overlay.Tone(bg, ratio)
	for range 8 {
		if overlay.ContrastRatio(fg, peak) >= overlay.MarkFloor || ratio <= copyFlashFloor {
			break
		}
		ratio = max(ratio-0.02, copyFlashFloor)
		peak = overlay.Tone(bg, ratio)
	}
	return peak
}

// copyFlashBandFor is the fade on the frame at this point through the run.
func (m *OS) copyFlashBandFor(progress float64) copyFlashBand {
	step := copyFlashStepAt(progress)
	if step < 0 {
		return copyFlashBand{}
	}
	bg, fg := theme.TerminalBg(), theme.TerminalFg()
	peak := copyFlashPeak(bg, fg)
	// A colour the user named wins, for anyone who wants a particular one.
	if c := m.Settings.CopyFlashColor; c != "" {
		peak = lipgloss.Color(c)
	}
	return copyFlashBand{level: copyFlashLevels[step], peak: peak}
}

// copyFlashStepAt is which of the fade's steps this point falls in, or -1 once
// the fade is over.
//
// The step rather than the raw progress is what the pane is redrawn on, so one
// copy costs six repaints whatever the frame rate is, instead of one per tick
// for the whole duration.
func copyFlashStepAt(progress float64) int {
	if progress < 0 || progress >= 1 {
		return -1
	}
	step := int(progress * copyFlashSteps)
	return min(step, copyFlashSteps-1)
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
