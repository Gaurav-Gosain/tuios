package tuie2e

import (
	"bytes"
	"encoding/base64"
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

// TestCockpitThreadFoldsAndImages reads a Claude Code transcript in the
// agents view, written by the test with times just before now, so the working
// turn's time is real:
//
//   - turn 1, finished: a prompt with a pasted PNG (text block, then image
//     block, as Claude Code writes them), a thought with two lines, a Read and
//     an answer with a Go block of 20 lines.
//   - turn 2, at work: a prompt, a TodoWrite with one item done of three, and
//     a Bash call with no result yet.
//
// The pasted image is part of its prompt: a card under it, and the next
// prompt is turn 2, not turn 3. The card's picture is a kitty picture placed
// on the card's rows. Turn 1 folds to "Worked for 1m 0s · read 1 file", and a
// click on that line opens it. The thought shows its time and first line, and
// a click opens the rest. The code block shows 12 lines and "10 more lines",
// and a click shows all of it. Turn 2 says "Working for 3m". Its todo list is
// pinned above the thread, its items drawn once. A click on the image card
// opens the image view. The artifacts are the frames thread-cards-closed and
// thread-cards-open, with the host's pictures drawn in.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the image case cut from threadCardLines, the image is a "You"
//     prompt of "[image]", and the test fails at "no image card under its
//     prompt".
//   - with the threadImagePlacements call cut from flushReplyPreviewForFrame,
//     the test fails at "no picture on the image card".
//   - with the fold cut from ckMD.code, the test fails at "the code block is
//     not folded".
//   - with the hit cut from ckThoughtLines, the test fails at "a click did not
//     open the thought".
//   - with the ThreadPinnedLines call cut from drawFocus, the test fails at
//     "the todo list is not pinned".
//   - with the timg prefix cut from cockpitAction, the test fails at "the
//     image view did not open".
func TestCockpitThreadFoldsAndImages(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	path := filepath.Join(atProjects(base), "work", "c4a7d00e.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, cardsTranscript(t, time.Now()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "header-fix"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "header-fix")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "c4a7d00e"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "api", "-w", "header-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	host := &lockedBuffer{}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 90, shippedLooks: true, out: host,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Show me where the header overlaps.", "Now fix the padding.", "PNG image  320×180")
	}, uiTimeout); err != nil {
		_ = term.WaitStable(uiTimeout)
		saveCockpitFrames(t, term, "thread-cards-missing", hostPictures(t, []byte(host.String()))...)
		t.Fatalf("ASSERTION: the thread has no image card under its prompt: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatal(err)
	}
	text := term.Screen().Text()
	if !strings.Contains(text, "turn 2") || strings.Contains(text, "turn 3") || strings.Contains(text, "▎  [image]") {
		t.Fatalf("ASSERTION: the image counted as a prompt: want the next prompt as turn 2 and no [image] prompt\n%s", term.Snapshot())
	}
	for _, w := range []string{"Worked for 1m 0s · read 1 file", "Working for 3m · ran 1 command", "click to view"} {
		if !strings.Contains(text, w) {
			t.Fatalf("ASSERTION: the thread does not show %q\n%s", w, term.Snapshot())
		}
	}
	// The todo list is pinned above the thread, whole, and its items are
	// drawn there only.
	plan := strings.Index(text, "Fix the header padding")
	if plan < 0 || plan > strings.Index(text, "Show me where the header overlaps.") || strings.Count(text, "Fix the header padding") != 1 {
		t.Fatalf("ASSERTION: the todo list is not pinned above the thread, once\n%s", term.Snapshot())
	}
	// The pasted image sits under its prompt, above the turn's line.
	headRow, headCol := findText(t, term, "PNG image  320×180")
	if turnRow, _ := findText(t, term, "Worked for 1m 0s"); turnRow < headRow {
		t.Fatalf("ASSERTION: the image card is under the turn's line (row %d), not under its prompt (row %d)\n%s", turnRow, headRow, term.Snapshot())
	}
	// The picture sits on the card's rows, under its head line.
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return strings.Contains(host.String(), fmt.Sprintf("\x1b[%d;%dH\x1b_Ga=p,i=", headRow+2, headCol+1))
	}, 5*time.Second); err != nil {
		saveCockpitFrames(t, term, "thread-cards-missing", hostPictures(t, []byte(host.String()))...)
		t.Fatalf("ASSERTION: no picture on the image card (head at row %d col %d): %v\n%s", headRow, headCol, err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "thread-cards-closed", hostPictures(t, []byte(host.String()))...)

	// The finished turn opens on a click on its line.
	row, col := findText(t, term, "Worked for 1m 0s")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the opened turn", "Thought for 10s  Compare the header with the list grid.")
	// The code block, 22 lines with its func line and brace: 12 lines and
	// the rest folded.
	scrollUntil(t, term, "K", "10 more lines")
	if s := term.Screen().Text(); !strings.Contains(s, "line11 := 11") || strings.Contains(s, "line12 := 12") {
		t.Fatalf("ASSERTION: the code block is not folded at 12 lines\n%s", term.Snapshot())
	}
	row, col = findText(t, term, "10 more lines")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	scrollUntil(t, term, "J", "line20 := 20")
	scrollUntil(t, term, "K", "Thought for 10s")
	row, col = findText(t, term, "Thought for 10s")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Then check the padding on the right.", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click did not open the thought: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "thread-cards-open", hostPictures(t, []byte(host.String()))...)

	// A click on the image card opens the image view.
	scrollUntil(t, term, "K", "click to view")
	row, col = findText(t, term, "click to view")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("pasted by you for header-fix", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the image view did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the thread after the image view", "PNG image  320×180")
	alive(t, term, "after the thread cards")
}

// scrollUntil presses key until want shows, at most 15 times.
func scrollUntil(t *testing.T, term *tuitest.Terminal, key, want string) {
	t.Helper()
	for range 15 {
		if strings.Contains(term.Screen().Text(), want) {
			return
		}
		if err := term.SendKeys(key); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
	}
	if !strings.Contains(term.Screen().Text(), want) {
		t.Fatalf("ASSERTION: %q never showed while pressing %s\n%s", want, key, term.Snapshot())
	}
}

// cardsTranscript is the transcript of TestCockpitThreadFoldsAndImages, with
// times before now.
func cardsTranscript(t *testing.T, now time.Time) []byte {
	t.Helper()
	at := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	png := base64.StdEncoding.EncodeToString(gradientPNG(t, 320, 180))
	var code []string
	for i := 1; i <= 20; i++ {
		code = append(code, fmt.Sprintf("\tline%d := %d", i, i))
	}
	answer := "The header overlaps the list by two rows:\n\n```go\nfunc layout() {\n" + strings.Join(code, "\n") + "\n}\n```\n\nThe grid starts too high."
	recs := []map[string]any{
		{"type": "user", "timestamp": at(5 * time.Minute), "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "Show me where the header overlaps."},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": png}},
		}}},
		{"type": "assistant", "timestamp": at(4*time.Minute + 50*time.Second), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "Compare the header with the list grid.\nThen check the padding on the right.", "signature": "c2ln"},
		}}},
		{"type": "assistant", "timestamp": at(4*time.Minute + 45*time.Second), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_r", "name": "Read", "input": map[string]any{"file_path": "/work/ui/header.go"}},
		}}},
		{"type": "user", "timestamp": at(4*time.Minute + 44*time.Second), "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_r", "content": "package ui"},
		}}},
		{"type": "assistant", "timestamp": at(4 * time.Minute), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": answer},
		}}},
		{"type": "user", "timestamp": at(3*time.Minute + 10*time.Second), "message": map[string]any{"role": "user", "content": "Now fix the padding."}},
		{"type": "assistant", "timestamp": at(3*time.Minute + 5*time.Second), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_t", "name": "TodoWrite", "input": map[string]any{"todos": []any{
				map[string]any{"content": "Find the overlap", "status": "completed"},
				map[string]any{"content": "Fix the header padding", "status": "in_progress"},
				map[string]any{"content": "Run the UI tests", "status": "pending"},
			}}},
		}}},
		{"type": "user", "timestamp": at(3*time.Minute + 4*time.Second), "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_t", "content": "Todos have been modified successfully."},
		}}},
		{"type": "assistant", "timestamp": at(3 * time.Minute), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_b", "name": "Bash", "input": map[string]any{"command": "go test ./ui/..."}},
		}}},
	}
	var buf bytes.Buffer
	for i, r := range recs {
		r["uuid"] = fmt.Sprintf("u%d", i)
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
