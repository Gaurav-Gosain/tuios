package app

import "testing"

// Quitting a daemon session kills it, and the daemon announces the session
// ending and the connection dropping back to the client that asked. Those
// announcements can arrive before the program finishes quitting, so the client
// has to tell its own quit apart from a session killed from somewhere else.
// Getting that wrong made a deliberate ctrl+b q print an error telling the user
// their session had been terminated unexpectedly.

func TestDeliberateQuitExitsNormally(t *testing.T) {
	tests := []struct {
		name string
		msg  any
	}{
		{"session ended announcement", SessionEndedMsg{SessionName: "s", Reason: "killed"}},
		{"daemon disconnect announcement", DaemonDisconnectedMsg{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &OS{QuitRequested: true}
			if _, _ = m.Update(tc.msg); m.ExitReason != ExitNormal {
				t.Errorf("after a deliberate quit, ExitReason = %v, want ExitNormal", m.ExitReason)
			}
		})
	}
}

func TestUnexpectedTerminationKeepsDiagnostic(t *testing.T) {
	t.Run("session killed elsewhere", func(t *testing.T) {
		m := &OS{}
		if _, _ = m.Update(SessionEndedMsg{SessionName: "s", Reason: "killed"}); m.ExitReason != ExitSessionKilled {
			t.Errorf("ExitReason = %v, want ExitSessionKilled", m.ExitReason)
		}
	})
	t.Run("daemon lost", func(t *testing.T) {
		m := &OS{}
		if _, _ = m.Update(DaemonDisconnectedMsg{}); m.ExitReason != ExitDaemonLost {
			t.Errorf("ExitReason = %v, want ExitDaemonLost", m.ExitReason)
		}
	})
}

// QuitSession is the single deliberate quit path, so the flag it sets is what
// every quit keybinding, the confirmation dialog, and the dialog's mouse
// handler all depend on.
func TestQuitSessionRecordsIntent(t *testing.T) {
	m := &OS{}
	if m.QuitRequested {
		t.Fatal("QuitRequested set before quitting")
	}
	m.QuitSession()
	if !m.QuitRequested {
		t.Error("QuitSession did not record that the quit was deliberate")
	}
}
