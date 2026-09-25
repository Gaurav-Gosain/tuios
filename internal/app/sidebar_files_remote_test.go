package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The file section for a pane whose process is on another machine.
//
// The section needs a directory before it asks for a listing, and a remote
// pane has none of the usual sources: no process here to read, and a shell
// that never announces over OSC 7 says nothing. The daemon that owns the
// window asks the machine running the process and puts the answer in the
// window state, so the client's whole job is to take it.

// TestMovingBetweenTwoMachinesAtTheSamePathAsksAgain.
//
// The section only asks for a listing when the answer would be different, and
// it decided that by comparing the directory alone. Two machines are very
// often in the same directory, because /home/ubuntu is /home/ubuntu
// everywhere, so moving focus from a pane on one machine to a pane on another
// left the first machine's files on screen with nothing saying so.
//
// Negative control: comparing only the directory in FilesSyncCmd returns no
// command for the second pane and this fails.
func TestMovingBetweenTwoMachinesAtTheSamePathAsksAgain(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.filesView.Show = 1

	onBuild := &terminal.Window{
		ID: "w1", Cwd: "/home/ubuntu", Host: "build",
		Width: 40, Height: 20, Workspace: 1, PTYID: "pty-1",
	}
	onLab := &terminal.Window{
		ID: "w2", Cwd: "/home/ubuntu", Host: "lab",
		Width: 40, Height: 20, Workspace: 1, PTYID: "pty-2",
	}
	m.Windows = []*terminal.Window{onBuild, onLab}
	m.CurrentWorkspace = 1

	m.FocusedWindow = 0
	if cmd := m.FilesSyncCmd(); cmd == nil {
		t.Fatal("ASSERTION: the first pane asked for no listing, so there is nothing to compare against")
	}
	if m.filesView.Host != "build" {
		t.Fatalf("the listing was not recorded against build: %q", m.filesView.Host)
	}

	m.FocusedWindow = 1
	if cmd := m.FilesSyncCmd(); cmd == nil {
		t.Fatal("moving to a pane on another machine at the same path asked for nothing")
	}
	if m.filesView.Host != "lab" {
		t.Errorf("the listing is recorded against %q, want lab", m.filesView.Host)
	}
}
