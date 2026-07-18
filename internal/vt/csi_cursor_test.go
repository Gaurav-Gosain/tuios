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

// TestEmulator_ScrollRegionNoRowJump verifies that CR, IL, and DL do not move
// the cursor vertically when the scroll-region top margin is greater than zero.
// Previously these passed an absolute cursor Y with margins=true, which re-added
// the top margin and jumped the cursor down by that amount.
func TestEmulator_ScrollRegionNoRowJump(t *testing.T) {
	t.Run("CR with DECOM keeps the absolute row", func(t *testing.T) {
		emu := vt.NewEmulator(80, 24)
		// Scroll region rows 5..20 (top margin at 0-based row 4), origin mode
		// on, CUP to relative row 3 (absolute row 6).
		if _, err := emu.Write([]byte("\x1b[5;20r\x1b[?6h\x1b[3;1H")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := emu.CursorPosition(); got.X != 0 || got.Y != 6 {
			t.Fatalf("setup cursor = (%d,%d), want (0,6)", got.X, got.Y)
		}
		if _, err := emu.Write([]byte("\r")); err != nil {
			t.Fatalf("write CR: %v", err)
		}
		if got := emu.CursorPosition(); got.Y != 6 {
			t.Fatalf("after CR cursor Y = %d, want 6 (must not jump by top margin)", got.Y)
		}
	})

	t.Run("IL keeps the absolute row", func(t *testing.T) {
		emu := vt.NewEmulator(80, 24)
		// Non-top-anchored scroll region, origin mode off, cursor at absolute
		// row 8 (inside the region).
		if _, err := emu.Write([]byte("\x1b[5;20r\x1b[9;1H")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := emu.CursorPosition(); got.Y != 8 {
			t.Fatalf("setup cursor Y = %d, want 8", got.Y)
		}
		if _, err := emu.Write([]byte("\x1b[L")); err != nil {
			t.Fatalf("write IL: %v", err)
		}
		if got := emu.CursorPosition(); got.Y != 8 {
			t.Fatalf("after IL cursor Y = %d, want 8 (must not jump by top margin)", got.Y)
		}
	})

	t.Run("DL keeps the absolute row", func(t *testing.T) {
		emu := vt.NewEmulator(80, 24)
		if _, err := emu.Write([]byte("\x1b[5;20r\x1b[9;1H")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := emu.Write([]byte("\x1b[M")); err != nil {
			t.Fatalf("write DL: %v", err)
		}
		if got := emu.CursorPosition(); got.Y != 8 {
			t.Fatalf("after DL cursor Y = %d, want 8 (must not jump by top margin)", got.Y)
		}
	})
}
