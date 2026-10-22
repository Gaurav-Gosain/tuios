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

// progressAgentScript is the stand-in claude's side. It reads its scenario a
// line at a time: a hook payload goes to the real Claude Code hook, "osc X"
// writes the progress sequence ESC ] 9 ; 4 ; X BEL as the agent itself, and
// "title X" sets the window title to X, and "wait LABEL" prints FAKE-AT LABEL
// and waits for a line from the test. A sequence is followed by a line of
// output a moment later: the daemon reads a parked sequence on the output
// event after the one that carried it, when the emulator may not have parsed
// it yet.
const progressAgentScript = `#!/bin/sh
scenario=$1
log=$2
hook() { printf '%s' "$1" | "__TUIOS__" agent-hook claude-code --timeout 10s --explain 2>>"$log"; }
while IFS= read -r line <&3; do
	case "$line" in
	''|'#'*) ;;
	'wait '*) printf 'FAKE-AT %s\n' "${line#wait }"; read -r _ ;;
	'osc '*) printf '\033]9;4;%s\007' "${line#osc }"; sleep 0.3; printf 'FAKE-OSC\n' ;;
	'title '*) printf '\033]2;%s\007' "${line#title }"; sleep 0.3; printf 'FAKE-TITLE\n' ;;
	*) hook "$line" ;;
	esac
done 3<"$scenario"
printf 'FAKE-DONE\n'
`

// The programs that are not agents. mise draws a progress bar with OSC 9;4
// the way mise, brew and cargo do: a determinate bar, a while, the bar
// cleared. spin animates a braille spinner in the window title, which is the
// shape of Claude Code's own working title. Each sequence is followed by a
// line a moment later, for the reason progressAgentScript gives.
const (
	progressBarScript = `#!/bin/sh
printf 'MISE-START\n'
printf '\033]9;4;1;50\007'
sleep 0.3
printf 'downloading\n'
sleep 2
printf '\033]9;4;0\007'
sleep 0.3
printf 'MISE-DONE\n'
`
	titleSpinnerScript = `#!/bin/sh
printf 'SPIN-START\n'
for f in '⠋' '⠙' '⠹' '⠸'; do
	printf '\033]2;%s Downloading\007' "$f"
	sleep 0.3
	printf 'tick\n'
	sleep 0.4
done
printf 'SPIN-DONE\n'
`
)

// progressReading is one get-agent-state read, kept for the saved trace.
type progressReading struct {
	At      string `json:"at"`
	Step    string `json:"step"`
	State   string `json:"state"`
	Source  string `json:"source"`
	Harness string `json:"harness_id"`
}

// TestAgentSignalsOnAnExitedAgentsPane runs programs that are not agents, one
// drawing an OSC 9;4 progress bar and one a spinner in the window title, in a
// pane whose agent has left, and agents that send the same sequences in panes
// where they run.
//
// The bug: Claude Code ran in a pane and left it, and the pane was then used
// for `mise upgrade`. mise's progress bar was read as the agent's, so the pane
// showed Claude Code working, then idle for good, with no agent in it. A pane
// kept the harness its agent was attributed to after the agent left (a none
// report that named it kept it, see TestListAgentsDropsAPaneWhoseAgentEnded),
// and that remembered harness was taken for an agent being there: it let
// OSC 9;4 apply, and it ran the harness's title, screen and
// notification rules against whatever ran in the pane next. What held this
// off was only the rank of the claim the agent's last report left: a hook's
// SessionEnd ranks above the OSC tier, a claim at the OSC tier or below (or
// none at all, which a live upgrade leaves) does not.
//
// The cases:
//   - hook-exit: a stand-in claude reports through the real Claude Code hook,
//     finishes a turn, sends SessionEnd and exits, then mise runs. A guard:
//     the SessionEnd claim held this off already.
//   - osc-tier-exit-bar: a reporter at the OSC tier says working and then
//     none, naming claude-code, then mise runs.
//   - osc-tier-exit-title: the same, then spin runs.
//   - agent-progress: the positive half. A stand-in claude that sends no
//     hooks, which the foreground-process detector finds, draws its own bar
//     and clears it, and the pane goes idle on the OSC tier's word.
//   - agent-title: the same agent puts a spinner in the title, and it is read.
//   - restart-after-exit: hook-exit, then a new claude in the same pane draws
//     and clears its bar before any hook of its own. It is read once the
//     detector has seen the new agent.
//
// How this could pass wrongly, written down first:
//   - A sequence the daemon never parsed would leave every pane at none. The
//     positive cases fail then: they need source osc.
//   - A detector that clears the pane between the sequence and the read would
//     hide the state. The trace is read every 200ms through the program's run
//     and for four seconds after, and any reading other than none fails.
//
// Negative controls: NEGATIVE_CONTROLS.md, "Agent signals on an exited
// agent's pane".
func TestAgentSignalsOnAnExitedAgentsPane(t *testing.T) {
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	hooked := []string{
		cc("", `"hook_event_name":"SessionStart","source":"startup"`),
		cc("", `"hook_event_name":"UserPromptSubmit","prompt":"Say hello."`),
		cc("", `"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Hello.","background_tasks":[],"session_crons":[]`),
		"wait rest",
		cc("", `"hook_event_name":"SessionEnd","reason":"prompt_input_exit"`),
	}
	for _, tc := range []struct {
		name string
		// exit is how an agent leaves the pane first: "hook" or "osc", "" for
		// a pane no agent ran in.
		exit string
		// program is the program that is not an agent run next, "mise" or
		// "spin"; "" runs a second agent instead, with agent as its scenario,
		// and what it sends has to be read.
		program string
		agent   []string
		// want is the state the second agent's sequence has to leave.
		want string
	}{
		{name: "hook-exit", exit: "hook", program: "mise"},
		{name: "osc-tier-exit-bar", exit: "osc", program: "mise"},
		{name: "osc-tier-exit-title", exit: "osc", program: "spin"},
		{name: "agent-progress", agent: []string{"wait up", "osc 1;50", "osc 0", "wait sent"}, want: "idle"},
		{name: "agent-title", agent: []string{"wait up", "title ⠋ Thinking", "wait sent"}, want: "working"},
		{name: "restart-after-exit", exit: "hook", agent: []string{"wait up", "osc 1;50", "osc 0", "wait sent"}, want: "idle"},
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
			for name, body := range map[string]string{"mise": progressBarScript, "spin": titleSpinnerScript} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
			script := filepath.Join(base, "fake-claude.sh")
			if err := os.WriteFile(script, []byte(strings.ReplaceAll(progressAgentScript, "__TUIOS__", tuiosBin)), 0o755); err != nil {
				t.Fatal(err)
			}
			writeScenario := func(name string, lines []string) string {
				p := filepath.Join(base, name)
				if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return p
			}
			hookLog := filepath.Join(base, "hooks.log")
			writeConfig(t, base, "[daemon]\nagent_detect_seconds = 1\n")
			killDaemon(t, base)
			const session = "e2e-signals"
			if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
				t.Fatalf("create %s: %v\n%s", session, err, out)
			}

			began := time.Now()
			var trace []progressReading
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
				if b, err := os.ReadFile(hookLog); err == nil {
					_ = os.WriteFile(filepath.Join(dir, "hooks.log"), b, 0o644)
				}
				t.Logf("agent state trace and pane saved under %s", dir)
			}()
			read := func(step string) progressReading {
				t.Helper()
				st := readAgentState(t, base, session, "0")
				r := progressReading{
					At:   time.Since(began).Round(10 * time.Millisecond).String(),
					Step: step, State: st.State, Source: st.Source, Harness: st.Harness,
				}
				trace = append(trace, r)
				return r
			}
			until := func(step string, d time.Duration, ok func(progressReading) bool) (progressReading, bool) {
				t.Helper()
				var r progressReading
				for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
					if r = read(step); ok(r) {
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

			switch tc.exit {
			case "hook":
				scenario := writeScenario("hooked.txt", hooked)
				send("claude "+script+" "+scenario+" "+hookLog+"\n", "start the agent")
				waitCapture(t, base, session, "0", "FAKE-AT rest")
				if r, ok := until("agent at rest", 10*time.Second, func(r progressReading) bool {
					return r.Harness == "claude-code" && r.State != "none"
				}); !ok {
					t.Fatalf("the hook never reported the agent, last read %+v", r)
				}
				send("\n", "let the agent end its session and exit")
				waitCapture(t, base, session, "0", "FAKE-DONE")
			case "osc":
				for _, state := range []string{"working", "none"} {
					if out, err := tuiosCLI(t, base, "set-agent-state", state, "--source", "osc", "--harness", "claude-code", "-s", session, "-w", "0"); err != nil {
						t.Fatalf("report %s: %v\n%s", state, err, out)
					}
				}
			}
			if tc.exit != "" {
				send("echo SHELL-BACK\n", "check the shell has the pane")
				waitCapture(t, base, session, "0", "SHELL-BACK")
				// Two detector passes with the pane at its shell.
				time.Sleep(2 * time.Second)
				if r := read("agent gone"); r.State != "none" || r.Harness != "" {
					t.Fatalf("after its agent left, the pane is %s (source %s, harness %q), want none and no harness", r.State, r.Source, r.Harness)
				}
			}

			if tc.program == "" {
				scenario := writeScenario("unhooked.txt", tc.agent)
				send("claude "+script+" "+scenario+" "+hookLog+"\n", "start the agent that sends a sequence")
				waitCapture(t, base, session, "0", "FAKE-AT up")
				if r, ok := until("agent found", 10*time.Second, func(r progressReading) bool {
					return r.State == "working" && r.Harness == "claude-code"
				}); !ok {
					t.Fatalf("the detector never found the agent, last read %+v", r)
				}
				send("\n", "send the sequence")
				waitCapture(t, base, session, "0", "FAKE-AT sent")
				if r, ok := until("agent's sequence", 10*time.Second, func(r progressReading) bool {
					return r.State == tc.want && r.Source == "osc"
				}); !ok {
					t.Fatalf("ASSERTION: the agent's own sequence was not read: the pane is %s (source %s, harness %s), want %s from osc", r.State, r.Source, r.Harness, tc.want)
				}
				send("\n", "let the agent exit")
				waitCapture(t, base, session, "0", "FAKE-DONE")
				return
			}

			done := strings.ToUpper(tc.program) + "-DONE"
			send(tc.program+" upgrade\n", "run the program that is not an agent")
			waitCapture(t, base, session, "0", strings.ToUpper(tc.program)+"-START")
			if r, ok := until(tc.program+" running", 10*time.Second, func(r progressReading) bool {
				return r.State != "none" || strings.Contains(captureOf(t, base, session), done)
			}); ok && r.State != "none" {
				t.Fatalf("ASSERTION: %s, a program that is not an agent, made the pane an agent: state %s (source %s, harness %s)", tc.program, r.State, r.Source, r.Harness)
			}
			if r, ok := until(tc.program+" done", 4*time.Second, func(r progressReading) bool { return r.State != "none" }); ok {
				t.Fatalf("ASSERTION: %s, a program that is not an agent, made the pane an agent: state %s (source %s, harness %s)", tc.program, r.State, r.Source, r.Harness)
			}
		})
	}
}

// captureOf is the pane's text, "" when it cannot be read.
func captureOf(t *testing.T, base, session string) string {
	t.Helper()
	out, _ := tuiosCLI(t, base, "capture-pane", "-s", session, "-w", "0")
	return out
}
