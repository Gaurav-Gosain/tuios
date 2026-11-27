package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestReviewRevertsAHunk reverts hunks from the review with the mouse only,
// and puts one back:
//
//   - a.go has a hunk of Claude Code's (line 10, turn 1) and one of the
//     person's (line 45). A click on Claude Code's line and on "X revert"
//     takes the line out of the file on disk, at once. The diff marks where
//     it was, and the status line says the agent is not told.
//   - "z undo revert" puts the line back in the file.
//   - X on the person's own line asks first. "n keep" leaves the file as it
//     is, and "y revert" takes the line out.
//   - While Claude Code works again, X on its hunk is refused, and says that
//     c sends it a note instead. The file keeps the line.
//   - "o open" opens a.go in an editor pane at the cursor's line.
//
// How it could pass wrongly, written down first:
//   - The status line could say "Reverted" while the file is unchanged. Each
//     step reads a.go from disk.
//   - The revert could take out the wrong hunk. After each, the other hunk's
//     line must still be in the file.
func TestReviewRevertsAHunk(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	lines := map[int]string{}
	file := filepath.Join(repo, "a.go")
	write := func() {
		t.Helper()
		var b strings.Builder
		for i := 1; i <= 60; i++ {
			if s, ok := lines[i]; ok {
				b.WriteString(s)
			} else {
				fmt.Fprintf(&b, "a.go line %d", i)
			}
			b.WriteByte('\n')
		}
		if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	testutil.Git(t, repo, "add", "a.go")
	testutil.Git(t, repo, "commit", "-q", "-m", "a")

	// The editor o opens: a script named vi, so it is given +line, that
	// prints what it was given.
	bin := t.TempDir()
	editor := filepath.Join(bin, "vi")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\necho EDITOR-GOT \"$@\"\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	cli := func(args ...string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	cli("set-agent-state", "working", "-s", "agent", "-w", "0", "--harness", "claude-code")
	// The pane has no transcript, so nothing could tell an edit that raced
	// the turn start's snapshot from the person's: the test waits for it.
	waitUntil(t, "the turn start is looked at", uiTimeout, func() (bool, string) {
		b, _ := os.ReadFile(daemonLog)
		return strings.Contains(string(b), "Turn start of window"), string(b)
	})
	lines[10] = "a.go A-CLAUDE"
	write()
	cli("set-agent-state", "done", "-s", "agent", "-w", "0", "--harness", "claude-code", "-m", "done")
	waitUntil(t, "the checkpoint is taken", uiTimeout, func() (bool, string) {
		out, _ := tuiosCLI(t, base, "call", "list-checkpoints", `{"session":"agent","window":"0"}`)
		var got struct {
			Checkpoints []struct{} `json:"checkpoints"`
		}
		_ = json.Unmarshal([]byte(out), &got)
		return len(got.Checkpoints) >= 1, out
	})
	lines[45] = "a.go A-YOU"
	write()
	onDisk := func(want, gone string) func() (bool, string) {
		return func() (bool, string) {
			b, _ := os.ReadFile(file)
			s := string(b)
			return (want == "" || strings.Contains(s, want)) && (gone == "" || !strings.Contains(s, gone)), s
		}
	}

	const rows = 40
	term := attachIn(t, base, "agent", startOpts{cols: 140, rows: rows, env: []string{"EDITOR=" + editor, "VISUAL="}})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	// No key from here on.
	waitScreen(t, term, "the review never named the author", "A-CLAUDE", "claude-code · turn 1")
	art := artifactDir(t)
	frame := func(name string) {
		t.Helper()
		saveArtifact(t, term, art, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), art, name)
	}
	until := func(what string, ok func(tuitest.Screen) bool) {
		t.Helper()
		if err := term.WaitFor(ok, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: %v\n%s", what, err, term.Snapshot())
		}
	}
	footer := rows - 2
	// hint clicks the footer's hint that starts with words.
	hint := func(words string) {
		t.Helper()
		var x int
		until("the footer never offered "+words, func(s tuitest.Screen) bool {
			i := strings.Index(s.Line(footer), words)
			if i < 0 {
				return false
			}
			x = len([]rune(s.Line(footer)[:i]))
			return true
		})
		leftClick(t, term, x, footer)
	}
	onLine := func(text string) {
		t.Helper()
		row, col := findText(t, term, text)
		leftClick(t, term, col, row)
	}

	// Claude Code's hunk: reverted at once, in the file only.
	onLine("A-CLAUDE")
	hint("X revert")
	until("the revert never marked where the hunk was", func(s tuitest.Screen) bool {
		return screenHas(s, "Reverted 1 line at line 10 (claude-code, turn 1)") && screenHas(s, "claude-code is not told") && !screenHas(s, "A-CLAUDE")
	})
	waitUntil(t, "A-CLAUDE leaves a.go, and A-YOU stays", uiTimeout, onDisk("A-YOU", "A-CLAUDE"))
	frame("1-reverted")

	// z puts it back.
	hint("z undo revert")
	waitUntil(t, "A-CLAUDE is back in a.go", uiTimeout, onDisk("A-CLAUDE", ""))
	until("the diff never showed A-CLAUDE again", func(s tuitest.Screen) bool {
		return screenHas(s, "A-CLAUDE") && screenHas(s, "The hunk is back in the file") && !screenHas(s, "Reverted 1 line")
	})

	// The person's own line: asked first.
	onLine("A-YOU")
	hint("X revert")
	until("X on the person's line never asked", func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(footer), "Revert your own lines in a.go? Nothing else keeps a copy.")
	})
	frame("2-asks-first")
	hint("n keep")
	until("n never closed the question", func(s tuitest.Screen) bool { return strings.Contains(s.Line(footer), "X revert") })
	if ok, s := onDisk("A-YOU", "")(); !ok {
		t.Fatalf("ASSERTION: n kept the line, yet a.go is:\n%s", s)
	}
	hint("X revert")
	hint("y revert")
	waitUntil(t, "A-YOU leaves a.go, and A-CLAUDE stays", uiTimeout, onDisk("A-CLAUDE", "A-YOU"))
	until("the status line never said how to put it back", func(s tuitest.Screen) bool {
		return screenHas(s, "Reverted. z puts it back") && screenHas(s, "Reverted 1 line at line 45.")
	})

	// While Claude Code works, X on its hunk is refused.
	cli("set-agent-state", "working", "-s", "agent", "-w", "0", "--harness", "claude-code")
	onLine("A-CLAUDE")
	hint("X revert")
	until("X under a writer was not refused", func(s tuitest.Screen) bool {
		return screenHas(s, "claude-code writes this hunk now, so X is refused until it stops. c sends it a note instead.")
	})
	frame("3-refused-while-it-writes")
	if ok, s := onDisk("A-CLAUDE", "")(); !ok {
		t.Fatalf("ASSERTION: the refused revert wrote a.go:\n%s", s)
	}
	// The daemon refuses it too, for a client that did not yet know the
	// agent works.
	out, err := tuiosCLI(t, base, "call", "review-diff", `{"session":"agent","window":"0","paths":["a.go"]}`)
	var diff struct {
		Files []review.File `json:"files"`
	}
	if err != nil || json.Unmarshal([]byte(out), &diff) != nil || len(diff.Files) != 1 {
		t.Fatalf("review-diff: %v\n%s", err, out)
	}
	var claude *review.Hunk
	for i, h := range diff.Files[0].Hunks {
		for _, ln := range h.Lines {
			if ln.Text == "a.go A-CLAUDE" {
				claude = &diff.Files[0].Hunks[i]
			}
		}
	}
	if claude == nil {
		t.Fatalf("review-diff has no hunk with A-CLAUDE:\n%s", out)
	}
	params, _ := json.Marshal(map[string]any{"action": "revert", "session": "agent", "window": "0", "path": "a.go", "hunk": claude.Header, "sum": claude.Sum()})
	out, _ = tuiosCLI(t, base, "call", "review-revert", string(params))
	if !strings.Contains(out, "agent_writing") {
		t.Fatalf("ASSERTION: the daemon did not refuse a revert under a writer:\n%s", out)
	}
	if ok, s := onDisk("A-CLAUDE", "")(); !ok {
		t.Fatalf("ASSERTION: the daemon's refused revert wrote a.go:\n%s", s)
	}
	// A hunk not as the caller read it is refused before anything else.
	params, _ = json.Marshal(map[string]any{"action": "revert", "session": "agent", "window": "0", "path": "a.go", "hunk": claude.Header, "sum": "0000000000000000"})
	out, _ = tuiosCLI(t, base, "call", "review-revert", string(params))
	if !strings.Contains(out, "hunk_changed") {
		t.Fatalf("ASSERTION: the daemon took a hunk whose lines differ from the ones read:\n%s", out)
	}

	// o opens a.go in an editor pane at the cursor's line.
	hint("o open")
	until("o never opened the editor at line 10", func(s tuitest.Screen) bool {
		return screenHas(s, "EDITOR-GOT +10 ") && screenHas(s, "a.go")
	})
	frame("4-open-in-editor")
}
