package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// scratchOS is a client with one pane, "alpha", on workspace 1, 120x40, and
// the default [scratch] table. daemon picks a daemon session or a local one.
// The daemon client is not connected, so a test that shows or hides a group
// runs local: a daemon session would push the state.
func scratchOS(t *testing.T, daemon bool) *OS {
	t.Helper()
	m := dockSessionOS(t, 120, daemon)
	m.SessionName = "work"
	m.UserConfig = config.DefaultConfig()
	if m.WorkspaceFocus == nil {
		m.WorkspaceFocus = map[int]int{}
	}
	m.WorkspaceLayouts = map[int][]WindowLayout{}
	m.WorkspaceHasCustom = map[int]bool{}
	m.WorkspaceMasterRatio = map[int]float64{}
	m.WorkspaceStackRatio = map[int]float64{}
	m.PendingResizes = map[string][2]int{}
	if m.NumWorkspaces == 0 {
		m.NumWorkspaces = 9
	}
	return m
}

// addScratch adds a pane of the scratch group name, on the group's
// workspace (the one another pane of the group is on, or a free one).
func addScratch(m *OS, name, id string) *terminal.Window {
	ws := 0
	for _, w := range m.Windows {
		if isScratch(w) && scratchNameOf(w) == name {
			ws = w.Workspace
		}
	}
	if ws == 0 {
		ws = m.scratchWorkspaceFree()
	}
	stored := name
	if name == scratchName {
		stored = ""
	}
	w := &terminal.Window{
		ID: id, IsScratch: true, ScratchName: stored, CustomName: name,
		Workspace: ws, Width: 40, Height: 10,
	}
	m.Windows = append(m.Windows, w)
	return w
}

func TestScratchPlan(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*OS)
		want  scratchAction
	}{
		{"none creates", func(*OS) {}, scratchCreate},
		{"hidden group shows", func(m *OS) { addScratch(m, scratchName, "s1") }, scratchShow},
		{"shown group hides", func(m *OS) {
			addScratch(m, scratchName, "s1")
			m.showScratch(1)
		}, scratchHide},
		// A pane the user opened, even one named scratch, is not in the
		// group. Only the daemon's mark makes one.
		{"other panes create", func(m *OS) {
			m.Windows = append(m.Windows, &terminal.Window{ID: "plain", CustomName: scratchName, Workspace: 1})
		}, scratchCreate},
		{"session on another machine refuses", func(m *OS) {
			m.IsDaemonSession, m.DaemonClient = true, &session.TUIClient{}
			m.AttachedHost = "build"
		}, scratchRefuse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := scratchOS(t, false)
			tc.setup(m)
			if got := m.planScratch(scratchName); got.action != tc.want {
				t.Fatalf("plan = %+v, want action %d", got, tc.want)
			}
		})
	}
}

// A show makes the group's workspace the current one, over the workspace the
// user was on, in terminal mode. A hide goes back to that workspace, to the
// pane and the mode the show found. No pane is made or closed.
func TestScratchShowAndHideSwitchToTheGroup(t *testing.T) {
	m := scratchOS(t, false)
	m.Mode = WindowManagementMode
	a := addScratch(m, scratchName, "s1")
	b := addScratch(m, scratchName, "s2")

	m.ToggleScratch()
	if m.CurrentWorkspace != a.Workspace || !m.InScratchView() || m.scratchBase != 1 {
		t.Fatalf("after show: current=%d base=%d, want %d over 1", m.CurrentWorkspace, m.scratchBase, a.Workspace)
	}
	if f := m.GetFocusedWindow(); f != a && f != b {
		t.Fatal("the show did not focus a pane of the group")
	}
	if m.Mode != TerminalMode || !m.AutoTiling {
		t.Fatalf("mode=%v tiling=%v, want terminal mode and a tiled group", m.Mode, m.AutoTiling)
	}

	m.ToggleScratch()
	if m.CurrentWorkspace != 1 || m.InScratchView() || m.FocusedWindow != 0 || m.Mode != WindowManagementMode {
		t.Fatalf("after hide: current=%d focused=%d mode=%v", m.CurrentWorkspace, m.FocusedWindow, m.Mode)
	}
	if m.AutoTiling {
		t.Fatal("the hide left tiling on where the user had it off")
	}
	if len(m.Windows) != 3 {
		t.Fatalf("windows = %d, want 3", len(m.Windows))
	}
}

// The session hears of the workspace the user is on, never the group's, so a
// peer is not moved onto a scratch workspace.
func TestScratchViewIsNotTheSessionWorkspace(t *testing.T) {
	m := scratchOS(t, false)
	m.AutoTiling = false
	addScratch(m, scratchName, "s1")
	m.ToggleScratch()
	st := m.BuildSessionState()
	if st.CurrentWorkspace != 1 || st.AutoTiling {
		t.Fatalf("pushed workspace=%d tiling=%v, want 1 and the user's own off", st.CurrentWorkspace, st.AutoTiling)
	}
}

// The group tiles inside the scratch box, and the box sits inside the pane
// region, sized by [scratch].
func TestScratchGroupTilesInsideTheBox(t *testing.T) {
	m := scratchOS(t, false)
	m.UserConfig.Scratch.Width, m.UserConfig.Scratch.Height = "50%", "50%"
	addScratch(m, scratchName, "s1")
	m.ToggleScratch()
	outer, inner, ok := m.scratchRegion()
	if !ok {
		t.Fatal("no scratch region in the view")
	}
	if outer.W != m.GetContentWidth()/2 || outer.H != m.GetUsableHeight()/2 {
		t.Fatalf("box = %+v, want half the region %dx%d", outer, m.GetContentWidth(), m.GetUsableHeight())
	}
	if m.PaneLeft() != inner.X || m.PaneTop() != inner.Y || m.PaneWidth() != inner.W || m.PaneHeight() != inner.H {
		t.Fatalf("pane region = %d,%d %dx%d, want the box's inside %+v", m.PaneLeft(), m.PaneTop(), m.PaneWidth(), m.PaneHeight(), inner)
	}
	m.ToggleScratch()
	if m.PaneWidth() != m.GetContentWidth() {
		t.Fatal("the pane region stayed cut down after the hide")
	}
}

// Closing a pane of the group closes that pane. Closing the last one ends the
// group and goes back.
func TestClosingTheLastScratchPaneEndsTheGroup(t *testing.T) {
	m := scratchOS(t, false)
	addScratch(m, scratchName, "s1")
	addScratch(m, scratchName, "s2")
	m.ToggleScratch()
	m.CloseWindowByHand(m.FocusedWindow)
	if !m.InScratchView() || len(m.Windows) != 2 {
		t.Fatalf("after one close: view=%v windows=%d", m.InScratchView(), len(m.Windows))
	}
	m.CloseWindowByHand(m.scratchIndex())
	if m.InScratchView() || m.CurrentWorkspace != 1 || len(m.Windows) != 1 {
		t.Fatalf("after the last close: view=%v current=%d windows=%d", m.InScratchView(), m.CurrentWorkspace, len(m.Windows))
	}
}

// A focus jump to a pane of a hidden group shows the group. A focus on an
// ordinary pane while a group is shown goes back.
func TestFocusMovesInAndOutOfTheGroup(t *testing.T) {
	m := scratchOS(t, false)
	w := addScratch(m, scratchName, "s1")
	m.FocusWindow(1)
	if !m.InScratchView() || m.CurrentWorkspace != w.Workspace || m.scratchBase != 1 {
		t.Fatalf("a focus on a hidden group's pane: view=%v current=%d base=%d", m.InScratchView(), m.CurrentWorkspace, m.scratchBase)
	}
	m.FocusWindow(0)
	if m.InScratchView() || m.CurrentWorkspace != 1 {
		t.Fatalf("a focus back on alpha: view=%v current=%d", m.InScratchView(), m.CurrentWorkspace)
	}
}

// A new window made inside the group joins it, locally.
func TestNewWindowInsideTheGroupJoinsIt(t *testing.T) {
	m := scratchOS(t, false)
	addScratch(m, "notes", "s1")
	m.showScratch(1)
	m.AddWindow("")
	w := m.Windows[len(m.Windows)-1]
	t.Cleanup(func() { w.Close() })
	if !w.IsScratch || scratchNameOf(w) != "notes" || w.Workspace != m.CurrentWorkspace {
		t.Fatalf("new window scratch=%v name=%q ws=%d", w.IsScratch, scratchNameOf(w), w.Workspace)
	}
}

// Each group is its own workspace, so showing one group while another is
// on the screen switches between them and keeps the workspace both are shown
// over.
func TestSecondGroupReplacesTheFirst(t *testing.T) {
	m := scratchOS(t, false)
	a := addScratch(m, "one", "a")
	b := addScratch(m, "two", "b")
	if a.Workspace == b.Workspace {
		t.Fatal("two groups share a workspace")
	}
	m.showScratch(1)
	m.showScratch(2)
	if m.CurrentWorkspace != b.Workspace || m.scratchBase != 1 {
		t.Fatalf("current=%d base=%d, want %d over 1", m.CurrentWorkspace, m.scratchBase, b.Workspace)
	}
	m.leaveScratchView()
	if m.CurrentWorkspace != 1 {
		t.Fatalf("the hide went to %d, want 1", m.CurrentWorkspace)
	}
}

// A hidden group is in no list: not the window list, the rail or the
// workspace count, and not on a workspace a key can reach.
func TestScratchGroupIsInNoList(t *testing.T) {
	m := scratchOS(t, false)
	w := addScratch(m, scratchName, "s1")
	for _, it := range m.GetAggregateViewItems() {
		if it.Window == w {
			t.Fatal("the window list shows a scratch pane")
		}
	}
	for _, row := range m.currentSessionInput().Windows {
		if row.ID == w.ID {
			t.Fatal("the rail shows a scratch pane")
		}
	}
	if w.Workspace <= m.NumWorkspaces {
		t.Fatalf("scratch workspace %d is in reach of the workspace keys", w.Workspace)
	}
	m.showScratch(1)
	if m.dockWorkspace() != 1 {
		t.Fatalf("the dock names workspace %d, want 1", m.dockWorkspace())
	}
}

// A scratch pane is not minimized, not floated and not moved out of its
// group, and its pane menu dims Minimize.
func TestScratchPaneStaysInItsGroup(t *testing.T) {
	m := scratchOS(t, false)
	w := addScratch(m, scratchName, "s1")
	m.showScratch(1)
	ws := w.Workspace
	m.MinimizeWindow(1)
	m.ToggleFloating()
	m.MoveWindowToWorkspace(1, 2)
	if w.Minimized || w.IsFloating || w.Workspace != ws {
		t.Fatalf("minimized=%v floating=%v ws=%d", w.Minimized, w.IsFloating, w.Workspace)
	}
	_, items := m.paneMenu(1)
	for _, it := range items {
		if it.Action == "minimize_window" && !it.Dim {
			t.Error("minimize is live on a scratch pane")
		}
		if it.Action == "toggle_zoom" && it.Dim {
			t.Error("zoom is dimmed on a scratch pane")
		}
	}
}

func TestScratchCreateAsksOnceAndWaits(t *testing.T) {
	m := scratchOS(t, true)
	var asked []scratchRequest
	prev := scratchOpener
	scratchOpener = func(r scratchRequest) error { asked = append(asked, r); return nil }
	t.Cleanup(func() { scratchOpener = prev })

	cmd := m.ToggleScratch()
	if cmd == nil {
		t.Fatal("the first press asked for nothing")
	}
	if msg, ok := cmd().(ScratchOpenedMsg); !ok || msg.Err != nil {
		t.Fatalf("the create reported %#v", msg)
	}
	if len(asked) != 1 {
		t.Fatalf("asked %d times, want 1", len(asked))
	}
	if r := asked[0]; r.Session != "work" || r.Width != "80%" || r.Height != "80%" || r.Workspace != 1 || r.Dir == "" {
		t.Fatalf("request = %+v", r)
	}
	if m.ToggleScratch() != nil {
		t.Fatal("a second press asked again while the first was on its way")
	}
	m.handleScratchOpened(ScratchOpenedMsg{})
	if m.ToggleScratch() != nil || m.scratchPending == "" {
		t.Fatal("a press after the daemon's answer, before the pane arrived, asked again")
	}
	m.scratchPendingAt = time.Now().Add(-scratchPendingMax)
	if m.ToggleScratch() == nil {
		t.Fatal("a press after the backstop asked for nothing")
	}
}

// The pane that arrives is shown the way a press shows it: its group comes
// on the screen and the keyboard goes to it.
func TestScratchArrivalTakesTheKeyboard(t *testing.T) {
	m := scratchOS(t, false)
	m.Mode = WindowManagementMode
	m.rememberScratchReturn()
	m.scratchPending, m.scratchPendingAt = scratchName, time.Now()

	m.maybeFocusScratch()
	if m.Mode == TerminalMode || m.scratchPending == "" {
		t.Fatal("terminal mode came before the pane")
	}
	w := addScratch(m, scratchName, "arrived")
	m.maybeFocusScratch()
	if m.Mode != TerminalMode || m.GetFocusedWindow() != w || m.scratchPending != "" {
		t.Fatalf("mode=%v focused=%v pending=%v", m.Mode, m.GetFocusedWindow() == w, m.scratchPending)
	}
	m.ToggleScratch()
	if m.FocusedWindow != 0 || m.Mode != WindowManagementMode {
		t.Fatal("the hide after an arrival did not go back to alpha in window mode")
	}
}

func TestScratchCreateFailureIsShown(t *testing.T) {
	m := scratchOS(t, true)
	m.scratchPending, m.scratchPendingAt = scratchName, time.Now()
	m.handleScratchOpened(ScratchOpenedMsg{Err: errString("no client")})
	if m.scratchPending != "" {
		t.Fatal("a failed create stayed on its way")
	}
	if n := len(m.Notifications); n == 0 || !strings.Contains(m.Notifications[n-1].Message, "did not open") {
		t.Fatalf("notifications = %+v", m.Notifications)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// tuios attach --terminal-mode enters terminal mode on a session somebody
// already arranged, where [startup] start_in_terminal_mode is not consulted.
func TestForcedTerminalModeOnAnArrangedSession(t *testing.T) {
	m := scratchOS(t, true)
	m.Mode = WindowManagementMode
	m.sessionUnarranged = false
	m.forceTerminalMode = true
	m.applyStartupPreferences()
	if m.Mode != TerminalMode {
		t.Fatal("--terminal-mode left the client in window mode")
	}
}

// With no pane yet, terminal mode waits for the first one.
func TestForcedTerminalModeWaitsForAPane(t *testing.T) {
	m := scratchOS(t, true)
	m.Windows = nil
	m.FocusedWindow = -1
	m.Mode = WindowManagementMode
	m.forceTerminalMode = true
	m.UserConfig.Startup.OpenDefaultWindow = false
	m.UserConfig.Startup.Tiled = false
	m.applyStartupPreferences()
	if m.Mode == TerminalMode {
		t.Fatal("terminal mode with no pane to type into")
	}
	m.Windows = []*terminal.Window{{ID: "late", Workspace: 1}}
	m.FocusedWindow = 0
	m.maybeEnterPendingTerminalMode()
	if m.Mode != TerminalMode {
		t.Fatal("the first pane arrived and the client stayed in window mode")
	}
}

// The dock menu's Restore counts in RestoreMinimizedByIndex's order, which
// leaves the scratch terminal out.
func TestMinimizedPositionSkipsScratch(t *testing.T) {
	m := scratchOS(t, false)
	addScratch(m, scratchName, "s1").Minimized = true
	parked := &terminal.Window{ID: "parked", Workspace: 1, Minimized: true}
	m.Windows = append(m.Windows, parked)
	if pos := m.minimizedPosition(2); pos != 0 {
		t.Fatalf("parked is at %d, want 0", pos)
	}
	m.RestoreMinimizedByIndex(m.minimizedPosition(2))
	if parked.Minimized {
		t.Fatal("restore picked the wrong pane")
	}
}

// A layout template neither stores the scratch terminal nor gives it a slot.
func TestLayoutLeavesScratchOut(t *testing.T) {
	useTempConfig(t)
	a, _ := layoutWindow(t, "a")
	a.Workspace = 1
	m := layoutOS(a)
	if m.WorkspaceFocus == nil {
		m.WorkspaceFocus = map[int]int{}
	}
	s := addScratch(m, scratchName, "s1")
	// On the layout's workspace, which a scratch pane never is, so the test
	// reads the mark and not the workspace.
	s.Workspace = 1
	s.X, s.Y = 20, 5
	if err := SaveLayoutTemplate("with-scratch", m); err != nil {
		t.Fatal(err)
	}
	tmpls, err := LoadLayoutTemplates()
	if err != nil || len(tmpls) != 1 || len(tmpls[0].Windows) != 1 {
		t.Fatalf("templates = %+v, err %v, want one pane", tmpls, err)
	}
	ApplyLayoutTemplate(LayoutTemplate{Windows: []LayoutWindow{{X: 0, Y: 0, Width: 40, Height: 10}}}, m)
	if s.X != 20 || s.Y != 5 {
		t.Fatalf("the layout moved the scratch terminal to %d,%d", s.X, s.Y)
	}
}

// Multifocus inside a group takes the group's panes, and none of the
// workspace it is shown over.
func TestMultifocusAllInsideTheGroup(t *testing.T) {
	m := scratchOS(t, false)
	a := addScratch(m, scratchName, "s1")
	b := addScratch(m, scratchName, "s2")
	m.showScratch(1)
	m.ToggleMultifocusAll()
	if !m.MultifocusSet[a.ID] || !m.MultifocusSet[b.ID] || m.MultifocusSet[m.Windows[0].ID] {
		t.Fatalf("multifocus set = %v, want the two scratch panes only", m.MultifocusSet)
	}
}

// A state push that puts the focus on a scratch pane shows its group on this
// client, and one that puts it back on an ordinary pane hides the group. The
// focus is session state and the view is not, so this is how every client of
// a session agrees on whether a group is up.
//
// Negative control, confirmed red: drop the show branch in syncScratchView.
func TestSyncScratchViewFollowsTheFocus(t *testing.T) {
	m := scratchOS(t, false)
	w := addScratch(m, scratchName, "s1")
	m.FocusedWindow = 1 // as ApplyStateSync sets it from FocusedWindowID
	m.syncScratchView()
	if !m.InScratchView() || m.CurrentWorkspace != w.Workspace {
		t.Fatalf("a focus on a scratch pane: view=%v current=%d", m.InScratchView(), m.CurrentWorkspace)
	}
	m.FocusedWindow = 0
	m.syncScratchView()
	if m.InScratchView() || m.CurrentWorkspace != 1 {
		t.Fatalf("a focus back on alpha: view=%v current=%d", m.InScratchView(), m.CurrentWorkspace)
	}
}

// The rename key does nothing on a scratch workspace, and a show or hide
// fires no workspace-switch hook.
func TestScratchWorkspaceIsNotRenamed(t *testing.T) {
	m := scratchOS(t, false)
	addScratch(m, scratchName, "s1")
	m.showScratch(1)
	m.BeginRenameCurrentWorkspace()
	if m.Renaming() {
		t.Fatal("the rename key opened a rename of the scratch workspace")
	}
	if got := m.dockWorkspace(); got != 1 {
		t.Fatalf("reported workspace %d, want 1", got)
	}
}

// A scratch popup (from an older daemon, or with an older client attached)
// hides and shows with the key, by minimizing, whatever the session says.
//
// Negative control, confirmed red: drop the legacy branch in toggleScratch
// and the second press finds the popup on the current workspace and leaves
// it.
func TestScratchPopupFromAnOlderDaemonHides(t *testing.T) {
	m := scratchOS(t, false)
	w := &terminal.Window{ID: "old", IsPopup: true, IsFloating: true, IsScratch: true, Workspace: 1, Width: 40, Height: 10}
	m.Windows = append(m.Windows, w)
	m.FocusedWindow = 1
	m.ToggleScratch()
	if !w.Minimized || m.InScratchView() || m.FocusedWindow != 0 {
		t.Fatalf("minimized=%v view=%v focused=%d, want the popup hidden", w.Minimized, m.InScratchView(), m.FocusedWindow)
	}
	m.ToggleScratch()
	if w.Minimized || m.GetFocusedWindow() != w {
		t.Fatal("the second press did not show the popup")
	}
}

// Without scratch workspaces in the session (an older client attached), the
// scratch key does not show a group, which that client could not draw: it
// says why.
func TestScratchWorkspacesOffDoesNotShowAGroup(t *testing.T) {
	m := scratchOS(t, false)
	addScratch(m, scratchName, "s1")
	m.IsDaemonSession, m.DaemonClient = true, &session.TUIClient{}
	m.sessionScratchWSOff = true
	if m.scratchWorkspaces() {
		t.Fatal("scratch workspaces on in a session that has them off")
	}
	m.ToggleScratch()
	if m.InScratchView() {
		t.Fatal("the key showed a group with scratch workspaces off")
	}
	if n := len(m.Notifications); n == 0 || !strings.Contains(m.Notifications[n-1].Message, "older tuios client") {
		t.Fatalf("notifications = %+v", m.Notifications)
	}
}

// A layout is not saved or loaded inside a group, and tiling stays on there.
func TestLayoutAndTilingInsideTheGroup(t *testing.T) {
	useTempConfig(t)
	m := scratchOS(t, false)
	addScratch(m, scratchName, "s1")
	m.showScratch(1)
	if err := SaveLayoutTemplate("in-scratch", m); err == nil {
		t.Fatal("a layout was saved inside the group")
	}
	ApplyLayoutTemplate(LayoutTemplate{Windows: []LayoutWindow{{Width: 40, Height: 10}, {Width: 40, Height: 10}}}, m)
	if len(m.Windows) != 2 {
		t.Fatalf("a layout load inside the group made panes: %d windows", len(m.Windows))
	}
	m.ToggleAutoTiling()
	if !m.AutoTiling {
		t.Fatal("tiling went off inside the group")
	}
}
