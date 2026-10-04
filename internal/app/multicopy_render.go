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

// copyModeHelp is the copy-mode legend for the focused pane (see
// mode_legend.go): the plain copy-mode keys, or in multi copy mode the keys
// that act on the whole set first, with the current format among them.
func (m *OS) copyModeHelp(focused *terminal.Window) []overlay.Hint {
	mc := m.MultiCopy
	if !mc.Has(focused.ID) {
		return copyModeLegend(focused.CopyMode.State)
	}
	format := overlay.Hint{Key: "tab", Label: "format: " + mc.Format}
	if mc.Save != nil {
		return []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "save", Priority: overlay.HintEssential},
			format,
			{Key: "ctrl+u", Label: "clear", Priority: overlay.HintOptional},
			{Key: "esc", Label: "cancel"},
		}
	}
	// The lead's state is what the keys mean, so it is what the help says.
	state := focused.CopyMode.State
	if lead := m.MultiCopyLead(focused); lead != nil && lead.CopyMode != nil {
		state = lead.CopyMode.State
	}
	if state == terminal.CopyModeSearch {
		return copyModeLegend(state)
	}
	yank := overlay.Hint{Key: "y", Label: "yank all", Priority: overlay.HintEssential}
	save := overlay.Hint{Key: "Y", Label: "save to file", Priority: overlay.HintOptional}
	format.Priority = overlay.HintEssential
	if state == terminal.CopyModeNormal {
		return []overlay.Hint{
			{Key: "/", Label: "search all"},
			{Key: "n/N", Label: "next", Priority: overlay.HintOptional},
			{Key: "v/V", Label: "select"},
			yank, save, format,
			{Key: "q/esc", Label: "quit", Priority: overlay.HintEssential},
		}
	}
	return []overlay.Hint{
		{Key: "hjkl", Label: "extend", Priority: overlay.HintOptional},
		yank, save, format,
		{Key: "esc", Label: "cancel"},
	}
}

// multiCopySaveLayer draws the save prompt at the bottom of the focused pane,
// where copy mode's search prompt sits. It is two rows: above, the full path
// the text names (so a relative or ~ path is never a guess) or the reason the
// last save failed; below, the path being typed. Each row is cut to the pane's
// width so it never runs over the next pane.
func (m *OS) multiCopySaveLayer() *lipgloss.Layer {
	mc := m.MultiCopy
	fw := m.GetFocusedWindow()
	if mc == nil || mc.Save == nil || fw == nil || !mc.Has(fw.ID) {
		return nil
	}
	pal := theme.UI()
	bg := pal.Surface
	off := fw.BorderOffset()
	width := max(fw.Width-2*off-2, 12)

	var info string
	switch full, err := m.MultiCopySaveResolved(); {
	case mc.Save.Err != "":
		info = overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).
			Render(overlay.Truncate(mc.Save.Err, width-2))
	case err == nil:
		info = overlay.Style(bg).Foreground(pal.FgMute).
			Render(overlay.Truncate("File: "+full, width-2))
	}
	// The typed text keeps its end in view: that is where the cursor is.
	path := mc.Save.Path
	room := width - 2 - len("Save to: ") - 1
	if r := []rune(path); len(r) > room && room > 1 {
		path = "…" + string(r[len(r)-room+1:])
	}
	input := overlay.Style(bg).Foreground(pal.FgMute).Render("Save to: ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(path) +
		overlay.Cursor(" ", bg, pal.Fg)

	pad := overlay.Style(bg).Render(" ")
	body := pad + input + pad
	y := fw.Y + fw.Height - off - 1
	if info != "" {
		body = pad + info + pad + "\n" + body
		y--
	}
	x, y := m.paneChromeAt(fw.X+off+1, y, lipgloss.Width(body), lipgloss.Height(body))
	return lipgloss.NewLayer(body).
		X(x).
		Y(y).
		Z(config.ZIndexHelp + 1).
		ID("multi-copy-save")
}
