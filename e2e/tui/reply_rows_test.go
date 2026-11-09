package tuie2e

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// replyRowRe is a line the test types into the reply editor.
var replyRowRe = regexp.MustCompile(`row-\d\d`)

// shownReplyRows counts the typed lines on screen.
func shownReplyRows(s tuitest.Screen) int {
	return len(replyRowRe.FindAllString(s.Text(), -1))
}

// TestReplyRowsGrowTheEditor types 20 lines into the agents view's reply
// editor and counts the lines it shows. With [agents] reply_rows unset it
// shows 8, its own height. With reply_rows = 12 it shows 12. alt+m makes it as
// tall as its area, which holds all 20, and alt+m again brings it back to 12.
//
// How it could pass wrongly, written down first:
//   - The lines could show somewhere else than the editor, such as a draft
//     line. The editor is the only place the agent's reply is drawn while it is
//     open, and the count is taken after the last line is typed.
//   - alt+m could be eaten by the view and the count stay put. The test waits
//     for the count to change both ways.
//
// Negative control: on main, which has no reply_rows, the run with 12 shows 8
// lines and the wait for 12 fails.
func TestReplyRowsGrowTheEditor(t *testing.T) {
	for _, tc := range []struct {
		name string
		conf string
		want int
	}{
		{"unset", "", 8},
		{"twelve", "[agents]\nreply_rows = 12\n", 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			useShippedLooks(base)
			if tc.conf != "" {
				writeConfig(t, base, tc.conf)
			}
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
			term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 50, shippedLooks: true,
				env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			windowManagementMode(t, term)
			sendKeys(t, term, tuitest.Ctrl('b'), "i")
			waitText(t, term, "the closed reply area", "Reply to docs-cli", "Write a reply.")
			row, col := findText(t, term, "Write a reply.")
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
			waitText(t, term, "the open reply editor", "Type your reply.")

			for i := 1; i <= 20; i++ {
				if err := term.SendKeys(fmt.Sprintf("row-%02d", i)); err != nil {
					t.Fatal(err)
				}
				if i < 20 {
					sendKeys(t, term, tuitest.Ctrl('j'))
				}
			}
			waitCount := func(what string, want int) {
				t.Helper()
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return shownReplyRows(s) == want && replyRowRe.MatchString(s.Text())
				}, uiTimeout); err != nil {
					t.Fatalf("ASSERTION: %s: the editor shows %d of the 20 lines, want %d: %v\n%s", what, shownReplyRows(term.Screen()), want, err, term.Snapshot())
				}
			}
			waitText(t, term, "the last typed line", "row-20")
			time.Sleep(insertGuard)
			waitCount("after typing", tc.want)
			railShot(t, term, "reply-"+tc.name)

			sendKeys(t, term, tuitest.Alt('m'))
			waitCount("after alt+m", 20)
			railShot(t, term, "reply-"+tc.name+"-full")

			sendKeys(t, term, tuitest.Alt('m'))
			waitCount("after alt+m again", tc.want)
		})
	}
}
