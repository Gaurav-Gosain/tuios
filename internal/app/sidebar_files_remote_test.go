package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The file section for a pane whose process is on another machine.
//
// The section needs a directory before it asks for a listing, and a remote
// pane has none of the usual sources: no process here to read, and a shell
// that never announces over OSC 7 says nothing. The daemon that owns the
// window asks the machine running the process and puts the answer in the
// window state, so the client's whole job is to take it.

// TestTheClientTakesARemotePanesDirectoryFromTheDaemon.
//
// Negative control: dropping adoptWindowCwd from updateWindowFromState leaves
// the section with nothing to ask about and this fails.
func TestTheClientTakesARemotePanesDirectoryFromTheDaemon(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")

	w := &terminal.Window{ID: "w1", Width: 40, Height: 20, Workspace: 1, PTYID: "pty-1"}
	m.updateWindowFromState(w, &session.WindowState{
		ID: "w1", PTYID: "pty-1", Width: 40, Height: 20, Workspace: 1,
		Host: "build", Cwd: "/home/ubuntu",
	})
	m.Windows = []*terminal.Window{w}
	m.FocusedWindow = 0

	if w.Host != "build" {
		t.Errorf("the client did not take the machine: %q", w.Host)
	}
	if w.Cwd != "/home/ubuntu" {
		t.Errorf("the client did not take the directory: %q", w.Cwd)
	}
	if got := m.filesWantDir(); got != "/home/ubuntu" {
		t.Errorf("the file section would ask about %q, want /home/ubuntu", got)
	}
}

// TestAnAnnouncedDirectoryStillWinsForARemotePane. A shell on the far machine
// that does announce over OSC 7 is saying where it is right now, which is
// fresher than the daemon's reading of the process, and the rule that the
// announcement wins does not change because the process is elsewhere.
func TestAnAnnouncedDirectoryStillWinsForARemotePane(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")

	w := &terminal.Window{ID: "w1", Cwd: "/home/ubuntu/src", Width: 40, Height: 20, PTYID: "pty-1"}
	m.updateWindowFromState(w, &session.WindowState{
		ID: "w1", PTYID: "pty-1", Width: 40, Height: 20,
		Host: "build", Cwd: "/home/ubuntu",
	})

	if w.Cwd != "/home/ubuntu/src" {
		t.Errorf("the daemon's slower copy overwrote what the pane announced: %q", w.Cwd)
	}
}
