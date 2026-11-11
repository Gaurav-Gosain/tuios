package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// registerPaletteHandlers binds the actions that began as command palette
// rows with code of their own. As actions they take a key and a tape line,
// and the palette runs them by name like every other row.
func (d *ActionDispatcher) registerPaletteHandlers() {
	d.Register("new_window_on_host", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.OpenHostPicker()
		return o, nil
	})
	d.Register("toggle_shared_borders", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.ToggleSharedBorders()
	})
	d.Register("next_saved_layout", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.NextLayout()
	})
	d.Register("prev_saved_layout", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.PrevLayout()
	})
	d.Register("clear_multifocus", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.ClearMultifocus()
		return o, nil
	})
	d.Register("layout_bsp", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.EnableBSPLayout()
		return o, nil
	})
	d.Register("layout_master_stack", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.EnableMasterStackLayout()
		return o, nil
	})
	d.Register("layout_scrolling", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.EnableScrollingLayout()
		return o, nil
	})
	d.Register("layout_off", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.DisableAllTiling()
		return o, nil
	})
	d.Register("all_windows", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.ShowAggregateView = true
		o.AggregateViewQuery = ""
		o.AggregateViewSelected = 0
		o.AggregateViewScroll = 0
		return o, nil
	})
	d.Register("theme_picker", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.OpenThemePicker()
		return o, nil
	})
	d.Register("look_picker", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.OpenLookPicker()
	})
	d.Register("share_look", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.ShareLook("", "", "")
	})
	d.Register("edit_theme", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.OpenThemeEditor(theme.CurrentThemeID(), false, "")
		return o, nil
	})
	d.Register("toggle_focus_follows_mouse", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		save := o.ToggleFocusFollowsMouse()
		state := "off"
		if o.Settings.FocusFollowsMouse {
			state = "on"
		}
		o.ShowNotification("Focus follows mouse "+state, "success", o.Settings.NotificationDuration)
		return o, save
	})
	d.Register("add_machine", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.OpenMachineSheet("")
	})
	d.Register("open_mailbox", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.OpenAgentMail()
	})
	d.Register("dispatch_pause", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.DispatchSet("pause")
	})
	d.Register("dispatch_resume", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.DispatchSet("resume")
	})
	d.Register("multi_copy_mode", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.EnterMultiCopyMode()
		return o, nil
	})
}
