package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// paneSize matches the marker the pane's own shell computes from its terminal
// size: "SZ<tag>-<rows>-<cols>".
var paneSize = regexp.MustCompile(`SZ([AB])-(\d+)-(\d+)`)

// reportedPaneSize asks the focused pane's shell how big its terminal is and
// returns the answer.
//
// The shell computes it from the kernel's window size for its own PTY, so this
// is the one number the bug is about: what the program running in the pane
// thinks it has to draw into. It cannot be satisfied by an echo of the
// keystrokes, because the digits are not in what is typed.
func reportedPaneSize(t *testing.T, term *tuitest.Terminal, tag string) (rows, cols int) {
	t.Helper()
	// The marker is assembled by the shell rather than typed, so the command
	// line itself never matches paneSize and cannot be mistaken for the answer.
	cmd := `stty size | while read r c; do echo "SZ` + tag + `-$r-$c"; done`
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("ask pane %s for its size: %v", tag, err)
	}
	find := func(s tuitest.Screen) (int, int, bool) {
		for _, line := range strings.Split(s.Text(), "\n") {
			if strings.Contains(line, "stty") {
				continue
			}
			if m := paneSize.FindStringSubmatch(line); m != nil && m[1] == tag {
				r, _ := strconv.Atoi(m[2])
				c, _ := strconv.Atoi(m[3])
				return r, c, true
			}
		}
		return 0, 0, false
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := find(s)
		return ok
	}, shellTimeout); err != nil {
		t.Fatalf("pane %s never reported its size: %v\n%s", tag, err, term.Snapshot())
	}
	r, c, _ := find(term.Screen())
	return r, c
}

// TestNewWindowOnNewWorkspaceGetsPaneSize drives the reported bug end to end: on
// a freshly opened workspace, create a window and check that its shell was told
// how big its pane is.
//
// The report was a full-screen pane running cmatrix that painted only the
// top-left corner, because the shell had been left at the small box a new window
// is first placed in. So the assertion is twofold: the new workspace's pane must
// report the same size as an identical pane on the first workspace, and that
// size must actually be most of the terminal rather than a fraction of it. The
// second half is what a placement-box-sized shell fails; the first half is what
// any future divergence between the two paths fails.
func TestNewWindowOnNewWorkspaceGetsPaneSize(t *testing.T) {
	const cols, rows = 130, 55

	term, _ := start(t, startOpts{cols: cols, rows: rows})
	waitBoot(t, term)

	// Tiling on, so a lone window fills its workspace and the expected size is
	// unambiguous.
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("toggle tiling: %v", err)
	}

	newWindow(t, term)
	enterTerminalMode(t, term)
	rows1, cols1 := reportedPaneSize(t, term, "A")
	t.Logf("workspace 1 pane: %dx%d", cols1, rows1)
	leaveTerminalMode(t, term)

	// A workspace with nothing on it: what the dock's "+" opens, and what the
	// report was about.
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "3"); err != nil {
		t.Fatalf("switch to workspace 3: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 0
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 3 is not empty: %v\n%s", err, term.Snapshot())
	}

	newWindow(t, term)
	enterTerminalMode(t, term)
	rows2, cols2 := reportedPaneSize(t, term, "B")
	t.Logf("workspace 3 pane: %dx%d", cols2, rows2)
	leaveTerminalMode(t, term)

	if rows1 != rows2 || cols1 != cols2 {
		t.Fatalf("the new workspace's pane runs at %dx%d but the same pane on workspace 1 runs at %dx%d\n%s",
			cols2, rows2, cols1, rows1, term.Snapshot())
	}

	// A pane that fills the workspace must not be running a shell sized like the
	// small box a new window is first placed in. Stated as a fraction of the
	// terminal rather than an exact number, so the dock and margins can change
	// size without this needing an edit.
	if cols2*4 < cols*3 || rows2*4 < rows*3 {
		t.Fatalf("the full-workspace pane runs a %dx%d shell in a %dx%d terminal: "+
			"a full-screen program would paint only its top-left corner\n%s",
			cols2, rows2, cols, rows, term.Snapshot())
	}
}
