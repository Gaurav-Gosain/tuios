package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

func cockpitCopyTerm(t *testing.T) (*tuitest.Terminal, *lockedBuffer) {
	t.Helper()
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
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 60, shippedLooks: true, out: out,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Why does the retry loop stop", "Worked for 50s")
	if err := term.SendKeys("e"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the open thread", "func retry(", "backoff = min(backoff*2")
	_ = term.WaitStable(uiTimeout)
	return term, out
}

// TestCockpitThreadMouseSelects selects the agents view's thread with the
// mouse and reads what reached the clipboard: a drag from the prompt card
// across the answer into its code block, the right click menu's Copy as
// Markdown on it, a double click on a path, and a triple click on a line. None of the copies holds a card's edge, and the
// wrapped paragraph comes out as one line. In copy mode a click moves the
// cursor (v e y copies the word clicked) and the wheel scrolls without
// ending copy mode. The artifacts are the frames cockpit-mouse-drag,
// cockpit-mouse-menu and cockpit-mouse-word.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with cockpitMousePress not called from handleMouseClick, the test
//     fails at "the drag copied nothing".
//   - with gridSelectWord a no-op, the test fails at "the double click".
//   - with the copy mode click not moving the cursor (gridPoint a no-op),
//     the test fails at "a click in copy mode".
func TestCockpitThreadMouseSelects(t *testing.T) {
	term, out := cockpitCopyTerm(t)

	s := term.Screen()
	r0 := threadRowWith(s, "Why does the retry loop stop")
	r1 := threadRowWith(s, "func retry(n int)")
	if r0 < 0 || r1 < r0 {
		t.Fatalf("rows of the prompt and the code: %d %d\n%s", r0, r1, term.Snapshot())
	}
	c0, _ := mustFind(t, term, "Why does the retry loop stop")
	c1, _ := mustFind(t, term, "func retry(n int)")
	from := len(clipboardWrites(out))
	mouseDrag(t, term, c0, r0, c1+len("func retry(n int)")-1, r1, tuitest.MouseLeft, 0)
	got := rawWrite(t, term, out, from, "ASSERTION: the drag copied nothing")
	saveCockpitFrames(t, term, "cockpit-mouse-drag")
	if !strings.HasPrefix(got, "Why does the retry loop stop") || !strings.Contains(got, copyParagraph) ||
		!strings.HasSuffix(got, "func retry(n int)") || strings.Contains(got, "▎") {
		t.Fatalf("ASSERTION: the drag copied\n%q\nwant the prompt, the paragraph as one line and the code's first words, with no card edge", got)
	}

	mouseClick(t, term, c0+4, r0, tuitest.MouseRight, 0)
	if err := term.WaitForText("Copy as Markdown", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a right click on the selection opened no menu: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-mouse-menu")
	mc, mr := mustFind(t, term, "Copy as Markdown")
	from = len(clipboardWrites(out))
	mouseClick(t, term, mc+2, mr, tuitest.MouseLeft, 0)
	if got := rawWrite(t, term, out, from, "ASSERTION: Copy as Markdown copied nothing"); !strings.HasPrefix(got, "> Why does the retry loop stop") || !strings.Contains(got, "func retry(n int) error {") {
		t.Fatalf("ASSERTION: Copy as Markdown copied\n%q", got)
	}

	pc, pr := mustFind(t, term, "retry.go and")
	from = len(clipboardWrites(out))
	mouseClick(t, term, pc+2, pr, tuitest.MouseLeft, 0)
	mouseClick(t, term, pc+2, pr, tuitest.MouseLeft, 0)
	if got := rawWrite(t, term, out, from, "ASSERTION: the double click copied nothing"); got != "retry.go" {
		t.Fatalf("ASSERTION: the double click copied %q, want %q", got, "retry.go")
	}
	saveCockpitFrames(t, term, "cockpit-mouse-word")

	lc, lr := mustFind(t, term, "Fixed: the backoff stops")
	from = len(clipboardWrites(out))
	for range 3 {
		mouseClick(t, term, lc+3, lr, tuitest.MouseLeft, 0)
	}
	if got := rawWrite(t, term, out, from, "ASSERTION: the triple click copied nothing"); got != "Fixed: the backoff stops at a quarter of the deadline." {
		t.Fatalf("ASSERTION: the triple click copied %q", got)
	}

	mouseClick(t, term, lc+3, lr, tuitest.MouseLeft, 0)

	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Copy mode. v selects", uiTimeout); err != nil {
		t.Fatalf("copy mode did not start: %v\n%s", err, term.Snapshot())
	}
	wc, wr := mustFind(t, term, "backoff doubled")
	mouseClick(t, term, wc+2, wr, tuitest.MouseLeft, 0)
	from = len(clipboardWrites(out))
	sendEach(t, term, "b", "v", "e", "y")
	if got := rawWrite(t, term, out, from, "ASSERTION: a click in copy mode"); got != "backoff" {
		t.Fatalf("ASSERTION: a click in copy mode put the cursor elsewhere: v e y copied %q, want %q", got, "backoff")
	}
	wheelAt(t, term, wc, wr, tuitest.MouseWheelUp, 1)
	from = len(clipboardWrites(out))
	sendEach(t, term, "v", "y")
	_ = rawWrite(t, term, out, from, "ASSERTION: copy mode ended on the wheel")
	alive(t, term, "after the mouse selections")
}

// TestCopyModeClickMovesTheCursor clicks in copy mode over a pane and over
// quick look's text: the click puts the cursor on the cell, so v e y copies
// the word clicked. In quick look a drag selects and the release copies, and
// the wheel scrolls without ending copy mode.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with quickLookCopyPress not called from handleMouseClick, the test
//     fails at "a click in quick look's copy mode".
//   - with the copy grid clause cut from FilterMouseMotion, the drag never
//     moves and the test fails at "a drag in quick look's copy mode".
func TestCopyModeClickMovesTheCursor(t *testing.T) {
	text := "alpha beta gamma\nsecond line here\nthird row\nafter\n"
	out := &lockedBuffer{}
	term, dir := quickLookListed(t, []string{"TUIOS_KITTY_GRAPHICS=0"}, out)

	enterTerminalMode(t, term)
	runInShell(t, term, "printf 'one %s three\\n' two", "one two three", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	col, row, ok := findOnGrid(term.Screen(), "one two three")
	if !ok {
		t.Fatalf("the pane's line is not on screen\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+len("one two ")+1, row, tuitest.MouseLeft, 0)
	from := len(clipboardWrites(out))
	sendEach(t, term, "b", "v", "e", "y")
	if got := rawWrite(t, term, out, from, "ASSERTION: a click in a pane's copy mode"); got != "three" {
		t.Fatalf("ASSERTION: a click in a pane's copy mode: v e y copied %q, want %q", got, "three")
	}
	sendKeys(t, term, "q")

	if err := os.WriteFile(filepath.Join(dir, "zzcopy.txt"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	railLists(t, term, "the rail did not list zzcopy.txt", []string{"zzcopy.txt"}, nil)
	quickLookFocusRail(t, term, "zzcopy.txt")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show zzcopy.txt", []string{"zzcopy.txt ", "go inside"}, nil)
	sendKeys(t, term, tuitest.Tab)
	railLists(t, term, "tab did not go inside", []string{"Inside the file", "select"}, nil)
	sendKeys(t, term, "[")
	railLists(t, term, "[ did not start copy mode", []string{"yank", "visual"}, nil)

	col, row, ok = findOnGrid(term.Screen(), "alpha beta gamma")
	if !ok {
		t.Fatalf("the text is not in the preview\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+len("alpha beta ")+1, row, tuitest.MouseLeft, 0)
	from = len(clipboardWrites(out))
	sendEach(t, term, "b", "v", "e", "y")
	if got := rawWrite(t, term, out, from, "ASSERTION: a click in quick look's copy mode"); got != "gamma" {
		t.Fatalf("ASSERTION: a click in quick look's copy mode: v e y copied %q, want %q", got, "gamma")
	}

	sc, sr, ok := findOnGrid(term.Screen(), "second line here")
	if !ok {
		t.Fatalf("the second line is not in the preview\n%s", term.Snapshot())
	}
	from = len(clipboardWrites(out))
	mouseDrag(t, term, sc, sr, sc+len("second line")-1, sr, tuitest.MouseLeft, 0)
	if got := rawWrite(t, term, out, from, "ASSERTION: a drag in quick look's copy mode"); got != "second line" {
		t.Fatalf("ASSERTION: a drag in quick look's copy mode copied %q, want %q", got, "second line")
	}
	wheelAt(t, term, sc, sr, tuitest.MouseWheelDown, 1)
	railLists(t, term, "ASSERTION: the wheel ended quick look's copy mode", []string{"y yank"}, nil)
	alive(t, term, "after the copy mode clicks")
}
