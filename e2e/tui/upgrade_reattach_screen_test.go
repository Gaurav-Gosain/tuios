package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A live upgrade drops every client, and the person attaches again. The pane
// of a program on the alternate screen must come back exactly as it was: the
// daemon carried its screen across the upgrade, so nothing has to make the
// program draw it again.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// stillAltScreenGuest is a program on the alternate screen that draws one
// frame when the file named by its second argument appears, and never draws on
// its own. It draws every row of its window with the row's number, then parks
// the cursor on row 3, as a full-screen program parks it in its input box.
// Every SIGWINCH it gets is appended to the log named by its first argument,
// with the size it reads in the handler.
//
// It does not draw again on a SIGWINCH, so a screen changed under it stays
// changed, which is what a program that repaints only the cells it believes
// changed leaves behind. It is Python and not sh because a shell runs its
// trap only between commands, and two SIGWINCHes 30 ms apart arrive while it
// waits in one: a sh guest recorded neither.
const stillAltScreenGuest = `import fcntl, os, signal, struct, sys, termios, time
log, go = sys.argv[1], sys.argv[2]
def size():
    rows, cols, _, _ = struct.unpack("HHHH", fcntl.ioctl(0, termios.TIOCGWINSZ, b"\0" * 8))
    return rows, cols
def heard(sig, frame):
    with open(log, "a") as f:
        f.write("winch %d %d\n" % size())
signal.signal(signal.SIGWINCH, heard)
sys.stdout.write("\033[?1049h\033[2J\033[H")
sys.stdout.flush()
while True:
    if os.path.exists(go):
        os.remove(go)
        rows, _ = size()
        for r in range(1, rows + 1):
            sys.stdout.write("\033[%d;1HROW%02d" % (r, r))
        sys.stdout.write("\033[3;1H")
        sys.stdout.flush()
        with open(log, "a") as f:
            f.write("drawn\n")
    time.sleep(0.05)
`

// TestUpgradeReattachLeavesAltScreenPaneAlone attaches a client to a session
// with a program on the alternate screen, has it draw a frame, upgrades the
// daemon (which drops the client) and attaches a new client at the same size.
//
// The program must hear no SIGWINCH from the second attach, and the daemon's
// own grid of the pane must still read ROW01 at the top and the last row's
// number at the bottom.
//
// How it could fail:
//   - the new client could resize the pane, to another size or to a smaller
//     one and back, which SIGWINCHes the program and makes the daemon's
//     emulator drop or move rows;
//   - the adopted pane could come up at another size than it had, which the
//     first layout then puts back;
//   - the carried snapshot could restore the rows moved, or lose some.
//
// How it could pass wrongly:
//   - a program that draws again on SIGWINCH would hide a moved screen, so
//     this one never draws on its own;
//   - a frame drawn before the first client settled would count that
//     client's own layout, so the frame is drawn once the first client has
//     laid the pane out, and only SIGWINCHes after it count;
//   - a handler that never ran would record nothing either way. The
//     negative control records the sizes it logged on a build that resized.
func TestUpgradeReattachLeavesAltScreenPaneAlone(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	const sess = "e2e-upgrade-alt"
	dir := t.TempDir()
	logPath, goPath := filepath.Join(dir, "winch.log"), filepath.Join(dir, "draw")
	guest := filepath.Join(dir, "still_alt_screen.py")
	if err := os.WriteFile(guest, []byte(stillAltScreenGuest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	alt := newCommandWindow(t, base, sess, "alt", false, "python3", guest, logPath, goPath)

	attach := func(what string) *tuitest.Terminal {
		t.Helper()
		term := startIn(t, base, startOpts{cols: 120, rows: 30, args: []string{"attach", sess}})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "╰") }, bootTimeout); err != nil {
			t.Fatalf("%s never drew the session: %v\n%s", what, err, term.Snapshot())
		}
		// Every resize the attach makes has reached the pane by then: a held
		// size is written 30 ms after the last one.
		time.Sleep(time.Second)
		return term
	}
	readLog := func() []string {
		t.Helper()
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}

	first := attach("the first client")
	if err := os.WriteFile(goPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Contains(readLog(), "drawn") {
		if time.Now().After(deadline) {
			t.Fatalf("the program never drew its frame: log %q", readLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitHandoffPane(t, base, sess, alt, regexp.MustCompile(`^ROW01`), 10*time.Second)
	frame := trimBlankTail(mustDaemonPane(t, base, sess, alt))
	if len(frame) < 3 || frame[0] != "ROW01" || !strings.HasPrefix(frame[len(frame)-1], "ROW") {
		t.Fatalf("the frame did not fill the pane: %q", frame)
	}
	logBefore := readLog()

	img := helloImage(t, base)
	if outcome, out, err := daemonUpgrade(t, base, second); err != nil {
		t.Fatalf("tuios daemon upgrade failed: %v\n%s\n%v", err, out, outcome)
	}
	awaitNewImage(t, base, img, 30*time.Second)
	// The first client follows the upgrade and attaches again by itself, which
	// is a reattach to the restored session like the second client's.
	if err := first.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "tuios reattached") }, bootTimeout); err != nil {
		t.Fatalf("the first client did not follow the upgrade: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(time.Second)

	upgraded := trimBlankTail(mustDaemonPane(t, base, sess, alt))
	logUpgraded := readLog()

	attach("a client attached after the upgrade")
	logAfter := readLog()
	got := trimBlankTail(mustDaemonPane(t, base, sess, alt))

	var resizes []string
	daemonLog, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log"))
	for _, l := range strings.Split(string(daemonLog), "\n") {
		if strings.Contains(l, " resized ") || strings.Contains(l, "Live upgrade: adopted") {
			resizes = append(resizes, l)
		}
	}
	writeHandoffArtifact(t, map[string]any{
		"daemon_log_resizes": resizes,
		"frame_before":       frame, "frame_upgraded": upgraded, "frame_after": got,
		"winch_before_upgrade": logBefore, "winch_upgraded": logUpgraded, "winch_all": logAfter,
	})
	if len(logAfter) != len(logBefore) {
		t.Errorf("ASSERTION: the program heard a SIGWINCH after the upgrade and the second attach: sizes %q", logAfter[len(logBefore):])
	}
	if strings.Join(got, "\n") != strings.Join(frame, "\n") {
		t.Errorf("ASSERTION: the daemon's grid of the pane changed across the upgrade and the second attach\nbefore:\n%s\nafter:\n%s",
			strings.Join(frame, "\n"), strings.Join(got, "\n"))
	}
}

// trimBlankTail drops trailing spaces from each row and the blank rows at the end.
func trimBlankTail(rows []string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, strings.TrimRight(r, " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// mustDaemonPane is daemonPane, failing the test on an error.
func mustDaemonPane(t *testing.T, base, sess, window string) []string {
	t.Helper()
	rows, err := daemonPane(base, sess, window)
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}
	return rows
}
