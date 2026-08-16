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
	prev := config.ZenMode
	t.Cleanup(func() { config.ZenMode = prev })
}

// Disabled is the default and the historic behaviour: every window keeps its
// border regardless of focus or pointer activity.
func TestZenBordersHiddenDisabled(t *testing.T) {
	withZenMode(t)
	config.ZenMode = config.ZenModeDisabled

	m := &OS{}
	if m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = true with zen_mode disabled, want false")
	}
	if m.zenBordersHidden(true) {
		t.Error("zenBordersHidden(true) = true with zen_mode disabled, want false")
	}
}

// Always hides every unfocused border but keeps the focused window's frame, so
// the user always retains an anchor for where their keystrokes land.
func TestZenBordersHiddenAlways(t *testing.T) {
	withZenMode(t)
	config.ZenMode = config.ZenModeAlways

	m := &OS{}
	if !m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = false with zen_mode always, want true")
	}
	if m.zenBordersHidden(true) {
		t.Error("zenBordersHidden(true) = true with zen_mode always, want false")
	}
}

// Mouse reveals every border while the pointer is moving and hides the
// unfocused ones once it sits still past the reveal window.
func TestZenBordersHiddenMouse(t *testing.T) {
	withZenMode(t)
	config.ZenMode = config.ZenModeMouse

	m := &OS{}

	// No pointer event ever: treated as idle, so unfocused borders hide.
	if !m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = false with no pointer activity, want true")
	}

	// A recent motion reveals every border, focused or not.
	m.lastPointerAt = time.Now()
	if m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = true with a recent motion, want false")
	}
	if m.zenBordersHidden(true) {
		t.Error("zenBordersHidden(true) = true with a recent motion, want false")
	}

	// A pointer event older than the reveal window is idle again: unfocused
	// borders melt away, the focused one stays.
	m.lastPointerAt = time.Now().Add(-(zenModeMouseIdleTimeout + time.Second))
	if !m.zenBordersHidden(false) {
		t.Error("zenBordersHidden(false) = false after the idle window, want true")
	}
	if m.zenBordersHidden(true) {
		t.Error("zenBordersHidden(true) = true after the idle window, want false")
	}
}

// pointerRecentlyMoved answers strictly inside the reveal window.
func TestPointerRecentlyMoved(t *testing.T) {
	m := &OS{}

	if m.pointerRecentlyMoved() {
		t.Error("pointerRecentlyMoved() = true with no pointer event, want false")
	}

	m.lastPointerAt = time.Now()
	if !m.pointerRecentlyMoved() {
		t.Error("pointerRecentlyMoved() = false with a fresh event, want true")
	}

	m.lastPointerAt = time.Now().Add(-(zenModeMouseIdleTimeout + time.Millisecond))
	if m.pointerRecentlyMoved() {
		t.Error("pointerRecentlyMoved() = true past the idle window, want false")
	}
}
