package session

import (
	"testing"
	"time"
)

// The file section on the rail asks one question: where is the focused pane. It
// used to get an answer only from a shell that announced one over OSC 7, or
// from reading the pane's process on the machine the pane runs on. Neither is
// available to a client that attached a session on another machine, so a remote
// pane had no directory at all and the section drew nothing.

// TestTheDaemonReportsADirectoryNoShellAnnounced pins the fix. The daemon owns
// the process, so it is the only side that can answer, and the answer has to be
// in the state it hands out rather than left for the client to work out.
//
// Negative control: dropping the liveCwds fill from GetState made this fail
// with an empty directory while the session was plainly running in one.
func TestTheDaemonReportsADirectoryNoShellAnnounced(t *testing.T) {
	sess, id := sessionWithInheritCwd(t, false)

	// The shell has to exist before it has a directory. This also skips the
	// test on a platform that cannot read one, the same way the other cwd
	// tests do: the point is the choice tuios makes, not whether this OS can
	// answer.
	want := cwdOfWindow(t, sess, id)

	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if win, ok := findWindowState(sess.GetState(), id); ok && win.Cwd != "" {
			got = win.Cwd
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got == "" {
		t.Fatalf("the daemon hands out a window with no directory, so a client on another machine has nothing to list; the shell is in %s", want)
	}
	if got != want {
		t.Errorf("the daemon reports %q, the shell is in %q", got, want)
	}
}

// TestAnAnnouncedDirectoryIsNotOverwritten: a shell that announces its own
// directory is the better answer and keeps winning. It is the shell's own
// account of where it is, and it stays right when the pane is running something
// that moved without the process moving.
func TestAnAnnouncedDirectoryIsNotOverwritten(t *testing.T) {
	sess, id := sessionWithInheritCwd(t, false)
	_ = cwdOfWindow(t, sess, id) // wait for the shell, and skip if unsupported

	const announced = "/announced/by/the/shell"
	sess.stateMu.Lock()
	for i := range sess.state.Windows {
		if sess.state.Windows[i].ID == id {
			sess.state.Windows[i].Cwd = announced
		}
	}
	sess.stateMu.Unlock()

	win, ok := findWindowState(sess.GetState(), id)
	if !ok {
		t.Fatalf("window %s not found", id)
	}
	if win.Cwd != announced {
		t.Errorf("the process read overwrote what the shell announced: got %q, want %q", win.Cwd, announced)
	}
}

// TestTheDirectoryReadIsThrottled guards the cost. GetState is on the render
// path, and reading a process directory is a syscall per window, so a second
// call inside the interval must reuse the first one's answer rather than going
// back to the operating system.
func TestTheDirectoryReadIsThrottled(t *testing.T) {
	sess, id := sessionWithInheritCwd(t, false)
	_ = cwdOfWindow(t, sess, id)

	first := sess.liveCwds()
	sess.cwdCacheMu.Lock()
	at := sess.cwdReadAt
	sess.cwdCacheMu.Unlock()

	second := sess.liveCwds()
	sess.cwdCacheMu.Lock()
	again := sess.cwdReadAt
	sess.cwdCacheMu.Unlock()

	if !at.Equal(again) {
		t.Error("a second read inside the interval went back to the operating system")
	}
	if len(first) != len(second) {
		t.Errorf("the throttled read answered differently: %d then %d", len(first), len(second))
	}
}
