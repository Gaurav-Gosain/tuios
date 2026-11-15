package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The addresses in the thread of TestCockpitLinksOpenOnClick. Each one is
// written once in the transcript, so the cell a test finds it on is the only
// place it is drawn.
const (
	ckLinkMarkdown = "https://example.com/cockpit-markdown"
	ckLinkBare     = "https://example.com/cockpit-bare"
	ckLinkTool     = "https://example.com/cockpit-tool"
)

// TestCockpitLinksOpenOnClick clicks the links in the agents view's thread
// and reads what tuios did from the link opener's record: a Markdown link
// opens its address (not its label), a bare URL in the answer and one in a
// tool's command open as written, and gx in copy mode opens the link under
// the cursor. The pointer over the URL in the tool line, which is plain
// text, underlines it before the click. The artifacts are the frames
// cockpit-links-before (no pointer) and cockpit-links-hover (the pointer on
// the tool line's URL).
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the cockpitLinkClick call cut from CockpitClick, the test fails at
//     "a click on the Markdown link".
//   - with drawCockpitLinkHover not called, the test fails at "the hovered
//     URL never underlined".
//   - with the gx branch cut from handleCockpitCopyKey, the test fails at
//     "gx in copy mode".
func TestCockpitLinksOpenOnClick(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, fmt.Sprintf("[appearance]\nlink_opener = %q\n[agents]\nview_list = \"strip\"\n", opener))
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	path := filepath.Join(atProjects(base), "work", "0c0ffee2.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, linksTranscript(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "docs-fix"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "docs-fix")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "0c0ffee2"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "api", "-w", "docs-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 40, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Where are the retry rules", "the retry docs")
	// The finished turn folds; e opens every card, the tool's among them.
	if err := term.SendKeys("e"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the open thread", ckLinkTool)
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "cockpit-links-before")

	// The pointer on the URL in the tool line underlines it. The line is
	// plain text, not underlined before, so the underline is the hover's.
	col, row := mustFind(t, term, ckLinkTool)
	mid := col + len(ckLinkTool)/2
	if cellUnderlined(term.Screen(), mid, row) {
		t.Fatalf("ASSERTION: the tool line's URL is underlined with no pointer on it\n%s", term.Snapshot())
	}
	mouseHover(t, term, mid, row)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return cellUnderlined(s, mid, row) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the hovered URL never underlined: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-links-hover")

	var want []string
	click := func(text, url, what string) {
		t.Helper()
		col, row := mustFind(t, term, text)
		mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
		want = append(want, url)
		waitOpened(t, record, want, what)
	}
	click("the retry docs", ckLinkMarkdown, "ASSERTION: a click on the Markdown link")
	click(ckLinkBare, ckLinkBare, "ASSERTION: a click on the bare URL")
	click(ckLinkTool, ckLinkTool, "ASSERTION: a click on the URL in the tool line")

	// Keyboard: copy mode, a search to the link, then gx.
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Copy mode. v selects", uiTimeout); err != nil {
		t.Fatalf("copy mode did not start: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.Type("retry docs"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("/retry docs (", uiTimeout); err != nil {
		t.Fatalf("the search did not end: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("g", "x"); err != nil {
		t.Fatal(err)
	}
	want = append(want, ckLinkMarkdown)
	waitOpened(t, record, want, "ASSERTION: gx in copy mode")
	alive(t, term, "after the link clicks")
}

// linksTranscript is the transcript of TestCockpitLinksOpenOnClick: a prompt,
// an answer with a Markdown link and a bare URL, and a Bash call whose command
// holds a URL.
func linksTranscript(t *testing.T) []byte {
	t.Helper()
	recs := []map[string]any{
		{"type": "user", "timestamp": "2026-10-20T10:00:00.000Z", "message": map[string]any{"role": "user",
			"content": "Where are the retry rules written down?"}},
		{"type": "assistant", "timestamp": "2026-10-20T10:00:20.000Z", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_b", "name": "Bash", "input": map[string]any{"command": "curl -s " + ckLinkTool}},
		}}},
		{"type": "user", "timestamp": "2026-10-20T10:00:21.000Z", "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_b", "content": "ok"},
		}}},
		{"type": "assistant", "timestamp": "2026-10-20T10:00:30.000Z", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Read [the retry docs](" + ckLinkMarkdown + ") first.\n\nThe log is at " + ckLinkBare + " now."},
		}}},
	}
	var buf bytes.Buffer
	for i, r := range recs {
		r["uuid"] = fmt.Sprintf("l%d", i)
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
