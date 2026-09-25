package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The directory a pane is in can come from two places, and they are not equal.
// The shell announces one over OSC 7, which the client parses out of the stream
// as it arrives. The daemon reports one in the window state, which it fills from
// the process it owns and which reaches the client on the next sync. These pin
// which wins, because getting it backwards broke a security check.

// TestAnAnnouncedDirectoryIsNotOverwrittenByTheDaemons.
//
// The client sees OSC 7 the moment it arrives; the daemon's copy lands on the
// next state sync. So adopting over the top puts a directory the pane has
// already left back on the window.
//
// That is not just stale. cwdIsSpoofed compares the folder a pane announced
// against the one the kernel reports for its shell, and a stale announcement
// disagrees with a current process for a pane that did nothing wrong, so every
// cd turned into a spoof warning and took the file actions with it.
//
// Negative control: dropping the w.Cwd == "" condition fails here with the
// stale directory.
func TestAnAnnouncedDirectoryIsNotOverwrittenByTheDaemons(t *testing.T) {
	w := &terminal.Window{Cwd: "/src/tuios/internal/app"}
	adoptWindowCwd(w, "/src/tuios")
	if w.Cwd != "/src/tuios/internal/app" {
		t.Errorf("the daemon's slower copy overwrote what the pane announced: got %q", w.Cwd)
	}
}

// TestTheSpoofCheckIsNotRunAgainstAnotherMachinesPid.
//
// A window's ShellPgid is filled from the daemon's WindowState.ShellPID, and
// that is done for every pane including one attached from another machine. The
// number is then a pid on that machine, and reading it here asks the local
// operating system about whatever process happens to hold the same number.
//
// A security check is allowed to answer "no evidence". It is not allowed to
// answer using a fact from the wrong computer, which is what comparing a remote
// shell's directory against an unrelated local process does. The check is for
// panes this client's own daemon owns, and it runs only for those.
//
// Negative control: removing the AttachedHost gate makes the remote case carry
// a pid and this fails.
func TestTheSpoofCheckIsNotRunAgainstAnotherMachinesPid(t *testing.T) {
	for _, c := range []struct {
		name string
		host string
		want bool
	}{
		{"on this machine the check runs", "", true},
		{"on another machine it does not", "ente", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := spoofCheckApplies(c.host); got != c.want {
				t.Errorf("attached to %q the check applies=%v, want %v", c.host, got, c.want)
			}
		})
	}
}

// TestAFailedListingIsNotRetriedOnEveryMessage is the other half. FilesSyncCmd
// runs once per message, so an unpaced retry is a request to the daemon per
// keystroke for as long as the directory stays unreadable.
func TestAFailedListingIsNotRetriedOnEveryMessage(t *testing.T) {
	m := &OS{}
	m.filesView.Want = "/home/ubuntu"
	m.filesView.Err = "That folder is gone."
	m.filesView.ErrAt = time.Now()

	if filesShouldRetry(m.filesView, "/home/ubuntu") {
		t.Error("a listing that just failed is retried immediately")
	}
}

// TestAListingInFlightIsNotAskedForTwice.
func TestAListingInFlightIsNotAskedForTwice(t *testing.T) {
	m := &OS{}
	m.filesView.Want = "/home/ubuntu"
	m.filesView.Loading = true
	m.filesView.Err = "an older failure"
	m.filesView.ErrAt = time.Now().Add(-time.Hour)

	if filesShouldRetry(m.filesView, "/home/ubuntu") {
		t.Error("a read already in flight was asked for a second time")
	}
}
