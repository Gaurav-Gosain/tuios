package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestNoDaemonMessageNeverMisnamesTheFix pins the bug this message was reported
// for: with sessions saved on disk it told the user to run 'tuios new', which
// makes a new session instead of bringing back the ones they had.
func TestNoDaemonMessageNeverMisnamesTheFix(t *testing.T) {
	for _, state := range []session.DaemonState{session.DaemonAbsent, session.DaemonStaleSocket} {
		msg := session.DaemonDiagnosis{State: state, Restorable: 3}.Explain()
		if strings.Contains(msg, "tuios new") {
			t.Errorf("message points at 'tuios new' while 3 sessions are saved:\n%s", msg)
		}
		if !strings.Contains(msg, "3 saved sessions") {
			t.Errorf("message does not say how many sessions are saved:\n%s", msg)
		}
	}
}
