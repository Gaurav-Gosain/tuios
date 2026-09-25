package session

import (
	"fmt"
)

// benchState builds a session state with n windows spread over workspaces.
func benchState(n int) *SessionState {
	st := &SessionState{
		Name:             "bench",
		CurrentWorkspace: 1,
		MasterRatio:      0.5,
		AutoTiling:       true,
		Width:            207,
		Height:           55,
		WorkspaceFocus:   make(map[int]string, 9),
		Windows:          make([]WindowState, 0, n),
	}
	for i := range n {
		id := fmt.Sprintf("window-%04d-abcdef", i)
		st.Windows = append(st.Windows, WindowState{
			ID:        id,
			Title:     fmt.Sprintf("bash - /home/user/project/dir%02d", i),
			PTYID:     fmt.Sprintf("pty-%04d", i),
			X:         (i % 4) * 50,
			Y:         (i / 4) * 12,
			Width:     50,
			Height:    12,
			Z:         i,
			Workspace: 1 + (i % 9),
		})
	}
	if n > 0 {
		st.FocusedWindowID = st.Windows[0].ID
	}
	return st
}
