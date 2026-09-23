package tuie2e

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestMCPServerInAPaneIsHeldToItsSession runs tuios mcp the way a harness in a
// pane runs it: as a child of the pane's shell, fed a scripted MCP session on
// stdin. TUIOS_PANE_TOKEN is taken out of its environment, so the only thing
// that can place it is the kernel's record of its pid, walked up to the pane's
// shell. From there it reads its own session, is refused another session, and
// its state report lands on its own pane, which the rail then shows.
//
// Negative control: with the checkScope call in dispatchVerbLine cut, call 3
// lists the other session's windows and the test fails on it.
func TestMCPServerInAPaneIsHeldToItsSession(t *testing.T) {
	term, base := attachClientBase(t)
	if out, err := tuiosCLI(t, base, "new", "e2e-other", "--detach"); err != nil {
		t.Fatalf("create the other session: %v\n%s", err, out)
	}

	if strings.Contains(term.Screen().Text(), needsInputGlyph) {
		t.Fatalf("the needs_input indicator was on screen before the report\n%s", term.Snapshot())
	}

	out := filepath.Join(base, "mcp-out.jsonl")
	reqs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tuios_list_windows","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tuios_list_windows","arguments":{"session":"e2e-other"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tuios_set_agent_state","arguments":{"state":"needs_input","message":"mcp asks"}}}`,
	}
	line := "printf '%s\\n' '" + strings.Join(reqs, "' '") + "' | env -u TUIOS_PANE_TOKEN " + tuiosBin + " mcp > " + out + "; echo MCP_EXIT=$?\n"
	if o, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text failed: %v\n%s", err, o)
	}
	if err := term.WaitForText("MCP_EXIT=0", shellTimeout); err != nil {
		t.Fatalf("tuios mcp did not finish in the pane: %v\n%s", err, term.Snapshot())
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	byID := map[float64]map[string]any{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("tuios mcp wrote a line that is not JSON: %q", sc.Text())
		}
		if id, ok := m["id"].(float64); ok {
			byID[id] = m
		}
	}
	text := func(id float64) (string, bool) {
		res, _ := byID[id]["result"].(map[string]any)
		if res == nil {
			raw, _ := os.ReadFile(out)
			t.Fatalf("no result for call %v: %v\noutput:\n%s\nscreen:\n%s", id, byID[id], raw, term.Snapshot())
		}
		content := res["content"].([]any)
		isErr, _ := res["isError"].(bool)
		return content[len(content)-1].(map[string]any)["text"].(string), isErr
	}

	if body, isErr := text(2); isErr || !strings.Contains(body, `"windows"`) {
		t.Errorf("list_windows of its own session = %s", body)
	}
	if body, isErr := text(3); !isErr || !strings.Contains(body, "forbidden") {
		t.Errorf("list_windows of another session = %s, want forbidden", body)
	}
	if body, isErr := text(4); isErr {
		t.Errorf("set_agent_state = %s", body)
	}

	state, err := tuiosCLI(t, base, "get-agent-state", "-s", "e2e-ctrlp", "--json")
	if err != nil {
		t.Fatalf("get-agent-state: %v\n%s", err, state)
	}
	if !strings.Contains(state, "needs_input") || !strings.Contains(state, "mcp asks") {
		t.Errorf("the pane's state after its MCP report = %s, want needs_input", state)
	}
	// The report reached the client too: the pane's needs_input indicator is
	// drawn.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), needsInputGlyph)
	}, uiTimeout); err != nil {
		t.Fatalf("the client never drew the state the MCP server reported: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mcp-pane-report")
	alive(t, term, "after tuios mcp ran in a pane")
}
