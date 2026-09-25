package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// dockCrowdedOS is a session with named workspaces and minimized panes, which
// is the state the bar has to ration: the strip wants the left region, the
// meters want the right, and the entries are what is left in the middle.
func dockCrowdedOS(t testing.TB, width, workspaces, minimized int) *OS {
	t.Helper()
	m := &OS{
		Settings:         config.Global,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            width,
		Height:           30,
		FocusedWindow:    -1,
	}
	names := []string{"editor", "server", "logs", "notes", "build", "review"}
	m.WorkspaceNames = map[int]string{}
	for ws := 1; ws <= workspaces; ws++ {
		win := newTestWindow(t, fmt.Sprintf("ws%d", ws), 40, 12)
		win.Workspace = ws
		m.Windows = append(m.Windows, win)
		// Named workspaces are what make the strip wide enough to be worth
		// rationing, which is the state the audit captured.
		m.WorkspaceNames[ws] = names[(ws-1)%len(names)]
	}
	for i := range minimized {
		win := newTestWindow(t, fmt.Sprintf("min%d", i), 40, 12)
		win.Workspace = 1
		win.CustomName = fmt.Sprintf("min%d", i)
		win.Minimized = true
		win.MinimizeOrder = int64(i + 1)
		m.Windows = append(m.Windows, win)
	}
	return m
}
