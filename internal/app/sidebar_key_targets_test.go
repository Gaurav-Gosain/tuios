package app

import (
	"strings"
	"testing"
)

// The rail's keys act on the row the cursor is on. That was true of rename and
// accent and untrue of the kill key, which forced the row's kind to session
// before opening the menu: on a terminal row it opened the session's menu, and
// on a footer control it opened the attached session's menu. These pin the rule
// for every key that names a target, so the next one added has a table to fail
// against.

// railCursorOnto parks the rail cursor on the first nav row of a kind.
func railCursorOnto(t *testing.T, m *OS, kind sidebarRowKind) {
	t.Helper()
	i := m.sidebarFirstRowOfKind(kind)
	if i < 0 {
		t.Fatalf("the fixture drew no row of kind %v", kind)
	}
	m.sidebarSetCursor(i)
}

// TestRailKillKeyOpensTheCursorRowsMenu is the bug itself: x on a terminal row
// used to show the session's menu.
func TestRailKillKeyOpensTheCursorRowsMenu(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       sidebarRowKind
		wantTarget ContextMenuTarget
		wantWarn   string
	}{
		// "Kill session, go to next" comes first but is dimmed with no daemon
		// listing behind it, and selectWarn steps over dimmed rows the way the
		// arrow keys do.
		{"a session row", sidebarRowSession, CtxTargetDesktop, "Kill session and quit"},
		{"a terminal row", sidebarRowWindow, CtxTargetPane, "Close pane"},
		{"an agent row", sidebarRowAgent, CtxTargetPane, "Close pane"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, tree := sidebarMultiSessionOS(t, 120, 40)
			m.IsDaemonSession = true
			m.SidebarFocused = true
			m.sidebarPanelLinesForTree(tree)
			railCursorOnto(t, m, tc.kind)
			row, _ := m.sidebarCursorRow()

			m.SidebarOpenCursorMenu(true)

			cm := m.ContextMenu
			if cm == nil {
				t.Fatal("the kill key opened no menu")
			}
			if cm.Target != tc.wantTarget {
				t.Errorf("menu target = %v, want %v for %s", cm.Target, tc.wantTarget, tc.name)
			}
			// The menu opens on the row's own destructive action, which is what is
			// left of the key meaning "kill" once the row picks the menu.
			if cm.Selected < 0 || cm.Selected >= len(cm.Items) {
				t.Fatalf("selection %d is off the menu", cm.Selected)
			}
			if got := cm.Items[cm.Selected].Label; got != tc.wantWarn {
				t.Errorf("opened on %q, want %q", got, tc.wantWarn)
			}
			if !cm.Items[cm.Selected].Warn {
				t.Error("the selected row is not the destructive one")
			}
			// A pane menu must be about the pane the cursor named, not whichever
			// one happened to be focused.
			if tc.wantTarget == CtxTargetPane {
				if got := m.Windows[cm.WindowIndex].ID; got != row.WindowID {
					t.Errorf("menu is about pane %s, want the cursor row's %s", got, row.WindowID)
				}
			}
			if tc.wantTarget == CtxTargetDesktop && cm.SessionID != row.SessionID {
				t.Errorf("menu is about session %q, want the cursor row's %q", cm.SessionID, row.SessionID)
			}
		})
	}
}

// TestRailMenuKeyAndKillKeyAgreeOnTheRow: the two keys differ only in where the
// selection lands, never in what the menu is about.
func TestRailMenuKeyAndKillKeyAgreeOnTheRow(t *testing.T) {
	for _, kind := range []sidebarRowKind{sidebarRowSession, sidebarRowWindow, sidebarRowAgent} {
		m, tree := sidebarMultiSessionOS(t, 120, 40)
		m.IsDaemonSession = true
		m.SidebarFocused = true
		m.sidebarPanelLinesForTree(tree)
		railCursorOnto(t, m, kind)

		m.SidebarOpenCursorMenu(false)
		plain := m.ContextMenu
		m.SidebarOpenCursorMenu(true)
		killed := m.ContextMenu

		if plain.Target != killed.Target || plain.WindowIndex != killed.WindowIndex || plain.SessionID != killed.SessionID {
			t.Errorf("kind %v: the two keys opened menus about different things", kind)
		}
		if len(plain.Items) != len(killed.Items) {
			t.Errorf("kind %v: the two keys opened menus of different shapes", kind)
		}
	}
}

// TestRailKeysRefuseOnARowThatIsNotTheirs pins the audit's third column: which
// keys refuse on the wrong row kind rather than acting on something else. The
// rail's controls (the collapse toggle, the agents header's tokens) name no
// session and no pane, so a key that acts on a row has nothing to act on there.
func TestRailKeysRefuseOnARowThatIsNotTheirs(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(m *OS)
		want string
	}{
		{"the menu key", func(m *OS) { m.SidebarOpenCursorMenu(false) }, "Nothing on this row to act on"},
		{"the kill key", func(m *OS) { m.SidebarOpenCursorMenu(true) }, "Nothing on this row to act on"},
		{"rename", func(m *OS) { m.SidebarRenameCursor() }, "Nothing on this row to rename"},
		{"accent", func(m *OS) { m.SidebarAccentCursor() }, "Accents work on a pane or a session row"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, tree := sidebarMultiSessionOS(t, 120, 40)
			m.IsDaemonSession = true
			m.SidebarFocused = true
			m.sidebarPanelLinesForTree(tree)
			railCursorOnto(t, m, sidebarRowCollapse)

			tc.run(m)

			if m.ContextMenu != nil {
				t.Error("a rail control opened a menu about something else")
			}
			if len(m.Notifications) == 0 {
				t.Fatalf("%s said nothing on a row it cannot act on", tc.name)
			}
			if got := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(got, tc.want) {
				t.Errorf("%s said %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestRailReorderRefusesOffASessionRow is the same rule for the key that was
// already following it, so a later refactor cannot quietly drop the guard.
func TestRailReorderRefusesOffASessionRow(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.SidebarFocused = true
	m.sidebarPanelLinesForTree(tree)
	railCursorOnto(t, m, sidebarRowWindow)

	before := append([]string(nil), m.SidebarSessionIDs...)
	m.SidebarReorderCursor(1)
	m.sidebarPanelLinesForTree(tree)

	for i := range before {
		if m.SidebarSessionIDs[i] != before[i] {
			t.Fatalf("reorder on a terminal row moved the sessions: %v -> %v", before, m.SidebarSessionIDs)
		}
	}
}
