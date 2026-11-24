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

// codeLongBlock is a code block of 2000 short lines.
var codeLongBlock = func() string {
	var b strings.Builder
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&b, "line %04d\n", i)
	}
	return strings.TrimSuffix(b.String(), "\n")
}()

// codeBlocksAnswer is an answer with the 300-column Go block, then the
// 2000-line block.
var codeBlocksAnswer = "Here it is:\n\n```go\npackage words\n\n" + codeWrapLine + "\n```\n\nAnd the long one:\n\n```text\n" + codeLongBlock + "\n```\n\nThat is all."

// openCodeThread starts a client at 120 columns on the agents view, its
// thread on an agent whose answer is text, and focuses the thread.
func openCodeThread(t *testing.T, text string) (string, *tuitest.Terminal) {
	t.Helper()
	return openTextThread(t, text, "package words", "var table", "line 0001")
}

// openTextThread is openCodeThread for any reply text: it waits for want in
// the thread.
func openTextThread(t *testing.T, text string, want ...string) (string, *tuitest.Terminal) {
	t.Helper()
	return openTextThreadOn(t, text, "", nil, want...)
}

// openTextThreadOn is openTextThread with extra lines in the [agents] table
// of the config, and the client's output to its host copied to host when it
// is not nil.
func openTextThreadOn(t *testing.T, text, extra string, host *lockedBuffer, want ...string) (string, *tuitest.Terminal) {
	t.Helper()
	return openTextThreadTuned(t, text, extra, func(o *startOpts) {
		if host != nil {
			o.out = host
		}
	}, want...)
}

// openTextThreadTuned is openTextThreadOn with tune applied to the client's
// start options.
func openTextThreadTuned(t *testing.T, text, extra string, tune func(*startOpts), want ...string) (string, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n"+extra)
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	path := filepath.Join(atProjects(base), "work", "c0deb10c.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(ago time.Duration) string { return now.Add(-ago).Format("2006-01-02T15:04:05.000Z") }
	var buf bytes.Buffer
	for i, rec := range []map[string]any{
		{"type": "user", "timestamp": at(time.Minute), "message": map[string]any{"role": "user", "content": "Show me the code."}},
		{"type": "assistant", "timestamp": at(50 * time.Second), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": text}}}},
	} {
		rec["uuid"], rec["isSidechain"] = fmt.Sprintf("s%d", i), false
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(b, '\n'))
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "words"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "words")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "c0deb10c"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "idle", "-s", "api", "-w", "words", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	opts := startOpts{args: []string{"attach", "home"}, cols: 120, rows: 60, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}}
	tune(&opts)
	term := startIn(t, base, opts)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", want...)
	_ = settleScreen(term, uiTimeout)
	ckPress(t, term, tuitest.Tab)
	_ = settleScreen(term, uiTimeout)
	return base, term
}

// TestCockpitThreadCodeBlockScroll scrolls a 300-column Go block sideways in
// the agents view's thread, at 120 columns, with a 2000-line block under it.
//
// The ways it can go wrong, each an assertion here:
//   - ) and ( do nothing, or move the 2000-line block, which is not wider
//     than the thread, in place of the wide one above it;
//   - the block's lines do not move together, or its gutter, label or ground
//     move with them;
//   - a scrolled line gives no sign that text is left of the view;
//   - the scroll runs past the widest line's end, or past its start;
//   - shift and the wheel, or a sideways wheel, on the block do nothing.
//
// The artifacts are the frames code-scroll-open, code-scroll-right and
// code-scroll-wheel.
func TestCockpitThreadCodeBlockScroll(t *testing.T) {
	_, term := openCodeThread(t, codeBlocksAnswer)
	saveCockpitFrames(t, term, "code-scroll-open")
	s := term.Screen()
	row := rowWith(s, "var table")
	col := strings.Index(s.Line(row), "var table")
	pkgRow := rowWith(s, "package words")
	head := s.Line(rowWith(s, "line 0001") - 1)
	long := s.Line(rowWith(s, "line 0001"))
	gutter := func(s tuitest.Screen, r int) string { return string([]rune(s.Line(r))[:col]) }
	g0 := gutter(s, row)
	if strings.Contains(s.Text(), "END300") {
		t.Fatalf("ASSERTION: the end of the wide line shows before any scroll\n%s", term.Snapshot())
	}
	if strings.Contains(g0, "…") {
		t.Fatalf("ASSERTION: a line not scrolled has a mark in its gutter: %q\n%s", g0, term.Snapshot())
	}

	// 1. ) moves the wide block, every line of it, and leaves a mark in the
	// gutter of each line with text left of the view.
	if err := term.SendKeys(")"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Line(row), "var table") }, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "code-scroll-key-missing")
		t.Fatalf("ASSERTION: ) did not scroll the wide block: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	s = term.Screen()
	for _, r := range []int{row, pkgRow} {
		if g := []rune(s.Line(r)); g[col-1] != '…' {
			t.Fatalf("ASSERTION: scrolled row %d has no mark left of its text: %q\n%s", r, s.Line(r), term.Snapshot())
		}
		if !strings.HasPrefix(gutter(s, r), strings.TrimSuffix(strings.TrimRight(g0, " "), "…")) {
			t.Fatalf("ASSERTION: the edge left of row %d moved: %q, was %q\n%s", r, gutter(s, r), g0, term.Snapshot())
		}
	}
	if !strings.Contains(s.Line(pkgRow), "words") || strings.Contains(s.Line(pkgRow), "package") {
		t.Fatalf("ASSERTION: the short line did not move with the wide one: %q\n%s", s.Line(pkgRow), term.Snapshot())
	}
	if s.Line(rowWith(s, "line 0001")) != long || s.Line(rowWith(s, "line 0001")-1) != head {
		t.Fatalf("ASSERTION: the 2000-line block moved with the wide one\n%s", term.Snapshot())
	}

	// 2. ) up to the end of the widest line, and no further.
	for i := 0; i < 60 && !strings.Contains(term.Screen().Text(), "END300"); i++ {
		if err := term.SendKeys(")"); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
	}
	s = term.Screen()
	end := rowWith(s, "END300")
	if end != row {
		t.Fatalf("ASSERTION: ) never showed the end of the wide line (row %d, want %d)\n%s", end, row, term.Snapshot())
	}
	if line := []rune(strings.TrimRight(s.Line(row), " ")); line[len(line)-1] == '…' {
		t.Fatalf("ASSERTION: the line's end shows, and it still has a cut mark: %q\n%s", string(line), term.Snapshot())
	}
	before := s.Text()
	if err := term.SendKeys(")"); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	if after := term.Screen().Text(); after != before {
		t.Fatalf("ASSERTION: ) moved the block past the end of its widest line\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "code-scroll-right")

	// 3. Shift and the wheel on the block bring it back to its start, and no
	// further.
	wheel := func(button tuitest.MouseButton, mods tuitest.KeyMods) {
		t.Helper()
		if err := term.SendMouse(tuitest.MouseEvent{Col: col + 10, Row: row, Button: button, Action: tuitest.MousePress, Mods: mods}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 120 && !strings.Contains(term.Screen().Line(row), "var table"); i++ {
		wheel(tuitest.MouseWheelUp, tuitest.ModShift)
		_ = settleScreen(term, uiTimeout)
	}
	s = term.Screen()
	if !strings.Contains(s.Line(row), "var table") || strings.Index(s.Line(row), "var table") != col {
		t.Fatalf("ASSERTION: shift and the wheel did not bring the block back to its start\n%s", term.Snapshot())
	}
	if g := gutter(s, row); g != g0 {
		t.Fatalf("ASSERTION: the gutter at the start is %q, was %q\n%s", g, g0, term.Snapshot())
	}
	wheel(tuitest.MouseWheelUp, tuitest.ModShift)
	_ = term.WaitStable(uiTimeout)
	if strings.Index(term.Screen().Line(row), "var table") != col {
		t.Fatalf("ASSERTION: the wheel moved the block past its start\n%s", term.Snapshot())
	}

	// 4. A sideways wheel moves it too.
	wheel(tuitest.MouseWheelRight, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Line(row), "var table") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a sideways wheel did not scroll the block: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "code-scroll-wheel")
	alive(t, term, "after the code scroll")
}
