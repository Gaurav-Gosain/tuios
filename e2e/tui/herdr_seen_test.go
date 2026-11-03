package tuie2e

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// herdrTry sends one request to the herdr socket from the test process and
// returns the result, or the error object when the call failed.
func herdrTry(t *testing.T, base, method string, params any) (result, failure map[string]any) {
	t.Helper()
	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatalf("dial the herdr socket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	line, _ := json.Marshal(map[string]any{"id": "e2e", "method": method, "params": params})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var out map[string]any
	if err := json.Unmarshal(reply, &out); err != nil {
		t.Fatalf("%s: %q: %v", method, reply, err)
	}
	if e, ok := out["error"].(map[string]any); ok {
		return nil, e
	}
	return out["result"].(map[string]any), nil
}

// herdrStatusStream is an events.subscribe stream of one pane's
// pane.agent_status_changed events.
type herdrStatusStream struct {
	conn net.Conn
	r    *bufio.Reader
	log  strings.Builder
}

func subscribeHerdrStatus(t *testing.T, base, pane string) *herdrStatusStream {
	t.Helper()
	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	req, _ := json.Marshal(map[string]any{"id": "es", "method": "events.subscribe", "params": map[string]any{
		"subscriptions": []any{map[string]any{"type": "pane.agent_status_changed", "pane_id": pane}},
	}})
	_ = conn.SetDeadline(time.Now().Add(uiTimeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatal(err)
	}
	s := &herdrStatusStream{conn: conn, r: bufio.NewReader(conn)}
	if ack, _ := s.r.ReadString('\n'); !strings.Contains(ack, "subscription_started") {
		t.Fatalf("subscribe ack %q", ack)
	}
	return s
}

// next waits for the stream's next status and fails unless it is want.
func (s *herdrStatusStream) next(t *testing.T, want, why string) {
	t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(uiTimeout))
	line, err := s.r.ReadString('\n')
	if err != nil {
		t.Fatalf("%s: no pane.agent_status_changed %s arrived: %v\nstream so far:\n%s", why, want, err, s.log.String())
	}
	s.log.WriteString(line)
	var ev struct {
		Event string `json:"event"`
		Data  struct {
			AgentStatus string `json:"agent_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("%s: event line %q: %v", why, line, err)
	}
	if ev.Event != "pane.agent_status_changed" || ev.Data.AgentStatus != want {
		t.Fatalf("%s: got %s %s, want pane.agent_status_changed %s\nstream so far:\n%s", why, ev.Event, ev.Data.AgentStatus, want, s.log.String())
	}
}

// herdrPaneStatus is a pane's agent_status in session.snapshot.
func herdrPaneStatus(t *testing.T, base, pane string) string {
	t.Helper()
	snap := herdrCall(t, base, "session.snapshot", map[string]any{})["snapshot"].(map[string]any)
	for _, p := range snap["panes"].([]any) {
		if m := p.(map[string]any); m["pane_id"] == pane {
			return m["agent_status"].(string)
		}
	}
	t.Fatalf("no pane %s in the snapshot", pane)
	return ""
}

// finishTurn reports a turn that starts and finishes, which counts as a
// finished turn nobody has looked at yet.
func finishTurn(t *testing.T, base, session, win string) {
	t.Helper()
	for _, state := range []string{"working", "done"} {
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "-w", win, "--harness", "claude-code", state); err != nil {
			t.Fatalf("set-agent-state %s: %v\n%s", state, err, out)
		}
	}
}

// TestHerdrSeenDoneIsIdle holds the herdr socket to herdr's agent_status at
// the version tuios follows: done is an agent at rest that nobody has seen,
// and it turns idle once someone looks at the pane. tuios keeps done as its
// state and tracks the looking apart (finished_unread), so the socket has to
// put the two back together, and say so on the event stream when a pane is
// looked at, since the tuios state does not change then.
//
//   - With no client attached: a finished turn reads done in the snapshot,
//     and agent.wait for idle times out while agent.wait for done matches.
//     pane.focus marks it seen, as herdr's does: the reply, the snapshot and
//     the stream all say idle, agent.wait for idle matches and agent.wait for
//     done times out. The next finished turn reads done again.
//   - With a client attached: a pane that finishes off screen reads done,
//     and the client focusing it turns it idle on the stream, through the
//     client's push and not through pane.focus.
//
// Negative controls (e2e/tui/NEGATIVE_CONTROLS.md):
//   - sess.MarkCompletionSeen cut from herdrFocusPane: the first part fails
//     at "pane.focus marks a done pane seen: the reply says done".
//   - the agent-seen publish cut from the daemon's event sink: both parts
//     time out waiting for pane.agent_status_changed idle.
//   - herdrStatus reading done as done whether seen or not: the first part
//     fails at the pane.focus reply, the second waiting for idle.
//   - herdrWaitAgent matching tuios states instead of herdr's status: the
//     first part fails at "agent.wait for idle matched a pane nobody has
//     seen".
func TestHerdrSeenDoneIsIdle(t *testing.T) {
	t.Run("pane.focus with no client", func(t *testing.T) {
		base := t.TempDir()
		killDaemon(t, base)
		const session = "herdr-seen"
		if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
			t.Fatalf("create session: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "new-window", "agent", "-s", session, "--no-focus"); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
		win := windowID(t, base, session, "agent")
		pane := herdrPaneByLabel(t, base, "agent")["pane_id"].(string)
		stream := subscribeHerdrStatus(t, base, pane)

		finishTurn(t, base, session, win)
		stream.next(t, "working", "a turn starting")
		stream.next(t, "done", "a turn nobody has seen finishing")
		if got := herdrPaneStatus(t, base, pane); got != "done" {
			t.Fatalf("a finished turn nobody has seen reads %s in the snapshot, want done", got)
		}
		if _, e := herdrTry(t, base, "agent.wait", map[string]any{"target": pane, "until": []string{"idle"}, "timeout_ms": 500}); e == nil {
			t.Fatalf("agent.wait for idle matched a pane nobody has seen")
		}
		if _, e := herdrTry(t, base, "agent.wait", map[string]any{"target": pane, "until": []string{"done"}, "timeout_ms": 5000}); e != nil {
			t.Fatalf("agent.wait for done on an unseen pane: %v", e)
		}

		focused, e := herdrTry(t, base, "pane.focus", map[string]any{"pane_id": pane})
		if e != nil {
			t.Fatalf("pane.focus: %v", e)
		}
		if got := focused["pane"].(map[string]any)["agent_status"]; got != "idle" {
			t.Fatalf("pane.focus marks a done pane seen: the reply says %v, want idle", got)
		}
		stream.next(t, "idle", "pane.focus marks a done pane seen")
		if got := herdrPaneStatus(t, base, pane); got != "idle" {
			t.Fatalf("a seen pane reads %s in the snapshot, want idle", got)
		}
		if _, e := herdrTry(t, base, "agent.wait", map[string]any{"target": pane, "until": []string{"idle"}, "timeout_ms": 5000}); e != nil {
			t.Fatalf("agent.wait for idle on a seen pane: %v", e)
		}
		if _, e := herdrTry(t, base, "agent.wait", map[string]any{"target": pane, "until": []string{"done"}, "timeout_ms": 500}); e == nil {
			t.Fatalf("agent.wait for done matched a pane that was seen")
		}

		// The next turn is news again.
		finishTurn(t, base, session, win)
		stream.next(t, "working", "the next turn starting")
		stream.next(t, "done", "the next turn finishing")
		saveText(t, "herdr-status-stream-no-client", stream.log.String())
	})

	t.Run("a client focusing the pane", func(t *testing.T) {
		term, base := crushClient(t)
		ids := crushPanes(t, base, "agent")
		pane := herdrPaneByLabel(t, base, "agent")["pane_id"].(string)
		stream := subscribeHerdrStatus(t, base, pane)

		// The pane opened with --no-focus, so the client shows another pane
		// while it finishes.
		finishTurn(t, base, crushSession, ids["agent"])
		stream.next(t, "working", "a turn starting off screen")
		stream.next(t, "done", "a turn finishing off screen")

		if out, err := tuiosCLI(t, base, "focus-window", "-s", crushSession, ids["agent"]); err != nil {
			t.Fatalf("focus-window: %v\n%s", err, out)
		}
		stream.next(t, "idle", "the client focusing a done pane")
		if got := herdrPaneStatus(t, base, pane); got != "idle" {
			t.Fatalf("a pane the client shows reads %s in the snapshot, want idle", got)
		}
		saveText(t, "herdr-status-stream-client", stream.log.String())
		saveFrame(t, term, "herdr-seen-done-is-idle")
		alive(t, term, "after the client looked at a done pane")
	})
}
