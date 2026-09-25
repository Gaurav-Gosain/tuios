package session

import "testing"

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
