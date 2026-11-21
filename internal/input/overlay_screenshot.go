package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func isScreenshotKey(msg tea.KeyPressMsg, o *app.OS) bool {
	if o.KeybindRegistry == nil {
		return false
	}
	return tableAction(o, msg, o.KeybindRegistry, config.TablePrefixMode) == "prefix_screenshot"
}

func routeOverlayScreenshot(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if held := o.OverlayLeader; held != nil {
		o.OverlayLeader = nil
		if isScreenshotKey(msg, o) {
			o.NoteAction("prefix_screenshot")
			o.BeginCapture(false)
			return o, nil, true
		}
		m, first := routeKey(*held, o)
		m, second := routeKey(msg, m)
		return m, tea.Batch(first, second), true
	}
	if o.PrefixActive || !isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) || !o.OverlayOnScreen() {
		return o, nil, false
	}
	held := msg
	o.OverlayLeader = &held
	return o, nil, true
}
