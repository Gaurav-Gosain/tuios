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

// ownTranscriptParent is a Claude Code stand-in that works on and on: it
// writes a mid-turn record (a tool call) to its transcript every second, under
// $CLAUDE_CONFIG_DIR/projects/<folder with dashes>, as Claude Code does.
const ownTranscriptParent = `cwd=$(pwd -P)
dir="$CLAUDE_CONFIG_DIR/projects/$(printf %s "$cwd" | tr / -)"
mkdir -p "$dir"
echo PARENT-WORKING
while :; do
  printf '{"type":"assistant","sessionId":"parent","cwd":"%s","version":"claude","timestamp":"%s","message":{"stop_reason":"tool_use"}}\n' "$cwd" "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "$dir/parent.jsonl"
  sleep 1
done
`

// ownTranscriptChild is a Claude Code stand-in that stops at once on the
// folder-trust question, the way Claude Code does in a folder it has not been
// told to trust, before it writes a transcript of its own.
const ownTranscriptChild = `printf '%s\n' \
 '╭───────────────────────────────────────────────────────────╮' \
 '│ Do you trust the files in this folder?                    │' \
 '│                                                           │' \
 '│ ❯ 1. Yes, proceed                                         │' \
 '│   2. No, exit                                             │' \
 '╰───────────────────────────────────────────────────────────╯'
exec sleep 600
`

// TestStartAgentInItsParentsFolderShowsItsOwnQuestion is item 11 of the
// lead's list: a lead started a worker in the folder it works in itself, the
// worker stopped on Claude Code's folder-trust question, and tuios read the
// worker as working from the lead's transcript. peek-prompt and the Inbox saw
// nothing, and start-agent did not come back.
//
// A parent stand-in works on in a folder, its transcript saying working every
// second. A child is started there with start-agent and shows the trust
// question before it writes any transcript. The child must read needs_input,
// from its screen, with the trust rule's message, and start-agent must come
// back within its ready timeout, not ready, naming the question.
//
// How it could pass wrongly, written down first:
//   - the child could read needs_input only because no transcript was there
//     to join: the test waits for the parent's file to exist and for the
//     parent pane to read working from its transcript before the child starts;
//   - start-agent could come back on its timeout rather than on the question:
//     it must say blocked, within 15 s of a 45 s timeout. The question is
//     painted once and then sits still, as Claude Code's does, so this also
//     holds the daemon to reading the screen again once the detector has
//     named the harness: the first read had no harness and so no rules, and
//     without the second the question was found only by the held-prompt
//     timer, 30 s in;
//   - the parent's own transcript could stop working for it: the parent must
//     still read working, from its transcript, at the end.
//
// NEGATIVE CONTROLS: on main (a child joins the only verifying file in its
// folder), the child reads working from the transcript and start-agent comes
// back on its timeout. With the takeHarnessNamed look cut from the daemon's
// detect scan, start-agent comes back blocked after about 31 s.
func TestStartAgentInItsParentsFolderShowsItsOwnQuestion(t *testing.T) {
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeClaudeBin, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	claudeConfig := filepath.Join(base, "claude-config")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	proj := filepath.Join(base, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	parentScript, childScript := filepath.Join(base, "parent.sh"), filepath.Join(base, "child.sh")
	for path, body := range map[string]string{parentScript: ownTranscriptParent, childScript: ownTranscriptChild} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil { //nolint:gosec // a pane runs it
			t.Fatal(err)
		}
	}
	writeConfig(t, base, "[daemon]\nagent_detect_seconds = 1\n")
	killDaemon(t, base)
	const sess = "crew"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create %s: %v\n%s", sess, err, out)
	}

	type agentState struct {
		State   string `json:"state"`
		Source  string `json:"source"`
		Message string `json:"message"`
	}
	stateOf := func(window string) (agentState, string) {
		out, _ := tuiosCLI(t, base, "get-agent-state", "--json", "-s", sess, "-w", window)
		var st agentState
		_ = json.Unmarshal([]byte(out), &st)
		return st, out
	}

	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--no-focus", "--cwd", proj, "lead", "--", "claude", parentScript); err != nil {
		t.Fatalf("start the parent: %v\n%s", err, out)
	}
	waitUntil(t, "the parent pane reads working from its transcript", 30*time.Second, func() (bool, string) {
		st, out := stateOf("lead")
		return st.State == "working" && st.Source == "transcript", out
	})

	start := time.Now()
	out, err := tuiosCLI(t, base, "start-agent", "claude "+childScript, "-s", sess, "--name", "worker", "--cwd", proj, "--ready-timeout", "45000", "--json")
	took := time.Since(start)
	var res struct {
		Ready   bool   `json:"ready"`
		Outcome string `json:"outcome"`
	}
	if i := strings.Index(out, "{"); i >= 0 {
		_ = json.Unmarshal([]byte(out[i:]), &res)
	}
	if err == nil || res.Ready || res.Outcome != "blocked" || took > 15*time.Second {
		t.Errorf("ASSERTION: start-agent of a child that shows the trust question came back after %s with ready=%v outcome=%q (want blocked, not ready, well inside the 45 s timeout), err=%v:\n%s",
			took.Round(time.Second), res.Ready, res.Outcome, err, out)
	}
	st, raw := stateOf("worker")
	if st.State != "needs_input" || st.Source != "screen" || !strings.Contains(st.Message, "trust") {
		t.Errorf("ASSERTION: the child reads %s from %s (%q), want needs_input from its screen with the trust question:\n%s", st.State, st.Source, st.Message, raw)
	}
	if st, raw := stateOf("lead"); st.State != "working" || st.Source != "transcript" {
		t.Errorf("ASSERTION: the parent stopped reading working from its own transcript:\n%s", raw)
	}
	pane, _ := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", "worker")
	if err := os.WriteFile(filepath.Join(artifactDir(t), "worker-pane.txt"), []byte(pane+"\n\n"+raw+"\n\nstart-agent:\n"+out), 0o644); err != nil {
		t.Errorf("save the artifact: %v", err)
	}
}
