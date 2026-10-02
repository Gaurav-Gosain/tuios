package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuitest"
)

// The agent switch, [agents] enabled = false, on a real daemon and a real
// client. Each test writes the switch into config.toml before the daemon
// starts, or rewrites it while both run, and reads what a person sees: the
// rail, the Inbox key, the CLI's answer, and whether one pane can type into
// another pane's prompt.
//
// How these could pass wrongly, written down first:
//   - A rail that never lists agents at all would pass the "no row" half. So
//     every rail check has its positive half in the same fixture: the same
//     fake agent, with the switch on, gets a row under the agents header.
//   - A fake agent the detector never recognises would pass the "no row" half
//     too. The positive half is what proves it is recognised.
//   - A refusal that is only the CLI's would leave the daemon serving the
//     verb. The JSON answer is read for the daemon's code, agents_disabled.
//   - The respond test could pass because typing failed for some other
//     reason. Its positive half gives the typing pane respond and watches the
//     same keys answer the prompt.

// fakeRailAgent is a program the detector takes for an agent through
// [daemon] agent_binaries: its process name is its file name. It draws a
// line and waits on its terminal with no child process, so the foreground
// process is the script itself.
const fakeRailAgent = `#!/bin/sh
printf 'fakeagent ready>\n'
while read -r line; do :; done
`

// promptProgram asks a question the way a program says it needs a person:
// the OSC 9;4 warning state, then a prompt that waits for a line.
const promptProgram = `#!/bin/sh
printf '\033]9;4;4;0\007'
printf 'Allow the edit? [y/n] '
read -r ans
printf 'ANSWERED:%s\n' "$ans"
`

// agentsOffFixture writes a config with the fake agent named, and the switch
// set when off is true, then starts a detached session "rail" with one pane
// named AGENTPANE. It returns the isolation root.
func agentsOffFixture(t *testing.T, off bool) string {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"tuiosfakeagent": fakeRailAgent, "askprompt": promptProgram} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeAgentsConfig(t, base, off)
	if out, err := tuiosCLI(t, base, "new", "rail", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "rail", "--name", "AGENTPANE"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	return base
}

// writeAgentsConfig writes config.toml with the agent switch on or off. The
// detector reads every second so a test waits on it for a short time.
func writeAgentsConfig(t *testing.T, base string, off bool) {
	t.Helper()
	cfg := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[daemon]\nagent_binaries = [\"tuiosfakeagent\"]\nagent_detect_seconds = 1\n"
	if off {
		body += "\n[agents]\nenabled = false\n"
	}
	// Write and rename, the way an editor saves, so a watcher never reads a
	// half-written file.
	tmp := cfg + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, cfg); err != nil {
		t.Fatal(err)
	}
}

// railAgentRows reads the rows under the rail's agents header. nil means the
// rail has no agents header at all.
func railAgentRows(s tuitest.Screen, railCol int) []string {
	cols, rows := s.Size()
	text := func(y int) string {
		var b strings.Builder
		for x := railCol; x < cols; x++ {
			b.WriteString(s.Cell(x, y).Content)
		}
		return strings.TrimSpace(strings.Trim(b.String(), "│ "))
	}
	for y := range rows {
		if !strings.HasPrefix(text(y), "agents") {
			continue
		}
		out := []string{}
		for r := y + 1; r < rows; r++ {
			row := text(r)
			if row == "" {
				break
			}
			out = append(out, row)
		}
		return out
	}
	return nil
}

// railHasAgent reports whether the agents section lists the pane.
func railHasAgent(s tuitest.Screen, railCol int, pane string) bool {
	for _, row := range railAgentRows(s, railCol) {
		if strings.Contains(row, pane) {
			return true
		}
	}
	return false
}

// railShot saves the frame as text and as a PNG drawn by internal/shot.
func railShot(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := artifactDir(t)
	saveArtifact(t, term, dir, name)
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, name)
	t.Logf("frame %s saved under %s", name, dir)
}

// startFakeAgent runs the fake agent in AGENTPANE and waits for its line.
func startFakeAgent(t *testing.T, base string, term *tuitest.Terminal) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-text", "-s", "rail", "-w", "AGENTPANE", "tuiosfakeagent\n"); err != nil {
		t.Fatalf("start the fake agent: %v\n%s", err, out)
	}
	if err := term.WaitForText("fakeagent ready>", uiTimeout); err != nil {
		t.Fatalf("the fake agent never started: %v\n%s", err, term.Snapshot())
	}
}

// TestAgentsOffShowsNoAgentRow starts the same fake agent CLI with the agent
// features on and off. On, the rail lists it under the agents header, and the
// Inbox key opens the Inbox. Off, the rail has no agents header, the daemon
// holds no agent state for the pane, and the Inbox key shows one line and
// opens nothing.
//
// Negative control: on origin/main, which has no switch, the off run lists
// the agent on the rail and the wait for its absence fails.
func TestAgentsOffShowsNoAgentRow(t *testing.T) {
	const cols, rows, width = 120, 32, 24
	for _, tc := range []struct {
		name string
		off  bool
	}{{"on", false}, {"off", true}} {
		t.Run(tc.name, func(t *testing.T) {
			base := agentsOffFixture(t, tc.off)
			term := attachIn(t, base, "rail", startOpts{cols: cols, rows: rows, shippedLooks: true})
			railCol := cols - width
			startFakeAgent(t, base, term)

			if !tc.off {
				if err := term.WaitFor(func(s tuitest.Screen) bool { return railHasAgent(s, railCol, "AGENTPANE") }, uiTimeout); err != nil {
					t.Fatalf("the rail never listed the fake agent with the features on: %v\n%s", err, term.Snapshot())
				}
				railShot(t, term, "rail-agents-on")
				if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
					t.Fatal(err)
				}
				if err := term.WaitForText("Inbox", uiTimeout); err != nil {
					t.Fatalf("the Inbox key opened nothing with the features on: %v\n%s", err, term.Snapshot())
				}
				alive(t, term, "after the Inbox opened")
				return
			}

			// The detector reads every second. Five reads with no row is the
			// answer; a row at any point fails at once.
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if s := term.Screen(); railAgentRows(s, railCol) != nil {
					t.Fatalf("ASSERTION: the rail has an agents section with the features off: %q\n%s",
						railAgentRows(s, railCol), term.Snapshot())
				}
				time.Sleep(250 * time.Millisecond)
			}
			out, err := tuiosCLI(t, base, "list-windows", "-s", "rail", "--json")
			if err != nil {
				t.Fatalf("list-windows: %v\n%s", err, out)
			}
			var listing struct {
				Windows []struct {
					Name       string `json:"custom_name"`
					AgentState string `json:"agent_state"`
					Harness    string `json:"agent_harness"`
				} `json:"windows"`
			}
			if err := json.Unmarshal([]byte(out), &listing); err != nil {
				t.Fatalf("list-windows JSON: %v\n%s", err, out)
			}
			for _, w := range listing.Windows {
				if (w.AgentState != "" && w.AgentState != "none") || w.Harness != "" {
					t.Errorf("ASSERTION: window %s holds agent state %q (%q) with the features off", w.Name, w.AgentState, w.Harness)
				}
			}
			railShot(t, term, "rail-agents-off")

			if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("Agent features are off", uiTimeout); err != nil {
				t.Fatalf("the Inbox key did not say the features are off: %v\n%s", err, term.Snapshot())
			}
			if strings.Contains(term.Screen().Text(), "Nothing is waiting for you") {
				t.Errorf("ASSERTION: the Inbox opened with the features off\n%s", term.Snapshot())
			}
			alive(t, term, "after the Inbox key with the features off")
		})
	}
}

// TestAgentsOffRefusesAgentCommands runs agent commands against a daemon
// with the features off. Each fails with the one line that says what to
// change, and the daemon answers the verb with agents_disabled. A command
// that is not an agent feature still works.
//
// Negative control: on origin/main start-agent starts the agent, and the
// first assertion fails.
func TestAgentsOffRefusesAgentCommands(t *testing.T) {
	base := agentsOffFixture(t, true)
	const want = "Agent features are off. Set agents.enabled = true in the config to use this command."

	for _, args := range [][]string{
		{"start-agent", "tuiosfakeagent", "-s", "rail"},
		{"list-attention"},
		{"set-agent-state", "needs_input", "-s", "rail", "-w", "AGENTPANE"},
		{"send-agent-message", "-s", "rail", "-w", "AGENTPANE", "hello"},
	} {
		out, err := tuiosCLI(t, base, args...)
		if err == nil {
			t.Errorf("ASSERTION: %s succeeded with the features off:\n%s", args[0], out)
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("ASSERTION: %s did not print the agents-off line:\n%s", args[0], out)
		}
	}

	// The daemon's own answer, as a program reads it.
	out, _ := tuiosCLI(t, base, "start-agent", "tuiosfakeagent", "-s", "rail", "--json")
	if !strings.Contains(out, "agents_disabled") {
		t.Errorf("ASSERTION: start-agent --json does not carry agents_disabled:\n%s", out)
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", "rail", "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	if strings.Count(out, `"window_id"`) != 1 {
		t.Errorf("ASSERTION: a refused start-agent opened a pane:\n%s", out)
	}
}

// TestAgentsOffRespondGrantStillHolds: with the features off nothing tracks
// which pane waits on a prompt, and a pane must still not answer another
// pane's prompt. Pane B runs a program that asks a question, with the OSC
// 9;4 warning that says it needs a person. Pane A, which holds admin and not
// respond, types the answer into B, and is refused. Given respond, the same
// keys answer it.
//
// Negative control: on origin/main, B is not an agent pane, so its warning
// is not read, A's keys answer the prompt, and ANSWERED:y appears before A
// holds respond.
func TestAgentsOffRespondGrantStillHolds(t *testing.T) {
	base := agentsOffFixture(t, true)
	if out, err := tuiosCLI(t, base, "new-window", "-s", "rail", "PROMPTPANE", "--no-focus"); err != nil {
		t.Fatalf("open the prompt pane: %v\n%s", err, out)
	}
	term := attachIn(t, base, "rail", startOpts{cols: 120, rows: 32, shippedLooks: true})

	if out, err := tuiosCLI(t, base, "send-text", "-s", "rail", "-w", "PROMPTPANE", "askprompt\n"); err != nil {
		t.Fatalf("start the prompt: %v\n%s", err, out)
	}
	waitPane := func(what, text string) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			out, _ := tuiosCLI(t, base, "capture-pane", "-s", "rail", "-w", what)
			if strings.Contains(out, text) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("pane %s never showed %q:\n%s", what, text, out)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	waitPane("PROMPTPANE", "Allow the edit? [y/n]")

	// A types y and Enter into B, from a shell in A.
	answer := tuiosBin + " send-keys -s rail -w PROMPTPANE 'y Enter'; echo RESP_EXIT=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "rail", "-w", "AGENTPANE", answer); err != nil {
		t.Fatalf("send-text into A: %v\n%s", err, out)
	}
	waitPane("AGENTPANE", "RESP_EXIT=")
	a, _ := tuiosCLI(t, base, "capture-pane", "-s", "rail", "-w", "AGENTPANE")
	// The pane wraps the message, so it is read with its line breaks folded.
	// Its start can scroll off a short pane; the end names the grant.
	if flat := strings.Join(strings.Fields(a), " "); !strings.Contains(flat, "RESP_EXIT=1") || !strings.Contains(flat, "needs the respond grant") {
		t.Errorf("ASSERTION: A was not refused for the respond grant:\n%s", a)
	}
	// Give stray keys time to land before reading B.
	time.Sleep(time.Second)
	if b, _ := tuiosCLI(t, base, "capture-pane", "-s", "rail", "-w", "PROMPTPANE"); strings.Contains(b, "ANSWERED") {
		t.Fatalf("ASSERTION: A answered B's prompt without respond:\n%s", b)
	}
	saveFrame(t, term, "agents-off-respond-refused")

	// The positive half: with respond, the same keys answer the prompt.
	out, err := tuiosCLI(t, base, "list-windows", "-s", "rail", "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
			Name     string `json:"custom_name"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("list-windows JSON: %v\n%s", err, out)
	}
	var paneA string
	for _, w := range listing.Windows {
		if w.Name == "AGENTPANE" {
			paneA = w.WindowID
		}
	}
	if paneA == "" {
		t.Fatalf("no AGENTPANE in %s", out)
	}
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "rail", "-w", paneA, "--grants", "admin,respond"); err != nil {
		t.Fatalf("give A respond: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "rail", "-w", "AGENTPANE", "clear; "+answer); err != nil {
		t.Fatalf("send-text into A: %v\n%s", err, out)
	}
	waitPane("PROMPTPANE", "ANSWERED:y")
	alive(t, term, "after the respond check")
}

// TestAgentsSwitchAppliesOnReload flips the switch in config.toml while the
// daemon and a client run. Off takes the fake agent's row off the rail and
// makes the daemon refuse agent verbs; on brings both back, with no restart.
//
// Negative control: on origin/main the file change does nothing, the row
// stays, and the wait for it to go fails.
func TestAgentsSwitchAppliesOnReload(t *testing.T) {
	const cols, rows, width = 120, 32, 24
	base := agentsOffFixture(t, false)
	term := attachIn(t, base, "rail", startOpts{cols: cols, rows: rows, shippedLooks: true})
	railCol := cols - width
	startFakeAgent(t, base, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railHasAgent(s, railCol, "AGENTPANE") }, uiTimeout); err != nil {
		t.Fatalf("the rail never listed the fake agent: %v\n%s", err, term.Snapshot())
	}

	writeAgentsConfig(t, base, true)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railAgentRows(s, railCol) == nil }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the agents section stayed after the switch went off: %v\n%s", err, term.Snapshot())
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "get-agent-state", "-s", "rail", "-w", "AGENTPANE")
		if err != nil && strings.Contains(out, "Agent features are off") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the daemon still serves get-agent-state after the switch went off: %v\n%s", err, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	railShot(t, term, "rail-after-reload-off")

	writeAgentsConfig(t, base, false)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railHasAgent(s, railCol, "AGENTPANE") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the agent did not come back after the switch went on: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "get-agent-state", "-s", "rail", "-w", "AGENTPANE"); err != nil {
		t.Errorf("ASSERTION: get-agent-state still refused after the switch went on: %v\n%s", err, out)
	}
	railShot(t, term, "rail-after-reload-on")
	alive(t, term, "after the switch went off and on")
}
