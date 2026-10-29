package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A hook keeps reaching its verified own pane when its inherited session name
// is gone or is a rename alias belonging to another session.
//
// After alpha is renamed to omega, a new alpha is either killed (a lost alias)
// or renamed to replacement (a reused alias). The real hook must reach omega;
// the same call from outside or a second pane must be refused, and the
// replacement session's pane must stay unchanged.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "A pane whose session name is
// gone" and "Reports after a session alias is reused".
func TestHookFromPaneWhoseSessionNameIsGone(t *testing.T) {
	art := &moveArtifact{}
	defer art.save(t)
	for _, mode := range []string{"gone", "reused_alias"} {
		t.Run(mode, func(t *testing.T) {
			art.add("case", map[string]any{"name": mode}, nil, "")
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
			replacement := ""
			var replacementBefore agentStateJSON
			wantError := "session_not_found"
			if mode == "reused_alias" {
				windows := must("list-windows", map[string]any{"session": "alpha"})["windows"].([]any)
				replacement = windows[0].(map[string]any)["window_id"].(string)
				must("rename-session", map[string]any{"session": "alpha", "name": "replacement"})
				replacementBefore = readAgentState(t, base, "replacement", replacement)
				wantError = "window_not_found"
			} else {
				if out, err := tuiosCLI(t, base, "kill-session", "alpha"); err != nil {
					t.Fatalf("kill-session alpha: %v\n%s", err, out)
				}
				if out, err := tuiosCLI(t, base, "list-windows", "-s", "alpha"); err == nil {
					t.Fatalf("alpha still names a session:\n%s", out)
				}
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
			if err == nil || !strings.Contains(outside, wantError) {
				t.Errorf("ASSERTION: a call from outside the pane naming the stale session was not refused: %v\n%s", err, outside)
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
			// Refusal can precede session/window lookup, so accept its lookup error
			// or the own-pane permission refusal.
			if !strings.Contains(otherContent, wantError) && !strings.Contains(otherContent, "forbidden") {
				t.Errorf("ASSERTION: a call from a second pane naming the stale session and the first pane was not refused:\n%s", otherContent)
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
					if replacement != "" {
						after := readAgentState(t, base, "replacement", replacement)
						art.add("replacement pane", map[string]any{"session": "replacement", "window": replacement}, map[string]any{"state": after.State, "agent_session_id": after.AgentSession}, "")
						if after != replacementBefore {
							t.Fatalf("the replacement pane changed: before=%+v after=%+v", replacementBefore, after)
						}
					}
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("ASSERTION: the hook did not reach the pane after its session name became stale: state %q\n%s", got, content)
		})
	}
}
