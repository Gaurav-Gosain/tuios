package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Opening and creating a session on another machine, driven the way a person
// does it: the CLI flag from a real command line, and a real click on the rail.
//
// The link is the same loopback the other federation tests use. The ssh
// stand-in drops ssh's options and the address and runs the command locally, so
// the host named "build" is this test's own daemon reached over the real
// subprocess transport. Nothing reads the developer's ssh config, and no
// network connection is made.
//
// What would pass a weaker test and fail this one: a --host flag that writes an
// argv nothing runs, or a rail row that records a hit no handler answers. Both
// are proved here by the session actually appearing and the pane actually
// opening.

// TestNewOnAHostCreatesASessionAcrossTheLink is the CLI reachability proof for
// 'tuios new --host'. The far side creates the session, and it comes back in
// both the local listing and the host's own listing.
func TestNewOnAHostCreatesASessionAcrossTheLink(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}
	writeHostsConfig(t, base, tuiosBin)

	term := startIn(t, base, startOpts{args: []string{"new", "fed-open"}, env: env})
	waitBoot(t, term)

	// The command a person types. --detach is what lets this run without a
	// terminal: the far side creates the session and returns.
	out, err := tuiosCLIEnv(t, base, env, "new", "--host", "build", "extra", "--detach")
	if err != nil {
		t.Fatalf("ASSERTION: 'tuios new --host build extra --detach' failed: %v\n%s", err, out)
	}
	t.Logf("tuios new --host build extra --detach:\n%s", out)

	// The session the far side created is in this daemon now, because the link
	// loops back to it. Poll the session listing until it lands.
	if err := waitForLS(t, base, func(out string) bool {
		return strings.Contains(out, "extra")
	}); err != nil {
		t.Fatalf("ASSERTION: the session created with 'new --host' never appeared: %v", err)
	}

	// And it is in the host's own listing, which is the listing that crosses the
	// link.
	out, err = tuiosCLIEnv(t, base, env, "ls", "--host", "build")
	if err != nil {
		t.Fatalf("tuios ls --host build: %v\n%s", err, out)
	}
	if !strings.Contains(out, "extra") {
		t.Errorf("ASSERTION: the session created with 'new --host' is not in the host listing:\n%s", out)
	}
	t.Logf("tuios ls --host build:\n%s", out)
}

// waitForLS polls 'tuios ls' until the output satisfies want.
func waitForLS(t *testing.T, base string, want func(string) bool) error {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "ls")
		if want(out) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("the last 'tuios ls' was:\n%s", out)
}

// waitForHostRow blocks until the rail shows text, then returns the screen.
func railShows(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), want)
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never showed %q: %v\n%s", want, err, term.Snapshot())
	}
}

// TestRailOpensARemoteSession is the on-screen proof for #171: activating a
// foreign session row opens it in a local pane. The rail shows the host group
// and the remote session under it, and a click on that row opens a new pane
// running the remote client.
func TestRailOpensARemoteSession(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}
	writeHostsConfig(t, base, tuiosBin)

	// A detached session for the host to list. Over the loopback it is both a
	// local session and a session under host "build".
	if out, err := tuiosCLIEnv(t, base, env, "new", "remote-target", "--detach"); err != nil {
		t.Fatalf("create the session to open: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "remote-target"}, env: env})
	// Attaching to an existing session shows its shell pane, not the welcome
	// screen, so wait for a pane border rather than waitBoot.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──")
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	toggleSidebarViaPalette(t, term)

	// The host group and its session row: this is the frame the report captures.
	railShows(t, term, "@ build")
	railShows(t, term, "remote-target")
	t.Logf("rail with a host group and a remote session row:\n%s", term.Snapshot())

	// remoteRowAt finds the remote session row: the one under the "@ build"
	// header, which is indented, not the local row of the same name higher up.
	remoteRowAt := func() (col, row int) {
		s := term.Screen()
		_, rows := s.Size()
		hostRow := -1
		for r := 0; r < rows; r++ {
			if strings.Contains(s.Line(r), "@ build") {
				hostRow = r
				break
			}
		}
		if hostRow < 0 {
			return -1, -1
		}
		for r := hostRow + 1; r < rows; r++ {
			if c := strings.Index(s.Line(r), "remote-target"); c >= 0 && c < sidebarBand {
				return c, r
			}
		}
		return -1, -1
	}

	// windowsIn is how many windows the daemon holds for the session, or -1 when
	// it cannot be read. It is the load-insensitive truth: the screen is noisy
	// while the remote client boots in the pane, but the daemon's window set is
	// not.
	windowsIn := func() int {
		wl, err := daemonWindows(base, "remote-target")
		if err != nil {
			return -1
		}
		return len(wl.Windows)
	}
	reached := func(n int) bool {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if windowsIn() >= n {
				return true
			}
			time.Sleep(200 * time.Millisecond)
		}
		return false
	}

	// The session starts with one window, the shell. Let the rail settle so its
	// hit rectangles are registered, then click the remote session row read from
	// the same settled frame.
	if windowsIn() != 1 {
		t.Fatalf("the session did not start with one window: %d", windowsIn())
	}
	time.Sleep(insertGuard)
	col, row := remoteRowAt()
	if row < 0 {
		t.Fatalf("no remote session row under the host header:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)

	// Retry the click once, only if the first opened nothing. A second click on
	// an opened row would open a second pane, so the retry is gated on the
	// window count and never fires once the pane exists.
	if !reached(2) {
		if col, row = remoteRowAt(); row >= 0 {
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
		}
	}

	// The proof: the rail's click added a window to the outer session, running
	// the remote client. If nothing opened, the row recorded a hit no handler
	// answered. The daemon is asked directly, so a noisy screen cannot fail it.
	rects := waitForSettledGeometryIn(t, base, "remote-target", 2)
	if len(rects) != 2 {
		t.Fatalf("ASSERTION: clicking a remote session row opened no pane: window count is %d", len(rects))
	}
	alive(t, term, "after opening a remote session from the rail")
	t.Logf("after opening the remote session, the outer session holds %d windows", len(rects))
}
