package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// replyMarkersAnswer is an agent's answer with each reply marker, a synonym,
// a word the list does not know, the marks that take a colour, a tree, and a
// marker in a code span and in a code block, which stay as text.
const replyMarkersAnswer = "[[Done]] The parser reads markers.\n\n" +
	"- [[Failed: 3 tests]] in the store package\n" +
	"- [[Blocked]] on the CI runner\n" +
	"- [[Needs you]] pick the release name\n" +
	"- [[Unverified]] on Windows\n" +
	"- [[Complete]] docs updated\n" +
	"- [[Owned]] by the team\n\n" +
	"✓ unit tests pass ✗ lint fails → fix lint next\n\n" +
	"Layout:\n├── cmd\n└── internal\n\n" +
	"In code it stays: `[[Done]]`\n\n" +
	"```\n[[Failed]] in a block\n```"

// TestCockpitThreadReplyMarkers reads an agent's answer with reply markers in
// the agents view's thread, at truecolor and at 16 colours. Each marker is a
// pill: its label without the brackets, on a ground of its own at truecolor
// and in reverse at 16 colours, and a failure looks different from a success.
// A synonym shows the word it stands for. A marker in a code span or a code
// block stays as text. The check, the cross and the tree lines take inks of
// their own.
//
// How it could pass wrongly: labels drawn as plain text pass the "no
// brackets" check, so the pill cell is compared with the plain text beside it.
// A pill in one colour for every group passes that, so Failed and Done are
// compared too.
//
// Negative control: on main the thread shows "[[Done]]" and every other
// marker as written, and the bracket count fails.
func TestCockpitThreadReplyMarkers(t *testing.T) {
	for _, d := range []chromeDepth{chromeDepths[0], chromeDepths[2]} {
		t.Run(d.name, func(t *testing.T) {
			term := replyMarkersThread(t, d.env)
			name := "reply-markers-" + d.name
			saveFrame(t, term, name)
			savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), name)
			text := term.Screen().Text()
			if n := strings.Count(text, "[["); n != 2 {
				t.Errorf("ASSERTION: the thread shows %d markers with their brackets, want the 2 in code:\n%s", n, term.Snapshot())
			}
			for _, want := range []string{"Failed: 3 tests", "Blocked", "Needs you", "Unverified", "Owned", "[[Done]]", "[[Failed]] in a block"} {
				if !strings.Contains(text, want) {
					t.Errorf("ASSERTION: the thread does not show %q:\n%s", want, term.Snapshot())
				}
			}
			if !regexp.MustCompile(`Done\s+docs updated`).MatchString(text) {
				t.Errorf("ASSERTION: the Complete marker does not show as Done:\n%s", term.Snapshot())
			}
			s := term.Screen()
			cell := func(want string, at int) tuitest.Cell {
				row, col := findText(t, term, want)
				return s.Cell(col+at, row)
			}
			// The Done pill ends one cell of its ground and one gap before the text.
			failed, done := cell("Failed: 3", 0), cell("The parser", -4)
			plain := cell("in the store", 0)
			for name, c := range map[string]tuitest.Cell{"Failed": failed, "Done": done} {
				if !c.Reverse && c.Bg == plain.Bg {
					t.Errorf("ASSERTION: the %s pill has the ground of plain text and is not in reverse\n%s", name, term.SnapshotStyled())
				}
			}
			// The cell before the gap after a pill is the pill's own: a
			// pill in reverse whose end cells read as spaces between words
			// lost them.
			if end := cell("in the store", -2); !end.Reverse && end.Bg == plain.Bg {
				t.Errorf("ASSERTION: the Failed pill has no cell of its ground after its label\n%s", term.SnapshotStyled())
			}
			if failed.Fg == done.Fg && failed.Bg == done.Bg {
				t.Errorf("ASSERTION: the Failed and Done pills are in the same colours\n%s", term.SnapshotStyled())
			}
			if check, word := cell("✓ unit", 0), cell("✓ unit", 2); check.Fg == word.Fg {
				t.Errorf("ASSERTION: the check is in the ink of the text after it\n%s", term.SnapshotStyled())
			}
			// Each tree row is a line of its own, under the line before it.
			if r1, _ := findText(t, term, "├── cmd"); func() bool { r2, _ := findText(t, term, "└── internal"); return r2 != r1+1 }() {
				t.Errorf("ASSERTION: the tree rows are not on lines of their own:\n%s", term.Snapshot())
			}
			if tree, word := cell("├── cmd", 0), cell("├── cmd", 4); tree.Fg == word.Fg {
				t.Errorf("ASSERTION: the tree line is in the ink of its item\n%s", term.SnapshotStyled())
			}
			alive(t, term, "after a thread with reply markers")
		})
	}
}

// replyMarkersThread writes a Claude Code transcript whose answer is
// replyMarkersAnswer, binds it to a pane, and opens the agents view on it in
// a client with env.
func replyMarkersThread(t *testing.T, env []string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	now := time.Now()
	at := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	recs := []map[string]any{
		{"type": "user", "timestamp": at(2 * time.Minute), "message": map[string]any{"role": "user", "content": "Report on the release."}},
		{"type": "assistant", "timestamp": at(time.Minute), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": replyMarkersAnswer},
		}}},
	}
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
	path := filepath.Join(atProjects(base), "work", "c0a1d2c3.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "release"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "release")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "c0a1d2c3"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 44, shippedLooks: true, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "on Windows") && strings.Contains(s.Text(), "in a block")
	}, 20*time.Second); err != nil {
		t.Fatalf("the thread never showed the answer: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	return term
}
