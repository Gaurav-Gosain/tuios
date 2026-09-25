package ui

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// createTestWindow creates a minimal window for testing animations.
// It does not spawn a PTY or shell process.
func createTestWindow(x, y, width, height int) *terminal.Window {
	termWidth := max(width-2, 1)
	termHeight := max(height-2, 1)
	term := vt.NewEmulator(termWidth, termHeight)

	w := &terminal.Window{
		ID:                "test-window-id",
		X:                 x,
		Y:                 y,
		Width:             width,
		Height:            height,
		Terminal:          term,
		PreMinimizeX:      x,
		PreMinimizeY:      y,
		PreMinimizeWidth:  width,
		PreMinimizeHeight: height,
	}
	w.SetTitle("Test Window")
	return w
}

// =============================================================================
// NewRestoreAnimation Tests
// =============================================================================

func TestNewRestoreAnimation_ZeroDuration(t *testing.T) {
	w := createTestWindow(10, 300, 5, 3)
	defer func() { _ = w.Terminal.Close() }()

	w.Minimized = true
	w.PreMinimizeX = 100
	w.PreMinimizeY = 50
	w.PreMinimizeWidth = 80
	w.PreMinimizeHeight = 24

	anim := NewRestoreAnimation(w, 10, 300, 0)

	if anim != nil {
		t.Error("Expected nil animation for zero duration")
	}

	if w.Minimized {
		t.Error("Window should be restored instantly for zero duration")
	}

	if w.X != 100 || w.Y != 50 {
		t.Errorf("Expected position (100, 50), got (%d, %d)", w.X, w.Y)
	}
}

// =============================================================================
// NewSnapAnimation Tests
// =============================================================================

func TestNewSnapAnimation_ZeroDuration(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	anim := NewSnapAnimation(w, 0, 0, 160, 48, 0)

	if anim != nil {
		t.Error("Expected nil animation for zero duration")
	}

	if w.X != 0 || w.Y != 0 {
		t.Errorf("Expected position (0, 0), got (%d, %d)", w.X, w.Y)
	}

	if w.Width != 160 || w.Height != 48 {
		t.Errorf("Expected size (160, 48), got (%d, %d)", w.Width, w.Height)
	}
}

func TestNewSnapAnimation_AlreadyAtTarget(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	// Animation to same position should return nil
	anim := NewSnapAnimation(w, 100, 50, 80, 24, 200*time.Millisecond)

	if anim != nil {
		t.Error("Expected nil animation when already at target position")
	}
}

// =============================================================================
// Update Tests
// =============================================================================

func TestFinish_ZeroTypeLeavesMinimizedWindowAlone(t *testing.T) {
	w := createTestWindow(10, 300, 5, 3)
	defer func() { _ = w.Terminal.Close() }()
	w.Minimized = true

	anim := &Animation{Window: w, EndX: 10, EndY: 300, EndWidth: 5, EndHeight: 3}
	anim.Finish()

	if !w.Minimized {
		t.Error("an animation with no type restored a minimized window")
	}
}

func TestUpdate_RestoreCompletion(t *testing.T) {
	w := createTestWindow(10, 300, 5, 3)
	defer func() { _ = w.Terminal.Close() }()

	w.Minimized = true
	w.PreMinimizeX = 100
	w.PreMinimizeY = 50
	w.PreMinimizeWidth = 80
	w.PreMinimizeHeight = 24

	duration := 50 * time.Millisecond
	anim := NewRestoreAnimation(w, 10, 300, duration)
	if anim == nil {
		t.Fatal("Failed to create restore animation")
	}

	// Complete the animation
	anim.StartTime = time.Now().Add(-100 * time.Millisecond)
	anim.Update()

	if w.Minimized {
		t.Error("Window should not be minimized after restore animation completes")
	}

	if w.X != 100 || w.Y != 50 {
		t.Errorf("Expected position (100, 50), got (%d, %d)", w.X, w.Y)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAnimation_InterpolatesDuringProgress(t *testing.T) {
	w := createTestWindow(0, 0, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	duration := 100 * time.Millisecond
	anim := NewSnapAnimation(w, 100, 100, 80, 24, duration)
	if anim == nil {
		t.Fatal("Failed to create animation")
	}

	// Set to 50% progress
	anim.StartTime = time.Now().Add(-50 * time.Millisecond)
	anim.Update()

	// Position should be approximately halfway
	// Note: Due to easing, it should be exactly halfway since easeInOutCubic(0.5) = 0.5
	if w.X < 40 || w.X > 60 {
		t.Errorf("Expected X around 50 at 50%% progress, got %d", w.X)
	}

	if w.Y < 40 || w.Y > 60 {
		t.Errorf("Expected Y around 50 at 50%% progress, got %d", w.Y)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
