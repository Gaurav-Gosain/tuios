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
