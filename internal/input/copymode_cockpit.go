package input

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func handleCockpitCopyKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cm, grid := o.CockpitCopy()
	if cm == nil {
		return o, nil
	}
	fx := &copyModeEffects{}
	if msg.String() == "Y" && cm.State != terminal.CopyModeSearch && !waitingForChar(cm) {
		y0 := grid.ScrollbackLen() - cm.ScrollOffset + cm.CursorY
		y1 := y0
		if cm.State.InVisual() {
			y0, y1 = cm.VisualStart.Y, cm.VisualEnd.Y
		}
		cm.State = terminal.CopyModeNormal
		if text := o.CockpitCopyMarkdown(y0, y1); text != "" {
			fx.SetClipboard(text)
			fx.ShowNotification("Copied as Markdown, "+strconv.Itoa(len(text))+" chars", "success", o.Settings.NotificationDuration)
		}
		return fx.applyOver(o, o.CockpitCopyEnd)
	}
	if msg.String() == "x" && cm.Pending == terminal.CopyPendingG {
		cm.Pending = terminal.CopyPendingNone
		url, ok := o.CockpitCopyLink()
		if !ok {
			o.ShowNotification("No link under the cursor.", "info", o.Settings.NotificationDuration)
			return o, nil
		}
		return o, o.OpenLink(url)
	}
	state := cm.State
	runCopyModeKey(msg, copyModeAction(msg, o), cm, grid, fx, &o.Settings)
	if fx.setClipboard && state.InVisual() {
		fx.clipboard = o.CockpitCopyText(cm.VisualStart, cm.VisualEnd, state == terminal.CopyModeVisualLine, state == terminal.CopyModeVisualBlock)
		for i := range fx.notifications {
			if strings.HasPrefix(fx.notifications[i].message, "Yanked ") {
				fx.notifications[i].message = "Yanked " + strconv.Itoa(len(fx.clipboard)) + " chars"
			}
		}
	}
	return fx.applyOver(o, o.CockpitCopyEnd)
}
