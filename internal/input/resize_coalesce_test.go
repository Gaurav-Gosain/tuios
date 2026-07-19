package input

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
)

// releaseAt is the message bubbletea delivers when the drag ends.
func releaseAt(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg(uv.MouseReleaseEvent{
		X:      x,
		Y:      y,
		Button: uv.MouseButton(tea.MouseLeft),
	})
}

// TestResizeMotionRendersAreBoundedAndKeepFinalFrame drives a burst of motion
// events through the real Update path the way a fast drag does, then releases,
// and inspects the frames that actually came out of View.
//
// Two things are checked. Frames composed during the drag must not exceed the
// interaction frame budget, which is the property that stops a fast drag from
// building a render backlog. And the frame after release must show the geometry
// of the last pointer position: coalescing that dropped the final event would
// leave the layout settled somewhere other than where the user let go.
//
// The frame bound is stated as a rate rather than as "some event was skipped"
// so that it holds on slow builds too. Under the race detector a single frame
// can cost more than the budget on its own, in which case no event needs
// coalescing and the bound is satisfied trivially.
func TestResizeMotionRendersAreBoundedAndKeepFinalFrame(t *testing.T) {
	app.SetInputHandler(HandleInput)

	m := benchResizeOS(t, 4)
	startX, startY := m.ResizeStartX, m.ResizeStartY

	// Prime the cache so the coalesced path has a frame to repeat.
	prev := m.View().Content

	const steps = 40
	composed := 0
	start := time.Now()
	for i := 1; i <= steps; i++ {
		_, _ = m.Update(motionAt(startX-i, startY))
		got := m.View().Content
		if got != prev {
			composed++
		}
		prev = got
	}
	elapsed := time.Since(start)

	budget := time.Second / time.Duration(config.NormalFPS)
	// One frame of slack for the leading edge, one for scheduling jitter.
	maxFrames := int(elapsed/budget) + 2
	if composed > maxFrames {
		t.Fatalf("drag composed %d frames in %v, more than the %d the %v interaction "+
			"budget allows; motion events are still driving unbounded renders",
			composed, elapsed, maxFrames, budget)
	}

	finalX := startX - steps
	_, _ = m.Update(releaseAt(finalX, startY))
	finalFrame := m.View().Content

	// The geometry must reflect the last motion, not an earlier coalesced one.
	focused := m.GetFocusedWindow()
	if focused == nil {
		t.Fatal("no focused window after drag")
	}
	if got := focused.X + focused.Width; got != finalX {
		t.Fatalf("resized edge settled at %d, want the released pointer position %d", got, finalX)
	}

	// And that geometry must be what is on screen. Release is not a motion
	// event, so it is never coalesced and always composes.
	if finalFrame == prev {
		t.Fatal("frame after mouse release repeated the cached mid-drag frame; " +
			"the final position never reached the screen")
	}
}

// TestResizeInteractionTickFlushesSkippedMotion checks the other half of the
// contract: a motion event that was coalesced away is still guaranteed to reach
// the screen, because the interaction tick forces a render while a drag is live.
// Without that, a pointer that stops moving would leave the layout stale until
// the user nudged it again.
func TestResizeInteractionTickFlushesSkippedMotion(t *testing.T) {
	app.SetInputHandler(HandleInput)

	m := benchResizeOS(t, 4)
	startX, startY := m.ResizeStartX, m.ResizeStartY
	_ = m.View()

	_, _ = m.Update(motionAt(startX-1, startY))
	afterFirst := m.View().Content
	_, _ = m.Update(motionAt(startX-2, startY))
	afterSecond := m.View().Content

	if afterSecond != afterFirst {
		// The second event cleared the frame budget on its own and drew itself,
		// so there is nothing pending for the tick to flush. That happens on
		// slow builds where a frame costs more than the budget.
		t.Skip("second motion event was not coalesced on this build; nothing to flush")
	}

	_, _ = m.Update(app.TickerMsg(time.Now()))
	if afterTick := m.View().Content; afterTick == afterSecond {
		t.Fatal("interaction tick did not compose the coalesced motion; " +
			"a paused pointer would leave the layout stale")
	}
}
