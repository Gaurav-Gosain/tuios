package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A reattach while a program has set state no cell shows, and while its
// output stops in the middle of an escape sequence.
//
// A client that attaches is filled from the daemon's snapshot and then from
// the stream after it. Before this, the snapshot did not carry the tab stops,
// the title stack, the colours the guest set with OSC 4, the ANSI modes or
// the sequence the daemon's parser was part way through. A client that
// attached at such a moment looked right and then went wrong at the next byte
// that read the missing state.
//
// Ways this can go wrong, each an assertion below:
//   - the tab stops are lost, so the tabs printed after the reattach land on
//     the default stops;
//   - the OSC 4 palette entry is lost, so the red printed after the reattach
//     is the palette's red and not the guest's;
//   - the title stack is lost, so the pop after the reattach leaves the
//     program's title on the window;
//   - insert mode is lost, so text printed at the start of a row after the
//     reattach overwrites it instead of pushing it along;
//   - the snapshot is taken inside an OSC 8 hyperlink, and the rest of the
//     OSC prints as text;
//   - the snapshot is taken inside a UTF-8 character, and its last byte
//     prints as U+FFFD.
//
// The pane runs a script that sets all of that, then stops twice: once in the
// middle of an OSC 8 and once in the middle of a UTF-8 character, each time
// waiting on a fifo. At each stop the client detaches and a new one attaches,
// and the test then lets the script go on. A witness client stays attached
// the whole time and is never restored from a snapshot, so what it shows is
// what the reattached client must show.
//
// The artifacts are the witness's screen and the reattached client's screen,
// plain and styled, under artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Snapshot state"): each
// cuts one call in ApplyTerminalState (internal/session/session.go).
func TestReattachCarriesSnapshotState(t *testing.T) {
	const session = "e2e-snapstate"
	base := t.TempDir()
	killDaemon(t, base)

	dir := t.TempDir()
	fifo1, fifo2 := filepath.Join(dir, "go1"), filepath.Join(dir, "go2")
	for _, f := range []string{fifo1, fifo2} {
		if err := syscall.Mkfifo(f, 0o600); err != nil {
			t.Fatalf("mkfifo: %v", err)
		}
	}
	script := strings.Join([]string{
		`#!/bin/sh`,
		`printf '\033]2;SHELLTITLE-5521\007\033[22;0t\033]2;PROGTITLE-5521\007'`,
		`printf '\033]4;1;#ff0000\007'`,
		`printf '\033[2J\033[3g\033[1;5H\033H\033[1;14H\033H\033[1;23H\033H\033[1;1H'`,
		`printf 'STOP1 \033]8;;https://exa'`,
		`cat "` + fifo1 + `" >/dev/null`,
		`printf 'mple.test/\033\\LINKTEXT\033]8;;\033\\\r\n'`,
		`printf '\tTA\tTB\tTC\r\n'`,
		`printf '\033[31mREDTEXT\033[m\r\n'`,
		`printf '\033[4hSTOP2 \342\234'`,
		`cat "` + fifo2 + `" >/dev/null`,
		`printf '\263 END8\r\nxyzrow\rINS\033[4l\r\n'`,
		`printf '\033[23;0t'`,
		`printf 'DONE-%s\r\n' 5521`,
		`exec cat`,
	}, "\n") + "\n"
	scriptPath := filepath.Join(dir, "state.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write the script: %v", err)
	}

	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	attach := func(what string) *tuitest.Terminal {
		t.Helper()
		term := startIn(t, base, startOpts{cols: 100, rows: 30, args: []string{"attach", session}})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
			t.Fatalf("%s never attached: %v\n%s", what, err, term.Snapshot())
		}
		return term
	}
	detach := func(term *tuitest.Terminal, what string) {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
			t.Fatalf("detach %s: %v", what, err)
		}
		waitExit(t, term, "after detaching "+what)
	}
	release := func(fifo string) {
		t.Helper()
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("open %s: %v", fifo, err)
		}
		_, _ = f.WriteString("go\n")
		_ = f.Close()
	}

	witness := attach("the witness")
	first := attach("the first client")
	windowManagementMode(t, first)
	enterTerminalMode(t, first)
	if err := first.SendKeys("sh "+scriptPath, tuitest.Enter); err != nil {
		t.Fatalf("start the script: %v", err)
	}
	if err := first.WaitForText("STOP1", shellTimeout); err != nil {
		t.Fatalf("the script never reached its first stop: %v\n%s", err, first.Snapshot())
	}
	if err := witness.WaitForText("PROGTITLE-5521", uiTimeout); err != nil {
		t.Fatalf("the program's title is not drawn, so the title stack cannot be checked: %v\n%s", err, witness.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)

	detach(first, "the first client")
	second := attach("the second client")
	if err := second.WaitForText("STOP1", uiTimeout); err != nil {
		t.Fatalf("the second client never showed the pane: %v\n%s", err, second.Snapshot())
	}
	release(fifo1)
	if err := witness.WaitForText("STOP2", shellTimeout); err != nil {
		t.Fatalf("the script never reached its second stop: %v\n%s", err, witness.Snapshot())
	}
	if err := second.WaitForText("STOP2", uiTimeout); err != nil {
		t.Fatalf("the second client never showed the second stop: %v\n%s", err, second.Snapshot())
	}
	for _, term := range []*tuitest.Terminal{witness, second} {
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("screen never settled: %v", err)
		}
	}
	art := artifactDir(t)
	saveArtifact(t, witness, art, "witness-stop2")
	saveArtifact(t, second, art, "reattached-at-stop1")
	checkFirstStop(t, "the witness", witness.Screen())
	checkFirstStop(t, "the client reattached at the first stop", second.Screen())
	for _, marker := range []string{"LINKTEXT", "TA", "REDTEXT"} {
		diffPainted(t, "the row with "+marker, paintedLine(t, witness, marker), paintedLine(t, second, marker))
	}

	detach(second, "the second client")
	third := attach("the third client")
	if err := third.WaitForText("STOP2", uiTimeout); err != nil {
		t.Fatalf("the third client never showed the pane: %v\n%s", err, third.Snapshot())
	}
	release(fifo2)
	for _, term := range []*tuitest.Terminal{witness, third} {
		if err := term.WaitForText("DONE-5521", shellTimeout); err != nil {
			t.Fatalf("the script never finished: %v\n%s", err, term.Snapshot())
		}
	}
	if err := witness.WaitForText("SHELLTITLE-5521", uiTimeout); err != nil {
		t.Fatalf("the witness never showed the title the pop put back: %v\n%s", err, witness.Snapshot())
	}
	_ = third.WaitForText("SHELLTITLE-5521", uiTimeout)
	for _, term := range []*tuitest.Terminal{witness, third} {
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("screen never settled: %v", err)
		}
	}
	saveArtifact(t, witness, art, "witness")
	saveArtifact(t, third, art, "reattached-at-stop2")

	checkSecondStop(t, "the witness", witness.Screen())
	checkSecondStop(t, "the client reattached at the second stop", third.Screen())
	for _, marker := range []string{"END8", "INS"} {
		diffPainted(t, "the row with "+marker, paintedLine(t, witness, marker), paintedLine(t, third, marker))
	}
}

func screenRow(t *testing.T, who string, s tuitest.Screen, marker string) (string, int) {
	t.Helper()
	_, rows := s.Size()
	for y := range rows {
		if line := s.Line(y); strings.Contains(line, marker) {
			return line, y
		}
	}
	t.Errorf("%s: no row holds %q\n%s", who, marker, s.Text())
	return "", -1
}

func checkFirstStop(t *testing.T, who string, s tuitest.Screen) {
	t.Helper()
	text := s.Text()
	row := func(marker string) (string, int) {
		t.Helper()
		return screenRow(t, who, s, marker)
	}

	if strings.Contains(text, "mple.test") {
		t.Errorf("%s: the end of the OSC 8 the snapshot cut printed as text\n%s", who, text)
	}
	if line, _ := row("LINKTEXT"); line != "" && !strings.Contains(line, "STOP1 LINKTEXT") {
		t.Errorf("%s: the link text is not where the OSC 8 put it: %q", who, line)
	}

	if line, _ := row("TA"); line != "" {
		a, b, c := strings.Index(line, "TA"), strings.Index(line, "TB"), strings.Index(line, "TC")
		if b-a != 9 || c-b != 9 {
			t.Errorf("%s: the tabs landed %d and %d columns apart, want 9 and 9 (stops at 4, 13, 22): %q", who, b-a, c-b, line)
		}
	}

	if _, y := row("REDTEXT"); y >= 0 {
		rgb := tuitest.Color{Kind: tuitest.ColorRGB, R: 0xff}
		cube := tuitest.Color{Kind: tuitest.ColorIndexed, Index: 196}
		cols, _ := s.Size()
		found := false
		for col := 0; col+2 < cols; col++ {
			if s.Cell(col, y).Content != "R" || s.Cell(col+1, y).Content != "E" || s.Cell(col+2, y).Content != "D" {
				continue
			}
			if fg := s.Cell(col, y).Fg; fg != rgb && fg != cube {
				t.Errorf("%s: REDTEXT is painted %+v, want the guest's pure red, %+v or %+v", who, fg, rgb, cube)
			}
			found = true
			break
		}
		if !found {
			t.Errorf("%s: REDTEXT is in the row text but not in its cells", who)
		}
	}
}

func checkSecondStop(t *testing.T, who string, s tuitest.Screen) {
	t.Helper()
	text := s.Text()
	row := func(marker string) (string, int) {
		t.Helper()
		return screenRow(t, who, s, marker)
	}
	if line, _ := row("END8"); line != "" && !strings.Contains(line, "STOP2 ✳ END8") {
		t.Errorf("%s: the UTF-8 character the snapshot cut did not come out as ✳: %q", who, line)
	}
	if strings.Contains(text, "\uFFFD") {
		t.Errorf("%s: a replacement character is on screen\n%s", who, text)
	}

	if line, _ := row("INS"); line != "" && !strings.Contains(line, "INSxyzrow") {
		t.Errorf("%s: insert mode did not push the row along: %q", who, line)
	}

	if !strings.Contains(text, "SHELLTITLE-5521") || strings.Contains(text, "PROGTITLE-5521") {
		t.Errorf("%s: after the pop the window should show SHELLTITLE-5521 and not PROGTITLE-5521\n%s", who, text)
	}
}
