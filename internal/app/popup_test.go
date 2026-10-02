package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// popupOS builds a client holding one tiled pane and one popup, sized so the
// arithmetic in the assertions is readable: a 120x40 screen with the dock's two
// rows taken off leaves a 120x38 pane region.
func popupOS(t testing.TB, width, height string) (*OS, *terminal.Window) {
	t.Helper()
	tiled := newTestWindow(t, "tiled", 80, 24)
	popup := newTestWindow(t, "popup", 80, 24)
	popup.IsFloating = true
	popup.IsPopup = true
	popup.PopupWidth = width
	popup.PopupHeight = height
	m := &OS{
		Settings:         config.Global,
		Windows:          []*terminal.Window{tiled, popup},
		FocusedWindow:    1,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		Width:            120,
		Height:           40,
		CurrentWorkspace: 1,
		PendingResizes:   map[string][2]int{},
	}
	tiled.Workspace = 1
	popup.Workspace = 1
	return m, popup
}

// TestAPopupIsNotInTheWindowCycle pins the exclusion. A popup is one command
// with a lifetime, so cycling onto it would move the focus into a box that is
// about to close.
//
// Negative control, confirmed red: drop the !w.IsPopup term in cyclableWindows.
// The cycle then lists both windows and the test names the popup in it.
func TestAPopupIsNotInTheWindowCycle(t *testing.T) {
	m, popup := popupOS(t, "50%", "50%")
	m.FocusedWindow = 0

	cyclable := m.cyclableWindows()
	if len(cyclable) != 1 || m.Windows[cyclable[0]] == popup {
		t.Fatalf("the cycle lists %d windows including the popup, want only the tiled pane", len(cyclable))
	}

	// Cycling from the tiled pane comes back to it rather than landing on the
	// popup, in both directions.
	m.CycleToNextVisibleWindow()
	if m.FocusedWindow != 0 {
		t.Errorf("cycling forward landed on window %d, want the tiled pane at 0", m.FocusedWindow)
	}
	m.CycleToPreviousVisibleWindow()
	if m.FocusedWindow != 0 {
		t.Errorf("cycling back landed on window %d, want the tiled pane at 0", m.FocusedWindow)
	}
}

// TestAPopupMinimizesAndRestores pins the keyboard path. The minimize prefix
// used to refuse popups while the daemon's set-window verb accepted them, so
// the two disagreed about the same pane. A popup minimizes now like any other
// floating pane: the dock lists it and a restore digit brings it back.
//
// The restore must not land the popup at its pre-minimize box on a client that
// retiled in between — applyPopupRects recomputes the box, so the pre-minimize
// four are only for the animation the !AutoTiling branch runs.
func TestAPopupMinimizesAndRestores(t *testing.T) {
	m, popup := popupOS(t, "50%", "50%")

	m.MinimizeWindow(1)
	if !popup.Minimized {
		t.Fatal("the popup was not minimized")
	}
	listed := false
	for _, item := range m.getDockItems() {
		if item.WindowIndex == 1 {
			listed = true
		}
	}
	if !listed {
		t.Error("the minimized popup is not on the dock")
	}

	m.RestoreWindow(1)
	if popup.Minimized {
		t.Error("the popup did not restore")
	}

	// The tiled pane beside it still minimizes, so the change is about popups
	// being allowed and not about minimizing being broken.
	m.MinimizeWindow(0)
	if !m.Windows[0].Minimized {
		t.Error("an ordinary pane no longer minimizes")
	}
}

// TestAResizedPopupKeepsItsBox is the snap-back: applyPopupRects ran on every
// retile and restamped the box the creation request resolved to, so a popup
// the user had resized jumped back the next time the host session retiled —
// on every message a peer sent, since a sync retiles.
//
// A popup the user placed keeps the box they made, clamped into the region.
// The client resize still recentres a popup nobody touched, which
// TestAPopupRecentresWhenTheClientResizes pins on the other branch.
func TestAResizedPopupKeepsItsBox(t *testing.T) {
	m, popup := popupOS(t, "90%", "90%")
	m.tileAllWindows()

	// The user drags an edge: the mouse path commits the box and marks it placed.
	popup.PopupPlaced = true
	popup.X, popup.Y = 10, 3
	popup.Width, popup.Height = 40, 12

	m.tileAllWindows()
	if popup.X != 10 || popup.Y != 3 || popup.Width != 40 || popup.Height != 12 {
		t.Errorf("the retile snapped the popup back to (%d,%d %dx%d), want the user's (10,3 40x12)",
			popup.X, popup.Y, popup.Width, popup.Height)
	}

	// A client that shrinks below the box clamps it rather than losing it.
	m.Width, m.Height = 30, 10
	m.tileAllWindows()
	if popup.Width > m.PaneWidth() || popup.Height > m.PaneHeight() {
		t.Errorf("the clamp left the popup %dx%d in a %dx%d region", popup.Width, popup.Height, m.PaneWidth(), m.PaneHeight())
	}
	if popup.X < m.PaneLeft() || popup.Y < m.PaneTop() {
		t.Errorf("the clamp left the popup at (%d,%d), outside the content region", popup.X, popup.Y)
	}
}

// TestAPopupRecentresWhenTheClientResizes is why the box is recomputed on every
// retile rather than only when the popup arrives: the pane region moves when the
// client resizes, when the rail changes width and when the session's reserve is
// renegotiated, and a popup left at its old box is off centre or off screen.
//
// Negative control, confirmed red: remove the applyPopupRects call from
// tileAllWindows. The popup keeps its 120-column box on a 60-column screen and
// the test names the rectangle that ran off the edge.
func TestAPopupRecentresWhenTheClientResizes(t *testing.T) {
	m, popup := popupOS(t, "50%", "50%")
	m.tileAllWindows()
	if popup.Width != 60 {
		t.Fatalf("the popup opened at %d columns on a 120-column client, want 60", popup.Width)
	}

	m.Width, m.Height = 60, 20
	m.tileAllWindows()

	if popup.Width != 30 {
		t.Errorf("after the client narrowed to 60 the popup is %d columns, want 30", popup.Width)
	}
	if popup.X < 0 || popup.X+popup.Width > 60 {
		t.Errorf("the popup runs off the narrowed screen: [%d,%d) of 60", popup.X, popup.X+popup.Width)
	}
}

// TestAPopupIsNotSizedByPercent is the follow-up from the #140 merge: the
// popup guard in the percentage setters (SetFocusedWindowWidthPercent and
// SetFocusedWindowHeightPercent) had no test of its own, so removing w.IsPopup
// from either setter left the whole suite green. A popup carries its own
// percentage model (PopupWidth/PopupHeight) and applyPopupRects re-centres it
// on the next tile, so a percent resize must leave the focused popup's box
// exactly where it is.
//
// Negative control, confirmed red: drop the || w.IsPopup term from either
// setter and the popup's box moves to the tiled percentage.
func TestAPopupIsNotSizedByPercent(t *testing.T) {
	m, popup := popupOS(t, "50%", "40%")
	// The percentage setters drive the tiling resize path; the popup helpers
	// leave the layout maps nil because their tests never resize.
	m.AutoTiling = true
	m.WorkspaceHasCustom = map[int]bool{}
	m.WorkspaceLayouts = map[int][]WindowLayout{}
	m.WorkspaceMasterRatio = map[int]float64{}
	before := struct{ x, y, w, h int }{popup.X, popup.Y, popup.Width, popup.Height}

	m.SetFocusedWindowWidthPercent(80)
	m.SetFocusedWindowHeightPercent(80)

	if popup.X != before.x || popup.Y != before.y || popup.Width != before.w || popup.Height != before.h {
		t.Fatalf("percent resize moved the focused popup: box (%d,%d %dx%d) -> (%d,%d %dx%d)",
			before.x, before.y, before.w, before.h, popup.X, popup.Y, popup.Width, popup.Height)
	}
}
