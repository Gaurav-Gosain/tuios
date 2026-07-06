package vt_test

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestEmulator_REPUnboundedClamp verifies that a hostile CSI REP (repeat)
// count taken straight from the parameter does not spin ~2.1 billion times
// under the IO lock. The clamp caps work at one screenful of cells, so the
// call must return promptly.
func TestEmulator_REPUnboundedClamp(t *testing.T) {
	emu := vt.NewEmulator(80, 24)

	done := make(chan struct{})
	go func() {
		// Print a char so lastChar is set, then request ~2.1 billion repeats.
		_, _ = emu.Write([]byte("X\x1b[2000000000b"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("REP with a 2e9 count did not return promptly; loop is unbounded")
	}
}

// TestEmulator_TabUnboundedClamp verifies that CHT/CBT with a huge count
// return promptly rather than iterating the raw parameter.
func TestEmulator_TabUnboundedClamp(t *testing.T) {
	emu := vt.NewEmulator(80, 24)

	done := make(chan struct{})
	go func() {
		// CHT (I) forward and CBT (Z) backward with runaway counts.
		_, _ = emu.Write([]byte("\x1b[2000000000I\x1b[2000000000Z"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("CHT/CBT with a 2e9 count did not return promptly; loop is unbounded")
	}
}
