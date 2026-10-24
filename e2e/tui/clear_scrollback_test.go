package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// How TestClearScrollback could pass wrongly, written down first:
//   - The rows could never have been in the history. Copy mode goes to the top
//     before the clear and must show row-1 there.
//   - The key could do nothing and the rows scroll away by themselves. The
//     screen holds rows before the clear and must hold none after it, with
//     nothing printed in between.
//   - The clear could take the prompt with it, as ED 2 with the cursor at home
//     does. The command typed after the clear must sit on the pane's first row
//     behind the "$ " prompt.
//   - A client could clear only its own emulator. The daemon case detaches and
//     attaches again, and an attach rebuilds the pane from the daemon. Then it
//     reads the daemon's copy with capture-pane.
//   - The CLI could clear the screen too, or clear nothing. With no --screen it
//     must keep the newest row on the screen and drop the oldest.

// superK is super+k in the kitty keyboard protocol, which is what a host
// terminal sends for cmd+k when it lets the key through.
const superK = "\x1b[107;9u"

// clearScrollbackConfig binds clear_scrollback to super+k in the global
// section, which is the cmd+k binding the docs give.
const clearScrollbackConfig = copyCursorConfig + "\n[keybindings.global]\nclear_scrollback = [\"super+k\"]\n"

// rowLine matches a row-N line of the loop's output for one N.
func rowLine(n int) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^-\w])row-` + strconv.Itoa(n) + `(\D|$)`)
}

// anyRow matches any row-N line of the loop.
// The typed loop spells it "row-$i", so only output matches.
var anyRow = regexp.MustCompile(`row-\d`)

// TestClearScrollback prints numbered rows, then presses the clear_scrollback
// key in copy mode. The history and the rows above the prompt must go, the
// prompt must stay on the pane's first row, and in a daemon session an attach
// after the clear must not bring the rows back.
func TestClearScrollback(t *testing.T) {
	const rows = 200
	loop := `for i in $(seq 1 ` + strconv.Itoa(rows) + `); do echo "row-$i"; done`
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, clearScrollbackConfig)
			opts := copyColorOpts
			const sess = "e2e-clear"

			var term *tuitest.Terminal
			if daemon {
				killDaemon(t, base)
				if o, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
					t.Fatalf("create session: %v\n%s", err, o)
				}
				if o, err := tuiosCLI(t, base, "send-keys", "-s", sess, "--raw", loop); err != nil {
					t.Fatalf("send the loop: %v\n%s", err, o)
				}
				if o, err := tuiosCLI(t, base, "send-keys", "-s", sess, "Enter"); err != nil {
					t.Fatalf("send Enter: %v\n%s", err, o)
				}
				waitPaneCapture(t, base, sess, false, func(out string) bool { return rowLine(rows).MatchString(out) })
				term = attachClear(t, base, sess, opts)
			} else {
				term = startIn(t, base, opts)
				waitBoot(t, term)
				newWindow(t, term)
				enterTerminalMode(t, term)
				runInShell(t, term, loop, "row-"+strconv.Itoa(rows), shellTimeout)
			}
			time.Sleep(300 * time.Millisecond)

			// The pane's first content row is the first row the output fills.
			top := firstRowMatching(term.Screen(), anyRow)
			if top < 0 {
				t.Fatalf("no row-N output on the screen before the clear\n%s", term.Snapshot())
			}
			copyTop(t, term)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return rowLine(1).MatchString(s.Text()) }, uiTimeout); err != nil {
				t.Fatalf("copy mode at the top does not show row-1, so the rows were never in the history\n%s", term.Snapshot())
			}
			saveFrame(t, term, "clear-scrollback-"+name+"-before")

			// The key is pressed in copy mode, where cmd+k must work too.
			if err := term.SendKeys(superK); err != nil {
				t.Fatalf("send super+k: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return !anyRow.MatchString(s.Text()) }, uiTimeout); err != nil {
				t.Fatalf("super+k did not clear the rows from the screen\n%s", term.Snapshot())
			}
			checkPromptOnTop(t, term, top, "AFTER")
			saveFrame(t, term, "clear-scrollback-"+name+"-after")
			checkNoHistory(t, term, "after the clear")

			if !daemon {
				return
			}

			// Detach and attach again. The attach rebuilds the pane from the
			// daemon's emulator.
			if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
				t.Fatalf("send leader d: %v", err)
			}
			waitExit(t, term, "after leader d")
			term = attachClear(t, base, sess, opts)
			time.Sleep(300 * time.Millisecond)
			if s := term.Screen().Text(); anyRow.MatchString(s) {
				t.Fatalf("the attach after the clear shows the old rows again\n%s", term.Snapshot())
			}
			checkNoHistory(t, term, "after the attach")
			saveFrame(t, term, "clear-scrollback-daemon-reattach")

			out, err := tuiosOut(base, "capture-pane", "-s", sess, "--scrollback")
			if err != nil {
				t.Fatalf("capture-pane: %v\n%s", err, out)
			}
			if anyRow.MatchString(out) {
				t.Fatalf("the daemon's copy of the pane still holds the old rows\n%s", out)
			}

			// tuios clear-history without --screen drops the history and
			// keeps the screen, as tmux clear-history does.
			again := `for i in $(seq 1 ` + strconv.Itoa(rows) + `); do echo "again-$i"; done`
			if err := term.SendKeys(again, tuitest.Enter); err != nil {
				t.Fatalf("type the second loop: %v", err)
			}
			againLine := func(n int) *regexp.Regexp {
				return regexp.MustCompile(`(^|\n)again-` + strconv.Itoa(n) + `\s*(\n|$)`)
			}
			waitPaneCapture(t, base, sess, true, func(out string) bool {
				return againLine(1).MatchString(out) && againLine(rows).MatchString(out)
			})
			if o, err := tuiosCLI(t, base, "clear-history", "-s", sess); err != nil {
				t.Fatalf("tuios clear-history: %v\n%s", err, o)
			}
			out = waitPaneCapture(t, base, sess, true, func(out string) bool { return !againLine(1).MatchString(out) })
			if !againLine(rows).MatchString(out) {
				t.Fatalf("tuios clear-history removed the screen too: again-%d is gone\n%s", rows, out)
			}
		})
	}
}

// attachClear starts a client on sess and puts it in terminal mode.
func attachClear(t *testing.T, base, sess string, opts startOpts) *tuitest.Terminal {
	t.Helper()
	opts.args = []string{"attach", sess}
	term := startIn(t, base, opts)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)
	return term
}

// waitCapture polls the daemon's capture of sess until ok holds, and returns
// the last capture.
func waitPaneCapture(t *testing.T, base, sess string, scrollback bool, ok func(string) bool) string {
	t.Helper()
	args := []string{"capture-pane", "-s", sess}
	if scrollback {
		args = append(args, "--scrollback")
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		out, _ := tuiosOut(base, args...)
		if ok(out) {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("the capture of %s never matched\n%s", sess, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// firstRowMatching is the first screen row re matches, or -1.
func firstRowMatching(s tuitest.Screen, re *regexp.Regexp) int {
	for i, line := range strings.Split(s.Text(), "\n") {
		if re.MatchString(line) {
			return i
		}
	}
	return -1
}

// copyTop enters copy mode and goes to the top of the history.
func copyTop(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")
	sendEach(t, term, "g", "g")
}

// checkPromptOnTop types a command and requires it on row top behind the
// shell's "$ " prompt: the clear kept the prompt and moved it to the top.
func checkPromptOnTop(t *testing.T, term *tuitest.Terminal, top int, marker string) {
	t.Helper()
	runInShell(t, term, "echo "+splitMarker(marker), marker, shellTimeout)
	cmdRow := firstRowMatching(term.Screen(), regexp.MustCompile(`\$ echo `+regexp.QuoteMeta(splitMarker(marker))))
	if cmdRow != top {
		t.Fatalf("the command typed after the clear is on row %d behind the prompt, want row %d, the pane's first row\n%s",
			cmdRow, top, term.Snapshot())
	}
}

// checkNoHistory goes to the top of the history in copy mode and requires no
// row-N output there, then leaves copy mode.
func checkNoHistory(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	copyTop(t, term)
	time.Sleep(300 * time.Millisecond)
	if s := term.Screen().Text(); anyRow.MatchString(s) {
		t.Fatalf("copy mode at the top shows old rows %s\n%s", when, term.Snapshot())
	}
	sendEach(t, term, "q")
}
