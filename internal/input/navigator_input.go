package input

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleChooseTree is the choose_tree action: prefix / by default.
func handleChooseTree(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenNavigator()
}

// handleNavigatorInput takes a key while the navigator is up. It owns the
// keyboard, in either mode.
//
// In the list:
//   - the arrows, j and k, ctrl+n and ctrl+p, g, G, home, end and the page
//     keys move
//   - h and left shut a row or go to its parent, l and right open it, space
//     opens or shuts it
//   - enter goes to the row
//   - / moves to the search line
//   - esc and q close
//
// In the search line every printable key is text, the movement keys that are
// not letters still move, enter goes to the row, and esc leaves the search:
// the first esc keeps the query, and a second clears it.
func handleNavigatorInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if o.NavigatorSearching() {
		if listKey(key, false, listPage, o.NavigatorMove) {
			return o, nil
		}
		switch key {
		case "esc":
			o.NavigatorSearch(false)
		case "enter":
			o.NavigatorActivateSelected()
		case "backspace":
			if q := o.NavigatorQuery(); q != "" {
				_, size := utf8.DecodeLastRuneInString(q)
				o.NavigatorSetQuery(q[:len(q)-size])
			}
		case "ctrl+u":
			o.NavigatorSetQuery("")
		default:
			text := msg.Text
			if key == "space" {
				text = " "
			}
			if text != "" {
				o.NavigatorSetQuery(o.NavigatorQuery() + text)
			}
		}
		return o, nil
	}

	if listKey(key, true, listPage, o.NavigatorMove) {
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
	case "/":
		o.NavigatorSearch(true)
	case "h", "left":
		o.NavigatorFold(false)
	case "l", "right":
		o.NavigatorFold(true)
	case "space":
		o.NavigatorToggle()
	}
	return o, nil
}
