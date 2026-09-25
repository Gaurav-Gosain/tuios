package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// withZenMode saves and restores the zen-mode global, which is package state
// shared with every other test in the run.
func withZenMode(t *testing.T) {
	t.Helper()
	prev := config.Global.ZenMode
	t.Cleanup(func() { config.Global.ZenMode = prev })
}

// TestZenMouseTickMeltsAndConverges checks that once the melt frame is
// composed the state converges: the tick stops reporting work (so it drops
// back to the slow idle rate instead of spinning at 10fps).
func TestZenMouseTickMeltsAndConverges(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeMouse

	m := &OS{Settings: config.Global}
	m.zenHidden = false
	m.lastPointerAt = time.Now().Add(-(zenModeMouseIdleTimeout + time.Second))

	// Crossing: work due.
	if !m.tickNeedsWork() {
		t.Fatal("expected work at the crossing")
	}
	// A frame composed with the pointer idle records zenHidden=true, which is
	// what the tick compares against next time.
	m.zenHidden = m.zenBordersHidden(false)
	if m.zenHidden != true {
		t.Fatalf("zenHidden after idle frame = %v, want true", m.zenHidden)
	}
	if m.tickNeedsWork() {
		t.Fatal("tickNeedsWork = true after the melt converged; the tick would spin at 10fps forever")
	}
}
