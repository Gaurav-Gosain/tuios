package input

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// handleCockpitCopyKey runs a key in copy mode over the agents view's thread,
// with the code that runs it over a pane. The thread's rows are held while
// copy mode runs, so it takes no lock. Two things differ from a pane:
//
//   - A yank of a selection is the text as the agent wrote it
//     (app.OS.CockpitCopyText): wrapped rows joined, the chrome and the
//     shared indent taken off.
//   - Y yanks the entries under the selection, or under the cursor, as
//     Markdown (app.OS.CockpitCopyMarkdown).
func handleCockpitCopyKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cm, grid := o.CockpitCopy()
	if cm == nil {
		return o, nil
	}
	fx := &copyModeEffects{}
	if msg.String() == "Y" && cm.State != terminal.CopyModeSearch && !cm.PendingCharSearch {
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
		return fx.applyCockpit(o)
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
	return fx.applyCockpit(o)
}

// applyCockpit runs the queued effects of a key in copy mode over the
// thread. Leaving copy mode, to the pane too (i), goes back to the thread.
// The editor and hints work on a pane, so they do nothing here.
func (fx *copyModeEffects) applyCockpit(o *app.OS) (*app.OS, tea.Cmd) {
	if fx.exitCopyMode || fx.enterTerminal {
		o.CockpitCopyEnd()
	}
	o.NoteAction(fx.action)
	for _, n := range fx.notifications {
		o.ShowNotification(n.message, n.notyType, n.duration)
	}
	if fx.setClipboard {
		return o, fx.clipboardCmd(o)
	}
	return o, nil
}
