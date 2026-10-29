package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// How TestCockpitReplyEditorStdout could pass wrongly, written down first:
//   - The editor could be handed a pipe for stdout, as the scrollback editor
//     was: bubbletea gives a command the program's output, and the client's is
//     a wrapper, not a file. The fake editor records whether its stdout is a
//     terminal, and the test requires it.
//   - The fake editor could report a terminal wrongly. It tests [ -t 1 ] into
//     a variable before any redirect, since `if [ -t 1 ] ... fi > file` would
//     test the file.
//   - Ctrl+G could do something other than open the editor, and the record
//     would then never appear. The test waits for the record and fails on its
//     absence, not on a guess about the screen.
//   - replyEditorCmd has two callers, in inbox_reply.go and agent_mail.go.
//     This test reaches the one the agents view's reply box uses, and the
//     fix is in replyEditorCmd's command, which both share.

// fakeReplyEditor records whether its stdout is a terminal, then holds the
// terminal for a moment.
const fakeReplyEditor = `#!/bin/sh
if [ -t 1 ]; then out=terminal; else out=not-a-terminal; fi
printf '%s' "$out" > %DIR%/stdout
sleep 1
`

// TestCockpitReplyEditorStdout opens the reply box of an agent in the agents
// view and presses ctrl+g. The editor must get the terminal as its stdout.
func TestCockpitReplyEditorStdout(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "docs"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	for _, args := range [][]string{
		{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
		{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	rec := t.TempDir()
	editor := filepath.Join(t.TempDir(), "editor")
	script := []byte(strings.ReplaceAll(fakeReplyEditor, "%DIR%", rec))
	if err := os.WriteFile(editor, script, 0o755); err != nil {
		t.Fatal(err)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 55, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0", "EDITOR=" + editor, "VISUAL="}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the closed reply area", "Reply to docs-cli", "Write a reply.")
	row, col := findText(t, term, "Write a reply.")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the open reply editor", "Type your reply.")

	if err := term.SendKeys(tuitest.Ctrl('g')); err != nil {
		t.Fatalf("send ctrl+g: %v", err)
	}
	var got []byte
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(rec, "stdout")); err == nil && len(b) > 0 {
			got = b
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got == nil {
		t.Fatalf("ctrl+g in the reply box never ran the editor\n%s", term.Snapshot())
	}
	if string(got) != "terminal" {
		t.Errorf("the reply editor's stdout is %s, want the terminal: an editor on a pipe runs with echo on and prints the host's replies on the screen", got)
	}
}
