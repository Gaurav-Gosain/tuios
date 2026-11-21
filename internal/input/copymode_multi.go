package input

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

var parkedCopyKeys = map[string]bool{
	"/": true, "?": true, "n": true, "N": true, "ctrl+l": true,
	"q": true, "esc": true, "i": true, "ctrl+c": true,
}

func handleMultiCopyKey(msg tea.KeyPressMsg, o *app.OS, focused *terminal.Window) (*app.OS, tea.Cmd) {
	mc := o.MultiCopy
	if mc.Save != nil {
		return handleMultiCopySaveKey(msg, o)
	}

	windows := o.MultiCopyWindows()
	lead := o.MultiCopyLead(focused)
	lcm := lead.CopyMode
	leadState := lcm.State
	k := commandKey(msg)
	allParked := o.MultiCopyAllParked()

	if leadState != terminal.CopyModeSearch && !waitingForChar(lcm) {
		inVisual := leadState.InVisual()
		pipe, isPipe := copyPipeKey(msg, o, lcm)
		exitYank := inVisual && copyModeAction(msg, o) == config.ActionCopyModeExit
		wholeSet := isPipe || exitYank || k == "tab" || k == "y" || k == "Y" || (k == "c" && inVisual)
		if wholeSet {
			for _, w := range windows {
				if w.CopyMode != nil {
					w.CopyMode.PendingCount = 0
				}
			}
		}
		switch {
		case isPipe:
			panes, skipped, selected := collectMultiCopy(o, windows)
			if len(panes) == 0 {
				o.ShowNotification(fmt.Sprintf("No pane has a selection. Press v or V to select, then press %s.", pipe.Key), "warning", o.Settings.NotificationDuration)
				return o, nil
			}
			cmd := o.PipeMultiCopy(panes, skipped, pipe)
			for _, w := range selected {
				w.CopyMode.State = terminal.CopyModeNormal
				w.InvalidateCache()
			}
			if pipe.Cancel {
				o.ExitMultiCopyMode()
			}
			return o, cmd
		case k == "tab":
			o.CycleMultiCopyFormat()
			return o, nil
		case exitYank:
			panes, skipped, _ := collectMultiCopy(o, windows)
			cmd := o.YankMultiCopy(panes, skipped)
			o.ExitMultiCopyMode()
			return o, cmd
		case k == "y" || (k == "c" && inVisual):
			panes, skipped, selected := collectMultiCopy(o, windows)
			cmd := o.YankMultiCopy(panes, skipped)
			for _, w := range selected {
				w.CopyMode.State = terminal.CopyModeNormal
				w.InvalidateCache()
			}
			return o, cmd
		case k == "Y":
			panes, _, _ := collectMultiCopy(o, windows)
			o.OpenMultiCopySave(panes)
			return o, nil
		}
	}

	var leadFx *copyModeEffects
	for _, w := range windows {
		if !w.InCopyMode() {
			continue
		}
		if o.MultiCopyParked(w.ID) && (w != lead || allParked) {
			takes := leadState == terminal.CopyModeSearch ||
				(leadState == terminal.CopyModeNormal && (parkedCopyKeys[k] || copyModeAction(msg, o) == config.ActionCopyModeExit))
			if !takes {
				continue
			}
		}
		fx := &copyModeEffects{}
		dispatchCopyModeKey(msg, copyModeAction(msg, o), w, fx, &o.Settings)
		if w == lead {
			leadFx = fx
			continue
		}
		if fx.invalidate {
			w.InvalidateCache()
		}
	}
	if leadFx == nil {
		leadFx = &copyModeEffects{}
	}

	if leadFx.exitCopyMode {
		o.ExitMultiCopyMode()
		leadFx.exitCopyMode = false
		return leadFx.apply(o, lead)
	}

	o.SetMultiCopyParked(multiCopySearchMisses(windows))

	if leadState == terminal.CopyModeSearch && lead.CopyMode.State == terminal.CopyModeNormal && lead.CopyMode.SearchQuery() != "" {
		matched, total := o.MultiCopyMatched()
		leadFx.notifications = nil
		leadFx.ShowNotification(fmt.Sprintf("%s%s: found in %d of %d panes", searchPrompt(lead.CopyMode.SearchBackward), lead.CopyMode.SearchQuery(), matched, total),
			"info", o.Settings.NotificationDuration)
	}
	return leadFx.apply(o, lead)
}

func multiCopySearchMisses(windows []*terminal.Window) map[string]bool {
	var miss map[string]bool
	for _, w := range windows {
		cm := w.CopyMode
		if cm == nil || !cm.Active || cm.SearchQuery() == "" || len(cm.SearchMatches) > 0 {
			continue
		}
		if miss == nil {
			miss = make(map[string]bool)
		}
		miss[w.ID] = true
	}
	return miss
}

func collectMultiCopy(o *app.OS, windows []*terminal.Window) (panes []app.MultiCopyPane, skipped int, selected []*terminal.Window) {
	for _, w := range windows {
		if !w.HasSelection() || o.MultiCopyParked(w.ID) {
			skipped++
			continue
		}
		var text string
		func() {
			w.RLockIO()
			defer w.RUnlockIO()
			text = extractVisualText(w.CopyMode, w)
		}()
		panes = append(panes, o.MultiCopyPaneFor(w, text))
		selected = append(selected, w)
	}
	return panes, skipped, selected
}

func handleMultiCopySaveKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	s := o.MultiCopy.Save
	key := msg.Key()
	switch {
	case key.Code == tea.KeyEnter || key.Code == tea.KeyKpEnter:
		ids := make(map[string]bool, len(s.Panes))
		for _, p := range s.Panes {
			ids[p.WindowID] = true
		}
		if o.CommitMultiCopySave() {
			for _, w := range o.MultiCopyWindows() {
				if ids[w.ID] && w.HasSelection() {
					w.CopyMode.State = terminal.CopyModeNormal
					w.InvalidateCache()
				}
			}
		}
	case key.Code == tea.KeyEscape:
		o.CancelMultiCopySave()
	case commandKey(msg) == "ctrl+c":
		o.ExitMultiCopyMode()
		o.ShowNotification("Left copy mode", "info", o.Settings.NotificationDuration)
	case key.Code == tea.KeyTab:
		o.CycleMultiCopyFormat()
	default:
		o.MultiCopySaveKey(msg.String(), key.Text)
	}
	return o, nil
}
