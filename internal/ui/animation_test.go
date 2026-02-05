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

	return &terminal.Window{
		ID:                "test-window-id",
		Title:             "Test Window",
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
}

// =============================================================================
// NewMinimizeAnimation Tests
// =============================================================================

func TestNewMinimizeAnimation_CreatesAnimation(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	duration := 200 * time.Millisecond
	dockX, dockY := 10, 300

	anim := NewMinimizeAnimation(w, dockX, dockY, duration)

	if anim == nil {
		t.Fatal("NewMinimizeAnimation returned nil for non-zero duration")
	}

	if anim.Type != AnimationMinimize {
		t.Errorf("Expected animation type AnimationMinimize, got %d", anim.Type)
	}

	if anim.Window != w {
		t.Error("Animation window reference does not match")
	}

	if anim.Duration != duration {
		t.Errorf("Expected duration %v, got %v", duration, anim.Duration)
	}

	// Check start position matches window position
	if anim.StartX != 100 || anim.StartY != 50 {
		t.Errorf("Expected start position (100, 50), got (%d, %d)", anim.StartX, anim.StartY)
	}

	if anim.StartWidth != 80 || anim.StartHeight != 24 {
		t.Errorf("Expected start size (80, 24), got (%d, %d)", anim.StartWidth, anim.StartHeight)
	}

	// Check end position matches dock position
	if anim.EndX != dockX || anim.EndY != dockY {
		t.Errorf("Expected end position (%d, %d), got (%d, %d)", dockX, dockY, anim.EndX, anim.EndY)
	}

	// Check minimized end size (5x3)
	if anim.EndWidth != 5 || anim.EndHeight != 3 {
		t.Errorf("Expected end size (5, 3), got (%d, %d)", anim.EndWidth, anim.EndHeight)
	}

	if anim.Progress != 0 {
		t.Errorf("Expected initial progress 0, got %f", anim.Progress)
	}

	if anim.Complete {
		t.Error("Animation should not be complete initially")
	}
}

func TestNewMinimizeAnimation_ZeroDuration(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	anim := NewMinimizeAnimation(w, 10, 300, 0)

	if anim != nil {
		t.Error("Expected nil animation for zero duration")
	}

	if !w.Minimized {
		t.Error("Window should be minimized instantly for zero duration")
	}

	if w.Minimizing {
		t.Error("Window minimizing flag should be false for zero duration")
	}
}

// =============================================================================
// NewRestoreAnimation Tests
// =============================================================================

func TestNewRestoreAnimation_CreatesAnimation(t *testing.T) {
	w := createTestWindow(10, 300, 5, 3)
	defer func() { _ = w.Terminal.Close() }()

	// Set pre-minimize values (where the window was before minimize)
	w.PreMinimizeX = 100
	w.PreMinimizeY = 50
	w.PreMinimizeWidth = 80
	w.PreMinimizeHeight = 24

	duration := 200 * time.Millisecond
	dockX, dockY := 10, 300

	anim := NewRestoreAnimation(w, dockX, dockY, duration)

	if anim == nil {
		t.Fatal("NewRestoreAnimation returned nil for non-zero duration")
	}

	if anim.Type != AnimationRestore {
		t.Errorf("Expected animation type AnimationRestore, got %d", anim.Type)
	}

	// Check start position matches dock position
	if anim.StartX != dockX || anim.StartY != dockY {
		t.Errorf("Expected start position (%d, %d), got (%d, %d)", dockX, dockY, anim.StartX, anim.StartY)
	}

	// Check start size is minimized (5x3)
	if anim.StartWidth != 5 || anim.StartHeight != 3 {
		t.Errorf("Expected start size (5, 3), got (%d, %d)", anim.StartWidth, anim.StartHeight)
	}

	// Check end position matches pre-minimize position
	if anim.EndX != 100 || anim.EndY != 50 {
		t.Errorf("Expected end position (100, 50), got (%d, %d)", anim.EndX, anim.EndY)
	}

	if anim.EndWidth != 80 || anim.EndHeight != 24 {
		t.Errorf("Expected end size (80, 24), got (%d, %d)", anim.EndWidth, anim.EndHeight)
	}

	if anim.Progress != 0 {
		t.Errorf("Expected initial progress 0, got %f", anim.Progress)
	}

	if anim.Complete {
		t.Error("Animation should not be complete initially")
	}
}

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

func TestNewSnapAnimation_CreatesAnimation(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	targetX, targetY := 0, 0
	targetWidth, targetHeight := 160, 48
	duration := 150 * time.Millisecond

	anim := NewSnapAnimation(w, targetX, targetY, targetWidth, targetHeight, duration)

	if anim == nil {
		t.Fatal("NewSnapAnimation returned nil for non-zero duration")
	}

	if anim.Type != AnimationSnap {
		t.Errorf("Expected animation type AnimationSnap, got %d", anim.Type)
	}

	if anim.StartX != 100 || anim.StartY != 50 {
		t.Errorf("Expected start position (100, 50), got (%d, %d)", anim.StartX, anim.StartY)
	}

	if anim.StartWidth != 80 || anim.StartHeight != 24 {
		t.Errorf("Expected start size (80, 24), got (%d, %d)", anim.StartWidth, anim.StartHeight)
	}

	if anim.EndX != targetX || anim.EndY != targetY {
		t.Errorf("Expected end position (%d, %d), got (%d, %d)", targetX, targetY, anim.EndX, anim.EndY)
	}

	if anim.EndWidth != targetWidth || anim.EndHeight != targetHeight {
		t.Errorf("Expected end size (%d, %d), got (%d, %d)", targetWidth, targetHeight, anim.EndWidth, anim.EndHeight)
	}
}

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

func TestUpdate_ProgressCalculation(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	duration := 100 * time.Millisecond
	anim := NewSnapAnimation(w, 200, 100, 120, 36, duration)
	if anim == nil {
		t.Fatal("Failed to create animation")
	}

	// Manually set start time to control progress
	anim.StartTime = time.Now().Add(-50 * time.Millisecond) // 50% elapsed

	complete := anim.Update()

	if complete {
		t.Error("Animation should not be complete at 50%")
	}

	// Progress should be approximately 0.5 after easing
	// easeInOutCubic(0.5) = 0.5
	if anim.Progress < 0.4 || anim.Progress > 0.6 {
		t.Errorf("Expected progress around 0.5, got %f", anim.Progress)
	}
}

func TestUpdate_CompletesAtFullDuration(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	duration := 50 * time.Millisecond
	anim := NewSnapAnimation(w, 200, 100, 120, 36, duration)
	if anim == nil {
		t.Fatal("Failed to create animation")
	}

	// Set start time far in the past to ensure completion
	anim.StartTime = time.Now().Add(-100 * time.Millisecond)

	complete := anim.Update()

	if !complete {
		t.Error("Animation should be complete after full duration")
	}

	if !anim.Complete {
		t.Error("Animation Complete flag should be true")
	}

	if anim.Progress != 1.0 {
		t.Errorf("Expected final progress 1.0, got %f", anim.Progress)
	}
}

func TestUpdate_MinimizeCompletion(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	w.PreMinimizeX = 100
	w.PreMinimizeY = 50
	w.PreMinimizeWidth = 80
	w.PreMinimizeHeight = 24
	w.Minimizing = true

	duration := 50 * time.Millisecond
	anim := NewMinimizeAnimation(w, 10, 300, duration)
	if anim == nil {
		t.Fatal("Failed to create minimize animation")
	}

	// Complete the animation
	anim.StartTime = time.Now().Add(-100 * time.Millisecond)
	anim.Update()

	if !w.Minimized {
		t.Error("Window should be minimized after animation completes")
	}

	if w.Minimizing {
		t.Error("Minimizing flag should be cleared after animation completes")
	}

	// Position should be restored to pre-minimize values
	if w.X != 100 || w.Y != 50 {
		t.Errorf("Expected position restored to (100, 50), got (%d, %d)", w.X, w.Y)
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

func TestUpdate_AlreadyComplete(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	anim := NewSnapAnimation(w, 200, 100, 120, 36, 100*time.Millisecond)
	if anim == nil {
		t.Fatal("Failed to create animation")
	}

	anim.Complete = true

	// Update should return true immediately
	complete := anim.Update()

	if !complete {
		t.Error("Update should return true for already complete animation")
	}
}

// =============================================================================
// easeInOutCubic Tests
// =============================================================================

func TestEaseInOutCubic_BoundaryConditions(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected float64
		epsilon  float64
	}{
		{
			name:     "at 0%",
			input:    0.0,
			expected: 0.0,
			epsilon:  0.0001,
		},
		{
			name:     "at 50%",
			input:    0.5,
			expected: 0.5,
			epsilon:  0.0001,
		},
		{
			name:     "at 100%",
			input:    1.0,
			expected: 1.0,
			epsilon:  0.0001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := easeInOutCubic(tt.input)
			if absFloat(result-tt.expected) > tt.epsilon {
				t.Errorf("easeInOutCubic(%f) = %f, expected %f", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEaseInOutCubic_ProgressValues(t *testing.T) {
	// Test that easing is monotonically increasing
	prevValue := 0.0
	for i := 0; i <= 100; i++ {
		progress := float64(i) / 100.0
		value := easeInOutCubic(progress)
		if value < prevValue {
			t.Errorf("easeInOutCubic is not monotonically increasing: %f < %f at progress=%f", value, prevValue, progress)
		}
		prevValue = value
	}
}

func TestEaseInOutCubic_SlowStartAndEnd(t *testing.T) {
	// The easing function should be slower at the start and end
	// At t=0.25, the value should be less than 0.25 (slow start)
	at25 := easeInOutCubic(0.25)
	if at25 >= 0.25 {
		t.Errorf("Expected easeInOutCubic(0.25) < 0.25, got %f", at25)
	}

	// At t=0.75, the value should be more than 0.75 (slow end)
	at75 := easeInOutCubic(0.75)
	if at75 <= 0.75 {
		t.Errorf("Expected easeInOutCubic(0.75) > 0.75, got %f", at75)
	}
}

func TestEaseInOutCubic_Formula(t *testing.T) {
	// Test specific values based on the formula
	// For t < 0.5: 4 * t * t * t
	// For t >= 0.5: 1 + (2*t - 2)^3 / 2

	// Test at t = 0.25
	expected := 4 * 0.25 * 0.25 * 0.25 // = 0.0625
	result := easeInOutCubic(0.25)
	if absFloat(result-expected) > 0.0001 {
		t.Errorf("easeInOutCubic(0.25) = %f, expected %f", result, expected)
	}

	// Test at t = 0.75
	p := 2*0.75 - 2 // = -0.5
	expected = 1 + p*p*p/2
	result = easeInOutCubic(0.75)
	if absFloat(result-expected) > 0.0001 {
		t.Errorf("easeInOutCubic(0.75) = %f, expected %f", result, expected)
	}
}

// =============================================================================
// interpolate Tests
// =============================================================================

func TestInterpolate_LinearInterpolation(t *testing.T) {
	tests := []struct {
		name     string
		start    int
		end      int
		progress float64
		expected int
	}{
		{
			name:     "at 0%",
			start:    100,
			end:      200,
			progress: 0.0,
			expected: 100,
		},
		{
			name:     "at 50%",
			start:    100,
			end:      200,
			progress: 0.5,
			expected: 150,
		},
		{
			name:     "at 100%",
			start:    100,
			end:      200,
			progress: 1.0,
			expected: 200,
		},
		{
			name:     "at 25%",
			start:    0,
			end:      100,
			progress: 0.25,
			expected: 25,
		},
		{
			name:     "negative direction",
			start:    200,
			end:      100,
			progress: 0.5,
			expected: 150,
		},
		{
			name:     "same start and end",
			start:    50,
			end:      50,
			progress: 0.5,
			expected: 50,
		},
		{
			name:     "negative values",
			start:    -100,
			end:      100,
			progress: 0.5,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := interpolate(tt.start, tt.end, tt.progress)
			if result != tt.expected {
				t.Errorf("interpolate(%d, %d, %f) = %d, expected %d",
					tt.start, tt.end, tt.progress, result, tt.expected)
			}
		})
	}
}

func TestInterpolate_Rounding(t *testing.T) {
	// Test that rounding works correctly
	// 100 + (50 * 0.333...) = 116.666... should round to 117
	result := interpolate(100, 150, 1.0/3.0)
	if result != 117 {
		t.Errorf("interpolate(100, 150, 1/3) = %d, expected 117", result)
	}

	// 100 + (50 * 0.666...) = 133.333... should round to 133
	result = interpolate(100, 150, 2.0/3.0)
	if result != 133 {
		t.Errorf("interpolate(100, 150, 2/3) = %d, expected 133", result)
	}
}

// =============================================================================
// Animation Type Tests
// =============================================================================

func TestAnimationType_Constants(t *testing.T) {
	// Verify animation type constants are distinct
	types := map[AnimationType]string{
		AnimationMinimize: "AnimationMinimize",
		AnimationRestore:  "AnimationRestore",
		AnimationSnap:     "AnimationSnap",
	}

	if len(types) != 3 {
		t.Error("Expected 3 distinct animation types")
	}

	// Verify they start from 0 (iota)
	if AnimationMinimize != 0 {
		t.Errorf("Expected AnimationMinimize = 0, got %d", AnimationMinimize)
	}

	if AnimationRestore != 1 {
		t.Errorf("Expected AnimationRestore = 1, got %d", AnimationRestore)
	}

	if AnimationSnap != 2 {
		t.Errorf("Expected AnimationSnap = 2, got %d", AnimationSnap)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAnimation_FullCycle(t *testing.T) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	// Create a very short animation
	duration := 10 * time.Millisecond
	anim := NewSnapAnimation(w, 200, 100, 120, 36, duration)
	if anim == nil {
		t.Fatal("Failed to create animation")
	}

	// Run animation to completion
	maxIterations := 1000
	for range maxIterations {
		if anim.Update() {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	if !anim.Complete {
		t.Error("Animation did not complete within expected time")
	}

	// Verify final position
	if w.X != 200 || w.Y != 100 {
		t.Errorf("Expected final position (200, 100), got (%d, %d)", w.X, w.Y)
	}
}

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
// Benchmarks
// =============================================================================

func BenchmarkEaseInOutCubic(b *testing.B) {
	for b.Loop() {
		_ = easeInOutCubic(0.5)
	}
}

func BenchmarkInterpolate(b *testing.B) {
	for b.Loop() {
		_ = interpolate(100, 200, 0.5)
	}
}

func BenchmarkAnimationUpdate(b *testing.B) {
	w := createTestWindow(100, 50, 80, 24)
	defer func() { _ = w.Terminal.Close() }()

	anim := NewSnapAnimation(w, 200, 100, 120, 36, 1*time.Second)
	if anim == nil {
		b.Fatal("Failed to create animation")
	}

	b.ResetTimer()
	for b.Loop() {
		anim.Complete = false
		anim.StartTime = time.Now().Add(-500 * time.Millisecond)
		anim.Update()
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
