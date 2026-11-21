package tuie2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadStreamsInPlace streams one turn into a followed thread in
// the agents view, as Claude Code writes it: the person sends a prompt from
// the reply area, the agent thinks, runs three commands whose results arrive
// late (one fails, so its card grows by its output), and writes a long reply.
// The hooks name each tool before the transcript has its call, as they do.
//
// The ways it can go wrong, each an assertion over every frame drawn:
//   - a sent prompt goes to the top of the view and leaves a band of blank
//     rows under it, which the reply then fills;
//   - the live activity row is drawn under the newest line, and the card that
//     replaces it lands elsewhere, so the lines above jump;
//   - the echo of a sent prompt and the prompt the transcript holds differ in
//     height, so the lines above move down when the prompt lands;
//   - a card that grows in place moves the lines above it down, or up and
//     back.
//
// While the view follows, a line that stays on screen keeps its row or moves
// up by the lines added under it, so no line moves less than one under it. No
// frame has blank rows under the newest line past the one that ends a card.
// Scrolled up, nothing moves when an entry arrives. The artifact is the
// frame strip stream-frames.txt and the frames stream-*.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - on main (the anchor rule), it fails at "blank rows under the newest
//     line" and "a line moved down".
//   - with the echo's blank line kept under its state line, it fails at "a
//     line moved down 1 rows" when the prompt lands.
func TestCockpitThreadStreamsInPlace(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	path := filepath.Join(atProjects(base), "work", "5e7a11ed.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	at := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	n := 0
	rec := func(typ string, ago time.Duration, msg map[string]any) {
		n++
		appendTranscript(t, path, map[string]any{"type": typ, "timestamp": at(ago), "uuid": fmt.Sprintf("s%d", n), "isSidechain": false, "message": msg})
	}
	say := func(ago time.Duration, text string) {
		rec("assistant", ago, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}})
	}
	live := func(typ string, msg map[string]any) {
		now = time.Now()
		rec(typ, 0, msg)
	}
	liveSay := func(text string) {
		live("assistant", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}})
	}
	for i := 1; i <= 3; i++ {
		rec("user", time.Duration(40-i)*time.Minute, map[string]any{"role": "user", "content": fmt.Sprintf("Earlier question %d.", i)})
		var b strings.Builder
		for k := 1; k <= 8; k++ {
			fmt.Fprintf(&b, "Earlier answer %d, paragraph %d.\n\n", i, k)
		}
		say(time.Duration(39-i)*time.Minute, b.String())
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "stream"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := func(payload string) {
		t.Helper()
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "stream")
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("agent-hook %s: %v\n%s", payload, err, out)
		}
	}
	join := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "stream")
	join.Dir = workDirIn(t, base)
	join.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		join.Env = append(join.Env, key+"="+xdgDir(base, key))
	}
	join.Stdin = bytes.NewReader(hookPayload(path, "5e7a11ed"))
	if out, err := join.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "idle", "-s", "api", "-w", "stream", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 44, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Earlier answer 3, paragraph 8.")
	_ = term.WaitStable(uiTimeout)

	fr := &streamFrames{t: t, term: term}
	fr.settle("open")
	saveCockpitFrames(t, term, "stream-0-open")

	sendReply(t, term, "Add a footer to the page.")
	fr.until("the echo", func(s string) bool { return strings.Contains(s, "Add a footer to the page.") })
	fr.settle("echo")
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "api", "-w", "stream", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"5e7a11ed","prompt":"Add a footer to the page."}`)
	live("user", map[string]any{"role": "user", "content": "Add a footer to the page."})
	fr.until("the prompt lands", func(s string) bool { return !strings.Contains(s, "Sent, waiting for") })
	fr.settle("prompt")
	saveCockpitFrames(t, term, "stream-1-prompt")

	live("assistant", map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "thinking", "thinking": "Find the layout file first.\nThen add the footer.", "signature": "c2ln"}}})
	fr.settle("thinking")

	call := func(id, cmd string) {
		live("assistant", map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": cmd}}}})
	}
	result := func(id, text string, failed bool) {
		live("user", map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": failed}}})
	}
	pre := func(cmd string) {
		hook(`{"hook_event_name":"PreToolUse","session_id":"5e7a11ed","tool_name":"Bash","tool_input":{"command":"` + cmd + `"}}`)
	}
	post := func(cmd string) {
		hook(`{"hook_event_name":"PostToolUse","session_id":"5e7a11ed","tool_name":"Bash","tool_input":{"command":"` + cmd + `"},"tool_response":{"stdout":"ok","stderr":"","interrupted":false}}`)
	}
	cmds := []string{"ls web/layout", "grep -rn footer web/", "go test ./web/..."}
	for i, cmd := range cmds {
		pre(cmd)
		fr.until("the hook names "+cmd, func(s string) bool { return strings.Contains(s, cmd) })
		fr.settle("pre " + cmd)
		call(fmt.Sprintf("toolu_s%d", i), cmd)
		fr.settle("call " + cmd)
	}
	result("toolu_s0", "base.html\nfooter.html", false)
	post(cmds[0])
	fr.settle("result 0")
	result("toolu_s1", "", false)
	post(cmds[1])
	fr.settle("result 1")
	result("toolu_s2", "--- FAIL: TestFooter\n    footer_test.go:12: want footer\nFAIL\nFAIL\tweb\t0.01s", true)
	fr.settle("result 2")
	saveCockpitFrames(t, term, "stream-2-tools")

	var b strings.Builder
	for k := 1; k <= 14; k++ {
		fmt.Fprintf(&b, "Reply line %d: the footer goes under the main column.\n\n", k)
	}
	liveSay(b.String())
	fr.until("the reply", func(s string) bool { return strings.Contains(s, "Reply line 14:") })
	fr.settle("reply")
	saveCockpitFrames(t, term, "stream-3-reply")
	fr.check()

	ckPress(t, term, tuitest.Tab)
	ckPress(t, term, tuitest.PageUp)
	_ = term.WaitStable(uiTimeout)
	held := term.Screen().Text()
	liveSay("One more line after the reply.")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "1 new entry") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a held thread does not count the entry that arrived: %v\n%s", err, term.Snapshot())
	}
	got := term.Screen().Text()
	if a, b := streamRows(held), streamRows(got); len(a) == len(b) {
		for i := range a[:len(a)-3] {
			if a[i] != b[i] && !strings.Contains(b[i], "new entr") {
				t.Fatalf("ASSERTION: a held thread moved when an entry arrived, row %d:\n  was %q\n  now %q\n%s", i, a[i], b[i], got)
			}
		}
	}
	saveCockpitFrames(t, term, "stream-4-held")
	alive(t, term, "after streaming the turn")
}

type streamFrames struct {
	t      *testing.T
	term   *tuitest.Terminal
	frames []streamFrame
}

type streamFrame struct {
	step   string
	text   string
	screen tuitest.Screen
}

func (f *streamFrames) record(step string, s tuitest.Screen) string {
	text := s.Text()
	if n := len(f.frames); n == 0 || f.frames[n-1].text != text {
		f.frames = append(f.frames, streamFrame{step: step, text: text, screen: s})
	}
	return text
}

func (f *streamFrames) until(step string, cond func(string) bool) {
	f.t.Helper()
	if err := f.term.WaitFor(func(s tuitest.Screen) bool { return cond(f.record(step, s)) }, uiTimeout); err != nil {
		f.write()
		f.t.Fatalf("waiting for %s: %v\n%s", step, err, f.term.Snapshot())
	}
}

func (f *streamFrames) settle(step string) {
	f.t.Helper()
	deadline := time.Now().Add(uiTimeout)
	last := f.record(step, f.term.Screen())
	for time.Now().Before(deadline) {
		err := f.term.WaitFor(func(s tuitest.Screen) bool {
			return f.record(step, s) != last
		}, 600*time.Millisecond)
		if errors.Is(err, tuitest.ErrTimeout) {
			return
		}
		if err != nil {
			f.write()
			f.t.Fatalf("settling after %s: %v", step, err)
		}
		last = f.term.Screen().Text()
	}
}

func (f *streamFrames) write() {
	var b strings.Builder
	for i, fr := range f.frames {
		fmt.Fprintf(&b, "=== frame %d (%s)\n%s\n", i, fr.step, fr.text)
		if os.Getenv("TUIOS_E2E_FRAMES") != "" {
			saveScreenPNG(f.t, fr.screen, fmt.Sprintf("stream-frame-%02d", i))
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir(f.t), "stream-frames.txt"), []byte(b.String()), 0o644); err != nil {
		f.t.Errorf("save the frame strip: %v", err)
	}
}

func (f *streamFrames) check() {
	f.t.Helper()
	f.write()
	gap := streamBlankUnder(streamRows(f.frames[0].text))
	var bad []string
	for i := 1; i < len(f.frames); i++ {
		a, b := streamRows(f.frames[i-1].text), streamRows(f.frames[i].text)
		if len(a) == 0 || len(a) != len(b) {
			continue
		}
		where := func(rows []string) map[string]int {
			seen, at := map[string]int{}, map[string]int{}
			for r, s := range rows {
				if strings.Trim(s, " ▎│|") == "" {
					continue
				}
				seen[s]++
				at[s] = r
			}
			for s, c := range seen {
				if c > 1 {
					delete(at, s)
				}
			}
			return at
		}
		was, now := where(a), where(b)
		shift := make([]int, len(a))
		for r := range shift {
			shift[r] = -1
		}
		why := ""
		for s, r0 := range was {
			if r1, ok := now[s]; ok {
				shift[r0] = r0 - r1
				if r1 > r0 && why == "" {
					why = fmt.Sprintf("a line moved down %d rows: %q", r1-r0, strings.TrimSpace(s))
				}
			}
		}
		lower := -1
		for r := len(shift) - 1; r >= 0 && why == ""; r-- {
			if shift[r] < 0 {
				continue
			}
			if lower >= 0 && shift[r] < lower {
				why = fmt.Sprintf("a line moved up %d rows and one under it %d: the view shook", shift[r], lower)
			}
			lower = max(lower, shift[r])
		}
		if blank := streamBlankUnder(b); why == "" && blank > gap && blank < len(b) {
			why = fmt.Sprintf("%d blank rows under the newest line, %d at rest", blank, gap)
		}
		if why != "" {
			bad = append(bad, fmt.Sprintf("frame %d (%s): %s", i, f.frames[i].step, why))
		}
	}
	if len(bad) > 0 {
		f.t.Errorf("ASSERTION: the followed thread moved in %d of %d frames (the strip is stream-frames.txt in %s):\n  %s",
			len(bad), len(f.frames), artifactDir(f.t), strings.Join(bad, "\n  "))
	}
}

func streamBlankUnder(rows []string) int {
	n := 0
	for r := len(rows) - 1; r >= 0 && strings.TrimSpace(rows[r]) == ""; r-- {
		n++
	}
	return n
}

func streamRows(screen string) []string {
	lines := strings.Split(screen, "\n")
	top, bot := -1, -1
	for r, s := range lines {
		if top < 0 && strings.Contains(s, "Thread") && strings.Contains(s, "Pane") {
			top = r + 2
		}
		if top >= 0 && r > top && strings.Contains(s, "Reply to") {
			bot = r - 1
			break
		}
	}
	if top < 0 || bot <= top {
		return nil
	}
	rows := lines[top:bot]
	for i, s := range rows {
		rows[i] = strings.TrimRight(s, " ━┃")
	}
	return rows
}
