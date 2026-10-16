package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// railSectionHeads are the words that open a section of the rail.
var railSectionHeads = []string{"sessions", "spaces", "terminals", "agents", "files"}

// agentsSectionLines is what the rail's agents section shows in one frame,
// header excluded: nil when the section is not drawn at all.
func agentsSectionLines(s tuitest.Screen) []string {
	lines := railOf(s)
	at := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "agents") })
	if at < 0 {
		return nil
	}
	out := []string{}
	for _, l := range lines[at+1:] {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(l, "───") || slices.ContainsFunc(railSectionHeads, func(h string) bool { return strings.HasPrefix(t, h) }) {
			break
		}
		out = append(out, l)
	}
	return out
}

// agentRowShown reports whether the agents section has a row for the pane
// named name.
func agentRowShown(s tuitest.Screen, name string) bool {
	return slices.ContainsFunc(agentsSectionLines(s), func(l string) bool { return strings.Contains(l, name) })
}

// TestAgentRowGoesWhenTheAgentExits runs a stand-in claude in a pane through
// the real Claude Code hook, the way the shipped integration does, under a
// client with the rail open and the pane focused. The stand-in is a program
// named claude, so the pane's foreground is called claude as a real one's is.
// It works a turn with tool calls and finishes it (Stop: done, seen because
// the pane is focused, so the row wears the at-rest mark), then leaves the way
// /exit does, and the pane stays open with its shell running.
//
// The agents section lists every pane running an agent (docs/AGENT_STATE.md,
// "The rail's agents section"), so once the agent has left, the row must go.
// The cases:
//
//   - session-end: SessionEnd reports none, then the process ends.
//   - session-end-typed: the same, typed through the client, so every key is
//     also a state push racing the hook's.
//   - session-end-other-session: the client is attached to another session
//     and reads the pane from its listing.
//   - crash: the process ends with no SessionEnd, as a crashed or killed
//     Claude Code does, or one whose SessionEnd hook did not land. The
//     daemon kept the last report for the life of the pane, the row with it.
//   - note-after-exit: once the agent has gone, a person leaves a state on
//     the pane with set-agent-state, and it has to stay: only the harness's
//     own claim lapses with the harness.
//
// The daemon's listing is printed on failure, so a failure says which side
// kept the row.
//
// Negative controls: NEGATIVE_CONTROLS.md, "Agent row after exit".
func TestAgentRowGoesWhenTheAgentExits(t *testing.T) {
	sessionEnd := cc("", `"hook_event_name":"SessionEnd","reason":"prompt_input_exit"`)
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name string
		end  string
		// home is the session the client attaches to, "" for the agent's own.
		home string
		// typed types into the pane through the client, as the person does,
		// so every keystroke is also a state push from the client.
		typed bool
		// note has a person leave a state on the pane once the agent has
		// gone, which has to stay: only the harness's own claim lapses.
		note bool
	}{
		{"session-end", sessionEnd, "", false, false},
		{"session-end-typed", sessionEnd, "", true, false},
		{"session-end-other-session", sessionEnd, "e2e-home", false, false},
		{"crash", "", "", false, false},
		{"note-after-exit", sessionEnd, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			useShippedLooks(base)
			// claude on PATH and run by that name, so the foreground-process
			// detector takes it for Claude Code as it does the real one.
			bin := filepath.Join(base, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
			writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\n")
			killDaemon(t, base)
			const session = "e2e-exit"
			if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
				t.Fatalf("create %s: %v\n%s", session, err, out)
			}
			if out, err := tuiosCLI(t, base, "set-window", "-s", session, "-w", "0", "--name", "pilot"); err != nil {
				t.Fatalf("name the pane: %v\n%s", err, out)
			}
			// A program named claude runs the stand-in's script, so the pane's
			// foreground process is called claude, as a real one's is.
			if err := os.Symlink(fakeClaudeBin, filepath.Join(bin, "claude")); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(base, "fake-claude.sh")
			if err := os.WriteFile(script, []byte(strings.ReplaceAll(fakeClaudeScript, "__TUIOS__", tuiosBin)), 0o755); err != nil {
				t.Fatal(err)
			}
			lines := []string{
				cc("", `"hook_event_name":"SessionStart","source":"startup"`),
				cc("", `"hook_event_name":"UserPromptSubmit","prompt":"Run the tests and read the rail."`),
				cc("", `"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","description":"Run the tests"},"tool_use_id":"toolu_01x1"`),
				cc("", `"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","description":"Run the tests"},"tool_response":{"stdout":"ok","stderr":"","interrupted":false},"tool_use_id":"toolu_01x1","duration_ms":40`),
				cc("", `"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"internal/app/render_sidebar.go"},"tool_use_id":"toolu_01x2"`),
				cc("", `"hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"internal/app/render_sidebar.go"},"tool_response":{"type":"text"},"tool_use_id":"toolu_01x2","duration_ms":3`),
				cc("", `"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Hello.","background_tasks":[],"session_crons":[]`),
				"wait rest",
			}
			if tc.end != "" {
				lines = append(lines, tc.end)
			}
			scenario := filepath.Join(base, "scenario.txt")
			if err := os.WriteFile(scenario, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			hookLog := filepath.Join(base, "hooks.log")
			defer func() {
				if b, err := os.ReadFile(hookLog); err == nil && t.Failed() {
					t.Logf("hook log:\n%s", b)
				}
			}()

			attach := session
			if tc.home != "" {
				attach = tc.home
				if out, err := tuiosCLI(t, base, "new", tc.home, "--detach"); err != nil {
					t.Fatalf("create %s: %v\n%s", tc.home, err, out)
				}
			}
			term := attachIn(t, base, attach, startOpts{cols: 120, rows: 36})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			typeLine := func(line string) {
				t.Helper()
				if tc.typed {
					if err := term.Type(line + "\r"); err != nil {
						t.Fatalf("type %q: %v", line, err)
					}
					return
				}
				if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "0", line+"\n"); err != nil {
					t.Fatalf("send %q: %v\n%s", line, err, out)
				}
			}
			if tc.typed {
				enterTerminalMode(t, term)
			}
			typeLine("claude " + script + " " + scenario + " " + hookLog)
			waitCapture(t, base, session, "0", "FAKE-AT rest")

			// The finished turn, looked at: the row is in the section, at rest.
			if err := term.WaitFor(func(s tuitest.Screen) bool { return agentRowShown(s, "pilot") }, uiTimeout); err != nil {
				t.Fatalf("the agents section never listed the agent: %v\n%s", err, term.Snapshot())
			}
			saveFrame(t, term, "agent-exit-"+tc.name+"-before")

			// Leave the way /exit does: SessionEnd (when there is one), then the
			// process ends and the shell has the pane again.
			typeLine("/exit")
			if tc.end != "" {
				waitCapture(t, base, session, "0", "FAKE-DONE")
			}
			typeLine("echo SHELL-BACK")
			waitCapture(t, base, session, "0", "SHELL-BACK")

			err := term.WaitFor(func(s tuitest.Screen) bool { return !agentRowShown(s, "pilot") }, uiTimeout)
			listing, _ := tuiosCLI(t, base, "list-agents", "-s", session, "--all", "--json")
			saveFrame(t, term, "agent-exit-"+tc.name+"-after")
			if err != nil {
				t.Fatalf("the agents section still lists the pane after its agent left it, with the shell running: %v\nagents section: %q\ndaemon listing:\n%s\n%s",
					err, agentsSectionLines(term.Screen()), listing, term.Snapshot())
			}
			if !tc.note {
				return
			}
			if out, err := tuiosCLI(t, base, "set-agent-state", "idle", "-s", session, "-w", "pilot", "-m", "back after lunch"); err != nil {
				t.Fatalf("leave a note: %v\n%s", err, out)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return agentRowShown(s, "pilot") }, uiTimeout); err != nil {
				t.Fatalf("the note never showed: %v\n%s", err, term.Snapshot())
			}
			// Three detection ticks at the default interval, at the shell, with
			// the harness that once reported here long gone.
			time.Sleep(6 * time.Second)
			if !agentRowShown(term.Screen(), "pilot") {
				listing, _ := tuiosCLI(t, base, "list-agents", "-s", session, "--all", "--json")
				t.Fatalf("a person's note on the pane was cleared as if the harness had said it:\n%s\n%s", listing, term.Snapshot())
			}
		})
	}
}
