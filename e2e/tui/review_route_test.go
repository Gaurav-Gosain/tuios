package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestReviewSendsEachNoteToItsAuthor leaves notes in a review of lines by
// Claude Code, Aider and the person, and sends them:
//
//   - c on Claude Code's line names Claude Code in the note editor, and c on
//     Aider's line names Aider. The review is on Claude Code's pane.
//   - c on the person's own line keeps the note for the person. The @ hint,
//     a click on the receiver's name and @ go through the receivers.
//   - C on a hunk of Aider's two lines and the person's one names Aider, its
//     main author by lines.
//   - The footer says how many notes go where. S types Claude Code's note
//     into its pane and Aider's two into Aider's pane, and the person's note
//     into neither.
//
// Aider, not Codex: the queue holds a message for Codex while its input box
// rule reads a draft on the screen, and these panes are shells.
//
// How it could pass wrongly, written down first:
//   - The editor could name Aider while the note still goes to the pane the
//     review is on. Each pane's screen is read after S.
//   - The person's note could be sent with the others. Neither pane may show
//     it, and the daemon must list it as unsent.
func TestReviewSendsEachNoteToItsAuthor(t *testing.T) {
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
	if out, err := tuiosCLIIn(t, base, repo, "new-window", "AIDER", "-s", "agent", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v: %s", err, out)
	}
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	turns := 0
	turn := func(window, harness string, edit func()) {
		t.Helper()
		cli("set-agent-state", "working", "-s", "agent", "-w", window, "--harness", harness)
		// These panes have no transcript: the test waits for the turn
		// start's snapshot, as TestReviewFiltersByAuthor does.
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
	turn("AIDER", "aider", func() {
		files["b.go"][10] = "b.go B-AIDER-1"
		files["b.go"][11] = "b.go B-AIDER-2"
		write("b.go")
	})
	// The person's lines: one in Aider's hunk, one alone.
	files["b.go"][13] = "b.go B-YOU-NEAR"
	files["b.go"][45] = "b.go B-YOU-FAR"
	write("b.go")

	const rows = 50
	term := attachIn(t, base, "agent", startOpts{cols: 150, rows: rows})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
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
	footer := rows - 2
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
	editorSays := func(label string) {
		t.Helper()
		until("the note editor never said "+label, func(s tuitest.Screen) bool { return screenHas(s, label+"_") })
	}

	// Claude Code's line, in the review of its own pane.
	onLine("A-CLAUDE")
	sendKeys(t, term, "c")
	editorSays("you → claude-code: ")
	sendKeys(t, term, "tell claude", tuitest.Enter)
	until("the note to claude-code was never listed", func(s tuitest.Screen) bool { return screenHas(s, "note: tell claude") })

	// Aider's line names Aider.
	onLine("b.go")
	onLine("B-AIDER-1")
	sendKeys(t, term, "c")
	editorSays("you → aider: ")
	frame("1-note-to-aider")
	sendKeys(t, term, "tell aider", tuitest.Enter)
	until("the note to aider was never listed", func(s tuitest.Screen) bool { return screenHas(s, "note to aider: tell aider") })

	// The person's own line keeps the note. The receivers go round: the @
	// hint, a click on the name, then @.
	onLine("B-YOU-FAR")
	sendKeys(t, term, "c")
	editorSays("you → you, not sent: ")
	hint("@ to claude-code")
	editorSays("you → claude-code: ")
	row, col := findText(t, term, "you → claude-code: ")
	leftClick(t, term, col+len([]rune("you → ")), row)
	editorSays("you → aider: ")
	sendKeys(t, term, "@")
	editorSays("you → you, not sent: ")
	frame("2-kept-for-you")
	sendKeys(t, term, "my own todo", tuitest.Enter)
	until("the kept note was never listed", func(s tuitest.Screen) bool { return screenHas(s, "note for you, not sent: my own todo") })

	// A hunk of two hands goes to its main author by lines.
	onLine("B-YOU-NEAR")
	sendKeys(t, term, "C")
	editorSays("you → aider (hunk): ")
	sendKeys(t, term, "hunk for aider", tuitest.Enter)
	until("the hunk note was never listed", func(s tuitest.Screen) bool { return screenHas(s, "note (hunk) to aider: hunk for aider") })

	until("the footer never said where the notes go", func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(footer), "S send 1 to claude-code, 2 to aider")
	})
	frame("3-send-counts")
	hint("S send")
	said := regexp.MustCompile(`1 note (sent to|queued for) claude-code\. 2 notes (sent to|queued for) aider`)
	until("the status line never said where the notes went", func(s tuitest.Screen) bool { return said.MatchString(s.Text()) })
	frame("4-sent")

	pane := func(window string, want, not []string) {
		t.Helper()
		waitUntil(t, "window "+window+" receives its notes", uiTimeout, func() (bool, string) {
			out, _ := tuiosCLI(t, base, "capture-pane", "-s", "agent", "-w", window)
			for _, w := range want {
				if !strings.Contains(out, w) {
					return false, out
				}
			}
			return true, out
		})
		out := cli("capture-pane", "-s", "agent", "-w", window)
		for _, n := range not {
			if strings.Contains(out, n) {
				t.Errorf("ASSERTION: window %s got %q:\n%s", window, n, out)
			}
		}
	}
	pane("0", []string{"Review notes on your changes", "tell claude"}, []string{"tell aider", "hunk for aider", "my own todo"})
	pane("AIDER", []string{"Review notes on your changes", "tell aider", "hunk for aider"}, []string{"tell claude", "my own todo"})

	var notes struct {
		Notes []struct {
			Text   string `json:"text"`
			To     string `json:"to"`
			SentAt int64  `json:"sent_at"`
		} `json:"notes"`
	}
	out := cli("review", "notes", "-s", "agent", "-w", "0", "--json")
	if err := json.Unmarshal([]byte(out), &notes); err != nil || len(notes.Notes) != 4 {
		t.Fatalf("review notes --json = %s (%v)", out, err)
	}
	for _, n := range notes.Notes {
		if kept := n.Text == "my own todo"; kept != (n.SentAt == 0) || kept != (n.To == "person") {
			t.Errorf("ASSERTION: note %q has to %q and sent_at %d", n.Text, n.To, n.SentAt)
		}
	}

	// The daemon refuses a receiver outside the worktree's agents, and a
	// send to the person.
	out, _ = tuiosCLI(t, base, "call", "review-note", `{"action":"add","session":"agent","window":"0","path":"a.go","line":10,"text":"x","to":"nowhere"}`)
	if !strings.Contains(out, "is not an agent pane") {
		t.Errorf("ASSERTION: review-note took to nowhere:\n%s", out)
	}
	out, _ = tuiosCLI(t, base, "review", "send", "-s", "agent", "-w", "0", "--to", "person")
	if !strings.Contains(out, "never sent") {
		t.Errorf("ASSERTION: send-review took to person:\n%s", out)
	}
}
