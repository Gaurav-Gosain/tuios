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

// TestSharedBorderResizeSettlesAtRelease drives a shared-borders resize drag
// through the real Update path and checks the two things deferring the ratio
// sync could break: the layout has to settle where the pointer was released,
// and it has to stay there when the workspace is retiled. The second check is
// the one that matters, because a retile rebuilds geometry from the tree's
// ratios, so a drag whose final sync was skipped would silently lose its result
// the next time anything triggered a layout.
func TestSharedBorderResizeSettlesAtRelease(t *testing.T) {
	app.SetInputHandler(HandleInput)

	prev := config.SharedBorders
	config.SharedBorders = true
	t.Cleanup(func() { config.SharedBorders = prev })

	m := benchResizeOS(t, 4)
	startX, startY := m.ResizeStartX, m.ResizeStartY
	before := m.View().Content

	const steps = 20
	midDrag := ""
	for i := 1; i <= steps; i++ {
		_, _ = m.Update(motionAt(startX-i, startY))
		// A real drag is interleaved with interaction ticks, which is what
		// flushes a motion whose draw was coalesced away.
		if i%4 == 0 {
			_, _ = m.Update(app.TickerMsg(time.Now()))
		}
		midDrag = m.View().Content
	}
	// The separator has to track the pointer while the drag is in flight, not
	// only once it ends. That is the whole reason the sync runs during a drag.
	if midDrag == before {
		t.Fatal("no frame during the drag differed from the pre-drag frame; the layout did not follow the pointer")
	}

	finalX := startX - steps
	_, _ = m.Update(releaseAt(finalX, startY))
	after := m.View().Content
	if after == before {
		t.Fatal("frame after release is identical to the frame before the drag; the resize never reached the screen")
	}

	focused := m.GetFocusedWindow()
	if focused == nil {
		t.Fatal("no focused window after drag")
	}
	settled := focused.X + focused.Width

	// Retiling reapplies the layout from the tree's ratios. If the drag's final
	// sync ran, the geometry is already what the ratios describe and nothing
	// moves; if it was skipped, the window snaps back to its pre-drag size.
	geom := make(map[string][4]int, len(m.Windows))
	for _, w := range m.Windows {
		geom[w.ID] = [4]int{w.X, w.Y, w.Width, w.Height}
	}
	m.TileAllWindows()
	for _, w := range m.Windows {
		if got, want := ([4]int{w.X, w.Y, w.Width, w.Height}), geom[w.ID]; got != want {
			t.Fatalf("window %s moved on retile: got %v, want %v; the drag's ratios were not committed to the tree", w.ID, got, want)
		}
	}

	if settled >= startX {
		t.Fatalf("resized edge settled at %d, which is not left of the drag start %d", settled, startX)
	}
}

// TestSharedBorderMotionCostDoesNotScaleWithWindowCount is the guard on the
// property this optimisation exists for. The ratio sync is whole-tree work, so
// running it per motion event made a shared-borders drag cost more the more
// windows the workspace held, while the same drag without shared borders stayed
// flat. Allocations stand in for cost here because they are deterministic.
func TestSharedBorderMotionCostDoesNotScaleWithWindowCount(t *testing.T) {
	prev := config.SharedBorders
	config.SharedBorders = true
	t.Cleanup(func() { config.SharedBorders = prev })

	motionAllocs := func(n int) float64 {
		m := benchResizeOS(t, n)
		startX, startY := m.ResizeStartX, m.ResizeStartY
		i := 0
		return testing.AllocsPerRun(200, func() {
			dx := i%6 - 3
			_, _ = HandleInput(motionAt(startX+dx, startY), m)
			i++
		})
	}

	small, large := motionAllocs(4), motionAllocs(9)
	// Slack for the honest per-window work a drag does: each pane whose size
	// crosses a cell boundary this motion reflows its emulator, and a crowded
	// workspace has more of them, so the geometry adjust is mildly proportional to
	// window count by design. It grew when BSP tiles stopped being inflated to a
	// fixed minimum: the smallest panes now hold their true size and reflow with
	// the drag instead of sitting pinned (and overlapping). The regression this
	// still catches is the whole-tree ratio sync running per motion, which more
	// than quadrupled the count.
	if large > small+8 {
		t.Fatalf("motion event allocated %v times at 9 windows against %v at 4; "+
			"per-event work is scaling with window count again", large, small)
	}
}
