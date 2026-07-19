package input

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// countDaemonResizes wires every window's daemon resize callback to a counter.
//
// The windows a benchmark builds are daemon windows with no daemon behind them,
// so DaemonResizeFunc is nil and a resize that would be a socket round trip in
// the real client costs a nil check here. That is precisely the gap that let a
// per-frame PTY resize during shared-border drags ship unnoticed, so the drag
// benchmarks below install a callback and report how often it fires.
func countDaemonResizes(m *app.OS) *atomic.Int64 {
	var n atomic.Int64
	for _, win := range m.Windows {
		win.DaemonResizeFunc = func(_, _ int) error {
			n.Add(1)
			return nil
		}
	}
	return &n
}

// feedOutput writes a line of fresh output into every window's emulator, the
// way a build log or a tailing process would while the user drags a divider.
func feedOutput(m *app.OS, seq int) {
	for _, win := range m.Windows {
		if win.Terminal == nil {
			continue
		}
		win.LockIO()
		_, _ = win.Terminal.Write(fmt.Appendf(nil, "output line %d\r\n", seq))
		win.UnlockIO()
	}
}

// BenchmarkResizeDragWithOutput measures one drag frame in the regime the user
// is actually in: terminals producing output while a divider is dragged.
//
// This is the case BenchmarkResizeMotionFrame cannot see. Motion renders are
// coalesced to one per frame interval, so in a tight loop with no output almost
// every motion event has its render skipped and View returns cached content,
// which makes that benchmark mostly a measurement of the cache hit. PTYDataMsg
// clears renderSkipped, so a single byte of output anywhere forces the next
// View to compose for real. One motion event plus one PTY wakeup per iteration
// is one composed frame per iteration, which is the unit that decides whether a
// drag feels smooth.
//
// ptyResizes/frame is reported alongside the timing because the expensive part
// of the frame was invisible to a benchmark's nil daemon callback.
func BenchmarkResizeDragWithOutput(b *testing.B) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		for _, n := range []int{2, 4, 9} {
			name := fmt.Sprintf("windows-%d/shared-%v", n, shared)
			b.Run(name, func(b *testing.B) {
				prev := config.SharedBorders
				config.SharedBorders = shared
				b.Cleanup(func() { config.SharedBorders = prev })

				m := benchResizeOS(b, n)
				resizes := countDaemonResizes(m)
				startX, startY := m.ResizeStartX, m.ResizeStartY

				b.ReportAllocs()
				b.ResetTimer()
				i := 0
				for b.Loop() {
					dx := i%6 - 3
					_, _ = m.Update(motionAt(startX+dx, startY))
					_ = m.View()

					feedOutput(m, i)
					_, _ = m.Update(app.PTYDataMsg{})
					_ = m.View()
					i++
				}
				b.StopTimer()
				b.ReportMetric(float64(resizes.Load())/float64(b.N), "ptyResizes/frame")
			})
		}
	}
}

// TestSharedBorderDragTracksPointerAndDoesNotAnimate pins the other half of the
// same bug, the half no timing measurement can see.
//
// A resize is direct manipulation. The edge belongs to the pointer and follows
// it cell for cell, with nothing easing toward it. The shared
// border ratio sync used to reapply the whole BSP layout on every composed
// frame, and that path builds a snap animation per window, each easing over
// 300ms toward a target the next frame would replace. The animations were
// discarded and rebuilt roughly every frame, so the panes never arrived at
// anything; they trailed the cursor on a curve that restarted continuously.
// That reads as mush, and it costs almost no CPU time, which is why it hid from
// every benchmark. It was shared-borders-only because only that mode reapplies
// the layout from the sync, which matches where the slowness was reported.
//
// Ticks are delivered here because animations advance on the tick, so a test
// that only sends motion would never see one move a window.
func TestSharedBorderDragTracksPointerAndDoesNotAnimate(t *testing.T) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared-%v", shared), func(t *testing.T) {
			prev := config.SharedBorders
			config.SharedBorders = shared
			t.Cleanup(func() { config.SharedBorders = prev })

			m := benchResizeOS(t, 9)
			focused := m.GetFocusedWindow()
			startX, startY := m.ResizeStartX, m.ResizeStartY

			// Retire what tiling the workspace left in flight, so what this
			// loop observes was created by the drag.
			m.Animations = nil

			const maxTrackingSlack = 2
			for i := 1; i <= 20; i++ {
				_, _ = m.Update(motionAt(startX+i, startY))
				_, _ = m.Update(app.TickerMsg(time.Now()))
				_ = m.View()

				for _, anim := range m.Animations {
					if anim.Type == ui.AnimationSnap {
						t.Fatalf("motion %d: a snap animation is easing a pane toward a target while the pointer owns it", i)
					}
				}

				// The edge stays with the pointer. It does not land exactly on it:
				// with shared borders the pointer is on the separator and the pane
				// ends the cell before, and the layout rounds ratios to whole
				// cells, so the gap breathes by a cell as the ratio crosses a
				// boundary. What must not happen is the edge falling steadily
				// behind, which is what easing toward a moving target looks like
				// over a drag this long.
				if offset := (startX + i) - (focused.X + focused.Width); offset < 0 || offset > maxTrackingSlack {
					t.Fatalf("motion %d: edge is %d cells from the pointer, more than the %d cells grid rounding accounts for",
						i, offset, maxTrackingSlack)
				}
			}
		})
	}
}

// TestResizeDragDefersPTYResizeUntilRelease pins the reason shared-border drags
// became expensive. The drag path resizes windows visually and records the new
// sizes in PendingResizes so mouse release can apply them once. The shared
// border ratio sync reapplies the whole BSP layout on every composed frame to
// keep the separator overlay in step, and that reapply used to go through the
// normal layout path, which resizes the emulator and notifies the daemon. With
// output flowing, frames compose as fast as output arrives, so the user paid a
// socket round trip per window per frame for a size they were still choosing.
//
// Not one PTY resize may escape before release, in either border mode.
func TestResizeDragDefersPTYResizeUntilRelease(t *testing.T) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared-%v", shared), func(t *testing.T) {
			prev := config.SharedBorders
			config.SharedBorders = shared
			t.Cleanup(func() { config.SharedBorders = prev })

			m := benchResizeOS(t, 9)
			resizes := countDaemonResizes(m)
			startX, startY := m.ResizeStartX, m.ResizeStartY

			for i := range 30 {
				dx := i%6 - 3
				_, _ = m.Update(motionAt(startX+dx, startY))
				// PTY output forces a real compose, the way a busy terminal does.
				_, _ = m.Update(app.PTYDataMsg{})
				_ = m.View()
			}

			if got := resizes.Load(); got != 0 {
				t.Fatalf("drag issued %d PTY resizes before release, want 0", got)
			}
			if len(m.PendingResizes) == 0 {
				t.Fatal("drag recorded no pending resizes, so release has nothing to apply")
			}

			// Release drains the pending resizes into one real resize per window.
			_, _ = m.Update(releaseAt(startX, startY))
			if got := resizes.Load(); got == 0 {
				t.Fatal("release applied no PTY resize, so the drag result never reached the daemon")
			}
		})
	}
}
