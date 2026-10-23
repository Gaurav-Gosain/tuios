package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// handbackExitScript is the agent's side, run by the stand-in claude. It draws
// what its first argument names, a Claude Code permission prompt or its empty
// prompt box, waits for a line from the test, then wipes the screen and exits
// at once. The wipe is what makes the daemon's next look find nothing, so the
// tier that read the screen hands its claim back in the moment the agent goes.
const handbackExitScript = `#!/bin/sh
if [ "$1" = prompt ]; then
  printf 'Bash command\n  rm -rf build\nDo you want to proceed?\n\342\235\257 1. Yes\n  2. No\n'
else
  printf '\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\n\342\235\257 \n\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\342\224\200\n'
fi
read -r _
printf '\033[2J\033[H'
exit 0
`

// TestAgentClaimHandedBackAsTheAgentExits runs a program called claude, which
// the foreground-process detector takes for Claude Code, in a pane whose agent
// state a tier other than the detector set before the agent ran, so the
// detector does not own the claim and clears it at the shell only because it
// saw an agent process in the pane. The screen tier then takes the pane, the
// agent wipes its screen and exits, and the look that finds the screen empty
// hands the screen tier's claim back. The pane has to go back to none.
//
// The two cases are the two ways a claim is handed back:
//   - blocker-released-on-exit: the pane is at idle from the OSC tier, the
//     agent draws a permission prompt, and the screen tier lends itself the
//     pane over the OSC claim. The empty screen ends the loan and the OSC claim
//     comes back (releaseAgentBlockerOverride).
//   - screen-idle-yielded-on-exit: the pane is at working from the stall tier,
//     the agent draws its empty prompt box, and the screen tier takes the pane
//     at idle. The empty screen yields that claim (yieldAgentClaim).
//
// The case that failed: the claim that came back had forgotten that the
// detector had seen the agent, and the agent had already gone, so no later
// detection pass could see it again. The pane stayed at idle for as long as it
// lived.
//
// How this could pass wrongly, written down first:
//   - A screen tier that never matched would leave the seeded state for the
//     detector to clear, which it does: the test waits for the screen tier's
//     state before it lets the agent exit.
//   - A detector pass between the exit and the look would clear the claim
//     while the screen tier still held it, with its mark intact, and so pass
//     without the fix. The detector runs every four seconds here, the exit
//     lands between two passes, and the test fails unless it read the claim
//     handed back, idle from a tier other than the screen, before none. The
//     daemon's clock decides where between two passes the exit lands, so a run
//     that misses the hand-back is tried again, up to three times.
//   - A pane that clears and comes back would pass a single read. The test
//     reads for a further four seconds after none.
func TestAgentClaimHandedBackAsTheAgentExits(t *testing.T) {
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name       string
		draw       string
		seedState  string
		seedSource string
		wantState  string
	}{
		{name: "blocker-released-on-exit", draw: "prompt", seedState: "idle", seedSource: "osc", wantState: "needs_input"},
		{name: "screen-idle-yielded-on-exit", draw: "box", seedState: "working", seedSource: "stall", wantState: "idle"},
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
			if err := os.WriteFile(script, []byte(handbackExitScript), 0o755); err != nil {
				t.Fatal(err)
			}
			// A slow detector, so its pass lands well away from the exit. The
			// look that hands the claim back runs on the shell's prompt or
			// 400ms after it, and a pass between the exit and that look
			// clears the pane while the screen tier still holds it, which is
			// the path that does not need the fix. At one second it landed
			// there on every run of blocker-released-on-exit.
			writeConfig(t, base, "[daemon]\nagent_detect_seconds = 4\n")
			killDaemon(t, base)
			var session string
			var began time.Time
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

			// The detector ticks on the daemon's clock, so where the exit falls
			// between two passes is chance. A pass that lands between the exit
			// and the look clears the pane before the hand-back, which tests
			// nothing, and under load that happened on about one run in three.
			// Such a run is tried again on a fresh session. An assertion fails
			// at once, and only a run that reached the hand-back can pass.
			const tries = 3
			for try := 1; ; try++ {
				session = fmt.Sprintf("e2e-handback-%d", try)
				if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
					t.Fatalf("create %s: %v\n%s", session, err, out)
				}
				began = time.Now()
				trace = nil

				if out, err := tuiosCLI(t, base, "set-agent-state", tc.seedState, "--source", tc.seedSource, "--harness", "claude-code", "-s", session, "-w", "0"); err != nil {
					t.Fatalf("give the pane a %s-tier state: %v\n%s", tc.seedSource, err, out)
				}
				send("claude "+script+" "+tc.draw+"\n", "start the agent")
				if r, ok := until(15*time.Second, func(r oscExitReading) bool {
					return r.State == tc.wantState && r.Source == "screen"
				}); !ok {
					t.Fatalf("the screen tier never took the pane at %s, last read %+v", tc.wantState, r)
				}
				// A detector pass with the agent in the foreground, so it has
				// seen the process before the agent leaves, and the exit half
				// way to the next one.
				time.Sleep(6 * time.Second)
				if r := read(); r.State != tc.wantState || r.Source != "screen" {
					t.Fatalf("the screen tier let go of the pane before the agent exited, read %+v", r)
				}

				exited := len(trace)
				send("\n", "let the agent exit")
				if r, ok := until(10*time.Second, func(r oscExitReading) bool { return r.State == "none" }); !ok {
					t.Fatalf("ASSERTION: the pane kept agent state %s (source %s, harness %s) with the agent gone and its shell in the foreground", r.State, r.Source, r.Harness)
				}
				handedBack := false
				for _, r := range trace[exited:] {
					if r.State == "idle" && r.Source != "screen" {
						handedBack = true
					}
				}
				if handedBack {
					break
				}
				if try == tries {
					t.Fatalf("the pane cleared before the screen tier handed its claim back on all %d tries, so the hand-back was not exercised; see the saved trace", tries)
				}
				t.Logf("try %d: a detector pass cleared the pane before the hand-back, so the scenario runs again", try)
			}
			if r, back := until(4*time.Second, func(r oscExitReading) bool { return r.State != "none" }); back {
				t.Fatalf("ASSERTION: the pane went back to agent state %s (source %s, harness %s) after it cleared at the shell", r.State, r.Source, r.Harness)
			}
		})
	}
}
