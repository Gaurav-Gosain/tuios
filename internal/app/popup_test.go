package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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

// TestPopupBoxIsCentredInTheContentRegion is the placement rule: the size the
// caller asked for, centred in the box the panes go in.
//
// Negative control, confirmed red: drop the centring in popupRect and return
// leftMargin/topMargin instead. Every case then reports x=0, y=0.
func TestPopupBoxIsCentredInTheContentRegion(t *testing.T) {
	for _, tc := range []struct {
		name         string
		width        string
		height       string
		wantW, wantH int
	}{
		{"a share of the region", "50%", "40%", 60, 15},
		{"cells", "40", "12", 40, 12},
		{"the default", "", "", 96, 22},
		{"more than the region has", "400", "400", 120, 38},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, popup := popupOS(t, tc.width, tc.height)
			x, y, w, h := m.popupRect(popup)
			if w != tc.wantW || h != tc.wantH {
				t.Errorf("popup box is %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
			region := m.GetContentWidth()
			usable := m.GetUsableHeight()
			if x != m.GetLeftMargin()+(region-w)/2 {
				t.Errorf("popup x is %d, want it centred at %d", x, m.GetLeftMargin()+(region-w)/2)
			}
			if y != m.GetTopMargin()+(usable-h)/2 {
				t.Errorf("popup y is %d, want it centred at %d", y, m.GetTopMargin()+(usable-h)/2)
			}
			if x < m.GetLeftMargin() || x+w > m.GetLeftMargin()+region {
				t.Errorf("popup spans [%d,%d), outside the content region [%d,%d)",
					x, x+w, m.GetLeftMargin(), m.GetLeftMargin()+region)
			}
			if y < m.GetTopMargin() || y+h > m.GetTopMargin()+usable {
				t.Errorf("popup spans rows [%d,%d), outside the pane rows [%d,%d)",
					y, y+h, m.GetTopMargin(), m.GetTopMargin()+usable)
			}
		})
	}
}

// TestPopupBoxFollowsTheClientNotThePeer is why the size travels and the
// rectangle does not: two clients of different sizes resolve the same request to
// two boxes, each centred on its own screen.
//
// Negative control, confirmed red: make popupRect measure against a constant 120
// instead of GetContentWidth(). The narrow client then reports the wide client's
// box and the test names both widths.
func TestPopupBoxFollowsTheClientNotThePeer(t *testing.T) {
	wide, widePopup := popupOS(t, "50%", "50%")
	narrow, narrowPopup := popupOS(t, "50%", "50%")
	narrow.Width, narrow.Height = 60, 20

	_, _, wideW, _ := wide.popupRect(widePopup)
	nx, _, narrowW, _ := narrow.popupRect(narrowPopup)

	if wideW != 60 {
		t.Errorf("the wide client resolved 50%% of 120 to %d, want 60", wideW)
	}
	if narrowW != 30 {
		t.Errorf("the narrow client resolved 50%% of 60 to %d, want 30", narrowW)
	}
	if nx+narrowW > 60 {
		t.Errorf("the popup runs off the narrow client's screen: [%d,%d) of 60", nx, nx+narrowW)
	}
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

// TestAPopupCannotBeMinimized is the dock exclusion, made where the dock reads
// from: the dock lists minimized panes, so a popup that cannot be minimized can
// never leave a pill behind for a pane that has closed itself.
//
// Negative control, confirmed red: remove the IsPopup guard at the top of
// MinimizeWindow. The popup is then minimized and getDockItems lists it.
func TestAPopupCannotBeMinimized(t *testing.T) {
	m, popup := popupOS(t, "50%", "50%")

	m.MinimizeWindow(1)
	if popup.Minimized || popup.Minimizing {
		t.Error("the popup was minimized")
	}
	for _, item := range m.getDockItems() {
		if item.WindowIndex == 1 {
			t.Error("the popup reached the dock as a pill")
		}
	}

	// The tiled pane beside it still minimizes, so the guard is about popups and
	// not about minimizing.
	m.MinimizeWindow(0)
	if !m.Windows[0].Minimized && !m.Windows[0].Minimizing {
		t.Error("an ordinary pane no longer minimizes")
	}
}

// TestAPeerRectangleIsDeclinedForAPopup holds the sync half of the same split:
// the mark and the asked-for size are adopted from a peer, the box is not.
//
// Negative control, confirmed red: drop the !ws.Popup term from adoptGeometry in
// updateWindowFromState. The popup takes the peer's 10x5 box at (7,7) and the
// test names it.
func TestAPeerRectangleIsDeclinedForAPopup(t *testing.T) {
	m, popup := popupOS(t, "50%", "50%")
	popup.X, popup.Y = 30, 11
	popup.Width, popup.Height = 60, 19

	m.updateWindowFromState(popup, &session.WindowState{
		ID: popup.ID, Title: "popup",
		X: 7, Y: 7, Width: 10, Height: 5,
		Workspace: 1, Popup: true, IsFloating: true,
		PopupWidth: "50%", PopupHeight: "50%",
	})

	if popup.X != 30 || popup.Y != 11 || popup.Width != 60 || popup.Height != 19 {
		t.Errorf("the popup adopted a peer's box: (%d,%d) %dx%d, want its own (30,11) 60x19",
			popup.X, popup.Y, popup.Width, popup.Height)
	}
	if !popup.IsPopup || popup.PopupWidth != "50%" {
		t.Errorf("the popup mark or its size did not travel: popup=%v %q x %q",
			popup.IsPopup, popup.PopupWidth, popup.PopupHeight)
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
