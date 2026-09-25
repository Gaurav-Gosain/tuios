package main

import (
	"errors"
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

// TestCapableTerminalsAreAccepted is the other half of the capability check:
// it must not reject terminals that work, or it becomes the problem.
func TestCapableTerminalsAreAccepted(t *testing.T) {
	for _, termEnv := range []string{
		"xterm", "xterm-256color", "screen", "screen-256color",
		"tmux-256color", "alacritty", "kitty", "wezterm", "linux", "vt100",
	} {
		if err := checkTerminalCapabilities(termEnv); err != nil {
			t.Errorf("TERM=%q was rejected but is renderable: %v", termEnv, err)
		}
	}
}

// TestExplainDialErrorPassesThroughMismatch is the CLI half of the upgrade bug:
// the protocol mismatch must reach the user with its own message, not be
// rewritten into a generic connection failure.
func TestExplainDialErrorPassesThroughMismatch(t *testing.T) {
	mismatch := &session.ProtocolMismatchError{
		ClientVersion: "1.4.0",
		DaemonVersion: "0.9.0",
	}

	got := explainDialError(mismatch)

	var out *session.ProtocolMismatchError
	if !errors.As(got, &out) {
		t.Fatalf("mismatch was rewritten into %T: %v", got, got)
	}
	msg := got.Error()
	for _, want := range []string{"daemon 0.9.0", "client 1.4.0", "tuios kill-server"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

// TestClosestNameMatchesTheDaemonPolicy keeps the CLI's suggestion policy
// aligned with the daemon's, so the two never disagree about what a typo meant.
func TestClosestNameMatchesTheDaemonPolicy(t *testing.T) {
	names := []string{"work", "notes", "scratch"}

	tests := []struct {
		target string
		want   string
	}{
		{"wrok", "work"},
		{"scratchh", "scratch"},
		{"note", "notes"},
		{"work", ""},
		{"", ""},
		{"completely-different", ""},
	}
	for _, tc := range tests {
		if got := closestName(tc.target, names); got != tc.want {
			t.Errorf("closestName(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}
