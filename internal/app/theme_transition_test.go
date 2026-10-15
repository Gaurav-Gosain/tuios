package app

import (
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestAThemeTransitionEndsAndLetsTheClockLapse. The transition is nothing but
// frames, so the ways it goes wrong are quiet ones: a transition that never
// ends keeps the motion clock at the frame rate on an idle screen for good,
// and one that ends without dropping the kept frame holds a screen of cells.
// Motion none must start nothing at all.
//
// Negative controls: dropping the expireThemeTransition term from
// handleMotionFrame fails the "still runs" check, and dropping t.old = nil
// from end fails the "kept frame" check.
func TestAThemeTransitionEndsAndLetsTheClockLapse(t *testing.T) {
	prev := theme.CurrentThemeID()
	t.Cleanup(func() { _ = theme.Initialize(prev) })

	m := sidebarTestOS(t, 80, 24, "left")
	m.Settings.Motion = config.MotionFull
	m.Settings.ThemeTransition = config.ThemeTransitionCircle
	_ = theme.Initialize("catppuccin_latte")
	m.motionCmd()
	m.composeFrame()
	idle := m.motionInterval()

	m.applyTheme("tokyonight")
	m.motionCmd()
	if got := m.motionInterval(); got != m.themeTransitionFrame() {
		t.Fatalf("a theme change asks the motion clock for a frame every %v, want %v", got, m.themeTransitionFrame())
	}
	m.composeFrame()
	if m.themeFx.old == nil || m.themeFx.pending {
		t.Fatal("the first frame after the change did not keep the old one")
	}

	// A frame mid-way draws; the frame after the end draws once more and
	// the clock stops.
	m.renderSkipped = true
	m.handleMotionFrame(motionFrameMsg{gen: m.motion.gen})
	if m.renderSkipped {
		t.Error("a motion frame during the transition was skipped")
	}
	m.themeFx.start = time.Now().Add(-time.Hour)
	m.renderSkipped = true
	m.handleMotionFrame(motionFrameMsg{gen: m.motion.gen})
	if m.renderSkipped {
		t.Error("the frame that ends the transition was skipped, so the old theme stays on the screen")
	}
	if got := m.motionInterval(); got != idle {
		t.Errorf("the motion clock runs every %v after the transition, want %v as before it", got, idle)
	}
	if m.themeFx.old != nil {
		t.Error("the kept frame outlived the transition")
	}

	// Motion none: a change of theme is a cut.
	m.Settings.Motion = config.MotionNone
	m.applyTheme("catppuccin_latte")
	m.motionCmd()
	if m.themeFx.running() {
		t.Error("motion none started a theme transition")
	}
}

// TestASoftThemeCircleFinishesEveryStep. The soft edge is drawn only at
// truecolor, which the suite's other tests never run at, and its loop skips
// the inner circle by jumping over it. A row the inner circle does not reach
// once made that jump land on itself, so the first frame of a truecolor circle
// looped for good and froze the client.
//
// Negative control: making the skip test "x == ilo" with ilo = ihi set to the
// middle of the row, as it was, hangs at the third step.
func TestASoftThemeCircleFinishesEveryStep(t *testing.T) {
	m := sidebarTestOS(t, 80, 24, "left")
	m.themeFx.style, m.themeFx.soft = config.ThemeTransitionCircle, true
	m.themeFx.old = &frameCanvas{Buffer: *uv.NewBuffer(80, 24)}
	canvas := &frameCanvas{Buffer: *uv.NewBuffer(80, 24)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 101 {
			m.themeCircle(canvas, float64(i)/100)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a soft theme circle did not finish its frame")
	}
}
