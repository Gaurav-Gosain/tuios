package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// filterOS builds a model with the rail on the left and one pane beside it.
func filterOS(t *testing.T) *OS {
	t.Helper()
	pe, pp, pw := config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth
	config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = true, "left", 30
	prevFFM := config.Global.FocusFollowsMouse
	config.Global.FocusFollowsMouse = false
	t.Cleanup(func() {
		config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = pe, pp, pw
		config.Global.FocusFollowsMouse = prevFFM
	})

	cfg := config.DefaultConfig()
	o := NewOS(OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "editor", X: 31, Y: 1, Width: 40, Height: 20, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	return o
}

// TestMotionFilterPassesRailHover is the second gate the rail's hover has to
// clear. The view asks the host for all-motion tracking so hover has events to
// work with; this whitelist decides which of them reach Update at all, and a
// motion it drops is a hover that never happens. Terminal mode is pinned
// alongside window management because that is where hover looked broken.
func TestMotionFilterPassesRailHover(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode Mode
	}{
		{"window management", WindowManagementMode},
		{"terminal", TerminalMode},
	} {
		t.Run(mode.name, func(t *testing.T) {
			o := filterOS(t)
			o.Mode = mode.mode

			// Deep inside the rail band, well below the rows, where the footer
			// controls live.
			onRail := tea.MouseMotionMsg{X: 3, Y: 35}
			if FilterMouseMotion(o, onRail) == nil {
				t.Error("motion over the rail was dropped; nothing downstream can hover")
			}

			// The pane keeps the CPU guard when nothing out there hovers. Link
			// hover is the one thing that does, so the guard is now conditional
			// on it rather than absolute: with appearance.links off, a plain
			// shell asked for no mouse mode, tuios draws no hover out there, and
			// that motion is noise exactly as it always was.
			o.Settings.Links = config.LinksOff

			offRail := tea.MouseMotionMsg{X: 50, Y: 10}
			if FilterMouseMotion(o, offRail) != nil {
				t.Error("motion over a plain pane was passed; the guard is what keeps a mouse sweep cheap")
			}
		})
	}
}

// TestMotionFilterPassesTheBandExitEvent pins the event the rail's hover peek
// snaps back on. The peek owns no clock: it is cleared by the first motion that
// resolves off the sessions rows, and when the pointer leaves the band
// altogether the only motion that can carry that is the one extra event
// SidebarHoverActive keeps flowing. Drop it here and a preview outlives the
// pointer that made it, with nothing left to take it down.
func TestMotionFilterPassesTheBandExitEvent(t *testing.T) {
	o := filterOS(t)
	o.Mode = TerminalMode

	// Links off, so the rail's own clause is the only thing that can pass this
	// event and the assertions below are about it and nothing else.
	o.Settings.Links = config.LinksOff

	// The pointer is in the band and hovering, then steps out over a plain pane.
	o.SidebarHoverActive = true
	exit := tea.MouseMotionMsg{X: 50, Y: 10}
	if FilterMouseMotion(o, exit) == nil {
		t.Fatal("the band-exit event was dropped; the peek and the hover highlight both outlive the pointer")
	}

	// And once it has been delivered the guard closes again: the handler clears
	// HoverActive, so the next event over the same pane is noise once more.
	o.SidebarHoverActive = false
	if FilterMouseMotion(o, exit) != nil {
		t.Error("motion over a plain pane stayed whitelisted after the exit event")
	}
}

// TestMotionFilterPassesPaneContentForLinks is the other half of the clause the
// two tests above now qualify. A link under the pointer is drawn by the pane
// itself, so unlike every other hover in tuios its target is not a rectangle the
// chrome recorded. The clause used to pass every motion over a pane's content
// box on that strength, which composed one frame per cell for a sweep across
// any pane at all; it now asks the pane whether a link is under the cell.
//
// Negative control, confirmed red: with PointerOverLink replaced by a test of
// the pane's content box the "plain text" assertion fails, and with the clause
// removed the first assertion fails and so does the underline in a real
// session. With the cell-change guard removed the last assertion fails.
func TestMotionFilterPassesPaneContentForLinks(t *testing.T) {
	o := filterOS(t)
	o.Mode = WindowManagementMode
	o.Settings.Links = config.LinksAll

	// A real emulator in the pane, with a bare URL on one row and plain text
	// on another. The pane is at X=31, Y=1 with a border, so its content
	// starts at (32, 2).
	win := newTestWindow(t, "aaaaaaaa1111", 40, 20)
	win.X, win.Y, win.Workspace = 31, 1, 1
	win.WriteOutput([]byte("\x1b[3;1Hplain text with no address on it\x1b[10;1Hsee https://example.com/e2e now"))
	o.Windows = []*terminal.Window{win}
	linkX, linkY := screenOf(win, 8, 9)
	textX, textY := screenOf(win, 8, 2)

	if FilterMouseMotion(o, tea.MouseMotionMsg{X: linkX, Y: linkY}) == nil {
		t.Error("motion over a link was dropped; no link can ever underline itself")
	}
	if FilterMouseMotion(o, tea.MouseMotionMsg{X: textX, Y: textY}) != nil {
		t.Error("motion over plain text passed; a sweep across a pane composes a frame per cell")
	}

	// Off is off: the guard the two tests above pin is restored exactly.
	o.Settings.Links = config.LinksOff
	if FilterMouseMotion(o, tea.MouseMotionMsg{X: linkX, Y: linkY}) != nil {
		t.Error("appearance.links = off still passed pane motion")
	}

	// A pane's border is not its content, so the pointer resting on the frame
	// buys nothing. Column 31 is the border and column 32 the first content cell.
	o.Settings.Links = config.LinksAll
	if FilterMouseMotion(o, tea.MouseMotionMsg{X: 31, Y: linkY}) != nil {
		t.Error("motion on a pane's border was passed as content")
	}

	// A hover that is showing keeps one more event flowing, so the pointer
	// leaving the run for plain text is the event that clears the underline.
	if !o.LinkHoverAt(linkX, linkY) {
		t.Fatal("the fixture's URL did not resolve as a link")
	}
	if FilterMouseMotion(o, tea.MouseMotionMsg{X: textX, Y: textY}) == nil {
		t.Error("the motion that leaves a link was dropped; the underline would stay")
	}
	o.clearLinkHover()

	// And a motion that lands on the cell the pointer is already on resolves to
	// the run it is already showing, so it is dropped.
	o.LastMouseX, o.LastMouseY = linkX, linkY
	if FilterMouseMotion(o, tea.MouseMotionMsg{X: linkX, Y: linkY}) != nil {
		t.Error("a motion that changed no cell was passed")
	}
}

// TestMotionFilterPassesACtrlDragGrab. A ctrl-click on pane content arms a grab
// that becomes a drag once the pointer moves far enough, and only motion can
// commit it. Nothing is dragging yet, so the drag clause does not cover it, and
// it used to reach Update only because every motion over content did.
//
// Negative control: remove the CtrlDragPending clause and this fails.
func TestMotionFilterPassesACtrlDragGrab(t *testing.T) {
	o := filterOS(t)
	o.Settings.Links = config.LinksOff
	over := tea.MouseMotionMsg{X: 50, Y: 10, Mod: tea.ModCtrl}
	if FilterMouseMotion(o, over) != nil {
		t.Fatal("motion over plain content passed with no grab armed")
	}
	o.CtrlDragPending = true
	if FilterMouseMotion(o, over) == nil {
		t.Error("motion was dropped with a ctrl-drag grab armed; the drag can never commit")
	}
}

// TestMotionFilterFeedsZenMouseMode. The mouse variant of zen mode reveals the
// borders while the pointer moves and reads the clock of the last motion that
// reached Update, so it has to see motion, over chrome as much as over a pane.
//
// Negative control: remove the zen clause and this fails.
func TestMotionFilterFeedsZenMouseMode(t *testing.T) {
	o := filterOS(t)
	o.Settings.Links = config.LinksOff
	// A pane's border: chrome no other clause claims.
	overChrome := tea.MouseMotionMsg{X: 31, Y: 10}
	if FilterMouseMotion(o, overChrome) != nil {
		t.Fatal("motion over chrome passed with zen off; the CPU guard is gone")
	}
	o.Settings.ZenMode = config.ZenModeMouse
	if FilterMouseMotion(o, overChrome) == nil {
		t.Error("motion was dropped under zen = mouse; the borders can never reveal")
	}
	o.LastMouseX, o.LastMouseY = overChrome.X, overChrome.Y
	if FilterMouseMotion(o, overChrome) != nil {
		t.Error("a motion inside the same cell passed under zen = mouse")
	}
}

// TestMotionFilterRecordsThePointerItDrops. A floating pane spawns at the
// pointer, and the pointer is wherever the host last said it was, not where the
// last motion that reached Update happened to be. The filter sees every motion,
// so it is the one place that can keep that current.
//
// Negative control: remove the NotePointerSeen call and this fails.
func TestMotionFilterRecordsThePointerItDrops(t *testing.T) {
	o := filterOS(t)
	o.Settings.Links = config.LinksOff
	o.AutoTiling = false
	if FilterMouseMotion(o, tea.MouseMotionMsg{X: 70, Y: 12}) != nil {
		t.Fatal("the motion was not dropped, so this proves nothing")
	}
	if x, y := o.PointerSeen(); x != 70 || y != 12 {
		t.Fatalf("PointerSeen = (%d,%d) after a dropped motion at (70,12)", x, y)
	}
	x, y, _, _ := o.NewWindowPlacement()
	if x != 70 || y != 12 {
		t.Errorf("a floating pane would spawn at (%d,%d), not at the pointer (70,12)", x, y)
	}
}

// TestMotionFilterPassesTheDockBand. The dock's session controls brighten under
// the pointer and a clipped workspace pill says its name, both off motion over
// the band, and no clause let that motion through: the hover and the labels
// were dead on every client. The filter passes a motion that changes what the
// dock shows, and only that: one onto a control, one off it, and nothing for
// the cells of bare band between, each of which composed a frame identical to
// the last.
//
// Negative control: remove the dock clause and the first assertion fails;
// drop dockHoverChangesAt from it, so every cell of the band passes again, and
// the bare-band assertion fails.
func TestMotionFilterPassesTheDockBand(t *testing.T) {
	o := filterOS(t)
	o.Settings.Links = config.LinksOff
	o.dockSessionHits = []dockSessionHit{{X0: 60, X1: 62, Y: 39, Action: DockSessionLeave}}
	overDock := tea.MouseMotionMsg{X: 60, Y: 39}
	if !o.InDockBand(overDock.Y) {
		t.Fatalf("row %d is not the dock band; the fixture moved", overDock.Y)
	}
	if FilterMouseMotion(o, overDock) == nil {
		t.Error("motion onto a dock control was dropped; it can never brighten")
	}
	o.LastMouseX, o.LastMouseY = overDock.X, overDock.Y
	if FilterMouseMotion(o, overDock) != nil {
		t.Error("a motion inside the same dock cell passed")
	}

	bareBand := tea.MouseMotionMsg{X: 45, Y: 39}
	o.LastMouseX, o.LastMouseY = 46, 39
	if FilterMouseMotion(o, bareBand) != nil {
		t.Error("motion across bare dock band passed; each cell composes an unchanged frame")
	}

	// The pointer leaves the band with a control still lit: that one event
	// must pass so the handler can clear it, and the next must not.
	o.dockSessionHover = DockSessionLeave
	offDock := tea.MouseMotionMsg{X: 60, Y: 30}
	if FilterMouseMotion(o, offDock) == nil {
		t.Error("the motion that leaves a lit control was dropped; it stays lit")
	}
	o.dockSessionHover = DockSessionNone
	o.LastMouseX, o.LastMouseY = overDock.X, overDock.Y
	if FilterMouseMotion(o, offDock) != nil {
		t.Error("motion off the dock with nothing lit passed; the CPU guard is gone")
	}
}

// TestDockHoverChangesAtAgreesWithTheHandler holds the filter's prediction to
// what the motion handler does. The filter drops a dock motion when
// dockHoverChangesAt says the hover would not change, so a case where it says
// no and the handler would have changed something is a control that never
// lights or a label that never arms or never clears.
//
// It walks every cell of the dock row from every hover state the dock can be
// in (nothing lit, each control lit, each label pending, a label from another
// surface) with tooltips on and off, runs DockSessionHoverAt and
// DockWorkspaceHoverAt as the handler does, and compares.
//
// Negative control: making dockHoverChangesAt ignore the tooltip, or answer
// from the session controls alone, fails this.
func TestDockHoverChangesAtAgreesWithTheHandler(t *testing.T) {
	o := filterOS(t)
	const row = 39
	o.WorkspaceNames = map[int]string{2: "a workspace name far too long for its pill", 3: "ok"}
	if !o.workspacePillClipped(2) || o.workspacePillClipped(3) {
		t.Fatal("setup: workspace 2 must clip and 3 must not")
	}
	o.dockWorkspaceHits = []dockWorkspaceHit{
		{X0: 40, X1: 46, Y: row, Workspace: 2},
		{X0: 47, X1: 51, Y: row, Workspace: 3},
		{X0: 52, X1: 55, Y: row, Workspace: 0}, // the "+" tab
	}
	o.dockSessionHits = []dockSessionHit{
		{X0: 110, X1: 112, Y: row, Action: DockSessionLeave},
		{X0: 113, X1: 115, Y: row, Action: DockSessionClose},
	}

	hovers := []DockSessionAction{DockSessionNone, DockSessionLeave, DockSessionClose}
	tips := []tooltipState{
		{},
		{Source: tooltipDockSession, Key: int(DockSessionLeave)},
		{Source: tooltipDockSession, Key: int(DockSessionClose), Shown: true},
		{Source: tooltipDockWorkspace, Key: 2},
		{Source: tooltipDockWorkspace, Key: 3},
		{Source: tooltipRailStrip, Key: 5},
	}
	checked := 0
	for _, enabled := range []bool{true, false} {
		for _, pillTips := range []bool{true, false} {
			o.Settings.Tooltips, o.Settings.DockWorkspaceTooltip = enabled, pillTips
			for _, hover := range hovers {
				for _, tip := range tips {
					for x := range 120 {
						o.dockSessionHover, o.Tooltip = hover, tip
						predicted := o.dockHoverChangesAt(x, row)
						o.DockSessionHoverAt(x, row)
						o.DockWorkspaceHoverAt(x, row)
						changed := o.dockSessionHover != hover || o.Tooltip != tip
						if predicted != changed {
							t.Errorf("tooltips=%v pill tooltips=%v lit=%d tooltip=%+v x=%d: predicted change %v, handler changed %v",
								enabled, pillTips, hover, tip, x, predicted, changed)
						}
						checked++
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("nothing was checked")
	}
}
