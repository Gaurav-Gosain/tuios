package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleSectionEditorInput handles keyboard input for the rail layout editor.
//
// The keys are the dock editor's, so a person who has laid out the dock already
// knows this panel: the arrows select, shifted arrows move the selected entry,
// Enter puts a section on the rail or takes it off. The left and right arrows
// are the one addition, and they walk the share.
func handleSectionEditorInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch keyStr := msg.String(); keyStr {
	case "esc":
		// Closes, keeping the layout. Every edit here was applied and saved as
		// it was made, so there is nothing pending for Esc to abandon.
		o.CloseSectionEditor()
	case "enter", "space":
		return o, o.SectionEditorToggle()
	case "up", "ctrl+p", "k":
		o.SectionEditorMove(-1)
	case "down", "ctrl+n", "j":
		o.SectionEditorMove(1)
	case "shift+up", "K":
		return o, o.SectionEditorShift(-1)
	case "shift+down", "J":
		return o, o.SectionEditorShift(1)
	case "left", "h":
		return o, o.SectionEditorShare(-1)
	case "right", "l":
		return o, o.SectionEditorShare(1)
	case "r":
		return o, o.SectionEditorReset()
	case "u":
		return o, o.SectionEditorRevert()
	}
	return o, nil
}
