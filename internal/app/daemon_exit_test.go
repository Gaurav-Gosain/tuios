package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestRemoteExitNoticeIsTheLastFrame checks what an SSH or web client leaves on
// screen when its session or its daemon goes away.
//
// The local client prints its reason to the shell after the program returns.
// These two have no shell to print to, so the reason has to be the final frame,
// and the final frame has to be outside the alternate screen or the terminal
// wipes it on the way out.
func TestRemoteExitNoticeIsTheLastFrame(t *testing.T) {
	cases := []struct {
		name   string
		reason ExitReason
		want   []string
	}{
		{"session killed", ExitSessionKilled, []string{"work", "stopped", "Connect again"}},
		{"daemon lost", ExitDaemonLost, []string{"daemon", "Connect again"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &OS{RemoteClient: true, SessionName: "work", ExitReason: tc.reason}
			notice := m.ExitNotice()
			for _, want := range tc.want {
				if !strings.Contains(notice, want) {
					t.Fatalf("the exit message never says %q: %q", want, notice)
				}
			}

			view := m.View()
			if view.AltScreen {
				t.Fatalf("the exit message is drawn on the alternate screen, so the terminal wipes it on exit")
			}
			if !strings.Contains(view.Content, strings.SplitN(notice, "\n", 2)[0]) {
				t.Fatalf("the last frame does not carry the exit message: %q", view.Content)
			}
		})
	}
}

// TestLocalExitPrintsNothingOnScreen pins the other half: the local client keeps
// its normal last frame, because its reason is printed by the caller instead.
func TestLocalExitPrintsNothingOnScreen(t *testing.T) {
	m := &OS{SessionName: "work", ExitReason: ExitDaemonLost}
	if notice := m.ExitNotice(); notice != "" {
		t.Fatalf("the local client draws an exit message it also prints: %q", notice)
	}
}

// TestNormalExitLeavesNoNotice checks a detach or a deliberate quit is silent.
func TestNormalExitLeavesNoNotice(t *testing.T) {
	m := &OS{RemoteClient: true, SessionName: "work", ExitReason: ExitNormal}
	if notice := m.ExitNotice(); notice != "" {
		t.Fatalf("a normal detach reports a failure: %q", notice)
	}
}

// TestQueuedDaemonExitReachesUpdate walks the whole path a host uses: queue from
// the read-loop goroutine, read with the command Init arms, apply in Update.
func TestQueuedDaemonExitReachesUpdate(t *testing.T) {
	m := &OS{RemoteClient: true, SessionName: "work"}
	m.DaemonExitChan = make(chan tea.Msg, daemonExitQueue)

	if dropped := m.QueueDaemonDisconnect(errors.New("EOF")); dropped {
		t.Fatalf("the disconnect was dropped with an empty queue")
	}
	msg := ListenForDaemonExit(m.DaemonExitChan)()
	if _, ok := msg.(DaemonDisconnectedMsg); !ok {
		t.Fatalf("got %T, want DaemonDisconnectedMsg", msg)
	}
	_, cmd := m.Update(msg)
	if cmd == nil || cmd() != (tea.QuitMsg{}) {
		t.Fatalf("the client did not stop")
	}
	if m.ExitReason != ExitDaemonLost {
		t.Fatalf("exit reason %v, want ExitDaemonLost", m.ExitReason)
	}
}

// TestQueuedSessionEndReachesUpdate is the same walk for the session-killed case.
func TestQueuedSessionEndReachesUpdate(t *testing.T) {
	m := &OS{RemoteClient: true, SessionName: "work"}
	m.DaemonExitChan = make(chan tea.Msg, daemonExitQueue)

	if dropped := m.QueueSessionEnded("work", "the session was terminated"); dropped {
		t.Fatalf("the session end was dropped with an empty queue")
	}
	msg := ListenForDaemonExit(m.DaemonExitChan)()
	ended, ok := msg.(SessionEndedMsg)
	if !ok {
		t.Fatalf("got %T, want SessionEndedMsg", msg)
	}
	if ended.SessionName != "work" {
		t.Fatalf("session named %q, want %q", ended.SessionName, "work")
	}
	_, cmd := m.Update(ended)
	if cmd == nil || cmd() != (tea.QuitMsg{}) {
		t.Fatalf("the client did not stop")
	}
	if m.ExitReason != ExitSessionKilled {
		t.Fatalf("exit reason %v, want ExitSessionKilled", m.ExitReason)
	}
}
