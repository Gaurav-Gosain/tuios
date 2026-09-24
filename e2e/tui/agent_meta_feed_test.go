package tuie2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// agentMeta reads a pane's agent metadata with get-agent-state.
func agentMeta(t *testing.T, base string, args ...string) map[string]string {
	t.Helper()
	out, err := tuiosCLI(t, base, append([]string{"get-agent-state", "--json"}, args...)...)
	if err != nil {
		return nil
	}
	var st struct {
		Meta map[string]string `json:"meta"`
	}
	if json.Unmarshal([]byte(out), &st) != nil {
		return nil
	}
	return st.Meta
}

// waitMeta polls a pane's metadata until every key has its value.
func waitMeta(t *testing.T, base string, want map[string]string, args ...string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		got := agentMeta(t, base, args...)
		ok := true
		for k, v := range want {
			if got[k] != v {
				ok = false
			}
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane's metadata is %v, want %v", got, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestStatusLineFeedReachesTheRail runs the real status line wrapper in a
// pane's shell, the way Claude Code runs it, against a real daemon and
// client. The model, context and cost land on the pane's metadata and the
// agent's row on the rail, and with --then the wrapper prints the person's own status line unchanged.
//
// Negative control: with the set-agent-meta call taken out of
// reportStatusLine, the metadata never arrives and the first wait fails.
func TestStatusLineFeedReachesTheRail(t *testing.T) {
	term, base := attachClientBase(t)
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ctrlp", "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	payload := `{"session_id":"s1","model":{"id":"claude-opus-4-7","display_name":"Opus 4.7"},"context_window":{"used_percentage":42},"cost":{"total_cost_usd":1.2}}`
	line := "printf '%s' '" + payload + "' | " + tuiosBin + " agent-statusline claude-code; echo FEED-DONE\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitMeta(t, base, map[string]string{"model": "Opus 4.7", "context": "42%", "cost": "$1.20"}, "-s", "e2e-ctrlp")
	waitCapture(t, base, "e2e-ctrlp", "0", "FEED-DONE")
	waitForAll(t, term, uiTimeout, "the rail row", "Opus 4.7")
	saveFrame(t, term, "statusline-feed-rail")

	// Chained: the person's command gets the same stdin and its output is
	// printed as it wrote it.
	chained := "printf '%s' '" + payload + "' | " + tuiosBin + " agent-statusline claude-code --then 'wc -c | tr -d \" \"; echo MINE-LINE'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", chained); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-ctrlp", "0", "MINE-LINE", itoa(len(payload)))
	alive(t, term, "after the status line feed")
}

// TestProtocolPaneFeedsMeta runs an ACP agent headless and checks that what
// it says about its model, context window, cost and plan lands on the pane's
// agent metadata: the model from session/new, and the rest from the plan and
// usage_update it sends during a turn.
//
// Negative control: with the Usage case taken out of Session.onEvent, the
// context and cost never arrive and the wait fails.
func TestProtocolPaneFeedsMeta(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	fake := buildFakeACP(t)
	if out, err := tuiosCLI(t, base, "new", "e2e-home", "--detach"); err != nil {
		t.Fatalf("create the attached session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	out, err := tuiosCLI(t, base, "start-agent", fake, "--protocol", "acp", "-s", "e2e-agent", "--name", "helper", "--prompt", "show usage")
	if err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-agent", "helper", "connected to fakeacp 1.0 (Fake Model)", "turn finished")
	waitMeta(t, base, map[string]string{"model": "Fake Model", "context": "42%", "cost": "$0.42", "plan": "1/2"}, "-s", "e2e-agent", "-w", "helper")
	alive(t, term, "after the protocol pane's metadata")
}
