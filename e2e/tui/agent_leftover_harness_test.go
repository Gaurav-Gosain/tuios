package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// listedAgents is the window ids list-agents reports for session, without
// --all, so only the panes it takes for agents.
func listedAgents(t *testing.T, base, session string) ([]string, string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-agents", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-agents: %v\n%s", err, out)
	}
	var payload struct {
		Agents []struct {
			ID string `json:"window_id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list-agents json: %v\n%s", err, out)
	}
	ids := make([]string, 0, len(payload.Agents))
	for _, a := range payload.Agents {
		ids = append(ids, a.ID)
	}
	return ids, out
}

// TestListAgentsDropsAPaneWhoseAgentEnded runs a stand-in claude through the
// real Claude Code hook and lets it end the way /exit does: SessionEnd
// reports none, then the process exits and the shell has the pane again.
// list-agents lists a pane while it has a state or a harness, and the case
// that failed kept the harness: the hook's none report names claude-code, the
// report kept the harness it named, and with the state already none nothing
// afterwards cleared it. The pane stayed in the listing, at none with harness
// claude-code and the shell at its prompt, for as long as it lived.
//
// The cases:
//   - session-end: the hooked agent, through tuios agent-hook.
//   - reported-none: a report from outside the pane says working, then none,
//     both naming the harness, with no agent process at all, which is what a
//     herdr pane.release_agent sends. A note left afterwards with
//     set-agent-state has to list the pane again: only the agent's own
//     attribution goes.
//
// How this could pass wrongly, written down first:
//   - A pane never listed would pass the absence check. Each case first waits
//     for the pane to be listed.
//   - A pane that leaves the listing and comes back would pass one read. The
//     listing is read for a further four seconds once it is gone.
//   - Clearing everything would also clear the conversation id, which the
//     resume offer after a restart needs. session-end checks it is kept.
//
// Negative controls: NEGATIVE_CONTROLS.md, "Agent listing after SessionEnd".
func TestListAgentsDropsAPaneWhoseAgentEnded(t *testing.T) {
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name   string
		hooked bool
	}{
		{name: "session-end", hooked: true},
		{name: "reported-none"},
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
			killDaemon(t, base)
			const session = "e2e-leftover"
			if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
				t.Fatalf("create %s: %v\n%s", session, err, out)
			}
			if out, err := tuiosCLI(t, base, "set-window", "-s", session, "-w", "0", "--name", "pilot"); err != nil {
				t.Fatalf("name the pane: %v\n%s", err, out)
			}
			win := windowID(t, base, session, "pilot")

			var trace []string
			began := time.Now()
			note := func(format string, args ...any) {
				trace = append(trace, time.Since(began).Round(10*time.Millisecond).String()+" "+strings.TrimSpace(fmt.Sprintf(format, args...)))
			}
			defer func() {
				dir := artifactDir(t)
				if err := os.WriteFile(filepath.Join(dir, "trace.txt"), []byte(strings.Join(trace, "\n")+"\n"), 0o644); err != nil {
					t.Errorf("save the trace: %v", err)
				}
				pane, _ := tuiosCLI(t, base, "capture-pane", "-s", session)
				if err := os.WriteFile(filepath.Join(dir, "pane.txt"), []byte(pane), 0o644); err != nil {
					t.Errorf("save the pane: %v", err)
				}
				t.Logf("trace and pane saved under %s", dir)
			}()
			listed := func() (bool, string) {
				ids, out := listedAgents(t, base, session)
				st := readAgentState(t, base, session, win)
				note("listed=%v state=%s source=%s harness=%q session=%q", slices.Contains(ids, win), st.State, st.Source, st.Harness, st.AgentSession)
				return slices.Contains(ids, win), out
			}
			waitListed := func(want bool, why string) {
				t.Helper()
				var out string
				for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
					var got bool
					if got, out = listed(); got == want {
						return
					}
				}
				t.Fatalf("%s: list-agents still says listed=%v for %s, want %v\nstate: %+v\nlisting:\n%s",
					why, !want, win, want, readAgentState(t, base, session, win), out)
			}
			stayGone := func(why string) {
				t.Helper()
				for end := time.Now().Add(4 * time.Second); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
					if got, out := listed(); got {
						t.Fatalf("%s: the pane came back to the listing\nstate: %+v\nlisting:\n%s", why, readAgentState(t, base, session, win), out)
					}
				}
			}

			if !tc.hooked {
				if out, err := tuiosCLI(t, base, "set-agent-state", "working", "--harness", "claude-code", "-s", session, "-w", win); err != nil {
					t.Fatalf("report working: %v\n%s", err, out)
				}
				waitListed(true, "the working report")
				if out, err := tuiosCLI(t, base, "set-agent-state", "none", "--harness", "claude-code", "-s", session, "-w", win); err != nil {
					t.Fatalf("report none: %v\n%s", err, out)
				}
				waitListed(false, "after none naming the harness")
				stayGone("after none naming the harness")
				if out, err := tuiosCLI(t, base, "set-agent-state", "idle", "-s", session, "-w", win, "-m", "back after lunch"); err != nil {
					t.Fatalf("leave a note: %v\n%s", err, out)
				}
				waitListed(true, "a person's note after the agent left")
				return
			}

			script := filepath.Join(base, "fake-claude.sh")
			if err := os.WriteFile(script, []byte(strings.ReplaceAll(fakeClaudeScript, "__TUIOS__", tuiosBin)), 0o755); err != nil {
				t.Fatal(err)
			}
			lines := []string{
				cc("", `"hook_event_name":"SessionStart","source":"startup"`),
				cc("", `"hook_event_name":"UserPromptSubmit","prompt":"Run the tests."`),
				cc("", `"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Done.","background_tasks":[],"session_crons":[]`),
				"wait rest",
				cc("", `"hook_event_name":"SessionEnd","reason":"prompt_input_exit"`),
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
			send := func(line string) {
				t.Helper()
				if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", win, line+"\n"); err != nil {
					t.Fatalf("send %q: %v\n%s", line, err, out)
				}
			}
			send("claude " + script + " " + scenario + " " + hookLog)
			waitCapture(t, base, session, "0", "FAKE-AT rest")
			waitListed(true, "the agent at rest")
			send("/exit")
			waitCapture(t, base, session, "0", "FAKE-DONE")
			send("echo SHELL-BACK")
			waitCapture(t, base, session, "0", "SHELL-BACK")
			waitListed(false, "after SessionEnd, with the shell back")
			stayGone("after SessionEnd, with the shell back")
			if st := readAgentState(t, base, session, win); st.AgentSession != ccSession {
				t.Fatalf("the conversation id went with the agent, so nothing can offer to resume it: %+v", st)
			}
		})
	}
}
