package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestAWindowNameWinsOverAnIdPrefix: -w took a unique id prefix before an
// exact name, and a window id is a uuid, so a name made of hex digits is also
// a prefix of some other window's id now and then. A pane called "db" was
// missed about once in fifty runs of TestNarrowRailKeepsTheAgentNameBeforeItsHarness,
// which opens six panes: set-agent-state -w db reported success, the state
// went to the pane whose id happened to start with "db", and the pane called
// db stayed at none. It looked like a race because it came and went with the
// uuids the run drew. The list-windows reply is saved under artifactDir.
//
// This test makes the collision certain instead of waiting for it: the first
// pane is named after the start of the second pane's id.
//
// How this could pass wrongly, written down first:
//   - The name could resolve to the named pane because the prefix matches
//     nothing, which proves nothing. The prefix is checked to match the other
//     pane and only it before the target is used.
//   - The state could land on both panes, or on neither with the verb failing
//     quietly. Both panes' states are read back, and the verb's exit is checked.
//   - The name could read as the index list-windows prints if it were all
//     digits and small, so a name that parses as an in-range index is refused
//     up front rather than silently tested on the wrong rule.
//   - The fix could be to stop taking id prefixes at all, which would pass
//     the first half. So the first pane is then renamed and the same prefix
//     must reach the other pane.
func TestAWindowNameWinsOverAnIdPrefix(t *testing.T) {
	const sess = "target"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "new-window", "other", "-s", sess, "--no-focus", "--print-id")
	if err != nil {
		t.Fatalf("open the second pane: %v\n%s", err, out)
	}
	otherID := strings.TrimSpace(out)
	if len(otherID) != 36 {
		t.Fatalf("new-window --print-id printed %q, want a full window id", out)
	}
	name := otherID[:8]
	if n, err := strconv.Atoi(name); err == nil && n < 2 {
		t.Skipf("the id prefix %q reads as a window index, so it cannot test the name rule", name)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", sess, "--name", name); err != nil {
		t.Fatalf("name the first pane %q: %v\n%s", name, err, out)
	}

	type window struct {
		ID         string `json:"window_id"`
		CustomName string `json:"custom_name"`
		AgentState string `json:"agent_state"`
	}
	listWindows := func(label string) []window {
		t.Helper()
		out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
		if err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		if err := os.WriteFile(filepath.Join(artifactDir(t), label+".json"), []byte(out), 0o644); err != nil {
			t.Errorf("save %s: %v", label, err)
		}
		var reply struct {
			Windows []window `json:"windows"`
		}
		if err := json.Unmarshal([]byte(out), &reply); err != nil {
			t.Fatalf("list-windows --json: %v\n%s", err, out)
		}
		return reply.Windows
	}
	byName := func(ws []window, n string) window {
		t.Helper()
		for _, w := range ws {
			if w.CustomName == n {
				return w
			}
		}
		t.Fatalf("no pane named %q in %+v", n, ws)
		return window{}
	}

	before := listWindows("before")
	named := byName(before, name)
	if named.ID == otherID || !strings.HasPrefix(otherID, name) || strings.HasPrefix(named.ID, name) {
		t.Fatalf("setup: %q should name the first pane and be an id prefix of the other pane only: %+v", name, before)
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", sess, "-w", name, "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state -w %s: %v\n%s", name, err, out)
	}
	after := listWindows("after-name")
	if got := byName(after, name).AgentState; got != "working" {
		t.Errorf("ASSERTION: the pane named %q is at %q, want working", name, got)
	}
	if got := byName(after, "other").AgentState; got != "none" {
		t.Errorf("ASSERTION: the pane whose id starts with %q took the state meant for the pane of that name: it is at %q", name, got)
	}

	// The prefix still reaches the other pane once no name is in the way.
	if out, err := tuiosCLI(t, base, "set-window", "-s", sess, "-w", name, "--name", "first"); err != nil {
		t.Fatalf("rename the first pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", sess, "-w", name, "idle", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state -w %s by id prefix: %v\n%s", name, err, out)
	}
	last := listWindows("after-prefix")
	if got := byName(last, "other").AgentState; got != "idle" {
		t.Errorf("ASSERTION: the id prefix %q no longer reaches its pane: it is at %q", name, got)
	}
	if got := byName(last, "first").AgentState; got != "working" {
		t.Errorf("ASSERTION: the renamed pane moved to %q on a report meant for the other pane", got)
	}
}
