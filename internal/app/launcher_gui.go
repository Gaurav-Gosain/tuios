package app

import (
	"fmt"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// runGUIProgram starts a graphical desktop entry with launcher.gui_command,
// when that is set, instead of in a new pane.
//
// A program with a window of its own has nothing to show in a pane: the pane
// would sit empty while the window opens somewhere else. A compositor that
// shows its windows as panes (tuios-wayland) or a desktop's own spawn command
// is the right place to start it. Terminal=true entries and $PATH programs
// still get a pane, because a pane is their window.
//
// The command is started from the client, detached, and not waited on by the
// UI: it is expected to hand the program over and return.
func (m *OS) runGUIProgram(e applist.Entry) (tea.Cmd, bool) {
	if m.UserConfig == nil || e.Source != applist.SourceDesktop || e.Terminal {
		return nil, false
	}
	prefix := m.UserConfig.Launcher.GUIPrefix()
	if len(prefix) == 0 {
		return nil, false
	}
	argv := append(prefix, e.Argv()...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = e.Cwd
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		m.ShowNotification(fmt.Sprintf("Cannot start %s: %v. Check launcher.gui_command.", e.Label(), err),
			"error", 6*time.Second)
		return nil, true
	}
	go func() { _ = cmd.Wait() }()
	return m.noteLaunch(e.Name), true
}
