package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fullScreenAgentScript is a stand-in for Claude Code that draws its whole
// screen, as Claude Code does: the alternate screen, a header, the reply in
// the middle and a footer on a fixed row, drawn again for each prompt. It
// joins its pane to a transcript with the real SessionStart hook, writes each
// prompt and its answer there in Claude Code's format, and ends each turn with
// a Stop hook. Run through fakeclaude, so the pane's program is claude.
//
// $1 is tuios, $2 the transcript.
const fullScreenAgentScript = `T="$1"; TR="$2"
hook() { printf '%s' "$1" | "$T" agent-hook claude-code >/dev/null 2>&1; }
draw() {
  printf '\033[H\033[2J\033[1;1H Claude Code\033[3;1H%s\033[20;1H ? for shortcuts\033[5;1H> ' "$1"
}
printf '\033]0;\342\234\263 Claude Code\007\033[?1049h'
: > "$TR"
hook '{"hook_event_name":"SessionStart","session_id":"tk-ask","transcript_path":"'"$TR"'","source":"startup"}'
draw "Ready."
n=0
while IFS= read -r l; do
  n=$((n+1))
  hook '{"hook_event_name":"UserPromptSubmit","session_id":"tk-ask","transcript_path":"'"$TR"'","prompt":"'"$l"'"}'
  draw "Thinking about it."
  sleep 1
  printf '{"type":"user","uuid":"u%s","timestamp":"2026-11-08T10:00:00.000Z","isSidechain":false,"message":{"role":"user","content":"%s"}}\n' "$n" "$l" >> "$TR"
  printf '{"type":"assistant","uuid":"a%s","timestamp":"2026-11-08T10:00:01.000Z","isSidechain":false,"message":{"role":"assistant","content":[{"type":"text","text":"The answer to [%s] is FORTY-TWO."}]}}\n' "$n" "$l" >> "$TR"
  draw "The answer to [$l] is FORTY-TWO."
  hook '{"hook_event_name":"Stop","session_id":"tk-ask","transcript_path":"'"$TR"'"}'
done
`

// TestAskAgentReadsAFullScreenAgentsReply asks a stand-in that redraws its
// whole screen, as Claude Code does. Its answer is on the screen, but the
// screen has as many lines after the answer as before the question, so a
// reply read from the screen's new lines is empty. The reply must be the
// agent's own text from its transcript.
//
// A second question gets its own answer, not the first: only the transcript
// after the question counts.
//
// How it could pass wrongly: the reply could hold the question echoed back,
// so it must hold the answer's FORTY-TWO; and the answer could reach the
// reply through the screen after all, so the result must say it came from the
// transcript.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestAskAgentReadsAFullScreenAgentsReply).
func TestAskAgentReadsAFullScreenAgentsReply(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const session = "e2e-ask-transcript"
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
	script := filepath.Join(base, "full-screen-agent.sh")
	if err := os.WriteFile(script, []byte(fullScreenAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// Under Claude Code's own folder, the only place a hook may name.
	transcript := filepath.Join(atProjects(base), "ask", "tk-ask.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "answerer", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	agent := windowID(t, base, session, "answerer")
	start := filepath.Join(bin, "claude") + " '" + script + "' '" + tuiosBin + "' '" + transcript + "'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", agent, start); err != nil {
		t.Fatalf("start the stand-in agent: %v\n%s", err, out)
	}
	waitCapture(t, base, session, agent, "Ready.")
	waitAgentState(t, base, session, agent, "idle", "", &stateLog{name: "ask-transcript"}, "stand-in agent ready")

	wait := askAsync(t, base, session, agent, "what is the answer", "--settle", "1500")
	res, out, err := wait()
	screen := capture(t, base, session, agent)
	writeFile := filepath.Join(artifactDir(t), "ask-transcript.log")
	_ = os.WriteFile(writeFile, []byte("== ask-agent --json\n"+out+"\n== the pane\n"+screen+"\n"), 0o644)
	if err != nil {
		t.Fatalf("ask-agent failed: %v\n%s", err, out)
	}
	if !strings.Contains(screen, "FORTY-TWO") {
		t.Fatalf("the stand-in did not draw its answer, so this proves nothing:\n%s", screen)
	}
	if !strings.Contains(res.Reply, "The answer to [what is the answer] is FORTY-TWO.") {
		t.Errorf("ASSERTION: the reply of a full-screen agent is %q, want its answer from the transcript\n%s", res.Reply, out)
	}
	if !strings.Contains(out, `"reply_source": "transcript"`) && !strings.Contains(out, `"reply_source":"transcript"`) {
		t.Errorf("ASSERTION: the reply does not say it came from the transcript:\n%s", out)
	}

	// A second question: its reply is its own answer, and not the first one,
	// which the transcript holds too.
	res, out, err = askAsync(t, base, session, agent, "and the second", "--settle", "1500")()
	if err != nil {
		t.Fatalf("the second ask-agent failed: %v\n%s", err, out)
	}
	if !strings.Contains(res.Reply, "The answer to [and the second] is FORTY-TWO.") || strings.Contains(res.Reply, "[what is the answer]") {
		t.Errorf("ASSERTION: the second reply is %q, want only the second answer", res.Reply)
	}
}
