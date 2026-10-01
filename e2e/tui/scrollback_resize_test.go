package tuie2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// History across resizes, through the daemon. Each test puts content where a
// resize can reach it, resizes the client's terminal (which is how a pane is
// resized for real: the client retiles and the daemon resizes the PTY), and
// then reads the daemon's own history with capture-pane. The daemon is the
// authority every attach rehydrates from, so content it loses no client can
// get back.

// scrollbackResizeSession creates a detached session and attaches one client
// to it at cols x rows.
func scrollbackResizeSession(t *testing.T, name string, cols, rows int) (*tuitest.Terminal, string, daemonWindow) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create session %s: %v: %s", name, err, out)
	}
	term := attachIn(t, base, name, startOpts{cols: cols, rows: rows})
	// Tiled, so the pane follows the client's terminal size. A floating
	// window keeps its size when the terminal changes.
	enableTiling(t, term)
	return term, base, firstWindow(t, base, name)
}

// sbGridSize is the daemon emulator's grid size, read through a text
// screenshot, which reports it without printing anything into the pane.
func sbGridSize(t *testing.T, base, session, window string) (cols, rows int) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "size.txt")
	got, err := daemonJSON[struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}](base, "screenshot", "-s", session, "-w", window, "--format", "txt",
		"--frame", "none", "--no-copy", "--out", out)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	return got.Cols, got.Rows
}

// waitPaneWidth waits until the daemon's emulator is a size for which pred
// holds.
func waitPaneWidth(t *testing.T, base, session string, pred func(w, h int) bool, what string) {
	t.Helper()
	win := firstWindow(t, base, session).ID
	deadline := time.Now().Add(uiTimeout)
	for {
		c, r := sbGridSize(t, base, session, win)
		if pred(c, r) {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%s: the pane stayed %dx%d", what, c, r)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// waitDaemonText waits for want in the daemon's history plus screen.
func waitDaemonText(t *testing.T, base, session, window, want string) {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		hist, err := daemonScrollback(base, session, window, 5000)
		if err == nil && strings.Contains(strings.Join(hist, "\n"), want) {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the pane never printed %q", want)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

const longBody = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ-abcdefghijklmnopqrst"

// TestScrollbackResizeShorterKeepsRowsBelowCursor: a program that draws below
// the cursor and then moves the cursor up (a progress display, less -X, a
// status line) loses those rows when the pane gets shorter. tmux and ghostty
// push rows off the top into history instead.
func TestScrollbackResizeShorterKeepsRowsBelowCursor(t *testing.T) {
	term, base, w := scrollbackResizeSession(t, "sb-short", 120, 40)
	// Clear, draw a marker on row 30, and leave the cursor on row 3 by
	// sleeping there: the shell prompt would otherwise move the cursor down.
	emit := "\x1b[2J\x1b[HTOPROW\x1b[30;1HBELOWCURSOR\x1b[3;1H"
	cmd := strings.TrimSuffix(paneEmitCmd(emit), "\n") + "; sleep 30\n"
	if err := paneSend(base, "sb-short", w.ID, cmd); err != nil {
		t.Fatal(err)
	}
	waitDaemonText(t, base, "sb-short", w.ID, "BELOWCURSOR")
	_, tall := sbGridSize(t, base, "sb-short", w.ID)

	if err := term.Resize(120, 15); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, "sb-short", func(_, h int) bool { return h < 15 }, "shorten")
	time.Sleep(500 * time.Millisecond)
	if err := term.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, "sb-short", func(_, h int) bool { return h == tall }, "grow")
	time.Sleep(500 * time.Millisecond)

	hist, err := daemonScrollback(base, "sb-short", w.ID, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(hist, "\n"), "BELOWCURSOR") {
		t.Errorf("a row below the cursor is gone from history and screen after the pane got shorter and taller again:\n%s",
			lastLines(hist, 20))
	}
}

// TestScrollbackResizeUnderAltScreenKeepsMainRows: the pane gets shorter while
// a full-screen program is on the alternate screen. When it exits, every line
// the shell printed must still be somewhere: on the screen or in history.
func TestScrollbackResizeUnderAltScreenKeepsMainRows(t *testing.T) {
	term, base, w := scrollbackResizeSession(t, "sb-alt", 120, 40)
	tag := w.tag()
	if err := paneSend(base, "sb-alt", w.ID, "clear; "+paneWitnessCmd(tag, 1, 30)); err != nil {
		t.Fatal(err)
	}
	waitDaemonText(t, base, "sb-alt", w.ID, "MK"+tag+"-30")
	_, tall := sbGridSize(t, base, "sb-alt", w.ID)

	// Enter the alternate screen and stay there while the pane is resized.
	enter := strings.TrimSuffix(paneAltCmd(tag, true), "\n") + "; sleep 3; " + paneAltCmd(tag, false)
	if err := paneSend(base, "sb-alt", w.ID, enter); err != nil {
		t.Fatal(err)
	}
	waitDaemonText(t, base, "sb-alt", w.ID, "ALT"+tag)
	if err := term.Resize(120, 15); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, "sb-alt", func(_, h int) bool { return h < 15 }, "shorten under alt")
	// The program leaves the alternate screen by itself.
	time.Sleep(4 * time.Second)
	if err := term.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, "sb-alt", func(_, h int) bool { return h == tall }, "grow")
	time.Sleep(500 * time.Millisecond)

	hist, err := daemonScrollback(base, "sb-alt", w.ID, 5000)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(hist, "\n")
	var missing []string
	for i := 1; i <= 30; i++ {
		want := "MK" + tag + "-" + itoa(i)
		if !containsLine(hist, want) {
			missing = append(missing, itoa(i))
		}
	}
	if len(missing) > 0 {
		t.Errorf("after a resize under the alternate screen, witness lines %v are gone from the daemon:\n%s",
			missing, lastLines(strings.Split(joined, "\n"), 40))
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

func lastLines(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// TestScrollbackResizeRestartKeepsWideHistory: history saved while the pane
// is narrow keeps lines wider than the pane, because history rows keep the
// width they were written at. The restore puts the newest saved rows back on
// the screen at the saved width, so any of those that is wider than the pane
// was at the save must still come back whole once the pane is wide again.
func TestScrollbackResizeRestartKeepsWideHistory(t *testing.T) {
	const session = "sb-restart"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	first := attachIn(t, base, session, startOpts{cols: 140, rows: 30})
	enableTiling(t, first)
	w := firstWindow(t, base, session)
	// 80 lines of 75 columns: the first ones scroll into history at the wide
	// width and keep it.
	const n = 80
	if err := paneSend(base, session, w.ID,
		"clear; for i in $(seq 1 "+itoa(n)+"); do printf 'W%03d-%s-END\\n' $i "+longBody[:60]+"; done\n"); err != nil {
		t.Fatal(err)
	}
	waitDaemonText(t, base, session, w.ID, "W080-"+longBody[:60]+"-END")
	// Erase the screen (no scrollback erase), so the screen holds only the
	// prompt and the restore has to put saved history rows back on it.
	if err := paneSend(base, session, w.ID, paneEmitCmd("\x1b[2J\x1b[H")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	whole := func() map[string]bool {
		hist, err := daemonScrollback(base, session, w.ID, 5000)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, l := range hist {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "W") && strings.HasSuffix(l, "-"+longBody[:60]+"-END") {
				got[l[:4]] = true
			}
		}
		return got
	}
	if err := first.Resize(50, 30); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, session, func(c, _ int) bool { return c < 60 }, "narrow")
	time.Sleep(500 * time.Millisecond)
	before := whole()
	if len(before) == 0 {
		// A reflowing backend (ghostty) rewraps history to the pane, so no
		// row is wider than the pane and there is nothing to cut.
		t.Skip("no history row is wider than the narrow pane: the backend reflowed it")
	}

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	waitExit(t, first, "after kill-server")
	if out, err := tuiosCLI(t, base, "new", session+"-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v\n%s", err, out)
	}
	if info := waitForSessionInfo(t, base, session); !info.Restored {
		t.Fatal("the session is not marked restored")
	}
	second := attachIn(t, base, session, startOpts{cols: 140, rows: 30})
	_ = second
	w = firstWindow(t, base, session)
	waitPaneWidth(t, base, session, func(c, _ int) bool { return c > 100 }, "wide after restart")
	time.Sleep(500 * time.Millisecond)
	after := whole()

	var lost []string
	for k := range before {
		if !after[k] {
			lost = append(lost, k)
		}
	}
	t.Logf("whole lines while narrow before the restart: %d; after the restart at full width: %d", len(before), len(after))
	if len(lost) > 0 {
		t.Errorf("%d lines were whole in the daemon's history before the restart and are cut after it: %v",
			len(lost), lost)
	}
}

// TestScrollbackResizeCaptureCountsMatch: after the pane gets shorter and
// taller again, capture-pane with history gives every witness line once, in
// order, and history_rows plus the screen bounds the rows it returns.
func TestScrollbackResizeCaptureCountsMatch(t *testing.T) {
	const session = "sb-capture"
	term, base, w := scrollbackResizeSession(t, session, 140, 30)
	tag := w.tag()
	const n = 100
	if err := paneSend(base, session, w.ID, "clear; "+paneWitnessCmd(tag, 1, n)); err != nil {
		t.Fatal(err)
	}
	waitDaemonText(t, base, session, w.ID, "MK"+tag+"-"+itoa(n))
	type capture struct {
		Content     string `json:"content"`
		HistoryRows int    `json:"history_rows"`
	}
	check := func(stage string) {
		t.Helper()
		got, err := daemonJSON[capture](base, "capture-pane", "-s", session, "-w", w.ID, "--scrollback")
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(got.Content, "\n"), "\n")
		_, rows := sbGridSize(t, base, session, w.ID)
		seen := map[int]int{}
		for _, wt := range witnessesIn(lines) {
			if wt.tag == tag {
				seen[wt.seq]++
			}
		}
		var missing, dup []int
		for i := 1; i <= n; i++ {
			switch seen[i] {
			case 0:
				missing = append(missing, i)
			case 1:
			default:
				dup = append(dup, i)
			}
		}
		t.Logf("%s: history_rows=%d rows=%d capture lines=%d", stage, got.HistoryRows, rows, len(lines))
		if len(missing) > 0 || len(dup) > 0 {
			t.Errorf("%s: missing %v, duplicated %v", stage, missing, dup)
		}
		if a, b, found := spliceIn(lines); found {
			t.Errorf("%s: history goes %d straight to %d", stage, a.seq, b.seq)
		}
		// The capture drops blank rows at the bottom of the screen, so it
		// can be shorter than history plus screen, never longer.
		if len(lines) > got.HistoryRows+rows {
			t.Errorf("%s: history_rows %d plus %d screen rows is less than the %d rows the capture returned",
				stage, got.HistoryRows, rows, len(lines))
		}
	}
	check("start")
	for i, size := range [][2]int{{140, 12}, {60, 12}, {140, 30}, {90, 20}, {140, 30}} {
		if err := term.Resize(size[0], size[1]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(700 * time.Millisecond)
		check("after resize " + itoa(i+1) + " to " + itoa(size[0]) + "x" + itoa(size[1]))
	}
}
