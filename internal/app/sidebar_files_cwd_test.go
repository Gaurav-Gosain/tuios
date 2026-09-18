package app

import (
	"testing"

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

// TestTheDaemonsDirectoryFillsTheGap is the positive half, and the whole reason
// the field is carried at all. A shell that never announces leaves the client
// with nothing, and a pane on another machine has no process here to read, so
// the daemon's copy is the only answer such a pane can ever have.
func TestTheDaemonsDirectoryFillsTheGap(t *testing.T) {
	w := &terminal.Window{}
	adoptWindowCwd(w, "/home/ubuntu")
	if w.Cwd != "/home/ubuntu" {
		t.Errorf("a pane whose shell never announced got %q, want the daemon's answer", w.Cwd)
	}
}

// TestAnEmptyDirectoryDoesNotWipeOne: a sync that omits the field must not take
// away a directory the pane did announce.
func TestAnEmptyDirectoryDoesNotWipeOne(t *testing.T) {
	w := &terminal.Window{Cwd: "/src/tuios"}
	adoptWindowCwd(w, "")
	if w.Cwd != "/src/tuios" {
		t.Errorf("a sync with no directory wiped one: got %q", w.Cwd)
	}
	adoptWindowCwd(nil, "/anything") // must not panic
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
