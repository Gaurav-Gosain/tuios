package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleSectionEditorInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(o, msg.String(), true, listPage, o.SectionEditorMove) {
		return o, nil
	}
	switch keyStr := msg.String(); keyStr {
	case "esc":
		o.CloseSectionEditor()
	case "enter", "space":
		return o, o.SectionEditorToggle()
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
