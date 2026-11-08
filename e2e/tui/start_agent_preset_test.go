package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestStartAgentFromAPreset starts a worker from an [agents.presets.NAME]
// entry, as a lead starts its crew: the preset names the agent, its args, its
// extra folders, its grants and the keys to send once it is ready.
// start-agent --preset with --print-id prints the new pane's full id and
// nothing else. The agent was started with the preset's args and one
// --add-dir per folder, it holds the preset's grants, and it got the keys
// after it was ready, in order. The plain success line carries the full id.
// An unknown preset is refused with the ones that exist, and a pane that may
// not give admin cannot start a preset that grants it.
//
// How it could pass wrongly: the keys could be typed before the agent's input
// is live and dropped (the stand-in drops early input, as Claude Code does),
// so they must come back from the agent in order; the args could come from the
// caller, so the caller passes none.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestStartAgentFromAPreset).
func TestStartAgentFromAPreset(t *testing.T) {
	base, _ := fanFixture(t)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	// A claude that writes its argv down, then is the fixture's claude.
	fake, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("the fixture's claude is not on PATH: %v", err)
	}
	argv := filepath.Join(base, "claude-argv")
	wrap := filepath.Join(base, "wrapbin")
	if err := os.MkdirAll(wrap, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argv + "'\nexec '" + fake + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrap, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrap+string(os.PathListSeparator)+os.Getenv("PATH"))
	notes, cache := filepath.Join(base, "notes"), filepath.Join(base, "scratch")
	writeConfig(t, base, `[agents.presets.worker]
agent = "claude"
args = ["--verbose"]
add_dirs = ["`+notes+`", "`+cache+`"]
grants = ["read", "write", "task"]
after_ready = ["/remote-control", "Enter", "hello there", "Enter"]

[agents.presets.boss]
agent = "claude"
grants = ["admin"]
`)
	if out, err := tuiosCLI(t, base, "new", "crew", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}

	out, err := tuiosCLI(t, base, "start-agent", "--preset", "worker", "-s", "crew", "--name", "w1", "--print-id")
	if err != nil {
		t.Fatalf("ASSERTION: start-agent --preset worker --print-id failed: %v\n%s", err, out)
	}
	id := strings.TrimSpace(out)
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("ASSERTION: --print-id printed %q, want the full pane id alone", out)
	}

	got, _ := os.ReadFile(argv)
	wantArgs := []string{"--add-dir", notes, "--add-dir", cache, "--verbose"}
	if !strings.Contains(string(got), strings.Join(wantArgs, "\n")) {
		t.Errorf("ASSERTION: the agent was started with %q, want the preset's dirs and args %q", got, wantArgs)
	}
	var listed struct {
		Windows []struct {
			ID     string   `json:"window_id"`
			Grants []string `json:"grants"`
		} `json:"windows"`
	}
	raw := mustCLI(t, base, "call", "list-windows", `{"session":"crew"}`)
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		t.Fatalf("list-windows: %v\n%s", err, raw)
	}
	held := []string(nil)
	for _, w := range listed.Windows {
		if w.ID == id {
			held = w.Grants
		}
	}
	if strings.Join(held, ",") != "read,write,task" {
		t.Errorf("ASSERTION: the pane holds grants %v, want the preset's read, write and task\n%s", held, raw)
	}
	deadline := time.Now().Add(uiTimeout)
	screen := ""
	for time.Now().Before(deadline) {
		screen = capture(t, base, "crew", id)
		if strings.Contains(screen, "GOT: hello there") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	first, second := strings.Index(screen, "GOT: /remote-control"), strings.Index(screen, "GOT: hello there")
	if first < 0 || second < 0 || second < first {
		t.Errorf("ASSERTION: the agent did not get the preset's keys after it was ready, in order:\n%s", screen)
	}

	// The plain success line carries the full id.
	out, err = tuiosCLI(t, base, "start-agent", "--preset", "worker", "-s", "crew", "--name", "w2")
	if err != nil {
		t.Fatalf("start-agent --preset worker: %v\n%s", err, out)
	}
	if !regexp.MustCompile(`w2 \([0-9a-f]{8}\) is ready: .*[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`).MatchString(out) {
		t.Errorf("ASSERTION: the plain success line has no full id:\n%s", out)
	}

	// An unknown preset: refused, with the ones that exist, and nothing starts.
	out, err = tuiosCLI(t, base, "start-agent", "--preset", "nosuch", "-s", "crew", "--name", "w3")
	if err == nil || !strings.Contains(out, "worker") || !strings.Contains(out, "boss") {
		t.Errorf("ASSERTION: an unknown preset was not refused with the names that exist: %v\n%s", err, out)
	}

	// From a pane that may start agents (fan) but not give admin, a preset
	// that grants admin is refused, as --grants admin is.
	ran := filepath.Join(base, "boss.out")
	pane := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "crew", "plain", "--no-focus", "--print-id"))
	mustCLI(t, base, "call", "set-pane-grants", `{"session":"crew","window":"`+pane+`","grants":["read","write","fan"]}`)
	mustCLI(t, base, "send-text", "-s", "crew", "-w", pane, "'"+tuiosBin+"' start-agent --preset boss -s crew --name w4 > '"+ran+"' 2>&1; echo EXIT=$? >> '"+ran+"'\n")
	res := waitForFileText(t, ran, func(s string) bool { return strings.Contains(s, "EXIT=") }, "the pane's start-agent never ended")
	if strings.Contains(res, "EXIT=0") || !strings.Contains(res, "cannot give more than it holds") {
		t.Errorf("ASSERTION: a pane without admin was not refused a preset that grants admin as more than it holds:\n%s", res)
	}
	if strings.Contains(mustCLI(t, base, "list-agents", "-s", "crew"), "w4") {
		t.Errorf("ASSERTION: the refused start opened a pane")
	}
}
