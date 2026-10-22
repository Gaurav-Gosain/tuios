package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// unclaimedBoxScript draws Claude Code's empty prompt box, waits for a line
// from the test, then wipes the screen and exits. It runs under sh, so the
// foreground-process detector never takes it for an agent.
const unclaimedBoxScript = `#!/bin/sh
printf '\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\n\342\235\257 \n\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\n'
read -r _
printf '\033[2J\033[H'
exit 0
`

// unclaimedAgentScript is the agent's side, run by the stand-in claude: it
// draws nothing and waits for a line from the test.
const unclaimedAgentScript = `#!/bin/sh
read -r _
exit 0
`

// TestAgentDetectorMarksAnUnclaimedPaneAtItsOwnRank runs a program called
// claude, which the foreground-process detector takes for Claude Code, in a
// pane that has an agent state and no claim: the screen tier read Claude's
// prompt box at idle, and yielded the pane when the box was wiped, before any
// agent process ran in it. The detector notes that it saw the agent on
// whatever claim the pane has, and on a pane with none it used to store the
// zero claim, whose empty source ranks as a report. That held the pane against
// every other tier for as long as the agent ran. A report from the OSC tier
// has to be applied.
//
// How this could pass wrongly, written down first:
//   - A screen tier that never took the pane, or never yielded it, would leave
//     a claim the detector writes onto rather than none: the test waits for
//     idle from the screen, then for idle from another source after the wipe.
//   - A detector that never saw the agent would leave the pane unclaimed, open
//     to the OSC report without the fix: the test waits for the stand-in
//     claude to be the pane's foreground command, then for two detector
//     passes, before it reports.
//   - An OSC report that the pane's state already matched would read the same
//     either way: the pane is at idle and the report says working.
func TestAgentDetectorMarksAnUnclaimedPaneAtItsOwnRank(t *testing.T) {
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	base := t.TempDir()
	useShippedLooks(base)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeClaudeBin, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	box := filepath.Join(base, "box.sh")
	if err := os.WriteFile(box, []byte(unclaimedBoxScript), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(base, "agent.sh")
	if err := os.WriteFile(agent, []byte(unclaimedAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[daemon]\nagent_detect_seconds = 1\n")
	killDaemon(t, base)
	const session = "e2e-unclaimed"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create %s: %v\n%s", session, err, out)
	}

	began := time.Now()
	var trace []oscExitReading
	defer func() {
		dir := artifactDir(t)
		b, _ := json.MarshalIndent(trace, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "agent-state-trace.json"), b, 0o644); err != nil {
			t.Errorf("save the trace: %v", err)
		}
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", session)
		if err := os.WriteFile(filepath.Join(dir, "pane.txt"), []byte(pane), 0o644); err != nil {
			t.Errorf("save the pane: %v", err)
		}
		t.Logf("agent state trace and pane saved under %s", dir)
	}()
	read := func() oscExitReading {
		out, err := tuiosCLI(t, base, "get-agent-state", "--json", "-s", session, "-w", "0")
		if err != nil {
			t.Fatalf("get-agent-state: %v\n%s", err, out)
		}
		var r oscExitReading
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("get-agent-state json: %v\n%s", err, out)
		}
		if err := json.Unmarshal([]byte(out), &r.Raw); err != nil {
			t.Fatalf("get-agent-state json: %v\n%s", err, out)
		}
		r.At = time.Since(began).Round(10 * time.Millisecond).String()
		trace = append(trace, r)
		return r
	}
	until := func(d time.Duration, ok func(oscExitReading) bool) (oscExitReading, bool) {
		var r oscExitReading
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
			if r = read(); ok(r) {
				return r, true
			}
		}
		return r, false
	}
	send := func(text, why string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "0", text); err != nil {
			t.Fatalf("%s: %v\n%s", why, err, out)
		}
	}
	foreground := func() string {
		out, err := tuiosCLI(t, base, "list-windows", "--json", "-s", session)
		if err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		return out
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "--source", "stall", "--harness", "claude-code", "-s", session, "-w", "0"); err != nil {
		t.Fatalf("attribute the pane to claude-code: %v\n%s", err, out)
	}
	send("sh "+box+"\n", "draw the prompt box")
	if r, ok := until(15*time.Second, func(r oscExitReading) bool { return r.State == "idle" && r.Source == "screen" }); !ok {
		t.Fatalf("the screen tier never took the pane at idle, last read %+v", r)
	}
	send("\n", "wipe the prompt box")
	if r, ok := until(10*time.Second, func(r oscExitReading) bool { return r.State == "idle" && r.Source != "screen" }); !ok {
		t.Fatalf("the screen tier never yielded the pane, last read %+v", r)
	}

	send("claude "+agent+"\n", "start the agent")
	end := time.Now().Add(10 * time.Second)
	for !strings.Contains(foreground(), `"claude"`) {
		if time.Now().After(end) {
			t.Fatalf("the stand-in claude never reached the pane's foreground:\n%s", foreground())
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(2500 * time.Millisecond)
	if r := read(); r.State != "idle" {
		t.Fatalf("the pane left idle before the OSC report, read %+v", r)
	}

	out, err := tuiosCLI(t, base, "set-agent-state", "working", "--source", "osc", "-s", session, "-w", "0")
	if err != nil {
		t.Fatalf("set-agent-state --source osc: %v\n%s", err, out)
	}
	if r, ok := until(3*time.Second, func(r oscExitReading) bool { return r.State == "working" && r.Source == "osc" }); !ok {
		t.Fatalf("ASSERTION: an OSC-tier report was not applied to a pane no tier held while the agent ran: read %s (source %s, harness %s), set-agent-state said %q", r.State, r.Source, r.Harness, strings.TrimSpace(out))
	}

	send("\n", "let the agent exit")
	if r, ok := until(10*time.Second, func(r oscExitReading) bool { return r.State == "none" }); !ok {
		t.Fatalf("ASSERTION: the pane kept agent state %s (source %s, harness %s) with the agent gone and its shell in the foreground", r.State, r.Source, r.Harness)
	}
}
