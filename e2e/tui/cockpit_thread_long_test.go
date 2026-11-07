package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadLongScroll reads a long conversation in the agents view.
// The thread draws only the newest turns the view needs, and draws older
// ones as the person scrolls up to them.
//
// Failure modes, written before the code:
//  1. The older turns drawn above the view move it: a page up jumps over
//     turns, because the new lines count as lines under a held view.
//  2. A page up stops at the oldest turn drawn and never reaches the first.
//  3. g stops at the oldest turn drawn, not at the first prompt.
//  4. Turn numbers start again from 1 at the oldest turn drawn.
//  5. G after all that does not come back to the newest answer.
//
// Each page up must show older prompts than the one before, at most four
// turns older (a page is about 40 rows, a turn about 14), until the first prompt
// shows. The artifacts are the frames cockpit-long-first and cockpit-long-last.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with Follow.Older not called, the test fails at "a page up jumped".
//   - with the growth cut from threadBody, it fails at "never reached".
//   - with the turn count before the kept turns cut, it fails at "turn".
func TestCockpitThreadLongScroll(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	path := filepath.Join(atProjects(base), "work", "10ad10ad.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, longTranscript(t, 40), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "long-read"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "long-read")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "10ad10ad"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "api", "-w", "long-read", "--harness", "claude-code"); err != nil {
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
	waitText(t, term, "the newest answer", "Answer 40 ends here.")
	// Only the newest turns are drawn now: the turn numbers still count
	// the ones above them.
	if rowWith(term.Screen(), "Question 40:") < 0 {
		t.Fatalf("question 40 is not on screen\n%s", term.Snapshot())
	}
	longTurnLabel(t, term, 40)
	ckPress(t, term, tuitest.Tab)
	waitRegion(t, term, "Focus", 0, 120, "tab from the list")

	// Pages up: each shows older prompts, never more than four turns older.
	top, ok := longTopQuestion(term.Screen())
	if !ok {
		t.Fatalf("no question on screen at the bottom\n%s", term.Snapshot())
	}
	for range 80 {
		ckPress(t, term, tuitest.PageUp)
		_ = term.WaitStable(uiTimeout)
		s := term.Screen()
		got, ok := longTopQuestion(s)
		if !ok {
			continue
		}
		if got > top {
			t.Fatalf("ASSERTION: a page up showed newer prompts: question %d after %d\n%s", got, top, term.Snapshot())
		}
		if top-got > 4 {
			t.Fatalf("ASSERTION: a page up jumped from question %d to %d\n%s", top, got, term.Snapshot())
		}
		top = got
		if got == 1 {
			break
		}
	}
	if top != 1 {
		t.Fatalf("ASSERTION: pages up never reached the first prompt; the oldest shown is question %d\n%s", top, term.Snapshot())
	}
	longTurnLabel(t, term, 2)

	// G back to the newest answer, then g to the first prompt at once.
	ckPress(t, term, "G")
	waitText(t, term, "G after the pages", "Answer 40 ends here.")
	ckPress(t, term, "g")
	if err := term.WaitFor(func(s tuitest.Screen) bool { q, ok := longTopQuestion(s); return ok && q == 1 }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: g did not show the first prompt: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-long-first")
	longTurnLabel(t, term, 2)
	ckPress(t, term, "G")
	waitText(t, term, "G after g", "Answer 40 ends here.")
	longTurnLabel(t, term, 40)
	saveCockpitFrames(t, term, "cockpit-long-last")
	alive(t, term, "after the long thread")
}

var longQuestionRe = regexp.MustCompile(`Question (\d+):`)

// longTopQuestion is the number of the first question on screen.
func longTopQuestion(s tuitest.Screen) (int, bool) {
	m := longQuestionRe.FindStringSubmatch(s.Text())
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

// longTurnLabel checks that the prompt of question n, when on screen, is
// labelled turn n.
func longTurnLabel(t *testing.T, term *tuitest.Terminal, n int) {
	t.Helper()
	s := term.Screen()
	y := rowWith(s, fmt.Sprintf("Question %d:", n))
	if y < 0 {
		return
	}
	for r := y - 1; r >= max(y-3, 0); r-- {
		if line := s.Line(r); strings.Contains(line, "You") {
			if !strings.Contains(line, "turn "+strconv.Itoa(n)) {
				t.Fatalf("ASSERTION: the prompt of question %d is labelled %q, want turn %d\n%s", n, strings.TrimSpace(line), n, term.Snapshot())
			}
			return
		}
	}
}

// longTranscript is a conversation of n turns: a numbered question and an
// answer of a few paragraphs, about fourteen rows at 120 columns.
func longTranscript(t *testing.T, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	for i := 1; i <= n; i++ {
		ts := fmt.Sprintf("2026-10-20T10:%02d:00.000Z", i)
		answer := fmt.Sprintf("Step %d of the retry fix.\n\nThe backoff doubled past the deadline.\n\nThe last call never ran.\n\nThe error said timeout.\n\nAnswer %d ends here.", i, i)
		for k, r := range []map[string]any{
			{"type": "user", "timestamp": ts, "message": map[string]any{"role": "user", "content": fmt.Sprintf("Question %d: why does step %d stop early?", i, i)}},
			{"type": "assistant", "timestamp": ts, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": answer}}}},
		} {
			r["uuid"] = fmt.Sprintf("l%d-%d", i, k)
			r["isSidechain"] = false
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}
