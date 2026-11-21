package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const superK = "\x1b[107;9u"

const clearScrollbackConfig = copyCursorConfig + "\n[keybindings.global]\nclear_scrollback = [\"super+k\"]\n"

func rowLine(n int) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^-\w])row-` + strconv.Itoa(n) + `(\D|$)`)
}

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

			top := firstRowMatching(term.Screen(), anyRow)
			if top < 0 {
				t.Fatalf("no row-N output on the screen before the clear\n%s", term.Snapshot())
			}
			copyTop(t, term)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return rowLine(1).MatchString(s.Text()) }, uiTimeout); err != nil {
				t.Fatalf("copy mode at the top does not show row-1, so the rows were never in the history\n%s", term.Snapshot())
			}
			saveFrame(t, term, "clear-scrollback-"+name+"-before")

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

func waitPaneCapture(t *testing.T, base, sess string, scrollback bool, ok func(string) bool) string {
	t.Helper()
	args := []string{"capture-pane", "-s", sess}
	if scrollback {
		args = append(args, "--scrollback")
	}
	return waitTextOf(t, "the capture of "+sess+" never matched", shellTimeout, func() string {
		out, _ := tuiosOut(base, args...)
		return out
	}, ok)
}

func firstRowMatching(s tuitest.Screen, re *regexp.Regexp) int {
	for i, line := range strings.Split(s.Text(), "\n") {
		if re.MatchString(line) {
			return i
		}
	}
	return -1
}

func copyTop(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")
	sendEach(t, term, "g", "g")
}

func checkPromptOnTop(t *testing.T, term *tuitest.Terminal, top int, marker string) {
	t.Helper()
	runInShell(t, term, "echo "+splitMarker(marker), marker, shellTimeout)
	cmdRow := firstRowMatching(term.Screen(), regexp.MustCompile(`\$ echo `+regexp.QuoteMeta(splitMarker(marker))))
	if cmdRow != top {
		t.Fatalf("the command typed after the clear is on row %d behind the prompt, want row %d, the pane's first row\n%s",
			cmdRow, top, term.Snapshot())
	}
}

func checkNoHistory(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	copyTop(t, term)
	time.Sleep(300 * time.Millisecond)
	if s := term.Screen().Text(); anyRow.MatchString(s) {
		t.Fatalf("copy mode at the top shows old rows %s\n%s", when, term.Snapshot())
	}
	sendEach(t, term, "q")
}

// TestClearKeepsScrollback runs what clear prints (ED 2 then ED 3) in a daemon
// pane after 200 numbered rows. By default the history must survive it: copy
// mode at the top shows row-1, and so does the daemon's copy, which an attach
// rebuilds the pane from. With appearance.scrollback_keep_on_clear = false the
// history must go, as before. How it could pass wrongly:
//   - The rows could never have reached the history: the drop case proves the
//     same rows are there to drop, and the keep case reads row-1 after it.
//   - The clear could do nothing: the screen must lose every row in both.
func TestClearKeepsScrollback(t *testing.T) {
	const rows = 200
	loop := `for i in $(seq 1 ` + strconv.Itoa(rows) + `); do echo "row-$i"; done`
	const clearSeq = `printf '\033[H\033[2J\033[3J'`
	for _, keep := range []bool{true, false} {
		name := "keep"
		config := copyCursorConfig
		if !keep {
			name = "drop"
			config = "[appearance]\nscrollback_keep_on_clear = false\n" + copyCursorConfig
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, config)
			killDaemon(t, base)
			const sess = "e2e-clear-keep"
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
			term := attachClear(t, base, sess, copyColorOpts)

			if err := term.SendKeys(clearSeq, tuitest.Enter); err != nil {
				t.Fatalf("type the clear: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return !anyRow.MatchString(s.Text()) }, uiTimeout); err != nil {
				t.Fatalf("the clear left rows on the screen\n%s", term.Snapshot())
			}

			out := waitPaneCapture(t, base, sess, true, func(out string) bool {
				return rowLine(1).MatchString(out) == keep
			})
			if keep && !rowLine(rows/2).MatchString(out) {
				t.Fatalf("the daemon kept only part of the history: row-%d is gone\n%s", rows/2, out)
			}

			if !keep {
				checkNoHistory(t, term, "after the clear with keep_on_clear off")
				return
			}
			copyTop(t, term)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return rowLine(1).MatchString(s.Text()) }, uiTimeout); err != nil {
				t.Fatalf("copy mode at the top does not show row-1 after clear\n%s", term.Snapshot())
			}
			saveFrame(t, term, "clear-keeps-scrollback")
			sendEach(t, term, "q")
		})
	}
}
