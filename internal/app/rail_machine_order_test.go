package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// railMachineOrder is the machines the rail draws, top to bottom.
func railMachineOrder(t *testing.T, m *OS) []string {
	t.Helper()
	here := []sessiontree.Node{{
		Kind: sessiontree.KindSession, ID: "here", Title: "home", Host: m.attachedMachine(),
	}}
	var order []string
	for _, n := range m.sidebarMachineRows(here, m.hostGroupNodes()) {
		if n.Kind == sessiontree.KindHost {
			order = append(order, n.Host)
		}
	}
	return order
}

// TestTheMachineOrderDoesNotDependOnWhereYouAreAttached.
//
// The attached machine's group is built from the sessions already in hand
// rather than from the host listing, because the listing leaves it out. That
// made it the first group built and therefore the first group sorted, so
// attaching a session on another machine moved that machine to the top of the
// rail and pushed everything under it down. Switching sessions reshuffled the
// rail, which is not something a switch should do.
//
// Negative control: with the table-order pass removed, attaching build gives
// [local build workstation] against [local build workstation] on local only
// because build already sorts second. Attaching workstation is the case that
// fails, so both are checked.
func TestTheMachineOrderDoesNotDependOnWhereYouAreAttached(t *testing.T) {
	m := hostRailOS(t)

	m.AttachedHost = ""
	onLocal := railMachineOrder(t, m)
	if len(onLocal) < 3 {
		t.Fatalf("ASSERTION: the fixture draws %d machines, too few to reorder: %v", len(onLocal), onLocal)
	}

	for _, host := range []string{"build", "workstation"} {
		m.AttachedHost = host
		got := railMachineOrder(t, m)
		if !slices.Equal(onLocal, got) {
			t.Errorf("attaching %s reorders the rail:\n  on this machine %v\n  on %s %v", host, onLocal, host, got)
		}
	}
}

// TestThisMachineIsDrawnFirst is the ordering rule that does hold, kept
// alongside the one that must not, so the fix above cannot be mistaken for
// "the order never changes at all".
func TestThisMachineIsDrawnFirst(t *testing.T) {
	m := hostRailOS(t)
	for _, host := range []string{"", "build", "workstation"} {
		m.AttachedHost = host
		got := railMachineOrder(t, m)
		if len(got) == 0 {
			t.Fatalf("attached to %q the rail draws no machines", host)
		}
		if got[0] != federation.LocalHostName {
			t.Errorf("attached to %q the rail draws %s first, want %s", host, got[0], federation.LocalHostName)
		}
	}
}
