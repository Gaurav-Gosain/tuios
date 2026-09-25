package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestTheRailKeepsItsWidthAcrossSessions.
//
// The rail's width says how much of this screen the rail may have. That is a
// fact about the person looking at it, not about the session they happen to be
// looking at, and it already has a home of its own in the sidebar state file.
//
// Adopting it from session state made the rail a property of whatever you were
// attached to: hop to a session on another machine and the rail changed width,
// because you inherited whatever width the last person to use that session left
// it at. Nothing about your screen changed, so nothing about your rail should.
//
// Negative control: restoring the adopt makes this fail with 24, the width the
// incoming session carries.
func TestTheRailKeepsItsWidthAcrossSessions(t *testing.T) {
	m := &OS{}
	m.SidebarWidthPref = 34

	m.adoptSidebarState(&session.SessionState{SidebarWidth: 24})

	if m.SidebarWidthPref != 34 {
		t.Errorf("attaching a session changed the rail to %d, want the viewer's own 34", m.SidebarWidthPref)
	}
}
