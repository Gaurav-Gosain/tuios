package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// customClient is a client with two panes on workspace 3, under the
// master-stack layout the custom flag is most visible in. The panes are real
// because these cases are about where a rectangle ends up, not only about which
// flag a map holds.
func customClient(t *testing.T, name string) *OS {
	t.Helper()
	m := &OS{
		Settings:             config.Global,
		SharedBorders:        config.Global.SharedBorders,
		PaneGap:              config.Global.PaneGap,
		AutoTiling:           true,
		UseBSPLayout:         false,
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceFocus:       map[int]int{},
		WorkspaceTrees:       map[int]*layout.BSPTree{},
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		Width:                160,
		Height:               40,
		MasterRatio:          config.Global.MasterRatioFraction(),
		FocusedWindow:        -1,
	}
	for i := range 2 {
		w := benchWindow(t, fmt.Sprintf("%s-w%d", name, i), 80, 40)
		w.Workspace, w.Width, w.Height = 3, 80, 40
		m.Windows = append(m.Windows, w)
	}
	return m
}

// TestASyncThatSaysNothingLeavesTheCustomFlagsAlone is the compatibility
// contract. A peer too old to know the field, and every state file written
// before it existed, carry no map at all; gob also drops an empty one, so absent
// and empty have to mean the same thing. Either way the client keeps what it
// holds, and a workspace nobody has an entry for is still the tiler's.
//
// NEGATIVE CONTROL: make adoptWorkspaceHasCustom replace the map instead of
// merging into it and workspace 4's flag disappears.
func TestASyncThatSaysNothingLeavesTheCustomFlagsAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags map[int]bool
	}{
		{"absent", nil},
		{"empty", map[int]bool{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := customClient(t, "m")
			m.WorkspaceHasCustom[4] = true

			m.adoptWorkspaceHasCustom(&session.SessionState{
				CurrentWorkspace:   1,
				WorkspaceHasCustom: tc.flags,
			})
			if !m.WorkspaceHasCustom[4] {
				t.Errorf("workspace 4 lost its custom flag to a sync that said nothing")
			}
			if m.WorkspaceHasCustom[5] {
				t.Errorf("workspace 5 came out custom; a workspace nobody has an entry for is the tiler's")
			}
		})
	}
}

// TestALaggingSyncCannotUndoALayoutJustArranged is the other direction of the
// merge: the daemon echoes a client's own push back, and an echo built before
// the resize must not roll the flag back.
//
// NEGATIVE CONTROL: replace the map in adoptWorkspaceHasCustom rather than
// merging and workspace 6 loses its flag.
func TestALaggingSyncCannotUndoALayoutJustArranged(t *testing.T) {
	m := customClient(t, "m")
	m.WorkspaceHasCustom[6] = true

	m.adoptWorkspaceHasCustom(&session.SessionState{
		CurrentWorkspace:   1,
		WorkspaceHasCustom: map[int]bool{1: true},
	})
	if !m.WorkspaceHasCustom[6] {
		t.Errorf("workspace 6 lost the flag this client set, to a sync that never heard of it")
	}
	if !m.WorkspaceHasCustom[1] {
		t.Errorf("workspace 1 is not custom, want the flag the sync carried")
	}
}
