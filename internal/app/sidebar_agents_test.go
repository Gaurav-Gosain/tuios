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
