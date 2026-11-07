package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// copyParagraph is an answer's first paragraph, long enough that the thread
// wraps it over several rows at 120 columns.
const copyParagraph = "The retry loop gave up after the third attempt because the backoff doubled past the deadline, so the last call never ran and the error said timeout instead of the real cause from the server."

// TestCockpitThreadCopyMode runs copy mode over the agents view's thread:
// ctrl+b [ starts it, / searches, V selects rows, y copies and Y copies as
// Markdown, and each yank is read back with `tuios show-buffer`.
//
// The yank of a wrapped paragraph is the paragraph as the agent wrote it, one
// line, with no card indent. The yank of a code block keeps its own indent and
// its lines. The yank of a prompt has no card edge (▎). Y on an edit is a
// ```diff block with its - and + lines. esc leaves copy mode and the view
// stays open; the next esc closes the view. The artifact is the frame cockpit-copy-visual.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with CockpitCopyText not called (the pane's own yank text kept), the
//     test fails at "the paragraph's yank".
//   - with the glyph set left empty (ckGlyphSet), the test still passes:
//     no row it yanks holds a glyph. See NEGATIVE_CONTROLS.md.
//   - with the CockpitCopyOn hook cut from handleInboxInput, the test fails
//     at "copy mode did not start" or the keys move the view.
//   - with the wrap flag left off ckMD.text's lines, the test fails at "the
//     paragraph's yank".
func TestCockpitThreadCopyMode(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	path := filepath.Join(atProjects(base), "work", "0c0ffee1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, copyTranscript(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "retry-fix"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "retry-fix")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "0c0ffee1"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "api", "-w", "retry-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 60, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Why does the retry loop stop", "Worked for 50s")
	// The finished turn folds; e opens every card.
	if err := term.SendKeys("e"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the open thread", "func retry(", "backoff = min(backoff*2")
	_ = term.WaitStable(uiTimeout)

	start := func() {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitForText("Copy mode. v selects", uiTimeout); err != nil {
			saveCockpitFrames(t, term, "cockpit-copy-missing")
			t.Fatalf("ASSERTION: copy mode did not start: %v\n%s", err, term.Snapshot())
		}
	}
	// search moves the cursor to text.
	search := func(text string) {
		t.Helper()
		if err := term.SendKeys("/"); err != nil {
			t.Fatal(err)
		}
		if err := term.Type(text); err != nil {
			t.Fatal(err)
		}
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
	}
	// lineSelect selects the rows from the one with first to the one with
	// last, and yanks them with key.
	lineSelect := func(first, last, key string) string {
		t.Helper()
		search(first)
		s := term.Screen()
		r0, r1 := rowWith(s, first), rowWith(s, last)
		if r0 < 0 || r1 < r0 {
			t.Fatalf("rows of %q and %q: %d %d\n%s", first, last, r0, r1, term.Snapshot())
		}
		keys := []any{"V"}
		for range r1 - r0 {
			keys = append(keys, "j")
		}
		// The last selected row is lit once the keys are drawn.
		lastCol := strings.Index(s.Line(r1), last)
		lastCol = len([]rune(s.Line(r1)[:lastCol]))
		ground := s.Cell(lastCol, r1).Bg
		if err := term.SendKeys(keys...); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return s.Cell(lastCol, r1).Bg != ground }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the selection is not drawn on the row of %q: %v\n%s", last, err, term.Snapshot())
		}
		if key == "y" && first == "gave up after the third" {
			saveCockpitFrames(t, term, "cockpit-copy-visual")
		}
		before := mustCLIOr(t, base, "show-buffer")
		if err := term.SendKeys(key); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(uiTimeout)
		for {
			if out := mustCLIOr(t, base, "show-buffer"); out != before {
				return out
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s copied nothing to the paste buffers\n%s", key, term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	start()
	if got := lineSelect("gave up after the third", "from the server.", "y"); got != copyParagraph {
		t.Fatalf("ASSERTION: the paragraph's yank is\n%q\nwant\n%q", got, copyParagraph)
	}
	want := "func retry(n int) error {\n    for i := 0; i < n; i++ {\n        if err := call(); err == nil {\n            return nil\n        }\n    }\n    return errTimeout"
	if got := lineSelect("func retry(n int)", "return errTimeout", "y"); got != want {
		t.Fatalf("ASSERTION: the code block's yank is\n%q\nwant\n%q", got, want)
	}
	if got := lineSelect("Why does the retry loop stop", "say it in two lines.", "y"); strings.Contains(got, "▎") || !strings.HasPrefix(got, "Why does the retry loop stop") || strings.Contains(got, "\n") {
		t.Fatalf("ASSERTION: the prompt's yank has the card edge, an indent or a break:\n%q", got)
	}
	got := lineSelect("Edit   retry.go", "backoff = min(backoff*2", "Y")
	if !strings.HasPrefix(got, "```diff\n--- a/retry.go\n+++ b/retry.go\n") || !strings.Contains(got, "-\tbackoff *= 2\n") || !strings.Contains(got, "+\tbackoff = min(backoff*2, deadline/4)\n") || !strings.HasSuffix(got, "```") {
		t.Fatalf("ASSERTION: Y on the edit is not a diff block:\n%s", got)
	}
	// esc leaves copy mode and the view stays. The next esc reaches the
	// view, which closes.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	if !strings.Contains(term.Screen().Text(), "Why does the retry loop stop") {
		t.Fatalf("ASSERTION: esc in copy mode closed the agents view\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Why does the retry loop stop") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the second esc did not reach the view, so copy mode did not end: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after copy mode")
}

// mustCLIOr is the output of a tuios command, "" when it fails.
func mustCLIOr(t *testing.T, base string, args ...string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, args...)
	if err != nil {
		return ""
	}
	return out
}

// copyTranscript is the transcript of TestCockpitThreadCopyMode: a prompt
// that wraps, an answer of a paragraph and a Go block, and an edit.
func copyTranscript(t *testing.T) []byte {
	t.Helper()
	code := "func retry(n int) error {\n    for i := 0; i < n; i++ {\n        if err := call(); err == nil {\n            return nil\n        }\n    }\n    return errTimeout\n}"
	recs := []map[string]any{
		{"type": "user", "timestamp": "2026-10-20T10:00:00.000Z", "message": map[string]any{"role": "user",
			"content": "Why does the retry loop stop before the deadline when the server is slow? Look at retry.go and the logs from last night, and say it in two lines."}},
		{"type": "assistant", "timestamp": "2026-10-20T10:00:30.000Z", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": copyParagraph + "\n\n```go\n" + code + "\n```"},
		}}},
		{"type": "assistant", "timestamp": "2026-10-20T10:00:40.000Z", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_e", "name": "Edit", "input": map[string]any{"file_path": "retry.go", "old_string": "\tbackoff *= 2", "new_string": "\tbackoff = min(backoff*2, deadline/4)"}},
		}}},
		{"type": "user", "timestamp": "2026-10-20T10:00:41.000Z", "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_e", "content": "The file retry.go has been updated."},
		}}},
		{"type": "assistant", "timestamp": "2026-10-20T10:00:50.000Z", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Fixed: the backoff stops at a quarter of the deadline."},
		}}},
	}
	var buf bytes.Buffer
	for i, r := range recs {
		r["uuid"] = fmt.Sprintf("c%d", i)
		r["isSidechain"] = false
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
