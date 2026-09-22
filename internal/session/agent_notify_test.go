package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestNotifyCallbackParksTheNotification checks the daemon's emulator hands a
// desktop notification to the read goroutine instead of dropping it, for each
// of the three sequences.
func TestNotifyCallbackParksTheNotification(t *testing.T) {
	sess, winID := bareSessionWithWindow(t)
	pty := sess.GetPTY(ptyIDOfWindow(t, sess, winID))
	for _, tc := range []struct {
		name, seq, title, body string
	}{
		{"osc 9", "\x1b]9;Approval requested: go test\x07", "", "Approval requested: go test"},
		{"osc 777", "\x1b]777;notify;Claude Code;Claude needs your permission to use Bash\x07", "Claude Code", "Claude needs your permission to use Bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feedVT(t, pty, tc.seq)
			n, ok := pty.takeAgentNotify()
			if !ok {
				t.Fatal("no notification parked")
			}
			if n.title != tc.title || n.body != tc.body {
				t.Fatalf("parked %+v, want title %q body %q", n, tc.title, tc.body)
			}
			if _, again := pty.takeAgentNotify(); again {
				t.Fatal("a taken notification came back")
			}
		})
	}
	// Progress is OSC 9 too, and must not read as a notification.
	feedVT(t, pty, "\x1b]9;4;1;50\x07")
	if _, ok := pty.takeAgentNotify(); ok {
		t.Fatal("an OSC 9;4 progress report was parked as a notification")
	}
}

// TestNotificationDrivesAgentState checks a notification from an attributed
// pane becomes its state through the harness's rules, and one from a pane no
// tier attributed changes nothing.
func TestNotificationDrivesAgentState(t *testing.T) {
	reg := bundledRegistry(t)

	t.Run("codex approval", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "codex", AgentStateWorking)
		if !sess.applyAgentNotify(ptyID, paneNotification{body: "Approval requested: go test ./..."}, reg) {
			t.Fatal("no rule matched")
		}
		w := windowStateOf(t, sess, winID)
		if w.AgentState != AgentStateNeedsInput {
			t.Fatalf("state = %q, want needs_input", w.AgentState)
		}
		if w.AgentMessage != "approval: Approval requested: go test ./..." {
			t.Fatalf("message = %q", w.AgentMessage)
		}
		if src := sess.agentClaimFor(winID).source; src != AgentSourceOSC {
			t.Fatalf("source = %q, want osc", src)
		}
	})

	t.Run("codex turn complete", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "codex", AgentStateWorking)
		sess.applyAgentNotify(ptyID, paneNotification{body: "All tests pass."}, reg)
		if got := agentStateOf(t, sess, winID); got != AgentStateDone {
			t.Fatalf("state = %q, want done", got)
		}
	})

	t.Run("unattributed pane", func(t *testing.T) {
		sess, winID := bareSessionWithWindow(t)
		ptyID := ptyIDOfWindow(t, sess, winID)
		if sess.applyAgentNotify(ptyID, paneNotification{body: "Approval requested: rm -rf /"}, reg) {
			t.Fatal("a pane with no harness matched a rule")
		}
		if got := agentStateOf(t, sess, winID); got != AgentStateNone {
			t.Fatalf("state = %q, want none", got)
		}
	})

	t.Run("a harness reporting for itself outranks it", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "codex", AgentStateWorking)
		if _, _, err := sess.ApplyAgentReport(winID, AgentReport{State: AgentStateWorking}); err != nil {
			t.Fatal(err)
		}
		sess.applyAgentNotify(ptyID, paneNotification{body: "Approval requested: go test"}, reg)
		if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
			t.Fatalf("state = %q, want working", got)
		}
	})
}

// codexWorkingScreen is a Codex turn in progress, as the working rule reads it.
const codexWorkingScreen = "\xe2\x80\xa2 Working (12s \xe2\x80\xa2 esc to interrupt)\r\n\r\n" +
	"\xe2\x80\xba Summarize recent commits\r\n\r\n" +
	"  ? for shortcuts        98% context left\r\n"

// TestNotificationClaimGoesStale checks a notification's claim gives way to a
// look at the pane once the pane has written again: the approval was given and
// the agent went back to work, and nothing would ever repeat the notification
// to take it back.
func TestNotificationClaimGoesStale(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "codex", AgentStateWorking)
	pty := sess.GetPTY(ptyID)
	feedVT(t, pty, clearScreen+codexWorkingScreen)
	pty.lastOutput.Store(time.Now().Add(-time.Second).UnixNano())
	sess.applyAgentNotify(ptyID, paneNotification{body: "Approval requested: go test"}, reg)

	// A look before the pane writes again describes the screen the
	// notification was sent over, and must not undo it.
	sess.scanPaneForAgent(ptyID, reg)
	if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
		t.Fatalf("state = %q before the pane wrote again, want needs_input", got)
	}

	time.Sleep(5 * time.Millisecond)
	paintPane(t, pty, codexWorkingScreen)
	sess.scanPaneForAgent(ptyID, reg)
	if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
		t.Fatalf("state = %q after the pane went back to work, want working", got)
	}
}

// TestNotificationReachesTheDaemon is the end to end: a pane that prints an
// OSC 9 notification, in a session nobody is attached to, raises a
// notification event and moves the pane's agent state, which is what the
// after-agent-state hook fires on.
func TestNotificationReachesTheDaemon(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "notify")
	winID := sess.GetState().Windows[0].ID
	if _, _, err := sess.ApplyAgentReport(winID, AgentReport{State: AgentStateWorking, Source: AgentSourceDetect, Harness: "codex"}); err != nil {
		t.Fatal(err)
	}

	sub := dialVerb(t, sp)
	ack := sub.call(t, `{"id":1,"verb":"subscribe","params":{"session":"notify","types":["notification","agent-state"]}}`)
	result(t, ack)

	ptyID := sess.GetState().Windows[0].PTYID
	if _, err := sess.GetPTY(ptyID).Write([]byte("printf '\\033]9;Approval requested: make\\007'\n")); err != nil {
		t.Fatal(err)
	}

	sawNotify, sawState := false, false
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for (!sawNotify || !sawState) && time.Now().Before(deadline) {
		_ = sub.conn.SetReadDeadline(deadline)
		line, err := sub.r.ReadBytes('\n')
		if err != nil {
			break
		}
		var ev map[string]any
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if e, ok := ev["event"].(map[string]any); ok {
			ev = e
		}
		switch ev["type"] {
		case EventNotification:
			if strings.Contains(ev["body"].(string), "Approval requested: make") {
				sawNotify = true
			}
		case EventAgentState:
			if ev["state"] == "needs_input" {
				sawState = true
			}
		}
	}
	if !sawNotify {
		t.Error("no notification event")
	}
	if !sawState {
		t.Errorf("no needs_input agent-state event; state is %q", agentStateOf(t, sess, winID))
	}
}

// windowStateOf returns a copy of a window's state.
func windowStateOf(t *testing.T, sess *Session, windowID string) WindowState {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w
		}
	}
	t.Fatalf("window %s not found", windowID)
	return WindowState{}
}
