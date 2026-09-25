package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAgentsControlsDefaultOnAGarbageStateFile: the file is shared with whatever
// tuios the user runs next, and a value this build does not know must read back
// as the default rather than emptying the section.
func TestAgentsControlsDefaultOnAGarbageStateFile(t *testing.T) {
	m := &OS{Settings: config.Global, SidebarAgentFilter: "nonsense", SidebarAgentSort: "nonsense"}
	if m.sidebarAgentsFilter() != sidebarAgentsAll || m.sidebarAgentsSort() != sidebarAgentsNeedsYou {
		t.Errorf("unknown values read back as filter=%q sort=%q", m.sidebarAgentsFilter(), m.sidebarAgentsSort())
	}
}

// TestAgentsControlsPersist: the two controls are shape the user chose, so they
// survive a restart beside the order and the width.
func TestAgentsControlsPersist(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := &OS{Settings: config.Global}
	m.SidebarCycleAgentsFilter()
	m.SidebarCycleAgentsSort()

	restored := &OS{Settings: config.Global}
	restored.loadSidebarState()
	if restored.sidebarAgentsFilter() != sidebarAgentsSession || restored.sidebarAgentsSort() != sidebarAgentsPriority {
		t.Errorf("after a restart filter=%q sort=%q, want session/priority",
			restored.sidebarAgentsFilter(), restored.sidebarAgentsSort())
	}

	m.SidebarCycleAgentsSort()
	restored = &OS{Settings: config.Global}
	restored.loadSidebarState()
	if restored.sidebarAgentsSort() != sidebarAgentsRecent {
		t.Errorf("after a second step sort=%q, want recent", restored.sidebarAgentsSort())
	}

	// Back to the defaults.
	m.SidebarCycleAgentsFilter()
	m.SidebarCycleAgentsSort()
	restored = &OS{Settings: config.Global}
	restored.loadSidebarState()
	if restored.sidebarAgentsFilter() != sidebarAgentsAll || restored.sidebarAgentsSort() != sidebarAgentsNeedsYou {
		t.Errorf("after flipping back filter=%q sort=%q, want all/needs_you",
			restored.sidebarAgentsFilter(), restored.sidebarAgentsSort())
	}
}

// TestAgentsSortKeepsAnExplicitPriority: needs-you replaced priority as the
// default. A state file that says priority was written by a user who picked it
// with the header control, and must keep it; only an empty one takes the new
// default.
func TestAgentsSortKeepsAnExplicitPriority(t *testing.T) {
	if got := (&OS{SidebarAgentSort: "priority"}).sidebarAgentsSort(); got != sidebarAgentsPriority {
		t.Errorf("a saved priority read back as %q", got)
	}
	if got := (&OS{}).sidebarAgentsSort(); got != sidebarAgentsNeedsYou {
		t.Errorf("an empty sort read back as %q, want needs_you", got)
	}
}
