package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestComingHomeFromAHostAsksForNoRetry: a click on this machine's session
// while the client is on a linked machine brings it home, and asks this
// machine's daemon to dial no link for it. This machine has no link, so a
// retry-host for it is refused as unknown_host and leaves a refusal line in
// the daemon's log on every way home. The way out to build must still work,
// and the session at home must be the one the rail marks.
//
// How this could pass wrongly, written down first:
//   - The log read could be the wrong file, or one nothing reaches. A
//     refusal made on purpose after the click home (ls --host on a machine
//     nobody configured) must reach the same file first, so the file is the
//     daemon's and refusals land in it.
//   - The retry could be sent late and land after the check. It is sent at
//     the click; the check starts only after the switch home has landed and a
//     later refusal from a new process has reached the log, then holds for a
//     second. That is a margin of time, not an order.
//   - The click could never leave home, so there is no way home to look at.
//     The rail must mark far-shell before the click back.
//
// Negative controls: NEGATIVE_CONTROLS.md, "Coming home from a host asks for
// no retry".
func TestComingHomeFromAHostAsksForNoRetry(t *testing.T) {
	requireReal(t, "another tuios daemon on its own isolation root, reached through the ssh stand-in")
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	names := []string{hostOpen + " local", "home", hostOpen + " build", "far-shell"}
	for _, want := range names {
		railShows(t, term, want)
	}
	waitRailCurrent(t, term, "home")
	rows := railRows(term.Screen(), names...)

	// This machine's daemon log, and its lines that hold every one of subs.
	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	logLinesWith := func(subs ...string) []string {
		data, _ := os.ReadFile(daemonLog) //nolint:gosec // the test's own daemon log
		var found []string
		for line := range strings.SplitSeq(string(data), "\n") {
			all := true
			for _, s := range subs {
				all = all && strings.Contains(line, s)
			}
			if all {
				found = append(found, line)
			}
		}
		return found
	}

	// The way out works.
	mouseClick(t, term, 3, rows["far-shell"], tuitest.MouseLeft, 0)
	waitRailCurrent(t, term, "far-shell")

	// And home, through this machine's row on the rail.
	mouseClick(t, term, 3, rows["home"], tuitest.MouseLeft, 0)
	waitRailCurrent(t, term, "home")
	alive(t, term, "after coming home from build")

	// A refusal made on purpose, after the click home.
	out, err := tuiosCLI(t, base, "ls", "--host", "no-such-box")
	t.Logf("ls --host no-such-box (a refusal on purpose): %v\n%s", err, out)
	waitUntil(t, "this machine's daemon log holds the refusal of ls --host no-such-box", uiTimeout, func() (bool, string) {
		if len(logLinesWith("list-host-sessions refused", "unknown_host")) > 0 {
			return true, ""
		}
		data, _ := os.ReadFile(daemonLog) //nolint:gosec // the test's own daemon log
		return false, daemonLog + ":\n" + string(data)
	})

	stayTrue(t, "this machine's daemon was asked for no retry of a link on the way home", time.Second, func() (bool, string) {
		found := logLinesWith("Verb retry-host refused")
		return len(found) == 0, strings.Join(found, "\n")
	})

	saveFrame(t, term, "home-from-host")
	if !railCurrentIs(term.Screen(), "home") {
		t.Fatalf("ASSERTION: the rail no longer marks home as the attached session:\n%s", term.Snapshot())
	}
	alive(t, term, "after the checks on the daemon log")
}
