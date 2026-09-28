package input

import (
	"fmt"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Multi copy mode key handling: one key, every pane of the mode. See
// internal/app/multicopy.go for what the mode is.
//
// Each pane runs the ordinary copy-mode handler against its own cursor and
// buffer, under its own lock, one pane at a time, so the motions, counts,
// search and visual selection are exactly single-pane copy mode's, repeated.
// Only the keys that make sense once for the whole set are handled here: the
// yank, the save, and the format cycle.
//
// One pane leads: the focused one, unless the last search found nothing in it,
// in which case the first pane that did find something. The lead's state
// decides what a key means (leave copy mode, or only leave visual mode), and
// its messages are the ones the dock shows. Without a lead, 32 panes would
// post 32 copies of "VISUAL".

// Keys a parked pane still takes while the lead is in normal mode: starting a
// new search, stepping through matches it does not have, clearing the search,
// and leaving. Anything else would move or select in a pane the search did not
// pick, which is the one thing parking exists to prevent.
var parkedCopyKeys = map[string]bool{
	"/": true, "?": true, "n": true, "N": true, "ctrl+l": true,
	"q": true, "esc": true, "i": true,
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

	// The keys that act once for the whole set. A search being typed and a
	// pending f/t character take every key as text, so they are left alone. A
	// count typed before them means nothing to them and is dropped in every
	// pane: "1y" is a yank of every selection, not a count handed to one pane.
	if leadState != terminal.CopyModeSearch && !lcm.PendingCharSearch {
		inVisual := leadState == terminal.CopyModeVisualChar || leadState == terminal.CopyModeVisualLine
		wholeSet := k == "tab" || k == "y" || k == "Y" || (k == "c" && inVisual)
		if wholeSet {
			for _, w := range windows {
				if w.CopyMode != nil {
					w.CopyMode.PendingCount = 0
				}
			}
		}
		switch {
		case k == "tab":
			o.CycleMultiCopyFormat()
			return o, nil
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
		// A parked pane takes only the parked keys. When the search matched no
		// pane at all, that holds for the lead too: nothing may move or select.
		if o.MultiCopyParked(w.ID) && (w != lead || allParked) {
			takes := leadState == terminal.CopyModeSearch ||
				(leadState == terminal.CopyModeNormal && parkedCopyKeys[k])
			if !takes {
				continue
			}
		}
		fx := &copyModeEffects{}
		dispatchCopyModeKey(msg, w, fx, &o.Settings)
		if w == lead {
			leadFx = fx
			continue
		}
		// Another pane's messages and mode switches are the lead's to make;
		// only its redraw is its own.
		if fx.invalidate {
			w.InvalidateCache()
		}
	}
	if leadFx == nil {
		leadFx = &copyModeEffects{}
	}

	if leadFx.exitCopyMode {
		// Leaving copy mode leaves it in every pane. The lead's effects still
		// run for the message and a switch into terminal mode.
		o.ExitMultiCopyMode()
		leadFx.exitCopyMode = false
		return leadFx.apply(o, lead)
	}

	o.SetMultiCopyParked(multiCopySearchMisses(windows))

	// A search just finished: say how many panes it found something in, which
	// is the number a person searching 32 panes at once needs.
	if leadState == terminal.CopyModeSearch && lead.CopyMode.State == terminal.CopyModeNormal && lead.CopyMode.SearchQuery != "" {
		matched, total := o.MultiCopyMatched()
		leadFx.notifications = nil
		leadFx.ShowNotification(fmt.Sprintf("/%s: found in %d of %d panes", lead.CopyMode.SearchQuery, matched, total),
			"info", o.Settings.NotificationDuration)
	}
	return leadFx.apply(o, lead)
}

// multiCopySearchMisses is the panes a search is active in and found nothing.
func multiCopySearchMisses(windows []*terminal.Window) map[string]bool {
	var miss map[string]bool
	for _, w := range windows {
		cm := w.CopyMode
		if cm == nil || !cm.Active || cm.SearchQuery == "" || len(cm.SearchMatches) > 0 {
			continue
		}
		if miss == nil {
			miss = make(map[string]bool)
		}
		miss[w.ID] = true
	}
	return miss
}

// collectMultiCopy reads the highlighted text of every pane that has a
// selection, in the mode's order. Panes without one are counted, not read.
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

// handleMultiCopySaveKey edits the save prompt's path.
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
	case key.Code == tea.KeyTab:
		o.CycleMultiCopyFormat()
	case key.Code == tea.KeyBackspace:
		if s.Path != "" {
			_, size := utf8.DecodeLastRuneInString(s.Path)
			s.Path = s.Path[:len(s.Path)-size]
		}
		s.Err = ""
	case msg.String() == "ctrl+u":
		s.Path = ""
		s.Err = ""
	case key.Text != "":
		s.Path += key.Text
		s.Err = ""
	}
	return o, nil
}
