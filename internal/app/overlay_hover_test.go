package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// hoverPanel builds one recorded overlay panel with three one-line rows, so
// the hover routing can be exercised without a full render.
func hoverPanel(kind string) overlayPanelHit {
	rows := make([]overlayRowHit, 3)
	for i := range rows {
		rows[i] = overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: 4 + i, X1: 40, Y1: 5 + i},
			Idx:  i,
		}
	}
	return overlayPanelHit{Kind: kind, OriginX: 10, OriginY: 10, Geo: overlay.Geometry{Width: 40, Height: 12}, Rows: rows}
}

// TestOverlayMotionSelectsRowUnderCursor checks the one hover mechanism moves
// each overlay's selection to the row under the cursor, and only selects: no
// overlay closes and nothing runs.
func TestOverlayMotionSelectsRowUnderCursor(t *testing.T) {
	cases := []struct {
		kind     string
		selected func(m *OS) int
	}{
		{"palette", func(m *OS) int { return m.CommandPaletteSelected }},
		{"session", func(m *OS) int { return m.SessionSwitcherSelected }},
		{"layout", func(m *OS) int { return m.LayoutPickerSelected }},
		{"settings", func(m *OS) int { return m.SettingsSelected }},
		{"quit", func(m *OS) int { return m.QuitMenuSelected }},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			m := &OS{Settings: config.Global, Width: 120, Height: 40}
			h := hoverPanel(tc.kind)
			m.OverlayHits = []overlayPanelHit{h}

			// Row 2 sits at panel-relative Y0 = 6.
			if !m.OverlayMouseMotion(h.OriginX+5, h.OriginY+6) {
				t.Fatal("motion over the panel was not consumed")
			}
			if got := tc.selected(m); got != 2 {
				t.Errorf("hover over row 2 selected %d", got)
			}
			// Back to row 0: the highlight tracks the cursor, not just forward.
			m.OverlayMouseMotion(h.OriginX+5, h.OriginY+4)
			if got := tc.selected(m); got != 0 {
				t.Errorf("hover back to row 0 selected %d", got)
			}
		})
	}
}

// TestOverlayMotionOffPanelIsNotConsumed checks motion away from every panel
// falls through (so pane hover forwarding keeps working) and selects nothing.
func TestOverlayMotionOffPanelIsNotConsumed(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.OverlayHits = []overlayPanelHit{hoverPanel("palette")}
	m.CommandPaletteSelected = 1

	if m.OverlayMouseMotion(90, 35) {
		t.Fatal("motion off the panel was consumed")
	}
	if m.CommandPaletteSelected != 1 {
		t.Errorf("motion off the panel moved the selection to %d", m.CommandPaletteSelected)
	}
}

// TestSettingsMotionThroughRealRender drives hover through the real settings
// renderer's recorded hit rects, so the row geometry and the routing agree.
func TestSettingsMotionThroughRealRender(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.ShowSettings = true
	m.renderSettingsHit()
	h := m.settingsHit()
	if len(h.Rows) < 3 {
		t.Fatal("expected settings rows")
	}

	row := h.Rows[2]
	if !m.OverlayMouseMotion(h.OriginX+4, h.OriginY+row.Rect.Y0) {
		t.Fatal("motion over the settings panel was not consumed")
	}
	if m.SettingsSelected != 2 {
		t.Errorf("hover over settings row 2 selected %d", m.SettingsSelected)
	}
}

// TestSettingsValueClickTogglesBool checks a click on a bool row's value area,
// not just the stepper rects, flips the setting (the D-note fix).
func TestSettingsValueClickTogglesBool(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.ShowSettings = true
	m.renderSettingsHit()
	h := m.settingsHit()

	// Find a bool row in the current category: it records only Inc (the whole
	// toggle) and no Dec.
	items := m.settingsCurrentItems()
	target := -1
	for _, row := range h.Rows {
		if row.Idx < len(items) && items[row.Idx].Control == controlBool {
			target = row.Idx
			break
		}
	}
	if target < 0 {
		t.Skip("no bool row on the first settings page")
	}
	row := h.Rows[target]
	before := items[target].boolVal(m)

	cx := h.OriginX + (row.Inc.X0+row.Inc.X1)/2
	cy := h.OriginY + row.Inc.Y0
	// The row writes a package global (the first bool row is shared borders,
	// which changes how every layout is measured), so flip it back rather than
	// leaving it on for whatever runs next.
	t.Cleanup(func() { m.OverlayMouseClick(cx, cy, false) })
	if handled, _ := m.OverlayMouseClick(cx, cy, false); !handled {
		t.Fatal("click on the toggle was not handled")
	}
	if got := m.settingsCurrentItems()[target].boolVal(m); got == before {
		t.Errorf("click on the bool value did not toggle it (still %v)", got)
	}
}

// TestSettingsValueClickCyclesEnum checks a click on an enum row's value text,
// between the stepper arrows, cycles the value like Enter does.
func TestSettingsValueClickCyclesEnum(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.ShowSettings = true
	m.SettingsCategory = 1 // Dock: first row is the position enum
	m.renderSettingsHit()
	h := m.settingsHit()

	items := m.settingsCurrentItems()
	target := -1
	for _, row := range h.Rows {
		if row.Idx < len(items) && items[row.Idx].Control == controlEnum && items[row.Idx].activate == nil {
			target = row.Idx
			break
		}
	}
	if target < 0 {
		t.Skip("no plain enum row on the dock settings page")
	}
	row := h.Rows[target]
	before := items[target].value(m)

	// Click dead center between the two arrow rects: on the value itself.
	cx := h.OriginX + (row.Dec.X1+row.Inc.X0)/2
	cy := h.OriginY + row.Dec.Y0
	if handled, _ := m.OverlayMouseClick(cx, cy, false); !handled {
		t.Fatal("click on the enum value was not handled")
	}
	after := m.settingsCurrentItems()[target].value(m)
	if after == before {
		t.Errorf("click on the enum value did not cycle it (still %q)", after)
	}
	// Put the global back so other tests see the default.
	m.settingsCurrentItems()[target].adjust(m, -1)
}

// TestQuitMenuRowsPerState pins the row sets the quit menu offers in each
// session state, and that the safe row is always first (the default).
func TestQuitMenuRowsPerState(t *testing.T) {
	t.Run("standalone", func(t *testing.T) {
		m := &OS{Settings: config.Global, Width: 120, Height: 40}
		m.OpenQuitMenu()
		if !m.ShowQuitMenu {
			t.Fatal("menu did not open")
		}
		want := []QuitMenuKind{QuitStandalone, QuitCancel}
		assertQuitKinds(t, m.QuitMenuItems, want)
	})

	t.Run("daemon last session", func(t *testing.T) {
		m := &OS{Settings: config.Global, Width: 120, Height: 40, IsDaemonSession: true}
		items := m.buildQuitMenuItems(nil, false)
		want := []QuitMenuKind{QuitDetach, QuitKillAndQuit}
		assertQuitKinds(t, items, want)
	})

	t.Run("daemon with other sessions", func(t *testing.T) {
		m := &OS{Settings: config.Global, Width: 120, Height: 40, IsDaemonSession: true}
		items := m.buildQuitMenuItems([]string{"other"}, false)
		want := []QuitMenuKind{QuitDetach, QuitSwitchSession, QuitKillGoNext, QuitKillAndQuit}
		assertQuitKinds(t, items, want)
	})

	t.Run("kill rows warn when a pane is busy", func(t *testing.T) {
		m := &OS{Settings: config.Global, Width: 120, Height: 40, IsDaemonSession: true}
		items := m.buildQuitMenuItems([]string{"other"}, true)
		for _, it := range items {
			isKill := it.Kind == QuitKillGoNext || it.Kind == QuitKillAndQuit
			if isKill && !it.Warn {
				t.Errorf("kill row %q not marked Warn with a busy pane", it.Label)
			}
			if !isKill && it.Warn {
				t.Errorf("non-kill row %q marked Warn", it.Label)
			}
		}
	})
}

func assertQuitKinds(t *testing.T, items []QuitMenuItem, want []QuitMenuKind) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("got %d rows %+v, want kinds %v", len(items), items, want)
	}
	for i, k := range want {
		if items[i].Kind != k {
			t.Errorf("row %d kind = %v (%q), want %v", i, items[i].Kind, items[i].Label, k)
		}
	}
}

// TestQuitMenuClickActivatesRow checks a click on a quit-menu row runs it (the
// cancel row is safe to run in a test: the menu closes and nothing quits).
func TestQuitMenuClickActivatesRow(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.OpenQuitMenu() // standalone: Quit / Cancel
	cancelIdx := m.QuitMenuIndexOfKind(QuitCancel)
	if cancelIdx < 0 {
		t.Fatal("no cancel row")
	}

	cmd := m.overlayRowClick("quit", overlayRowHit{Idx: cancelIdx}, 0, 0)
	if cmd != nil {
		t.Error("cancel returned a command")
	}
	if m.ShowQuitMenu {
		t.Error("click on the cancel row did not run it: the menu is still open")
	}
}

// TestQuitMenuSwitchRowOpensSwitcher checks the switch row closes the menu and
// opens the session switcher, matching its label.
func TestQuitMenuSwitchRowOpensSwitcher(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40, IsDaemonSession: true}
	m.QuitMenuItems = m.buildQuitMenuItems([]string{"other"}, false)
	m.ShowQuitMenu = true

	idx := m.QuitMenuIndexOfKind(QuitSwitchSession)
	if idx < 0 {
		t.Fatal("no switch row")
	}
	if cmd := m.QuitMenuActivate(idx); cmd != nil {
		t.Error("switch row returned a command")
	}
	if m.ShowQuitMenu {
		t.Error("switch row left the quit menu open")
	}
	if !m.ShowSessionSwitcher {
		t.Error("switch row did not open the session switcher")
	}
}

// TestKillSessionGoNextFallsBackToQuit checks the fallback: with no next
// session, or a switch that cannot succeed, the kill row quits outright
// instead of leaving the client on a dead session.
func TestKillSessionGoNextFallsBackToQuit(t *testing.T) {
	m := NewOS(OSOptions{})
	m.Width, m.Height = 120, 40

	if cmd := m.KillSessionGoNext(""); cmd == nil {
		t.Error("no next session: expected the quit fallback command")
	}
	if !m.QuitRequested {
		t.Error("the fallback did not record the quit intent")
	}

	// A named next session outside daemon mode cannot be switched to, so it
	// also falls back to quitting.
	m2 := NewOS(OSOptions{})
	m2.Width, m2.Height = 120, 40
	if cmd := m2.KillSessionGoNext("other"); cmd == nil {
		t.Error("failed switch: expected the quit fallback command")
	}
}

// TestSidebarMotionHighlightsRow checks motion inside the band records the
// hover and consumes the event, and motion outside clears it.
func TestSidebarMotionHighlightsRow(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40, SessionName: "alpha"}
	withSidebar(t, true, "left", 28)
	m.Settings = config.Global

	if !m.SidebarMotion(3, 5) {
		t.Fatal("motion inside the band was not consumed")
	}
	if !m.SidebarHoverActive || m.SidebarHoverX != 3 || m.SidebarHoverY != 5 {
		t.Errorf("hover not recorded: active=%v at (%d,%d)", m.SidebarHoverActive, m.SidebarHoverX, m.SidebarHoverY)
	}

	if m.SidebarMotion(60, 5) {
		t.Fatal("motion outside the band was consumed")
	}
	if m.SidebarHoverActive {
		t.Error("hover not cleared when the pointer left the band")
	}
}

// TestSidebarSessionRowRightClickOpensSessionMenu checks the sidebar session
// row's context menu carries the quit menu's lifecycle rows in daemon mode.
func TestSidebarSessionRowRightClickOpensSessionMenu(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40, SessionName: "alpha", IsDaemonSession: true}
	withSidebar(t, true, "left", 28)
	m.Settings = config.Global
	m.SidebarHits = []sidebarRowHit{{
		X0: 0, Y0: 4, X1: 28, Y1: 5,
		Kind:        sidebarRowSession,
		SessionID:   "alpha",
		WindowIndex: -1,
	}}

	if !m.SidebarClick(3, 4, true) {
		t.Fatal("right-click inside the band was not consumed")
	}
	cm := m.ContextMenu
	if cm == nil {
		t.Fatal("no context menu opened")
	}
	wantActions := map[string]bool{
		"prefix_detach":           false,
		"prefix_session_switcher": false,
		"kill_session_next":       false,
		"kill_session_quit":       false,
	}
	for _, it := range cm.Items {
		if _, ok := wantActions[it.Action]; ok {
			wantActions[it.Action] = true
		}
	}
	for action, seen := range wantActions {
		if !seen {
			t.Errorf("session menu is missing the %q row", action)
		}
	}
}

// TestQuitMenuHoverThroughRealRender drives hover through the real quit menu
// renderer, so its recorded rows route exactly like the other list overlays.
func TestQuitMenuHoverThroughRealRender(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.OpenQuitMenu()
	m.reconcileOverlayZOrder()
	content, geo, rows := m.renderQuitMenu()
	_ = content
	x, y := m.overlayOrigin("quit", geo)
	m.OverlayHits = []overlayPanelHit{{Kind: "quit", OriginX: x, OriginY: y, Z: m.overlayZ("quit"), Geo: geo, Rows: rows}}
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 quit rows, got %d", len(rows))
	}

	row := rows[1]
	if !m.OverlayMouseMotion(x+4, y+row.Rect.Y0) {
		t.Fatal("motion over the quit menu was not consumed")
	}
	if m.QuitMenuSelected != 1 {
		t.Errorf("hover over quit row 1 selected %d", m.QuitMenuSelected)
	}
}

// TestSessionSwitcherHoverDoesNotActivate pins "hover selects, click runs":
// motion alone must never switch sessions.
func TestSessionSwitcherHoverDoesNotActivate(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.ShowSessionSwitcher = true
	m.SessionSwitcherItems = []sessiontree.Node{
		{Kind: sessiontree.KindSession, ID: "alpha", Title: "alpha", IsCurrent: true},
		{Kind: sessiontree.KindSession, ID: "bravo", Title: "bravo"},
	}
	h := hoverPanel("session")
	m.OverlayHits = []overlayPanelHit{h}

	m.OverlayMouseMotion(h.OriginX+5, h.OriginY+5) // row 1: bravo
	if m.SessionSwitcherSelected != 1 {
		t.Fatalf("hover selected %d, want 1", m.SessionSwitcherSelected)
	}
	if !m.ShowSessionSwitcher {
		t.Error("hover closed the switcher")
	}
	if hasNotification(m, "Switch failed") {
		t.Error("hover attempted a session switch; it must only select")
	}
}
