package tuie2e

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestHostSwitchBackKeepsTheLocalSessionsWindows is issue #1837's repro: a
// client switches to a session on a linked host and back, again and again,
// while the host is busy. The host's connection has a state sync queued when
// the client leaves it, and a busy host numbers its syncs far ahead of this
// machine's. If the client applies that sync after it is back, its next push
// hands the local session the host's window list, the local daemon saves it,
// and the session's own panes run on outside it.
//
// The rows: home's window ids are the ones it started with after every round;
// the local daemon never had to refuse a list naming none of home's windows,
// so the client never sent one; and, in the same fixture, far keeps its
// windows and the client shows home's panes at the end.
func TestHostSwitchBackKeepsTheLocalSessionsWindows(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, remote, "new-window", "-s", "far", "farwin2", "--no-focus"); err != nil {
		t.Fatalf("open a second window in far: %v\n%s", err, out)
	}
	farBefore := windowIDsOf(t, remote, "far")
	if len(farBefore) != 2 {
		t.Fatalf("far holds %d windows, want 2: %v", len(farBefore), farBefore)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	for _, name := range []string{"side-a", "side-b"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "home", name, "--no-focus"); err != nil {
			t.Fatalf("open %s in home: %v\n%s", name, err, out)
		}
	}
	homeBefore := windowIDsOf(t, base, "home")
	if len(homeBefore) != 2 {
		t.Fatalf("home holds %d windows, want 2: %v", len(homeBefore), homeBefore)
	}
	toggleSidebarViaPalette(t, term)
	for _, want := range []string{hostOpen + " local", "home", hostOpen + " build", "far"} {
		railShows(t, term, want)
	}
	waitRailCurrent(t, term, "home")
	// A text home's panes print, read later in a pane. Every pane prints it,
	// since the windows overlap and the one in front shows it whole.
	const homeText = "HOME-42"
	for _, id := range homeBefore {
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "home", "-w", id, "--raw", "echo HOME-$((6*7))"); err != nil {
			t.Fatalf("type into home's pane: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "home", "-w", id, "Enter"); err != nil {
			t.Fatalf("press Enter in home's pane: %v\n%s", err, out)
		}
	}
	paneShows(t, term, homeText, shellTimeout)

	busy := keepSessionBusy(t, remote, "far")

	const rounds = 8
	for round := 1; round <= rounds; round++ {
		clickRailSessionUnder(t, term, "build", "far")
		if err := term.WaitFor(func(s tuitest.Screen) bool { return railMarksUnder(s, "build", "far") }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: round %d: the rail never marked far under build as the attached session: %v\n%s", round, err, term.Snapshot())
		}
		clickRailSessionUnder(t, term, "local", "home")
		waitBackOnLocal(t, term, base, homeBefore, round)
		if got := windowIDsOf(t, base, "home"); !slices.Equal(got, homeBefore) {
			t.Fatalf("ASSERTION: after round %d of %d home lists the windows %v, it started with %v (far's are %v)\n%s",
				round, rounds, got, homeBefore, farBefore, term.Snapshot())
		}
	}
	busy()

	logPath := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	log, err := os.ReadFile(logPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read the local daemon's log: %v", err)
	}
	for line := range strings.SplitSeq(string(log), "\n") {
		if strings.Contains(line, "it names none of the session's windows") {
			t.Errorf("ASSERTION: the client pushed a window list naming none of home's windows: %s", line)
		}
	}

	if got := windowIDsOf(t, remote, "far"); !slices.Equal(got, farBefore) {
		t.Errorf("ASSERTION: far lists the windows %v after the rounds, it started with %v", got, farBefore)
	}
	paneShows(t, term, homeText, uiTimeout)
	t.Logf("after %d rounds:\n%s", rounds, term.Snapshot())
	saveFrame(t, term, "host-switch-back")
}

// windowIDsOf is the session's window ids as the daemon at base lists them,
// sorted. It waits until list-windows answers.
func windowIDsOf(t *testing.T, base, session string) []string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		wl, err := daemonWindows(base, session)
		if err == nil {
			ids := make([]string, 0, len(wl.Windows))
			for _, w := range wl.Windows {
				ids = append(ids, w.ID)
			}
			slices.Sort(ids)
			return ids
		}
		if time.Now().After(deadline) {
			t.Fatalf("list-windows -s %s never answered: %v", session, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitBackOnLocal blocks until the rail marks a session under this machine
// as the attached one, or until the daemon at base lists home with windows
// other than want. A client that took the host's state back with it marks
// the host's session name under this machine, not home, and what that does
// to home is row (a)'s to report, not a wait's.
func waitBackOnLocal(t *testing.T, term *tuitest.Terminal, base string, want []string, round int) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		if railMarksALocalRow(term.Screen()) {
			return
		}
		if got := windowIDsOf(t, base, "home"); !slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: round %d: the rail never marked a session under local as the attached one, and home still lists %v:\n%s", round, want, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// railMarksUnder reports whether the rail marks name, in host's group, as
// the attached session.
func railMarksUnder(s tuitest.Screen, host, name string) bool {
	r := railRowUnder(s, host, name)
	return r >= 0 && strings.HasPrefix(s.Line(r), "▎")
}

// railMarksALocalRow reports whether the rail marks a row of this machine's
// group as the attached session.
func railMarksALocalRow(s tuitest.Screen) bool {
	local, build := railRowOf(s, hostOpen+" local"), railRowOf(s, hostOpen+" build")
	if local < 0 || build < local {
		return false
	}
	for r := local + 1; r < build; r++ {
		if strings.HasPrefix(s.Line(r), "▎") {
			return true
		}
	}
	return false
}

// paneShows blocks until text is on screen right of the rail, in a pane.
func paneShows(t *testing.T, term *tuitest.Terminal, text string, within time.Duration) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		for r := range rows {
			line := []rune(s.Line(r))
			if len(line) > railWidth && strings.Contains(string(line[railWidth:]), text) {
				return true
			}
		}
		return false
	}, within); err != nil {
		t.Fatalf("ASSERTION: no pane shows %q: %v\n%s", text, err, term.Snapshot())
	}
}

// keepSessionBusy runs four loops of set-agent-state against the session's
// first two windows on the daemon at base, so the daemon sends its clients a
// stream of state syncs. The returned func stops the loops and waits for them;
// it runs at cleanup too.
func keepSessionBusy(t *testing.T, base, session string) func() {
	t.Helper()
	dir := workDirIn(t, base)
	env := append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, failed int
	var firstErr string
	for g := range 4 {
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				state := "working"
				if i%2 == 1 {
					state = "idle"
				}
				cmd := exec.Command(tuiosBin, "set-agent-state", state, "--harness", "codex", "-s", session, "-w", strconv.Itoa(g%2))
				cmd.Dir, cmd.Env = dir, env
				out, err := cmd.CombinedOutput()
				mu.Lock()
				if err != nil {
					failed++
					if firstErr == "" {
						firstErr = err.Error() + ": " + strings.TrimSpace(string(out))
					}
				} else {
					ok++
				}
				mu.Unlock()
			}
		})
	}
	var once sync.Once
	done := func() {
		once.Do(func() {
			close(stop)
			wg.Wait()
			t.Logf("set-agent-state on %s: %d answered, %d failed %s", session, ok, failed, firstErr)
			if ok == 0 {
				t.Errorf("no set-agent-state on %s succeeded, so the daemon was never busy: %s", session, firstErr)
			}
		})
	}
	t.Cleanup(done)
	return done
}
