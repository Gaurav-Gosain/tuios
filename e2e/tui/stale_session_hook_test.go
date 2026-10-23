package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An agent hook in a pane whose TUIOS_SESSION names no session under any name
// still reaches its pane.
//
// alpha is renamed to omega, which leaves alpha as an alias. A new session
// then takes the name alpha, which drops the alias, and is killed. alpha now
// names nothing, which is where a pane is left when the alias is lost some
// other way (a daemon restart, a handoff from a build that did not carry it).
// The real `tuios agent-hook claude-code` runs inside the pane and names alpha
// and the pane's own id: its UserPromptSubmit must set the pane in omega to
// working. The same call from outside the pane, and from a second pane in
// omega with its own token, must still be refused with session_not_found,
// since only a pane may be followed to itself.
//
// NEGATIVE CONTROL (NEGATIVE_CONTROLS.md, "A pane whose session name is
// gone"): with followOwnPane cut from admitVerb, it fails at "the hook did not
// reach the pane after its session name was gone".
func TestHookFromPaneWhoseSessionNameIsGone(t *testing.T) {
	art := &moveArtifact{}
	defer art.save(t)
	base, must, win := moveSetup(t, art, "agent")
	call := func(verb string, params map[string]any) (map[string]any, error) {
		return must(verb, params), nil
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "alpha", "-w", win, "echo SESS-[$TUIOS_SESSION]\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitMoveCapture(t, call, "alpha", win, "SESS-[alpha]")

	must("rename-session", map[string]any{"session": "alpha", "name": "omega"})
	if out, err := tuiosCLI(t, base, "new", "alpha", "--detach"); err != nil {
		t.Fatalf("create the new alpha: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "kill-session", "alpha"); err != nil {
		t.Fatalf("kill-session alpha: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "list-windows", "-s", "alpha"); err == nil {
		t.Fatalf("alpha still names a session:\n%s", out)
	}

	state := func() string {
		t.Helper()
		res := must("list-windows", map[string]any{"session": "omega"})
		ws, _ := res["windows"].([]any)
		for _, w := range ws {
			m, _ := w.(map[string]any)
			if m["window_id"] == win {
				s, _ := m["agent_state"].(string)
				return s
			}
		}
		t.Fatalf("omega does not list the window: %v", res)
		return ""
	}
	if s := state(); s == "working" {
		t.Fatalf("the pane has agent state %q before any report", s)
	}

	raw, _ := json.Marshal(map[string]any{"session": "alpha", "window": win, "state": "working"})
	outside, err := tuiosCLI(t, base, "call", "set-agent-state", string(raw))
	art.add("set-agent-state from outside", map[string]any{"session": "alpha", "window": win}, nil, outside)
	if err == nil || !strings.Contains(outside, "session_not_found") {
		t.Errorf("ASSERTION: a call from outside the pane naming the gone session was not refused: %v\n%s", err, outside)
	}
	if s := state(); s == "working" {
		t.Errorf("ASSERTION: a call from outside the pane set the pane to %q", s)
	}

	other := must("new-window", map[string]any{"session": "omega", "name": "other"})
	otherWin, _ := other["window_id"].(string)
	if otherWin == "" {
		t.Fatalf("new-window gave no window_id: %v", other)
	}
	fromOther := tuiosBin + " call set-agent-state '" + string(raw) + "' 2>&1; echo OTHER-DONE-$((40+2))\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "omega", "-w", otherWin, fromOther); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	otherContent := waitMoveCapture(t, call, "omega", otherWin, "OTHER-DONE-42")
	art.add("set-agent-state from a second pane", map[string]any{"session": "alpha", "window": win, "from": otherWin}, map[string]any{"content": otherContent}, "")
	// A pane reports only for its own window, so this is refused before the
	// session is looked up (forbidden). session_not_found is the refusal of a
	// build that checks the session first.
	if !strings.Contains(otherContent, "session_not_found") && !strings.Contains(otherContent, "forbidden") {
		t.Errorf("ASSERTION: a call from a second pane naming the gone session and the first pane was not refused:\n%s", otherContent)
	}
	if s := state(); s == "working" {
		t.Errorf("ASSERTION: a call from a second pane set the first pane to %q", s)
	}
	if t.Failed() {
		t.FailNow()
	}

	payload := `{"hook_event_name":"UserPromptSubmit","session_id":"gone-hook","prompt":"keep going"}`
	hook := "printf '%s' '" + payload + "' | " + tuiosBin + " agent-hook claude-code --timeout 10s; echo HOOK-DONE-$((40+2))\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "omega", "-w", win, hook); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	content := waitMoveCapture(t, call, "omega", win, "HOOK-DONE-42")
	art.add("capture", map[string]any{"session": "omega", "window": win}, map[string]any{"content": content}, "")
	got := ""
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if got = state(); got == "working" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the hook did not reach the pane after its session name was gone: state %q\n%s", got, content)
}
