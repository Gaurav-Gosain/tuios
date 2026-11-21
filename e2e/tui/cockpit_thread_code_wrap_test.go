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

var codeWrapLine = func() string {
	var b strings.Builder
	b.WriteString(`var table = []string{`)
	for i := 0; b.Len() < 280; i++ {
		fmt.Fprintf(&b, `"word%02d", `, i)
	}
	b.WriteString(`"END300"}`)
	return b.String()
}()

// TestCockpitThreadCodeBlockWrap reads an answer with a 300-column Go line
// in the agents view's thread, at 120 columns.
//
// The ways it can go wrong, each an assertion here:
//   - the cut line looks whole: no mark at the block's right edge, or a mark
//     in the code's ink that reads as code;
//   - W does nothing, or wraps without a mark, so a wrapped row reads as
//     another line;
//   - copy mode yanks the wrapped rows as more lines, or joins them with a
//     space;
//   - W again leaves the rows wrapped.
//
// The artifacts are the frames code-open, code-cut and code-wrapped.
func TestCockpitThreadCodeBlockWrap(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
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
		{"type": "user", "timestamp": at(time.Minute), "message": map[string]any{"role": "user", "content": "Show me the word table."}},
		{"type": "assistant", "timestamp": at(50 * time.Second), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Here it is:\n\n```go\npackage words\n\n" + codeWrapLine + "\n```\n\nThat is the whole table."}}}},
	} {
		rec["uuid"], rec["isSidechain"] = fmt.Sprintf("w%d", i), false
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
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 44, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Show me the word table.", "That is the whole table.", "var table")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "code-open")

	s := term.Screen()
	row := rowWith(s, "var table")
	line := []rune(strings.TrimRight(s.Line(row), " "))
	if len(line) == 0 || line[len(line)-1] != '…' || strings.Contains(s.Text(), "END300") {
		saveCockpitFrames(t, term, "code-cut-missing")
		t.Fatalf("ASSERTION: the cut code line does not end in a cut mark:\n%q\n%s", string(line), term.Snapshot())
	}
	mark := s.Cell(len(line)-1, row)
	langRow := rowWith(s, "go")
	for r := row - 1; r >= 0; r-- {
		if strings.HasSuffix(strings.TrimRight(s.Line(r), " "), " go") {
			langRow = r
			break
		}
	}
	lang := []rune(strings.TrimRight(s.Line(langRow), " "))
	if label := s.Cell(len(lang)-1, langRow); mark.Fg != label.Fg {
		t.Fatalf("ASSERTION: the cut mark is drawn in %v, not in the mute ink %v of the language label\n%s", mark.Fg, label.Fg, term.Snapshot())
	}
	if code := s.Cell(len(line)-3, row); code.Fg == mark.Fg {
		t.Fatalf("ASSERTION: the cut mark is in the code's ink, %v\n%s", code.Fg, term.Snapshot())
	}
	saveCockpitFrames(t, term, "code-cut")

	ckPress(t, term, tuitest.Tab)
	if err := term.SendKeys("W"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "END300") }, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "code-wrap-missing")
		t.Fatalf("ASSERTION: W did not wrap the code line: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	s = term.Screen()
	first, last := rowWith(s, "var table"), rowWith(s, "END300")
	if last <= first {
		t.Fatalf("ASSERTION: the wrapped line is on rows %d to %d\n%s", first, last, term.Snapshot())
	}
	for r := first + 1; r <= last; r++ {
		if !strings.Contains(s.Line(r), "↳") {
			t.Fatalf("ASSERTION: wrapped row %d has no continuation mark:\n%q\n%s", r, s.Line(r), term.Snapshot())
		}
	}
	if strings.Contains(s.Line(first), "↳") {
		t.Fatalf("ASSERTION: the first row of the wrapped line has a mark\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "code-wrapped")

	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "copy mode", "Copy mode. v selects")
	if err := term.SendKeys("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.Type("var table"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the search", "/var table (")
	keys := []any{"V"}
	for range last - first {
		keys = append(keys, "j")
	}
	before := mustCLIOr(t, base, "show-buffer")
	if err := term.SendKeys(keys...); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	if err := term.SendKeys("y"); err != nil {
		t.Fatal(err)
	}
	got := before
	waitUntil(t, "the yank to reach the paste buffer", uiTimeout, func() (bool, string) {
		got = mustCLIOr(t, base, "show-buffer")
		return got != before, got
	})
	if got != codeWrapLine {
		t.Fatalf("ASSERTION: the yank of the wrapped line is\n%q\nwant\n%q", got, codeWrapLine)
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)

	if err := term.SendKeys("W"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "END300") && !strings.Contains(s.Text(), "↳")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: W again did not cut the line: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the code wrap")
}
