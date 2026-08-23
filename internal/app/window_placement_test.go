package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// placementOS is a floating (untiled) client with no pointer history, which is
// what every headless and scripted path looks like.
func placementOS(t testing.TB) *OS {
	t.Helper()
	return &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            120,
		Height:           40,
		FocusedWindow:    -1,
	}
}

// placeNext asks for a placement and records a window there, as both callers of
// NewWindowPlacement do.
func placeNext(m *OS, id string) *terminal.Window {
	x, y, width, height := m.NewWindowPlacement()
	w := &terminal.Window{ID: id, Workspace: m.CurrentWorkspace, X: x, Y: y, Width: width, Height: height}
	m.Windows = append(m.Windows, w)
	return w
}

// TestNewFloatingWindowsDoNotLandOnTopOfEachOther is the placement equivalent of
// a stuck layout: NewWindowPlacement was a pure function of the screen, so every
// floating window opened at the same rectangle and each one hid the last
// completely. Two windows existed, one was visible, and nothing on screen said
// otherwise.
//
// It surfaced through a reattach. A window the daemon creates while nothing is
// attached is placed by the first client to see it, and a window opened
// afterwards took the same slot, covering the pane underneath and its
// scrollback with it.
//
// NEGATIVE CONTROL: fails before the cascade, where the second placement is
// identical to the first.
func TestNewFloatingWindowsDoNotLandOnTopOfEachOther(t *testing.T) {
	m := placementOS(t)

	first := placeNext(m, "win-1")
	second := placeNext(m, "win-2")

	if first.X == second.X && first.Y == second.Y {
		t.Errorf("both windows were placed at (%d,%d): the second covers the first "+
			"exactly and nothing on screen says there are two", first.X, first.Y)
	}
}

// TestNewFloatingWindowsStayOnScreen is the bound the cascade must respect. An
// offset that walks a window off the edge trades one invisible pane for
// another, and enough of a window has to be reachable to drag it back.
func TestNewFloatingWindowsStayOnScreen(t *testing.T) {
	m := placementOS(t)

	// More windows than the cascade has distinct slots, so the wrap is covered.
	for i := range 12 {
		w := placeNext(m, fmt.Sprintf("win-%d", i))
		if w.X < m.GetLeftMargin() || w.Y < m.GetTopMargin() {
			t.Fatalf("window %d placed at (%d,%d), above or left of the content region",
				i, w.X, w.Y)
		}
		if w.X+w.Width > m.GetLeftMargin()+m.GetContentWidth() {
			t.Fatalf("window %d spans to column %d, past the %d the content region has",
				i, w.X+w.Width, m.GetLeftMargin()+m.GetContentWidth())
		}
		if w.Y+w.Height > m.GetTopMargin()+m.GetUsableHeight() {
			t.Fatalf("window %d spans to row %d, past the %d the content region has",
				i, w.Y+w.Height, m.GetTopMargin()+m.GetUsableHeight())
		}
	}
}

// TestFirstFloatingWindowKeepsTheHomeSlot pins the cascade as an exception
// rather than a new default. One window on an empty screen belongs where it has
// always gone; the offset is what happens when that slot is taken.
func TestFirstFloatingWindowKeepsTheHomeSlot(t *testing.T) {
	m := placementOS(t)
	x, y, width, height := m.NewWindowPlacement()
	wantX := m.GetLeftMargin() + m.GetContentWidth()/4
	wantY := m.GetUsableHeight() / 4
	if x != wantX || y != wantY {
		t.Errorf("the first window was placed at (%d,%d), want the home slot (%d,%d)",
			x, y, wantX, wantY)
	}
	if width != m.GetContentWidth()/2 || height != m.GetUsableHeight()/2 {
		t.Errorf("the first window is %dx%d, want %dx%d",
			width, height, m.GetContentWidth()/2, m.GetUsableHeight()/2)
	}
}
