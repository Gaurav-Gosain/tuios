package input

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// registerPrefixTreeHandlers registers the actions tuios had no equivalent of
// for a tmux-shaped keymap. None has a default key; each is an ordinary action
// a user can bind in any section.
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

	// Resize: tmux's resize-pane -L/-R/-U/-D 5 and 20.
	for _, step := range []int{5, 20} {
		for name, dx := range map[string][2]int{
			"left": {-step, 0}, "right": {step, 0}, "up": {0, -step}, "down": {0, step},
		} {
			d.Register("resize_pane_"+name+"_"+strconv.Itoa(step), makeResizeStepHandler(dx[0], dx[1]))
		}
	}

	// Workspaces as tmux windows.
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
	// The leader itself, to the pane: what pressing it twice does in terminal
	// mode, as a key of its own, for screen's C-a a and a nested tmux.
	d.Register("send_prefix", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		forwardKeyToFocusedWindow(o.LeaderKeyPress(), o)
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

	// Tools.
	d.Register("reload_config", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.ReloadConfigFromDisk()
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

// makeResizeStepHandler resizes the focused tiled pane by dx columns and dy
// rows, growing for a positive step and shrinking for a negative one.
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

// handleNewWorkspace goes to the first workspace with no pane, the one the
// dock's + button goes to, and opens one there: tmux's new-window. With
// workspaces.new_window_when_empty on, the switch has asked for that pane
// already.
func handleNewWorkspace(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	ws := o.NewWorkspaceTarget()
	if ws == 0 {
		o.ShowNotification("Every workspace has a pane", "info", o.Settings.NotificationDuration)
		return o, nil
	}
	o.SwitchToWorkspace(ws)
	if o.CurrentWorkspace != ws || (o.UserConfig != nil && o.UserConfig.Workspaces.NewWindowWhenEmpty) {
		return o, nil
	}
	return handleNewWindow(msg, o)
}
