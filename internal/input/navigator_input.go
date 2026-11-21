package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleChooseTree(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenNavigator()
}

func handleNavigatorInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if o.NavigatorSearching() {
		if listKey(o, key, false, listPage, o.NavigatorMove) {
			return o, nil
		}
		switch key {
		case "esc":
			if o.NavigatorTypeFirst() && o.NavigatorQuery() == "" {
				o.CloseNavigator()
				return o, nil
			}
			o.NavigatorSearch(false)
		case "enter":
			o.NavigatorActivateSelected()
			return o, o.TakeSidebarCmd()
		default:
			o.NavigatorType(key, msg.Text)
		}
		return o, nil
	}

	if listKey(o, key, true, listPage, o.NavigatorMove) {
		return o, nil
	}
	switch key {
	case "esc", "q":
		if key == "esc" && o.NavigatorQuery() != "" {
			o.NavigatorSetQuery("")
			return o, nil
		}
		o.CloseNavigator()
	case "enter":
		o.NavigatorActivateSelected()
		return o, o.TakeSidebarCmd()
	case "/":
		o.NavigatorSearch(true)
	case "h", "left":
		o.NavigatorFold(false)
	case "l", "right":
		o.NavigatorFold(true)
	case "space":
		o.NavigatorToggle()
	case "v":
		o.NavigatorCycleLayout()
	}
	return o, nil
}
