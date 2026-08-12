package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sessionCloseOS is a session of three panes, agent states left to the caller.
func sessionCloseOS(t testing.TB) *OS {
	t.Helper()
	m := dockSessionOS(t, 160, true)
	for _, id := range []string{"bravo", "gamma"} {
		w := newTestWindow(t, id, 60, 20)
		w.Workspace = 1
		m.Windows = append(m.Windows, w)
	}
	return m
}

// sessionCloseText renders the dialog and returns its rows with the styling
// taken off, which is what the user is actually reading.
func sessionCloseText(t *testing.T, m *OS) string {
	t.Helper()
	content, _, _ := m.renderSessionClose()
	return stripANSIForTrace(content)
}

// TestSessionCloseDialogCountsLiveState is the reason the dialog is worth the
// keystroke: it says what would die, read off the session as it draws, not a
// warning written once and shown forever.
func TestSessionCloseDialogCountsLiveState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		states []string
		want   string
	}{
		{"idle", []string{"", "", ""}, "3 panes, no agent working"},
		{"one working", []string{"", "working", ""}, "3 panes, 1 agent still working"},
		{"two working", []string{"working", "working", ""}, "3 panes, 2 agents still working"},
		{"one blocked", []string{"", "needs_input", ""}, "3 panes, 1 agent waiting on you"},
		{"both", []string{"working", "needs_input", ""}, "3 panes, 1 agent still working, 1 waiting on you"},
		{"idle agents are not working", []string{"idle", "done", "errored"}, "3 panes, no agent working"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sessionCloseOS(t)
			for i, s := range tc.states {
				m.Windows[i].AgentState = s
			}
			if got := m.sessionToll().Line(); got != tc.want {
				t.Errorf("toll line = %q, want %q", got, tc.want)
			}
			// And it reaches the frame uncut: the agent call-out is the part
			// that would be truncated, and it is the part that matters.
			if body := sessionCloseText(t, m); !strings.Contains(body, tc.want) {
				t.Errorf("the dialog never drew %q:\n%s", tc.want, body)
			}
		})
	}
}

// TestSessionCloseDialogNamesTheSession checks the question addresses the
// session by name where the daemon gave it one.
func TestSessionCloseDialogNamesTheSession(t *testing.T) {
	m := sessionCloseOS(t)
	if body := sessionCloseText(t, m); !strings.Contains(body, "Close session-1?") {
		t.Errorf("the dialog never named the session:\n%s", body)
	}

	m.SessionName = ""
	if body := sessionCloseText(t, m); !strings.Contains(body, "Close this session?") {
		t.Errorf("an unnamed session got no question:\n%s", body)
	}
}

// TestSessionCloseOpensOnCancel checks the destructive row is never what enter
// would run on the frame the dialog first draws.
func TestSessionCloseOpensOnCancel(t *testing.T) {
	m := sessionCloseOS(t)
	m.SessionCloseSelected = SessionCloseRowClose
	m.OpenSessionClose()
	if m.SessionCloseSelected != SessionCloseRowCancel {
		t.Fatalf("the dialog opened on row %d, want the cancel row %d", m.SessionCloseSelected, SessionCloseRowCancel)
	}
	if !m.ShowSessionClose {
		t.Fatal("OpenSessionClose did not raise the dialog")
	}
}

// TestSessionCloseCancelChangesNothing is the other half of a confirmation
// being worth having: saying no has to be free.
func TestSessionCloseCancelChangesNothing(t *testing.T) {
	m := sessionCloseOS(t)
	before := len(m.Windows)
	m.OpenSessionClose()

	if cmd := m.SessionCloseActivate(SessionCloseRowCancel); cmd != nil {
		t.Error("cancelling returned a command; it must do nothing at all")
	}
	if m.ShowSessionClose {
		t.Error("cancelling left the dialog up")
	}
	if m.QuitRequested {
		t.Error("cancelling asked to quit")
	}
	if len(m.Windows) != before {
		t.Errorf("cancelling changed the pane count to %d, want %d", len(m.Windows), before)
	}

	// Click-away is the same answer as esc.
	m.OpenSessionClose()
	m.closeOverlay("sessionclose")
	if m.ShowSessionClose || m.QuitRequested {
		t.Error("clicking away from the dialog did something")
	}
}

// TestSessionCloseIsAlwaysConfirmed checks the dialog is raised whatever the
// session holds. A dialog that only shows up when something is running is one
// people learn to click through, and then it is not there when it counts.
func TestSessionCloseIsAlwaysConfirmed(t *testing.T) {
	idle := dockSessionOS(t, 160, true)
	idle.Windows = []*terminal.Window{}
	idle.OpenSessionClose()
	if !idle.ShowSessionClose {
		t.Fatal("an empty session skipped the confirmation")
	}
	if body := sessionCloseText(t, idle); !strings.Contains(body, "0 panes") {
		t.Errorf("the empty session's dialog said nothing about it:\n%s", body)
	}

	busy := sessionCloseOS(t)
	busy.Windows[0].AgentState = "working"
	busy.OpenSessionClose()
	if !busy.ShowSessionClose {
		t.Fatal("a busy session skipped the confirmation")
	}
}

// TestSessionCloseRowsAreClickableWhereTheyAreDrawn checks the dialog's own hit
// rects line up with the rows it drew, so a click answers the question the user
// is pointing at.
func TestSessionCloseRowsAreClickableWhereTheyAreDrawn(t *testing.T) {
	m := sessionCloseOS(t)
	content, geo, rows := m.renderSessionClose()
	if len(rows) != sessionCloseRowCount {
		t.Fatalf("the dialog recorded %d rows, want %d", len(rows), sessionCloseRowCount)
	}

	lines := strings.Split(stripANSIForTrace(content), "\n")
	for _, tc := range []struct {
		idx   int
		label string
	}{
		{SessionCloseRowCancel, "Cancel"},
		{SessionCloseRowClose, "Close session"},
	} {
		r := rows[tc.idx]
		if r.Rect.Y0 < 0 || r.Rect.Y0 >= len(lines) {
			t.Fatalf("row %d claims line %d of %d", tc.idx, r.Rect.Y0, len(lines))
		}
		if !strings.Contains(lines[r.Rect.Y0], tc.label) {
			t.Errorf("row %d's rect points at %q, which is not the %q row", tc.idx, lines[r.Rect.Y0], tc.label)
		}
		if r.Rect.X1 != geo.Width {
			t.Errorf("row %d spans %d columns, want the dialog's %d", tc.idx, r.Rect.X1, geo.Width)
		}
	}
}
