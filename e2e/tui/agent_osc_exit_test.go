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

// oscExitScript is the agent's side, run by the stand-in claude. It writes a
// line, waits for one from the test, and exits. With "clear" as its argument
// it clears its OSC 9;4 progress bar as its last word, which is how Claude
// Code leaves a pane.
const oscExitScript = `#!/bin/sh
printf 'FAKE-UP\n'
read -r _
if [ "$1" = clear ]; then printf '\033]9;4;0\007'; fi
exit 0
`

// oscExitReading is one get-agent-state read, kept for the saved trace.
type oscExitReading struct {
	At      string         `json:"at"`
	State   string         `json:"state"`
	Source  string         `json:"source"`
	Harness string         `json:"harness_id"`
	Raw     map[string]any `json:"raw"`
}

// TestAgentStateClearsWhenTheLastWordWasAProgressClear runs a program called
// claude, which the foreground-process detector takes for Claude Code, in a
// pane whose agent state an OSC-tier source holds: the state a pane carries
// when a source other than the detector set it before the detector saw the
// agent, such as a pane left at idle by an earlier run. The detector then
// does not own the claim, and clears it at the shell only because it saw an
// agent process in the pane. The agent exits back to the shell, and the pane
// has to go back to none.
//
// The case that failed: the agent clears its progress bar as it exits. That
// report is applied, and the claim it rebuilt forgot that the detector had
// seen the agent, so the pane stayed at idle, source osc, harness
// claude-code, for as long as it lived.
//
// How this could pass wrongly, written down first:
//   - A detector that never saw the agent would leave both cases stuck, and a
//     test with only the failing case could not tell. The case without the
//     progress clear is the positive half: it clears only when the detector
//     saw the process.
//   - A progress clear the daemon never read would make the two cases the
//     same. The saved trace shows idle, source osc, after the clear.
//   - A pane that clears and comes back would pass a single read. The test
//     reads for a further four seconds after none.
func TestAgentStateClearsWhenTheLastWordWasAProgressClear(t *testing.T) {
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name  string
		clear bool
	}{
		{name: "exit-without-a-report"},
		{name: "progress-cleared-on-exit", clear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			script := filepath.Join(base, "agent.sh")
			if err := os.WriteFile(script, []byte(oscExitScript), 0o755); err != nil {
				t.Fatal(err)
			}
			writeConfig(t, base, "[daemon]\nagent_detect_seconds = 1\n")
			killDaemon(t, base)
			const session = "e2e-oscexit"
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

			if out, err := tuiosCLI(t, base, "set-agent-state", "working", "--source", "osc", "--harness", "claude-code", "-s", session, "-w", "0"); err != nil {
				t.Fatalf("give the pane an osc-tier state: %v\n%s", err, out)
			}
			arg := ""
			if tc.clear {
				arg = " clear"
			}
			send("claude "+script+arg+"\n", "start the agent")
			if r, ok := until(10*time.Second, func(r oscExitReading) bool {
				return r.State == "working" && r.Source == "osc"
			}); !ok {
				t.Fatalf("the pane lost its osc-tier state before the agent ran, last read %+v", r)
			}
			// Three detector passes with the agent in the foreground, so it
			// has seen the process before the agent leaves.
			time.Sleep(3 * time.Second)
			read()

			send("\n", "let the agent exit")
			if r, ok := until(10*time.Second, func(r oscExitReading) bool { return r.State == "none" }); !ok {
				t.Fatalf("ASSERTION: the pane kept agent state %s (source %s, harness %s) with the agent gone and its shell in the foreground", r.State, r.Source, r.Harness)
			}
			if r, back := until(4*time.Second, func(r oscExitReading) bool { return r.State != "none" }); back {
				t.Fatalf("ASSERTION: the pane went back to agent state %s (source %s, harness %s) after it cleared at the shell", r.State, r.Source, r.Harness)
			}
		})
	}
}
