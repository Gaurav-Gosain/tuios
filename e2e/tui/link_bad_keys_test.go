package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Issue #2303: while a link to a host stalled and dropped again and again,
// the client stopped taking keys, even for a pane on this machine.
//
// The ssh stand-in reaches a second real daemon. The test stops and kills the
// link's far side in a loop and types into a local pane all the while. Every
// command must come back within keyLatencyBudget. When one does not, the test
// sends SIGQUIT to the client and to the daemon and logs both goroutine dumps,
// so the failure says what held the keys.
//
// The case is a session with one window on build and one on this machine. A
// resize of the window on build used to wait for its resize-pane answer in the
// loop that reads the client's messages, for up to ten seconds while the link
// stalled, and every key behind it waited too.
//
// Negative control: with remotePane.SetWinsize calling resize-pane itself
// again, command 3, the first after a resize, does not come back in time, and
// the daemon's dump shows handleResize waiting in federation.(*caller).call.
// See NEGATIVE_CONTROLS.md.

// keyLatencyBudget is the longest a typed command may take to come back from
// a local shell while a link misbehaves. A link waits seconds before it gives
// up, so a key that waits on a link takes longer than this.
const keyLatencyBudget = 3 * time.Second

func TestLocalKeysDoNotWaitOnABadLink(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	dump := filepath.Join(t.TempDir(), "client-stderr.log")
	env := []string{"TUIOS_SSH=" + ssh, "GOTRACEBACK=all"}

	if out, err := tuiosCLI(t, remote, "new", "far-busy", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	// The client's stderr goes to a file, so a goroutine dump is kept whole
	// and does not paint over the screen the assertions read.
	term := startIn(t, base, startOpts{
		args: []string{"new", "home"},
		env:  env,
		wrap: []string{"/bin/sh", "-c", "exec \"$@\" 2>>" + posixQuote(dump), "sh"},
	})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	// The rail lists the far session, so the link is up and the client asks
	// the daemon about the host.
	railShows(t, term, "far-busy")
	// A window of this session that runs on build. The daemon resizes it over
	// the link each time the layout changes.
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	if err := term.WaitForText("build:faraway", uiTimeout); err != nil {
		t.Fatalf("the far window never appeared: %v\n%s", err, term.Snapshot())
	}
	newWindow(t, term)
	enableTiling(t, term)
	enterTerminalMode(t, term)
	// The keys go to the window on this machine: its shell has this
	// machine's runtime directory.
	runInShell(t, term, "echo LOCAL-\"\"$XDG_RUNTIME_DIR", "LOCAL-"+xdgDir(base, "XDG_RUNTIME_DIR"), shellTimeout)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { shakeTheLink(t, stop) })
	stopShaking := sync.OnceFunc(func() { close(stop); wg.Wait() })
	t.Cleanup(stopShaking)

	// Two rounds of shakeTheLink: a stall and a drop each.
	end := time.Now().Add(scaled(20 * time.Second))
	worst := time.Duration(0)
	for n := 1; time.Now().Before(end); n++ {
		marker := fmt.Sprintf("KEY%04d", n)
		// Every few commands the terminal changes size, as when a person
		// resizes the window or a dialog opens. The layout changes, and
		// every pane, the far one too, gets its new size.
		if n%3 == 0 {
			cols := 120 - 2*(n/3%2)
			if err := term.Resize(cols, 40); err != nil {
				t.Fatalf("resize the terminal: %v", err)
			}
		}
		began := time.Now()
		if err := term.SendKeys("echo "+splitMarker(marker), tuitest.Enter); err != nil {
			t.Fatalf("type command %d: %v", n, err)
		}
		if err := term.WaitForText(marker, scaled(keyLatencyBudget)); err != nil {
			stuck := time.Since(began)
			dumps := goroutineDumps(t, base, term.Pid(), dump)
			stopShaking()
			t.Fatalf("ASSERTION: command %d typed into a pane on this machine did not come back in %v while the link to build was bad. Keys must not wait on a link.\n%s\n%s",
				n, stuck.Round(time.Millisecond), term.Snapshot(), dumps)
		}
		worst = max(worst, time.Since(began))
	}
	stopShaking()
	t.Logf("the slowest command came back in %v", worst.Round(time.Millisecond))
	out, _ := tuiosCLI(t, base, "hosts")
	t.Logf("tuios hosts after the test:\n%s", out)
	alive(t, term, "after typing through a bad link")
}

// shakeTheLink makes the link to build bad until stop closes: it stalls the
// far side for a few seconds, lets it go, then kills it so the link drops and
// is dialed again. That is the pattern the report's log shows.
func shakeTheLink(t *testing.T, stop <-chan struct{}) {
	wait := func(d time.Duration) bool {
		select {
		case <-stop:
			return false
		case <-time.After(d):
			return true
		}
	}
	for round := 0; ; round++ {
		pids := testProxies(t)
		if round%2 == 0 {
			for _, pid := range pids {
				_ = syscall.Kill(pid, syscall.SIGSTOP)
			}
			ok := wait(6 * time.Second)
			for _, pid := range pids {
				_ = syscall.Kill(pid, syscall.SIGCONT)
			}
			if !ok {
				return
			}
		} else {
			for _, pid := range pids {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		if !wait(2 * time.Second) {
			return
		}
	}
}

// goroutineDumps sends SIGQUIT to the client and to the daemon at base and
// returns what each wrote. The Go runtime writes every goroutine to stderr on
// SIGQUIT and exits. The daemon's stderr is its log file.
func goroutineDumps(t *testing.T, base string, client int, clientStderr string) string {
	t.Helper()
	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	var daemonPid int
	_ = filepath.WalkDir(xdgDir(base, "XDG_RUNTIME_DIR"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".pid") && daemonPid == 0 {
			if b, rerr := os.ReadFile(p); rerr == nil {
				_, _ = fmt.Sscan(strings.TrimSpace(string(b)), &daemonPid)
			}
		}
		return nil
	})
	for _, pid := range []int{client, daemonPid} {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGQUIT)
		}
	}
	read := func(p, marker string) string {
		var s string
		waitUntil(t, "a goroutine dump in "+p, 10*time.Second, func() (bool, string) {
			b, _ := os.ReadFile(p)
			s = string(b)
			return strings.Contains(s, marker), fmt.Sprintf("%d bytes", len(b))
		})
		if i := strings.Index(s, "SIGQUIT"); i >= 0 {
			s = s[i:]
		}
		return s
	}
	return "client goroutines:\n" + read(clientStderr, "goroutine ") +
		"\ndaemon goroutines (pid " + fmt.Sprint(daemonPid) + "):\n" + read(daemonLog, "SIGQUIT")
}

// TestKeysLostWithALinkAreDroppedAndThePersonIsTold is what happens to input
// that waits for a pane on build when the link drops under it. It must not be
// sent later on the new link: keys typed into a shell long before can run a
// command the person no longer means. The person is told once, in the dock,
// and the resize verb that waited on the stalled link says it failed.
//
// The input waits behind a size: the link is stopped, the resize verb puts a
// size on it that is not answered, and send-text queues a command behind it.
// Then the link is killed.
//
// Negative controls: send the waiting input on the new connection, and
// LOSTKEYS-7 runs on build after the link is back. Remove the notice, and the
// dock never says it. Answer "resized" without waiting, and the resize verb
// passes. See NEGATIVE_CONTROLS.md.
func TestKeysLostWithALinkAreDroppedAndThePersonIsTold(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base, _, env := endedRig(t)
	openFarShell(t, base, env, "far-pane")
	term := startIn(t, base, startOpts{args: []string{"attach", "near"}, env: env, cols: 140, rows: 32})
	if err := term.WaitForText("far-pane", uiTimeout); err != nil {
		t.Fatalf("the client never drew the session: %v\n%s", err, term.Snapshot())
	}

	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	resizedBefore := countIn(daemonLog, " resized ")
	for _, pid := range testProxies(t) {
		_ = syscall.Kill(pid, syscall.SIGSTOP)
	}
	resized := make(chan error, 1)
	go func() {
		_, err := callVerb(t, base, "resize", map[string]any{"session": "near", "window": "far-pane", "width": 61, "height": 17})
		resized <- err
	}()
	waitUntil(t, "the daemon took the new size of far-pane", uiTimeout, func() (bool, string) {
		n := countIn(daemonLog, " resized ")
		return n > resizedBefore, fmt.Sprintf("%d resize lines, %d before", n, resizedBefore)
	})
	// Taken by the daemon and queued behind the size, which waits on the
	// stopped link.
	if _, err := callVerb(t, base, "send-text", map[string]any{"session": "near", "window": "far-pane", "text": "echo LOST\"\"KEYS-7\n"}); err != nil {
		t.Fatalf("send-text to far-pane: %v", err)
	}
	for _, pid := range testProxies(t) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}

	select {
	case err := <-resized:
		if err == nil || !strings.Contains(err.Error(), "host_unreachable") {
			t.Errorf("ASSERTION: the resize verb said the size was taken while the link was down: %v", err)
		}
	case <-time.After(scaled(uiTimeout * 2)):
		t.Errorf("ASSERTION: the resize verb never answered")
	}
	if err := term.WaitForText("Some keys did not reach far-pane on build. The link dropped.", uiTimeout*2); err != nil {
		saveFrame(t, term, "keys-lost-notice")
		t.Fatalf("ASSERTION: the person was not told that keys did not reach far-pane: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "keys-lost-notice")
	waitUntil(t, "list-windows counts the lost keys and far-pane is back", uiTimeout*3, func() (bool, string) {
		w, out := farPane(base)
		lost, _ := w["host_keys_lost"].(float64)
		return w != nil && lost >= 1 && w["host_link"] == nil, out
	})

	// The pane answers again, and the lost command never runs there.
	waitUntil(t, "far-pane answers after the link is back", uiTimeout*2, func() (bool, string) {
		_, _ = callVerb(t, base, "send-text", map[string]any{"session": "near", "window": "far-pane", "text": "echo BACK-\"\"FAR\n"})
		out, _ := tuiosOut(base, "capture-pane", "-s", "near", "-w", "far-pane")
		return strings.Contains(out, "\nBACK-FAR"), out
	})
	stayTrue(t, "the lost command is not sent on the new link", 3*time.Second, func() (bool, string) {
		out, _ := tuiosOut(base, "capture-pane", "-s", "near", "-w", "far-pane")
		return !strings.Contains(out, "LOSTKEYS-7"), out
	})
	alive(t, term, "after keys were lost with the link")
}

// countIn is how many times s is in the file at path.
func countIn(path, s string) int {
	b, _ := os.ReadFile(path)
	return strings.Count(string(b), s)
}
