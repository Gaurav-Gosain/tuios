package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// scratchOS is a client with one pane, "alpha", on workspace 1, 120x40, and
// the default [scratch] table. daemon picks a daemon session or a local one.
// The daemon client is not connected, so a test that shows or hides the pane
// runs local: a daemon session would push the state.
func scratchOS(t *testing.T, daemon bool) *OS {
	t.Helper()
	m := dockSessionOS(t, 120, daemon)
	m.SessionName = "work"
	m.UserConfig = config.DefaultConfig()
	return m
}

// addScratch puts the scratch terminal on workspace ws, hidden or shown.
func addScratch(m *OS, ws int, hidden bool) *terminal.Window {
	w := &terminal.Window{
		ID: "scratch-pane", IsPopup: true, IsScratch: true, IsFloating: true,
		CustomName: scratchName, Workspace: ws, Minimized: hidden,
		Width: 80, Height: 30,
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
		{"shown here hides", func(m *OS) { addScratch(m, 1, false) }, scratchHide},
		{"hidden shows", func(m *OS) { addScratch(m, 1, true) }, scratchShow},
		{"hidden elsewhere shows", func(m *OS) { addScratch(m, 3, true) }, scratchShow},
		// Left open on another workspace: the press brings it here.
		{"shown elsewhere shows", func(m *OS) { addScratch(m, 2, false) }, scratchShow},
		// A popup the user opened, even one called scratch, is not the
		// scratch terminal. Only the daemon's mark makes one.
		{"other popups create", func(m *OS) {
			m.Windows = append(m.Windows,
				&terminal.Window{ID: "user-popup", IsPopup: true, CustomName: scratchName, Workspace: 1},
				&terminal.Window{ID: "plain", CustomName: scratchName, Workspace: 1, IsScratch: true})
		}, scratchCreate},
		{"session on another machine refuses", func(m *OS) { m.AttachedHost = "build" }, scratchRefuse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := scratchOS(t, true)
			tc.setup(m)
			if got := m.planScratch(); got.action != tc.want {
				t.Fatalf("plan = %+v, want action %d", got, tc.want)
			}
		})
	}
}

// A hide and a show keep the same pane: nothing closes it, and the show puts
// it on the workspace the user is on, focused, in terminal mode. The hide
// gives the focus and the mode back.
func TestScratchHideAndShowKeepThePane(t *testing.T) {
	m := scratchOS(t, false)
	m.Mode = WindowManagementMode
	w := addScratch(m, 1, true)
	alpha := m.Windows[0]

	if m.ToggleScratch() != nil {
		t.Fatal("showing a scratch terminal that exists asked the daemon for another")
	}
	if w.Minimized || m.GetFocusedWindow() != w || m.Mode != TerminalMode {
		t.Fatalf("after show: minimized=%v focused=%v mode=%v", w.Minimized, m.GetFocusedWindow() == w, m.Mode)
	}

	m.ToggleScratch()
	if !w.Minimized || m.GetFocusedWindow() != alpha || m.Mode != WindowManagementMode {
		t.Fatalf("after hide: minimized=%v focused alpha=%v mode=%v", w.Minimized, m.GetFocusedWindow() == alpha, m.Mode)
	}
	if len(m.Windows) != 2 {
		t.Fatalf("the hide closed a pane: %d windows", len(m.Windows))
	}

	// The next show is on workspace 4, and the pane follows.
	m.CurrentWorkspace = 4
	m.ToggleScratch()
	if w.Workspace != 4 || w.Minimized || m.GetFocusedWindow() != w {
		t.Fatalf("show on workspace 4: workspace=%d minimized=%v", w.Workspace, w.Minimized)
	}
}

// A user in terminal mode goes back to terminal mode on the pane they left.
func TestScratchHideKeepsTerminalMode(t *testing.T) {
	m := scratchOS(t, false)
	m.Mode = TerminalMode
	addScratch(m, 1, true)
	m.ToggleScratch()
	m.ToggleScratch()
	if m.Mode != TerminalMode || m.FocusedWindow != 0 {
		t.Fatalf("mode=%v focused=%d, want terminal mode on alpha", m.Mode, m.FocusedWindow)
	}
}

// The hidden scratch terminal is minimized, but it is offered nowhere a
// minimized pane is: no dock entry, no restore, no row in the window list or
// the rail, and it does not count as a window on the workspace. A plain
// minimized pane next to it still gets all of them, so the filter is the
// scratch mark and not the minimize.
func TestHiddenScratchIsInNoList(t *testing.T) {
	m := scratchOS(t, false)
	parked := &terminal.Window{ID: "parked", Workspace: 1, Minimized: true, MinimizeOrder: 1}
	m.Windows = append(m.Windows, parked)
	w := addScratch(m, 1, true)
	scratchAt := len(m.Windows) - 1

	items := m.getDockItems()
	if len(items) != 1 || m.Windows[items[0].WindowIndex] != parked {
		t.Fatalf("dock items = %+v, want only the parked pane", items)
	}
	for _, it := range m.GetAggregateViewItems() {
		if it.Window == w {
			t.Fatal("the window list shows the hidden scratch terminal")
		}
	}
	for _, row := range m.currentSessionInput().Windows {
		if row.ID == w.ID {
			t.Fatal("the rail shows the hidden scratch terminal")
		}
	}
	if n := m.GetWorkspaceWindowCount(1); n != 2 {
		t.Fatalf("workspace 1 counts %d windows, want 2 (alpha and parked)", n)
	}

	// Restore all, and a restore by index, leave it hidden.
	m.RestoreWindow(scratchAt)
	m.RestoreMinimizedByIndex(1)
	if !w.Minimized {
		t.Fatal("a restore showed the scratch terminal")
	}
	m.RestoreMinimizedByIndex(0)
	if parked.Minimized {
		t.Fatal("the parked pane did not restore")
	}
	if m.HasMinimizedWindows() {
		t.Fatal("HasMinimizedWindows counts the hidden scratch terminal")
	}

	// Shown, it is still no window of the layout: the list and the rail
	// leave it out, and the workspace count does not include it.
	w.Minimized = false
	for _, it := range m.GetAggregateViewItems() {
		if it.Window == w {
			t.Fatal("the window list shows the scratch terminal")
		}
	}
	for _, row := range m.currentSessionInput().Windows {
		if row.ID == w.ID {
			t.Fatal("the rail shows the shown scratch terminal")
		}
	}
	if n := m.GetWorkspaceWindowCount(1); n != 2 {
		t.Fatalf("workspace 1 counts %d windows with the scratch terminal shown, want 2", n)
	}
}

// Hidden, it cannot be cycled to or joined to multifocus.
func TestHiddenScratchIsNotCycledOrMultifocused(t *testing.T) {
	m := scratchOS(t, false)
	addScratch(m, 1, true)
	for _, i := range m.cyclableWindows() {
		if isScratch(m.Windows[i]) {
			t.Fatal("the focus cycle reaches the scratch terminal")
		}
	}
	m.ToggleMultifocus(1)
	if m.MultifocusSet[m.Windows[1].ID] {
		t.Fatal("the scratch terminal joined multifocus")
	}
}

// In a daemon session the first press asks the daemon for the pane, in the
// focused pane's folder or the home folder, and a second press while that
// is on its way does nothing, so a double press cannot make two.
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
	if m.ToggleScratch() != nil || !m.scratchPending {
		t.Fatal("a press after the daemon's answer, before the pane arrived, asked again")
	}
	m.scratchPendingAt = time.Now().Add(-scratchPendingMax)
	if m.ToggleScratch() == nil {
		t.Fatal("a press after the backstop asked for nothing")
	}
}

// The pane that arrives is shown the way a press shows it.
func TestScratchArrivalTakesTheKeyboard(t *testing.T) {
	m := scratchOS(t, false)
	m.Mode = WindowManagementMode
	m.rememberScratchReturn()
	m.scratchPending, m.scratchPendingAt = true, time.Now()

	m.maybeFocusScratch()
	if m.Mode == TerminalMode || !m.scratchPending {
		t.Fatal("terminal mode came before the pane")
	}
	w := addScratch(m, 1, false)
	m.maybeFocusScratch()
	if m.Mode != TerminalMode || m.GetFocusedWindow() != w || m.scratchPending {
		t.Fatalf("mode=%v focused=%v pending=%v", m.Mode, m.GetFocusedWindow() == w, m.scratchPending)
	}
	m.ToggleScratch()
	if m.FocusedWindow != 0 || m.Mode != WindowManagementMode {
		t.Fatal("the hide after an arrival did not go back to alpha in window mode")
	}
}

func TestScratchCreateFailureIsShown(t *testing.T) {
	m := scratchOS(t, true)
	m.scratchPending, m.scratchPendingAt = true, time.Now()
	m.handleScratchOpened(ScratchOpenedMsg{Err: errString("no client")})
	if m.scratchPending {
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

// Closing the scratch terminal by hand hides it. Esc in window mode
// (CloseFocusedPopup) and the close key, button and palette entry
// (CloseWindowByHand) all keep the shell, so a running job survives. Another
// popup still closes.
func TestScratchCloseByHandHides(t *testing.T) {
	m := scratchOS(t, false)
	w := addScratch(m, 1, true)
	m.ToggleScratch()
	if !m.CloseFocusedPopup() {
		t.Fatal("esc did nothing on the shown scratch terminal")
	}
	if m.scratchIndex() < 0 || !w.Minimized {
		t.Fatalf("esc closed the scratch terminal: present=%v minimized=%v", m.scratchIndex() >= 0, w.Minimized)
	}
	m.ToggleScratch()
	m.CloseWindowByHand(m.scratchIndex())
	if m.scratchIndex() < 0 || !w.Minimized {
		t.Fatal("the close key closed the scratch terminal")
	}
	if m.FocusedWindow != 0 {
		t.Fatalf("focus after the hide = %d, want alpha", m.FocusedWindow)
	}
}

// Any focus on the hidden scratch terminal shows it first, so keys never go
// into a pane nobody can see. The rail, the Inbox, a notification and
// focus-window all focus through FocusWindow.
func TestFocusOnHiddenScratchShowsIt(t *testing.T) {
	m := scratchOS(t, false)
	m.Mode = WindowManagementMode
	w := addScratch(m, 3, true)
	m.FocusWindow(1)
	if w.Minimized || w.Workspace != 1 || m.CurrentWorkspace != 1 || m.GetFocusedWindow() != w {
		t.Fatalf("minimized=%v workspace=%d current=%d focused=%v", w.Minimized, w.Workspace, m.CurrentWorkspace, m.GetFocusedWindow() == w)
	}
	m.ToggleScratch()
	if m.FocusedWindow != 0 || m.Mode != WindowManagementMode {
		t.Fatal("the hide after a focus jump did not go back to alpha")
	}
}

// The dock menu's Restore counts in RestoreMinimizedByIndex's order, which
// leaves the scratch terminal out.
func TestMinimizedPositionSkipsScratch(t *testing.T) {
	m := scratchOS(t, false)
	addScratch(m, 1, true)
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

// The created and closed notices do not count the scratch terminal.
func TestNoticeCountLeavesScratchOut(t *testing.T) {
	m := scratchOS(t, false)
	addScratch(m, 1, false)
	if n := m.windowCountForNotice(); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

// A show reads [scratch] again, so a size set since the last show applies.
func TestScratchShowReadsTheSize(t *testing.T) {
	m := scratchOS(t, false)
	// A window with an emulator, so the box is really resized.
	w := newTestWindow(t, "scratch-pane", 80, 30)
	w.IsPopup, w.IsScratch, w.IsFloating, w.Minimized, w.Workspace = true, true, true, true, 1
	m.Windows = append(m.Windows, w)
	m.UserConfig.Scratch.Width, m.UserConfig.Scratch.Height = "50", "10"
	m.ToggleScratch()
	if w.PopupWidth != "50" || w.PopupHeight != "10" {
		t.Fatalf("size = %s x %s, want 50 x 10", w.PopupWidth, w.PopupHeight)
	}
	_, _, width, height := m.popupRect(w)
	if w.Width != width || w.Height != height || width != 50 || height != 10 {
		t.Fatalf("box = %dx%d, rect %dx%d, want 50x10", w.Width, w.Height, width, height)
	}
}

// A layout template neither stores the scratch terminal nor gives it a slot.
func TestLayoutLeavesScratchOut(t *testing.T) {
	useTempConfig(t)
	a, _ := layoutWindow(t, "a")
	a.Workspace = 1
	m := layoutOS(a)
	s := addScratch(m, 1, false)
	s.X, s.Y = 20, 5
	if err := SaveLayoutTemplate("with-scratch", m); err != nil {
		t.Fatal(err)
	}
	tmpls, err := LoadLayoutTemplates()
	if err != nil || len(tmpls) != 1 || len(tmpls[0].Windows) != 1 {
		t.Fatalf("templates = %+v, err %v, want one pane", tmpls, err)
	}
	ApplyLayoutTemplate(LayoutTemplate{Windows: []LayoutWindow{{X: 0, Y: 0, Width: 40, Height: 10}}}, m)
	// The load focuses a pane of the layout, which hides the scratch
	// terminal (it is a dropdown). It keeps its own box.
	if s.X != 20 || s.Y != 5 {
		t.Fatalf("the layout moved the scratch terminal to %d,%d", s.X, s.Y)
	}
}

// The scratch terminal is a dropdown: a focus that goes to another pane, by
// any path, hides it.
func TestFocusElsewhereHidesScratch(t *testing.T) {
	m := scratchOS(t, false)
	w := addScratch(m, 1, true)
	m.ToggleScratch()
	m.FocusWindow(0)
	if !w.Minimized || m.FocusedWindow != 0 {
		t.Fatalf("minimized=%v focused=%d, want hidden and alpha focused", w.Minimized, m.FocusedWindow)
	}
}

// The scratch terminal never enters the tiling: toggling floating on it does
// nothing, and a retile keeps it out of the BSP tree.
func TestScratchStaysOutOfTheTiling(t *testing.T) {
	m := scratchOS(t, false)
	m.AutoTiling = true
	w := addScratch(m, 1, true)
	m.ToggleScratch()
	m.ToggleFloating()
	if !w.IsFloating {
		t.Fatal("toggle floating tiled the scratch terminal")
	}
	m.TileAllWindows()
	if tree := m.WorkspaceTrees[1]; tree != nil && tree.HasWindow(m.GetWindowIntID(w.ID)) {
		t.Fatal("the BSP tree holds the scratch terminal")
	}
}

// A focus that hides the scratch terminal gives back the mode the show found.
func TestParkRestoresTheMode(t *testing.T) {
	m := scratchOS(t, false)
	m.Mode = WindowManagementMode
	addScratch(m, 1, true)
	m.ToggleScratch()
	if m.Mode != TerminalMode {
		t.Fatal("the show did not enter terminal mode")
	}
	m.FocusWindow(0)
	if m.Mode != WindowManagementMode {
		t.Fatalf("mode after a focus elsewhere = %v, want window mode", m.Mode)
	}
}

// A popup opened from inside the scratch terminal leaves it on the screen.
func TestPopupOverScratchKeepsItShown(t *testing.T) {
	m := scratchOS(t, false)
	w := addScratch(m, 1, true)
	m.ToggleScratch()
	m.Windows = append(m.Windows, &terminal.Window{ID: "picker", IsPopup: true, IsFloating: true, Workspace: 1})
	m.FocusWindow(2)
	if w.Minimized {
		t.Fatal("a popup over the scratch terminal hid it")
	}
}

// The scratch terminal's pane menu dims Zoom and the splits.
func TestScratchPaneMenuDimsZoomAndSplit(t *testing.T) {
	m := scratchOS(t, false)
	m.AutoTiling = true
	addScratch(m, 1, false)
	_, items := m.paneMenu(1)
	for _, it := range items {
		switch it.Action {
		case "toggle_zoom", "split_vertical", "split_horizontal":
			if !it.Dim {
				t.Errorf("%s is live on the scratch terminal", it.Action)
			}
		case "minimize_window":
			if it.Dim {
				t.Error("minimize is dimmed on the scratch terminal")
			}
		}
	}
}
