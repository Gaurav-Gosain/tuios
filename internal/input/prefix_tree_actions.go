package input

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

func (d *ActionDispatcher) registerPrefixTreeHandlers() {
	d.Register("mark_pane", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.MarkPane()
		return o, nil
	})
	for name, dir := range map[string]layout.PreselectionDir{
		"left": layout.PreselectionLeft, "right": layout.PreselectionRight,
		"up": layout.PreselectionUp, "down": layout.PreselectionDown,
	} {
		d.Register("join_marked_"+name, func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
			o.JoinMarkedPane(dir)
			return o, nil
		})
	}

	for _, step := range []int{5, 20} {
		for name, dx := range map[string][2]int{
			"left": {-step, 0}, "right": {step, 0}, "up": {0, -step}, "down": {0, step},
		} {
			d.Register("resize_pane_"+name+"_"+strconv.Itoa(step), makeResizeStepHandler(dx[0], dx[1]))
		}
	}

	d.Register("new_workspace", handleNewWorkspace)
	d.Register("last_workspace", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.LastWorkspace()
		return o, nil
	})
	d.Register("break_pane", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.BreakPane()
		return o, nil
	})
	d.Register("last_session", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.LastSession()
		return o, nil
	})
	d.Register("send_prefix", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		forwardKeyToFocusedWindow(o.LeaderKeyPress(), o)
		return o, nil
	})
	d.Register("nest", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		if o.EnterNest() {
			o.ShowNotification("Nest mode is on. Press "+config.KeyLabel(o.Settings.GetNestExitKey())+" to leave it.", "info", o.Settings.NotificationDuration)
		} else {
			o.ShowNotification("Nest mode needs a focused pane.", "info", o.Settings.NotificationDuration)
		}
		return o, nil
	})
	d.Register("move_workspace_left", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.MoveWorkspaceBy(-1)
	})
	d.Register("move_workspace_right", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.MoveWorkspaceBy(1)
	})
	for i := 1; i <= 9; i++ {
		d.Register("send_to_workspace_"+strconv.Itoa(i), func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
			if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
				o.MoveWindowToWorkspace(o.FocusedWindow, i)
			}
			return o, nil
		})
	}

	d.Register("reload_config", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.ReloadConfigAsked()
	})
	d.Register("toggle_mouse", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.ToggleMouse()
		return o, nil
	})
	d.Register("cycle_layout", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.ToggleLayoutMode()
		return o, nil
	})
}

func makeResizeStepHandler(dx, dy int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		if !o.AutoTiling {
			return o, nil
		}
		if dx != 0 {
			o.ResizeFocusedWindowWidth(dx)
		}
		if dy != 0 {
			o.ResizeFocusedWindowHeight(dy)
		}
		return o, nil
	}
}

func handleNewWorkspace(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	ws := o.NewWorkspaceTarget()
	if ws == 0 {
		o.ShowNotification("Every workspace has a pane", "info", o.Settings.NotificationDuration)
		return o, nil
	}
	o.SwitchToWorkspace(ws)
	if o.GetWorkspaceWindowCount(o.CurrentWorkspace) > 0 || (o.UserConfig != nil && o.UserConfig.Workspaces.NewWindowWhenEmpty) {
		return o, nil
	}
	return handleNewWindow(msg, o)
}
