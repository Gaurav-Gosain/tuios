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

// toolRowsCmd is the long command of TestCockpitThreadToolRowsOpen. Its end
// is past what the folded line shows.
const toolRowsCmd = "cd /work && go test -count=1 -run 'TestHeaderPadding|TestListGrid|TestSidebarWidth' ./ui/... ./layout/... ./theme/... 2>&1 | tee /tmp/ui-tests.log | grep -v '^ok' ; echo END_OF_CMD_A"

// TestCockpitThreadToolRowsOpen reads a Claude Code transcript in the agents
// view whose working turn has a thought, a long Bash call with 15 lines of
// output, a long Bash call with no output, a short "true" with no output, and
// two Reads, after 30 short turns that fill the thread's ruler with prompts.
//
// A tool row is folded: it shows its command cut and no output. A click opens
// it on the whole command and the first 8 lines of its output, with "7 more
// lines", and its fold mark turns to the open mark; a second click folds it.
// The empty call says "No output." when open. The "true" row has nothing more
// to show and no fold mark. The Read group opens on the whole path of each
// file. e opens every row with all of its output. The ruler, with a prompt on
// most of its rows, draws no prompt marks, and the status marks stay out of
// its column. The frames are tool-rows-folded, tool-rows-open and
// tool-rows-all.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with `head.hit = hit` cut from ckToolCard, the test fails at "a click
//     did not open the tool row".
//   - with ckToolCard's `more` forced true, the test fails at "the true row
//     has a fold mark".
//   - with the saturation return cut from drawThreadRuler, the test fails at
//     "the ruler draws prompt marks".
func TestCockpitThreadToolRowsOpen(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	path := filepath.Join(atProjects(base), "work", "7d1e0b2a.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, toolRowsTranscript(t, time.Now()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "ui-tests"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "ui-tests")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "7d1e0b2a"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "api", "-w", "ui-tests", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 45, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Run the UI tests.", "Bash  true", "Read 2")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatal(err)
	}
	text := term.Screen().Text()
	if strings.Contains(text, "END_OF_CMD_A") || strings.Contains(text, "out line 1") {
		t.Fatalf("ASSERTION: a tool row is open before a click\n%s", term.Snapshot())
	}
	// The status marks keep out of the ruler's column, and the ruler, with
	// prompts on most of its rows, draws none.
	if marks, status := toolRowsRuler(term); marks > 0 || status > 0 {
		t.Fatalf("ASSERTION: the ruler draws prompt marks (%d), or a status mark sits in its column (%d)\n%s", marks, status, term.Snapshot())
	}
	trueRow, trueCol := findText(t, term, "Bash  true")
	if mark := toolRowMark(term, trueRow, trueCol); mark != " " {
		t.Fatalf("ASSERTION: the true row has a fold mark %q, and nothing more to show\n%s", mark, term.Snapshot())
	}
	saveCockpitFrames(t, term, "tool-rows-folded")

	// A click opens the long call on its command and the head of its output.
	row, col := findLastText(t, term, "Bash  go test")
	folded := toolRowMark(term, row, col)
	if folded == " " {
		t.Fatalf("ASSERTION: the long call has no fold mark\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+8, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "END_OF_CMD_A", "out line 8", "7 more lines")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click did not open the tool row: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "out line 9") {
		t.Fatalf("ASSERTION: the open row shows more than 8 lines of output\n%s", term.Snapshot())
	}
	row, col = findLastText(t, term, "Bash  go test")
	if open := toolRowMark(term, row, col); open == folded || open == " " {
		t.Fatalf("ASSERTION: the fold mark did not turn to the open mark (%q, folded %q)\n%s", open, folded, term.Snapshot())
	}
	// The empty call says so.
	row, col = findText(t, term, "Bash  sleep 1")
	mouseClick(t, term, col+8, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the empty call open", "No output.")
	// The Read group opens on each file's whole path.
	row, col = findText(t, term, "Read 2")
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the read group open", "/work/ui/header.go", "/work/layout/list.go")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatal(err)
	}
	saveCockpitFrames(t, term, "tool-rows-open")

	// A second click folds the long call.
	row, col = findLastText(t, term, "Bash  go test")
	mouseClick(t, term, col+8, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "END_OF_CMD_A") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a second click did not fold the tool row: %v\n%s", err, term.Snapshot())
	}

	// e opens every row with all of its output.
	if err := term.SendKeys("e"); err != nil {
		t.Fatal(err)
	}
	scrollUntil(t, term, "J", "out line 15")
	scrollUntil(t, term, "K", "END_OF_CMD_A")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatal(err)
	}
	saveCockpitFrames(t, term, "tool-rows-all")
	alive(t, term, "after the tool rows")
}

// findLastText is findText for the last row that holds want.
func findLastText(t *testing.T, term *tuitest.Terminal, want string) (row, col int) {
	t.Helper()
	if err := term.WaitForText(want, uiTimeout); err != nil {
		t.Fatalf("%q never appeared: %v\n%s", want, err, term.Snapshot())
	}
	s := term.Screen()
	_, rows := s.Size()
	row = -1
	for r := range rows {
		if before, _, ok := strings.Cut(s.Line(r), want); ok {
			row, col = r, len([]rune(before))
		}
	}
	return row, col
}

// toolRowsRuler finds the thread's ruler, the column of its thumb, and
// counts the prompt marks in it and the tool lines whose status mark (a
// check) is drawn there.
func toolRowsRuler(term *tuitest.Terminal) (marks, status int) {
	s := term.Screen()
	_, rows := s.Size()
	col := -1
	for r := range rows {
		rs := []rune(s.Line(r))
		for i := len(rs) - 1; i >= 0; i-- {
			if rs[i] == '┃' {
				col = i
				break
			}
		}
		if col >= 0 {
			break
		}
	}
	if col < 0 {
		return 0, 0
	}
	for r := range rows {
		rs := []rune(s.Line(r))
		if col < len(rs) {
			switch rs[col] {
			case '━':
				marks++
			case '\uf00c', '√', 'v':
				status++
			}
		}
	}
	return marks, status
}

// toolRowMark is the fold mark of the tool line whose kind starts at col:
// the cell two to its left.
func toolRowMark(term *tuitest.Terminal, row, col int) string {
	rs := []rune(term.Screen().Line(row))
	if col < 2 || col-2 >= len(rs) {
		return ""
	}
	return string(rs[col-2])
}

// toolRowsTranscript is the transcript of TestCockpitThreadToolRowsOpen,
// with times before now.
func toolRowsTranscript(t *testing.T, now time.Time) []byte {
	t.Helper()
	at := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	var recs []map[string]any
	for i := 1; i <= 30; i++ {
		ago := time.Duration(40-i) * time.Minute
		recs = append(recs,
			map[string]any{"type": "user", "timestamp": at(ago), "message": map[string]any{"role": "user", "content": fmt.Sprintf("Ping %d.", i)}},
			map[string]any{"type": "assistant", "timestamp": at(ago - 10*time.Second), "message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": fmt.Sprintf("Pong %d.", i)},
			}}})
	}
	var out []string
	for i := 1; i <= 15; i++ {
		out = append(out, fmt.Sprintf("out line %d", i))
	}
	call := func(ago time.Duration, id, name string, input map[string]any) map[string]any {
		return map[string]any{"type": "assistant", "timestamp": at(ago), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": name, "input": input},
		}}}
	}
	result := func(ago time.Duration, id, text string) map[string]any {
		return map[string]any{"type": "user", "timestamp": at(ago), "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "content": text},
		}}}
	}
	recs = append(recs,
		map[string]any{"type": "user", "timestamp": at(5 * time.Minute), "message": map[string]any{"role": "user", "content": "Run the UI tests."}},
		map[string]any{"type": "assistant", "timestamp": at(4*time.Minute + 50*time.Second), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "Run the three tests first.\nThen read the header.", "signature": "c2ln"},
		}}},
		call(4*time.Minute+40*time.Second, "toolu_a", "Bash", map[string]any{"command": toolRowsCmd}),
		result(4*time.Minute+30*time.Second, "toolu_a", strings.Join(out, "\n")),
		call(4*time.Minute+20*time.Second, "toolu_b", "Bash", map[string]any{"command": "sleep 1; test -f /work/ui/header.go && test -f /work/layout/list.go && test -f /work/theme/colours.go && test -f /work/theme/fonts.go || exit 0"}),
		result(4*time.Minute+10*time.Second, "toolu_b", ""),
		call(4*time.Minute, "toolu_c", "Bash", map[string]any{"command": "true"}),
		result(3*time.Minute+50*time.Second, "toolu_c", ""),
		call(3*time.Minute+40*time.Second, "toolu_r1", "Read", map[string]any{"file_path": "/work/ui/header.go"}),
		result(3*time.Minute+39*time.Second, "toolu_r1", "package ui"),
		call(3*time.Minute+38*time.Second, "toolu_r2", "Read", map[string]any{"file_path": "/work/layout/list.go"}),
		result(3*time.Minute+37*time.Second, "toolu_r2", "package layout"),
	)
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
