package session

import "testing"

// The machine a window's process runs on is daemon-owned, and a client sync
// does not carry it.
//
// This is the field's whole failure mode, and it happened: Host was added to
// WindowState without being added to the carry-over, so the first state a
// client pushed wiped it. Everything downstream then read the pane as local.
// The rail stopped naming the machine, and the file section asked this machine
// for a directory that lives on another one, which is why a remote pane's
// files never appeared.
//
// Negative control: removing the Host lines from retainDaemonExclusive fails
// here with an empty host.
func TestAClientSyncDoesNotWipeTheWindowsMachine(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{
		{ID: "w1", Host: "build"},
		{ID: "w2"},
	}}
	// What a client pushes: it rebuilds the window set from what it draws and
	// says nothing about where any process runs.
	incoming := &SessionState{Windows: []WindowState{
		{ID: "w1"},
		{ID: "w2"},
	}}

	retainDaemonExclusive(incoming, canonical)

	if got := incoming.Windows[0].Host; got != "build" {
		t.Errorf("a client sync left the window on %q, want build", got)
	}
	if got := incoming.Windows[1].Host; got != "" {
		t.Errorf("a local window gained a machine: %q", got)
	}
}

// TestTheMachineIsCarriedByWindowRatherThanByPosition. Windows are reordered
// by every layout change, so carrying by index would hand one window's machine
// to another, which is worse than losing it.
func TestTheMachineIsCarriedByWindowRatherThanByPosition(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{
		{ID: "w1", Host: "build"},
		{ID: "w2", Host: "workstation"},
	}}
	incoming := &SessionState{Windows: []WindowState{
		{ID: "w2"},
		{ID: "w1"},
	}}

	retainDaemonExclusive(incoming, canonical)

	if incoming.Windows[0].Host != "workstation" || incoming.Windows[1].Host != "build" {
		t.Errorf("the machines followed the order rather than the windows: %q, %q",
			incoming.Windows[0].Host, incoming.Windows[1].Host)
	}
}

// TestAWindowThatSaysItsMachineIsBelieved. The carry-over is for a sync that
// omits the field, not an override: the daemon itself pushes state with the
// machine set, and that push has to be able to say so.
func TestAWindowThatSaysItsMachineIsBelieved(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{{ID: "w1", Host: "build"}}}
	incoming := &SessionState{Windows: []WindowState{{ID: "w1", Host: "workstation"}}}

	retainDaemonExclusive(incoming, canonical)

	if got := incoming.Windows[0].Host; got != "workstation" {
		t.Errorf("a state that named a machine was overruled: %q", got)
	}
}
