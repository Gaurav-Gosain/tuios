package app

import (
	"fmt"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

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
