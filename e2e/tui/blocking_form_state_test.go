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

// formAgentScript is a stand-in for Claude Code with auto mode on, run
// through fakeclaude so the pane's program is claude. It reports its states
// with the real hooks. A prompt runs a turn that ends with a Stop, and then
// the stand-in draws, over its input box, the form Claude Code draws to teach
// auto mode about the environment: exactly the lines captured from two
// blocked workers, with the footer that counts a background shell. No hook
// fires for the form. Esc closes it, and the input box comes back with the
// same footer.
//
// $1 is tuios.
const formAgentScript = `T="$1"
hook() { printf '%s' "$1" | "$T" agent-hook claude-code >/dev/null 2>&1; }
R=$(stty size | cut -d' ' -f1)
RULE="────────────────────────────────────────────────────────────────────────────────"
FOOT="  ⏵⏵ auto mode on (shift+tab to cycle) · 1 shell, 1 monitor · ↓ to manage"
box() {
  printf '\033[H\033[2J\033[1;1H Claude Code\033[3;1H%s' "$1"
  printf '\033[%d;1H%s\033[%d;1H❯ \033[%d;1H%s\033[%d;1H%s' $((R-3)) "$RULE" $((R-2)) $((R-1)) "$RULE" "$R" "$FOOT"
}
form() {
  printf '\033[%d;1H%s' $((R-10)) "$RULE"
  printf '\033[%d;1H  Teach auto mode about your environment?' $((R-9))
  printf '\033[%d;1H  Auto mode works better when it knows your environment. Takes about a minute.' $((R-8))
  printf '\033[%d;1H  ❯ 1. Yes' $((R-7))
  printf '\033[%d;1H    2. Not now' $((R-6))
  printf '\033[%d;1H    3. Don'"'"'t show again' $((R-5))
  printf '\033[%d;1H  Enter to confirm · Esc to cancel' $((R-4))
}
printf '\033]0;\342\234\263 Claude Code\007\033[?1049h'
hook '{"hook_event_name":"SessionStart","session_id":"tk-form","source":"startup"}'
box "Ready."
IFS= read -r l
hook '{"hook_event_name":"UserPromptSubmit","session_id":"tk-form","prompt":"'"$l"'"}'
box "Working on it."
sleep 1
box "Done with [$l]."
hook '{"hook_event_name":"Stop","session_id":"tk-form","last_assistant_message":"Done with it."}'
sleep 1
form
stty raw -echo
k=$(dd bs=1 count=1 2>/dev/null)
stty sane
box "Done with [$l]."
echo FORM-CLOSED >"$2"
sleep 600
`

// TestABlockingFormOutranksADoneReport: Claude Code ends a turn with a Stop
// (done), then draws a form over its input box and waits on it with no hook.
// The pane must read needs_input while the form is up, and be at rest again
// (done or idle) once Esc closes it, with the background shell its footer
// counts as a note.
//
// How it could pass wrongly: the form could be read before the Stop lands,
// so the pane is first seen done with the Stop's message; and needs_input
// could come from a rule that reads any ❯ line, so the message must be the
// form's. The note is shown beside done and idle only: the pane on the form
// must show none, though the footer under the form counts the shell too.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestABlockingFormOutranksADoneReport).
func TestABlockingFormOutranksADoneReport(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const session = "e2e-form-state"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "claude"), "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	script := filepath.Join(base, "form-agent.sh")
	if err := os.WriteFile(script, []byte(formAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	closed := filepath.Join(base, "form-closed")
	if out, err := tuiosCLI(t, base, "new-window", "agent", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	agent := windowID(t, base, session, "agent")
	// Wide enough for the footer on one line, as Claude Code fits it.
	if out, err := tuiosCLI(t, base, "call", "resize", `{"session":"`+session+`","window":"`+agent+`","width":120,"height":30}`); err != nil {
		t.Fatalf("resize: %v\n%s", err, out)
	}
	log := &stateLog{name: "blocking-form"}
	defer log.save(t)
	start := filepath.Join(bin, "claude") + " '" + script + "' '" + tuiosBin + "' '" + closed + "'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", agent, start); err != nil {
		t.Fatalf("start the stand-in: %v\n%s", err, out)
	}
	waitCapture(t, base, session, agent, "Ready.")
	waitAgentState(t, base, session, agent, "idle", "", log, "stand-in ready")

	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", agent, "fix the build\n"); err != nil {
		t.Fatalf("send the prompt: %v\n%s", err, out)
	}
	st := waitAgentState(t, base, session, agent, "done", "", log, "the turn ends with a Stop")
	if st.Message != "Done with it." {
		t.Fatalf("the done state is not the Stop's: %+v", st)
	}
	waitCapture(t, base, session, agent, "Teach auto mode about your environment?")
	deadline := time.Now().Add(uiTimeout)
	for {
		st = readAgentState(t, base, session, agent)
		if st.State == "needs_input" {
			break
		}
		if time.Now().After(deadline) {
			log.add("%-44s state=%s source=%s message=%q", "the form is up", st.State, st.Source, st.Message)
			t.Fatalf("ASSERTION: with the form over the input box the pane reads %s (%s, %q), want needs_input", st.State, st.Source, st.Message)
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.add("%-44s state=%s source=%s message=%q", "the form is up", st.State, st.Source, st.Message)
	if st.Message != "question: Teach auto mode about your environment?" {
		t.Errorf("ASSERTION: the pane waits with message %q, want the form's", st.Message)
	}
	if out, _ := tuiosCLI(t, base, "get-agent-state", "--json", "-s", session, "-w", agent); strings.Contains(out, `"waits_on": "1`) {
		t.Errorf("ASSERTION: a pane on needs_input shows a waits_on note:\n%s", out)
	}

	// Esc closes the form: done again, with the background shell noted.
	if out, err := tuiosCLI(t, base, "send-keys", "-s", session, "-w", agent, "Escape"); err != nil {
		t.Fatalf("send Esc: %v\n%s", err, out)
	}
	waitForFileText(t, closed, func(s string) bool { return strings.Contains(s, "FORM-CLOSED") }, "the stand-in did not close its form")
	// At rest again: done, or idle once the redraw lets the title's rest
	// glyph take the one-off done the Stop left (eventClaimStale). The note
	// is read on the look that sees the footer, which can come a frame after
	// the one that saw the form gone.
	var raw map[string]any
	out := ""
	deadline = time.Now().Add(uiTimeout)
	for {
		out, _ = tuiosCLI(t, base, "get-agent-state", "--json", "-s", session, "-w", agent)
		raw = nil
		_ = json.Unmarshal([]byte(out), &raw)
		atRest := raw["state"] == "done" || raw["state"] == "idle"
		if raw["waits_on"] == "1 background shell" && atRest || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.add("%-44s %s", "get-agent-state after the form", strings.TrimSpace(out))
	if raw["waits_on"] != "1 background shell" || raw["state"] != "done" && raw["state"] != "idle" {
		t.Errorf("ASSERTION: a pane at rest whose footer counts a shell reads %v with waits_on %v, want done or idle with \"1 background shell\"\n%s", raw["state"], raw["waits_on"], out)
	}
}
