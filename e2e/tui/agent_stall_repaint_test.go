package tuie2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The silence timer on a pane whose agent repaints without saying anything.
//
// A TUI agent that draws its own blinking cursor (Mistral Vibe does, as do
// Textual programs) writes a byte or two to the pane twice a second while it
// sits at its prompt. Before this test the stall clock counted every byte as
// activity, so a pane the foreground detector had called working stayed
// working for as long as the agent was open: hours, on a real machine.
//
// How this could pass wrongly, written down first:
//   - A timer that demotes every pane would pass the blink half. The counter
//     half runs the same fixture with text that changes every second and
//     must still be working after three stall windows.
//   - A fake agent the detector never recognises would never be working, so
//     the blink half would pass on "not working". Both halves first wait for
//     working with source detect.
//   - A demotion the next blink takes back would pass a single read. The
//     blink half reads again for two more stall windows and fails on any
//     working read.
//   - A blink the emulator dropped would be no repaint at all. The blink
//     agent inverts and clears a real cell, the shape Vibe's cursor has.
//
// Negative control: with the stall clock on raw output (PTY.LastOutput in
// stallScan, nextStallCheck and reconcileAgentOnOutput), the blink half never
// leaves working and fails at its first wait.

// blinkAgent draws a prompt and then blinks one cell, inverse video on and
// off, once a second, the way an agent's own cursor does. It runs no child
// process, so the foreground process stays the script the detector knows.
const blinkAgent = `#!/bin/bash
printf 'blinkagent ready> '
while :; do
  printf '\033[7m \033[27m\b'
  read -r -t 1 _
  printf ' \b'
  read -r -t 1 _
done
`

// countAgent writes a line whose text changes once a second, the way an
// agent's elapsed-time counter does while it works.
const countAgent = `#!/bin/bash
printf 'countagent ready\n'
n=0
while :; do
  n=$((n+1))
  printf '\rworking %d' "$n"
  read -r -t 1 _
done
`

// stallRepaintStall is the stall window the fixture configures, in seconds.
const stallRepaintStall = 2

// stallRepaintFixture starts a detached session "stall" with one pane named
// AGENTPANE, both fake agents on PATH and named by agent_binaries, and the
// stall window short. It returns the isolation root.
func stallRepaintFixture(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"tuiosblinkagent": blinkAgent, "tuioscountagent": countAgent} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeConfig(t, base, "[daemon]\n"+
		"agent_binaries = [\"tuiosblinkagent\", \"tuioscountagent\"]\n"+
		"agent_detect_seconds = 1\n"+
		"agent_stall_seconds = 2\n")
	if out, err := tuiosCLI(t, base, "new", "stall", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "stall", "--name", "AGENTPANE"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	return base
}

// TestStallTimerIgnoresARepaintThatChangesNoText runs an agent that only
// blinks its cursor and one whose text keeps changing. The blinking one goes
// from working to a quiet state within a few stall windows and stays there;
// the counting one stays working. The log of every read is the artifact.
func TestStallTimerIgnoresARepaintThatChangesNoText(t *testing.T) {
	stall := stallRepaintStall * time.Second
	for _, tc := range []struct {
		name, program string
		wantWorking   bool
	}{
		{"blink", "tuiosblinkagent", false},
		{"count", "tuioscountagent", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := stallRepaintFixture(t)
			log := &stateLog{name: "stall-repaint-" + tc.name}
			defer log.save(t)
			if out, err := tuiosCLI(t, base, "send-text", "-s", "stall", "-w", "AGENTPANE", tc.program+"\n"); err != nil {
				t.Fatalf("start %s: %v\n%s", tc.program, err, out)
			}
			st := waitAgentState(t, base, "stall", "AGENTPANE", "working", "", log, "detected as working")
			if st.Source != "detect" {
				t.Fatalf("the pane is working from %q, want the detector", st.Source)
			}

			if tc.wantWorking {
				// Three stall windows of changing text: every read is working.
				until := time.Now().Add(3 * stall)
				for time.Now().Before(until) {
					st := readAgentState(t, base, "stall", "AGENTPANE")
					if st.State != "working" {
						log.add("%-44s state=%s source=%s", "counter demoted", st.State, st.Source)
						t.Fatalf("an agent whose text changes every second went %s (source %s)", st.State, st.Source)
					}
					time.Sleep(200 * time.Millisecond)
				}
				log.add("%-44s state=working for %s", "counter kept working", 3*stall)
				return
			}

			// The blink is the only output. Within a few stall windows the
			// timer must see through it.
			deadline := time.Now().Add(4*stall + 5*time.Second)
			for {
				st = readAgentState(t, base, "stall", "AGENTPANE")
				if st.State != "working" {
					break
				}
				if time.Now().After(deadline) {
					log.add("%-44s state=%s source=%s", "still working after blink-only output", st.State, st.Source)
					t.Fatalf("a pane that only blinks its cursor is still working after %s", 4*stall+5*time.Second)
				}
				time.Sleep(200 * time.Millisecond)
			}
			log.add("%-44s state=%s source=%s", "blink demoted", st.State, st.Source)
			if st.State == "none" || st.State == "" {
				t.Fatalf("the pane lost its agent instead of going quiet: %+v", st)
			}

			// And the next blinks must not take it back to working.
			until := time.Now().Add(2 * stall)
			for time.Now().Before(until) {
				st := readAgentState(t, base, "stall", "AGENTPANE")
				if st.State == "working" {
					log.add("%-44s state=%s source=%s", "blink resumed working", st.State, st.Source)
					t.Fatalf("a blink took the quiet pane back to working (source %s)", st.Source)
				}
				time.Sleep(200 * time.Millisecond)
			}
			log.add("%-44s for %s", "blink stayed quiet", 2*stall)
		})
	}
}
