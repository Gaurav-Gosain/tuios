package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Quitting a daemon session kills it, and the daemon announces the session
// ending and the connection dropping back to the client that asked. Those
// announcements can arrive before the program finishes quitting, so the client
// has to tell its own quit apart from a session killed from somewhere else.
// Getting that wrong made a deliberate ctrl+b q print an error telling the user
// their session had been terminated unexpectedly.
func TestExitReasonTellsAQuitFromAKill(t *testing.T) {
	for _, tc := range []struct {
		name          string
		quitRequested bool
		msg           any
		want          ExitReason
	}{
		{"quit, then session ended", true, SessionEndedMsg{SessionName: "s", Reason: "killed"}, ExitNormal},
		{"quit, then daemon disconnect", true, DaemonDisconnectedMsg{}, ExitNormal},
		{"session killed elsewhere", false, SessionEndedMsg{SessionName: "s", Reason: "killed"}, ExitSessionKilled},
		{"daemon lost", false, DaemonDisconnectedMsg{}, ExitDaemonLost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &OS{Settings: config.Global, QuitRequested: tc.quitRequested}
			if _, _ = m.Update(tc.msg); m.ExitReason != tc.want {
				t.Errorf("ExitReason = %v, want %v", m.ExitReason, tc.want)
			}
		})
	}
}
