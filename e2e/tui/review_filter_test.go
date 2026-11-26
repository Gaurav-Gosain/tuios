package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestReviewFiltersByAuthor runs a turn of a Claude Code pane and a turn of a
// Codex pane in one repository, with lines of the person's, and filters the
// review by author with the mouse:
//
//   - a.go has Claude Code's hunk. b.go has Codex's hunk, with a line of the
//     person's next to it (a hunk of two authors), and a hunk of the person's
//     alone further down.
//   - A click on codex on its hunk's header shows Codex only: a.go leaves the
//     file list, the person's own hunk goes, and the hunk of two stays.
//   - A click on what the header says is hidden shows everyone again.
//   - The @ hint opens the menu. A click on Claude Code there hides it.
//
// How it could pass wrongly, written down first:
//   - The header could say "showing codex" while every hunk is still drawn.
//     The person's own hunk must be gone from the screen, and a.go from the
//     list.
//   - The hunk of two could go with the person's hunks. Its line of the
//     person's must stay on the screen while Codex is shown.
func TestReviewFiltersByAuthor(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	files := map[string]map[int]string{"a.go": {}, "b.go": {}}
	write := func(name string) {
		t.Helper()
		var b strings.Builder
		for i := 1; i <= 60; i++ {
			if s, ok := files[name][i]; ok {
				b.WriteString(s)
			} else {
				fmt.Fprintf(&b, "%s line %d", name, i)
			}
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go")
	write("b.go")
	testutil.Git(t, repo, "add", "a.go", "b.go")
	testutil.Git(t, repo, "commit", "-q", "-m", "two files")
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	if out, err := tuiosCLIIn(t, base, repo, "new-window", "CODEX", "-s", "agent", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v: %s", err, out)
	}
	cli := func(args ...string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	turns := 0
	turn := func(window, harness string, edit func()) {
		t.Helper()
		cli("set-agent-state", "working", "-s", "agent", "-w", window, "--harness", harness)
		// These panes have no transcript, so nothing can tell an edit that
		// raced the turn start's snapshot from the person's: the test waits
		// for the snapshot. TestReviewAsksTheAgentsOwnEdits covers the race.
		waitUntil(t, fmt.Sprintf("turn start %d is looked at", turns+1), uiTimeout, func() (bool, string) {
			b, _ := os.ReadFile(daemonLog)
			return strings.Count(string(b), "Turn start of window") > turns, string(b)
		})
		edit()
		cli("set-agent-state", "done", "-s", "agent", "-w", window, "--harness", harness, "-m", "done")
		turns++
		waitUntil(t, fmt.Sprintf("checkpoint %d is taken", turns), uiTimeout, func() (bool, string) {
			out, _ := tuiosCLI(t, base, "call", "list-checkpoints", `{"session":"agent","window":"`+window+`"}`)
			var got struct {
				Checkpoints []struct{} `json:"checkpoints"`
			}
			_ = json.Unmarshal([]byte(out), &got)
			return len(got.Checkpoints) >= 1, out
		})
	}
	turn("0", "claude-code", func() {
		files["a.go"][10] = "a.go A-CLAUDE"
		write("a.go")
	})
	turn("CODEX", "codex", func() {
		files["b.go"][10] = "b.go B-CODEX-1"
		files["b.go"][11] = "b.go B-CODEX-2"
		write("b.go")
	})
	// The person's lines, between the turns: one next to Codex's, one alone.
	files["b.go"][13] = "b.go B-YOU-NEAR"
	files["b.go"][45] = "b.go B-YOU-FAR"
	write("b.go")

	const rows = 50
	term := attachIn(t, base, "agent", startOpts{cols: 150, rows: rows})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	// No key from here on.
	waitScreen(t, term, "the review never named the authors", "A-CLAUDE", "claude-code · turn 1")
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

	// b.go, then Codex's name on its hunk.
	row, col := findText(t, term, "b.go")
	leftClick(t, term, col, row)
	until("b.go never opened", func(s tuitest.Screen) bool { return screenHas(s, "B-CODEX-1") && screenHas(s, "B-YOU-FAR") })
	frame("1-everyone")
	row, col = findText(t, term, "codex · turn 1")
	leftClick(t, term, col, row)
	until("a click on codex never showed Codex only", func(s tuitest.Screen) bool {
		return screenHas(s, "showing codex") && screenHas(s, "claude-code, you hidden (2 hunks)") && screenHas(s, "1 of 2 files")
	})
	until("the filter left the person's own hunk, or a.go, or dropped the hunk of two", func(s tuitest.Screen) bool {
		return !screenHas(s, "B-YOU-FAR") && !screenHas(s, "a.go") && screenHas(s, "B-YOU-NEAR") && screenHas(s, "B-CODEX-1")
	})
	frame("2-codex-only")

	// What is hidden, clicked: everyone again.
	row, col = findText(t, term, "claude-code, you hidden")
	leftClick(t, term, col, row)
	until("a click on what is hidden never showed everyone", func(s tuitest.Screen) bool {
		return !screenHas(s, "showing") && screenHas(s, "B-YOU-FAR") && screenHas(s, "a.go")
	})

	// The @ hint, then Claude Code in the menu.
	footer := rows - 2
	var x int
	until("the footer never offered @ who", func(s tuitest.Screen) bool {
		i := strings.Index(s.Line(footer), "@ who")
		if i < 0 {
			return false
		}
		x = len([]rune(s.Line(footer)[:i]))
		return true
	})
	leftClick(t, term, x, footer)
	until("the @ menu never opened", func(s tuitest.Screen) bool {
		l := s.Line(footer)
		return strings.Contains(l, "Who:") && strings.Contains(l, "claude-code 1") && strings.Contains(l, "codex 1") && strings.Contains(l, "you 1") && strings.Contains(l, "everyone")
	})
	frame("3-menu")
	until("the menu never placed claude-code", func(s tuitest.Screen) bool {
		i := strings.Index(s.Line(footer), "claude-code 1")
		if i < 0 {
			return false
		}
		x = len([]rune(s.Line(footer)[:i]))
		return true
	})
	leftClick(t, term, x, footer)
	until("a click on claude-code in the menu never hid it", func(s tuitest.Screen) bool {
		return screenHas(s, "showing codex you") && screenHas(s, "claude-code hidden (1 hunk)") && !screenHas(s, "a.go")
	})
	frame("4-claude-hidden")
}
