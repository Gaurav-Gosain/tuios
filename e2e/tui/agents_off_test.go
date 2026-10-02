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
				if !helpFinds(t, term, "Inbox on its", "Open the Inbox on its mail") {
					t.Fatalf("the help does not list the Inbox key with the features on\n%s", term.Snapshot())
				}
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
			if helpFinds(t, term, "Inbox on its", "Open the Inbox on its mail") {
				t.Errorf("ASSERTION: the help lists the Inbox key with the features off\n%s", term.Snapshot())
			}
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
	// The quotes keep the typed line itself from matching the marker.
	answer := tuiosBin + " send-keys -s rail -w PROMPTPANE 'y Enter'; echo RESP_\"EXIT\"=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "rail", "-w", "AGENTPANE", answer); err != nil {
		t.Fatalf("send-text into A: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		a, _ := tuiosCLI(t, base, "capture-pane", "-s", "rail", "-w", "AGENTPANE")
		if strings.Contains(a, "RESP_EXIT=0") || strings.Contains(a, "RESP_EXIT=1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A's send-keys never finished:\n%s", a)
		}
		time.Sleep(200 * time.Millisecond)
	}
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

// waitAgentsSwitch waits until the daemon under base applies the switch: an
// agent verb is refused while it is off and served while it is on.
func waitAgentsSwitch(t *testing.T, base string, off bool) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "get-agent-state", "-s", "rail", "-w", "AGENTPANE")
		refused := err != nil && strings.Contains(out, "Agent features are off")
		if refused == off {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never applied the switch (off=%v): %v\n%s", off, err, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestAgentsOffDropsTheQueue: a message queued for an agent while the
// features were on is not typed after they go off and come back. The
// positive half queues to another pane with no switch in between, and the
// message is typed when that pane rests.
//
// Negative control: with dropAllQueued cut from the switch, QPANE's message
// is typed when its state goes idle after the switch comes back on.
func TestAgentsOffDropsTheQueue(t *testing.T) {
	base := agentsOffFixture(t, false)
	if out, err := tuiosCLI(t, base, "new-window", "-s", "rail", "QPANE", "--no-focus"); err != nil {
		t.Fatalf("open QPANE: %v\n%s", err, out)
	}
	queue := func(pane, marker string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "rail", "-w", pane, "working"); err != nil {
			t.Fatalf("set-agent-state working on %s: %v\n%s", pane, err, out)
		}
		if out, err := tuiosCLI(t, base, "queue", "-s", "rail", "-w", pane, "echo", marker); err != nil {
			t.Fatalf("queue on %s: %v\n%s", pane, err, out)
		}
	}
	captureOf := func(pane string) string {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", "rail", "-w", pane)
		return out
	}

	// The positive half: a queue with no switch in between is typed.
	queue("AGENTPANE", "POSMARK-9c1")
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "rail", "-w", "AGENTPANE", "idle"); err != nil {
		t.Fatalf("set-agent-state idle: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(captureOf("AGENTPANE"), "POSMARK-9c1") {
		if time.Now().After(deadline) {
			t.Fatalf("the queued message was never typed with no switch:\n%s", captureOf("AGENTPANE"))
		}
		time.Sleep(200 * time.Millisecond)
	}

	queue("QPANE", "LEAKMARK-4e2")
	writeAgentsConfig(t, base, true)
	waitAgentsSwitch(t, base, true)
	writeAgentsConfig(t, base, false)
	waitAgentsSwitch(t, base, false)
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "rail", "-w", "QPANE", "idle"); err != nil {
		t.Fatalf("set-agent-state idle on QPANE: %v\n%s", err, out)
	}
	time.Sleep(3 * time.Second)
	if c := captureOf("QPANE"); strings.Contains(c, "LEAKMARK-4e2") {
		t.Fatalf("ASSERTION: a message queued before the switch went off was typed after it came back:\n%s", c)
	}
}

// TestAgentsOnAfterStartingOffStartsTheInbox: a client that started with the
// features off has no Inbox watcher. Turned on in the file, it starts one,
// and a pane in another session waiting on the person reaches its Inbox.
//
// Negative control: with the watcher started only when the switch moves, the
// client that started off never starts it, and the Inbox stays empty.
func TestAgentsOnAfterStartingOffStartsTheInbox(t *testing.T) {
	base := agentsOffFixture(t, true)
	if out, err := tuiosCLI(t, base, "new", "other", "--detach"); err != nil {
		t.Fatalf("create the other session: %v\n%s", err, out)
	}
	term := attachIn(t, base, "rail", startOpts{cols: 120, rows: 32, shippedLooks: true})

	writeAgentsConfig(t, base, false)
	waitAgentsSwitch(t, base, false)
	if out, err := tuiosCLI(t, base, "set-agent-state", "needs_input", "-s", "other", "-m", "PICKME-31b"); err != nil {
		t.Fatalf("set-agent-state on the other session: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("PICKME-31b", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Inbox never showed the other session's prompt after the switch went on: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "agents-on-inbox")
	alive(t, term, "after the Inbox came back")
}

// TestAgentsOffTmuxShimTypesIntoItsOwnPane: with the features off, a script
// under the tmux shim splits a pane and types into it, as an agent team
// does. The pane it opened takes the keys. TestAgentsOffRespondGrantStillHolds
// is the other half: a pane that runs a program and that the caller did not
// open still refuses them.
//
// Negative control: with offTypingAllowed cut from typingRefusal, the
// send-keys is refused and the split pane never echoes the keys.
func TestAgentsOffTmuxShimTypesIntoItsOwnPane(t *testing.T) {
	base := agentsOffFixture(t, true)
	term := attachIn(t, base, "rail", startOpts{cols: 120, rows: 32, shippedLooks: true})
	script := filepath.Join(base, "split.sh")
	body := strings.Join([]string{
		"set -e",
		"P=$(tmux split-window -d -P -F '#{pane_id}' -- cat)",
		"sleep 1",
		"tmux send-keys -t $P 'SHIMKEYS-5d7' Enter",
		"echo SHIM_DONE",
	}, "\n") + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	line := tuiosBin + " tmux-shim -- sh " + script + ` || echo SHIM_"FAILED"` + "\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "rail", "-w", "AGENTPANE", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "SHIM_DONE") || strings.Contains(s.Text(), "SHIM_FAILED")
	}, uiTimeout); err != nil {
		t.Fatalf("the script under the shim never finished: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "SHIM_FAILED") {
		a, _ := tuiosCLI(t, base, "capture-pane", "-s", "rail", "-w", "AGENTPANE")
		t.Fatalf("ASSERTION: the shim could not type into the pane it opened:\n%s", a)
	}
	// cat echoes the line in the pane it runs in. The pane opened with -d
	// may be out of sight, so it is read with capture-pane.
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
	split := ""
	for _, w := range listing.Windows {
		if w.Name != "AGENTPANE" {
			split = w.WindowID
		}
	}
	if split == "" {
		t.Fatalf("the shim opened no pane:\n%s", out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		c, _ := tuiosCLI(t, base, "capture-pane", "-s", "rail", "-w", split)
		if strings.Contains(c, "SHIMKEYS-5d7") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the split pane never got the keys:\n%s", c)
		}
		time.Sleep(200 * time.Millisecond)
	}
	alive(t, term, "after the shim typed into its pane")
}

// TestAgentsSwitchIsThePersons: a process in a pane cannot change
// agents.enabled with set-config, and the config file keeps no change. The
// same call from outside every pane, with a client attached, turns the
// features off.
//
// Negative control: with the pane check cut from verbSetOption, the pane's
// set-config exits 0 and SC_EXIT=1 never appears.
func TestAgentsSwitchIsThePersons(t *testing.T) {
	base := agentsOffFixture(t, false)
	term := attachIn(t, base, "rail", startOpts{cols: 120, rows: 32, shippedLooks: true})

	// The quotes keep the typed line itself from matching the marker.
	line := tuiosBin + " set-config agents.enabled false; echo SC_\"EXIT\"=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "rail", "-w", "AGENTPANE", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "SC_EXIT=0") || strings.Contains(s.Text(), "SC_EXIT=1")
	}, uiTimeout); err != nil {
		t.Fatalf("the pane's set-config never finished: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "SC_EXIT=1") {
		t.Fatalf("ASSERTION: a pane changed agents.enabled:\n%s", term.Snapshot())
	}
	if raw, _ := os.ReadFile(configPathIn(base)); strings.Contains(string(raw), "enabled = false") {
		t.Fatalf("ASSERTION: the config file took a pane's change:\n%s", raw)
	}
	waitAgentsSwitch(t, base, false)

	if out, err := tuiosCLI(t, base, "set-config", "agents.enabled", "false"); err != nil {
		t.Fatalf("set-config from outside every pane: %v\n%s", err, out)
	}
	waitAgentsSwitch(t, base, true)
	alive(t, term, "after the person turned the features off")
}

// TestAgentsOffHereRefusesACallToAnotherMachine: with this machine's
// features off, an agent command aimed at a session on a linked machine is
// refused here, by name of this machine's config, while the other machine
// has them on. A plain command to that machine still runs.
//
// Negative control: with the HostCallGuard cut from VerbClient, list-agents
// on build:far succeeds after the switch went off here.
func TestAgentsOffHereRefusesACallToAnotherMachine(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	env := hubWithBuild(t, base, remote)
	if out, err := tuiosCLIEnv(t, base, env, "list-agents", "-s", "build:far", "--all"); err != nil {
		t.Fatalf("list-agents on build with the features on here: %v\n%s", err, out)
	}

	cfg := configPathIn(base)
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(raw, []byte("\n[agents]\nenabled = false\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tuiosCLIEnv(t, base, env, "list-agents", "-s", "build:far", "--all")
	if err == nil || !strings.Contains(out, "Agent features are off in this machine's config") {
		t.Fatalf("ASSERTION: list-agents on build was not refused by this machine's switch: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, env, "list-windows", "-s", "build:far"); err != nil {
		t.Fatalf("ASSERTION: a plain command to build stopped working: %v\n%s", err, out)
	}
}

// helpFinds opens the help, searches it for query, and reports whether want
// is among the results. The search lists every tab at once.
func helpFinds(t *testing.T, term *tuitest.Terminal, query, want string) bool {
	t.Helper()
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Ctrl('b'), "?"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Keybindings", uiTimeout); err != nil {
		t.Fatalf("the help never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(query); err != nil {
		t.Fatal(err)
	}
	found := term.WaitForText(want, 2*time.Second) == nil
	if !found {
		t.Logf("help search for %q:\n%s", query, term.Snapshot())
	}
	_ = term.SendKeys(tuitest.Esc)
	_ = term.SendKeys(tuitest.Esc)
	return found
}

// TestAgentsOffHidesAnotherMachinesAgentMarks: a session on a linked machine
// whose agent waits on the person wears the needs_input mark on this rail.
// With this machine's features off the mark goes, though that machine still
// has them on.
//
// Negative control: with clearAgentMarks cut from withHostGroups, the mark
// stays on the remote session's row after the switch went off.
func TestAgentsOffHidesAnotherMachinesAgentMarks(t *testing.T) {
	const cols, rows, width = 120, 32, 24
	base := t.TempDir()
	useShippedLooks(base)
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, remote, "set-agent-state", "needs_input", "-s", "far"); err != nil {
		t.Fatalf("set-agent-state on build: %v\n%s", err, out)
	}
	env := hubWithBuild(t, base, remote)
	term := attachIn(t, base, "home", startOpts{cols: cols, rows: rows, shippedLooks: true, env: env})
	railCol := cols - width
	railHasMark := func(s tuitest.Screen) bool {
		_, h := s.Size()
		for y := range h {
			var b strings.Builder
			for x := railCol; x < cols; x++ {
				b.WriteString(s.Cell(x, y).Content)
			}
			if strings.Contains(b.String(), "far") && strings.Contains(b.String(), needsInputGlyph) {
				return true
			}
		}
		return false
	}
	if err := term.WaitFor(railHasMark, uiTimeout); err != nil {
		t.Fatalf("the rail never marked build's waiting session with the features on: %v\n%s", err, term.Snapshot())
	}

	cfg := configPathIn(base)
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(raw, []byte("\n[agents]\nenabled = false\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !railHasMark(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: build's agent mark stayed on the rail with the features off here: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the remote mark went")
}
