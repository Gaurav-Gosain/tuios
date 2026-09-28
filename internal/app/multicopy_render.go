package app

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// multiCopyPill is the dock's mode label in multi copy mode: how many panes
// are in the mode, and after a search how many it found something in.
func (m *OS) multiCopyPill() (string, bool) {
	fw := m.GetFocusedWindow()
	if fw == nil || !m.MultiCopy.Has(fw.ID) {
		return "", false
	}
	matched, total := m.MultiCopyMatched()
	searched := len(m.MultiCopy.Parked) > 0
	for _, w := range m.MultiCopyWindows() {
		searched = searched || (w.CopyMode != nil && w.CopyMode.SearchQuery != "")
	}
	if searched {
		return fmt.Sprintf(" MULTI %d/%d ", matched, total), true
	}
	return fmt.Sprintf(" MULTI %d ", total), true
}

// copyModeHelp is the dock's copy-mode help for the focused pane: the plain
// copy-mode keys, or in multi copy mode the keys that act on the whole set
// first, with the current format among them.
func (m *OS) copyModeHelp(focused *terminal.Window) [][]overlay.Hint {
	mc := m.MultiCopy
	if !mc.Has(focused.ID) {
		return copyModeHelpTiers(focused.CopyMode.State)
	}
	format := overlay.Hint{Key: "tab", Label: "format: " + mc.Format}
	if mc.Save != nil {
		return [][]overlay.Hint{
			{
				{Key: overlay.EnterKey(), Label: "save"}, format,
				{Key: "ctrl+u", Label: "clear"}, {Key: "esc", Label: "cancel"},
			},
			{{Key: overlay.EnterKey(), Label: "save"}, format, {Key: "esc", Label: "cancel"}},
		}
	}
	state := focused.CopyMode.State
	if state == terminal.CopyModeSearch {
		return copyModeHelpTiers(state)
	}
	yank := overlay.Hint{Key: "y", Label: "yank all"}
	save := overlay.Hint{Key: "Y", Label: "save to file"}
	if state == terminal.CopyModeNormal {
		return [][]overlay.Hint{
			{
				{Key: "/", Label: "search all"}, {Key: "n/N", Label: "next"},
				{Key: "v/V", Label: "select"}, yank, save, format, {Key: "q", Label: "quit"},
			},
			{{Key: "/", Label: "search all"}, {Key: "V", Label: "select"}, yank, format},
			{yank, format},
		}
	}
	return [][]overlay.Hint{
		{{Key: "hjkl", Label: "extend"}, yank, save, format, {Key: "esc", Label: "cancel"}},
		{yank, save, format},
		{yank, format},
	}
}

// multiCopySaveLayer draws the save prompt over the bottom row of the focused
// pane, where copy mode's search prompt sits: the path being typed, and under
// it the reason the last save failed, if it did.
func (m *OS) multiCopySaveLayer() *lipgloss.Layer {
	mc := m.MultiCopy
	fw := m.GetFocusedWindow()
	if mc == nil || mc.Save == nil || fw == nil || !mc.Has(fw.ID) {
		return nil
	}
	pal := theme.UI()
	bg := pal.Surface
	body := overlay.Style(bg).Foreground(pal.FgMute).Render("Save to: ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(mc.Save.Path) +
		overlay.Cursor(" ", bg, pal.Fg)
	if mc.Save.Err != "" {
		body += overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("  " + mc.Save.Err)
	}
	pad := overlay.Style(bg).Render(" ")
	off := fw.BorderOffset()
	return lipgloss.NewLayer(pad + body + pad).
		X(fw.X + off + 1).
		Y(fw.Y + fw.Height - off - 1).
		Z(config.ZIndexHelp + 1).
		ID("multi-copy-save")
}
