package input

import (
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
//   - v steps the layout: tree, flat, cards
//   - / moves to the search line
//   - esc and q close
//
// In the search line every printable key is text, the movement keys that are
// not letters still move, enter goes to the row, and esc leaves the search:
// the first esc keeps the query, and a second clears it. A navigator that
// opened on the search line (panes.navigator_search) closes on an esc with
// nothing typed, as the list does.
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
			// A session on another machine opens off the Update goroutine.
			return o, o.TakeSidebarCmd()
		default:
			q := o.NavigatorQuery()
			if changed, _ := editFilterQuery(msg, &q, true); changed {
				o.NavigatorSetQuery(q)
			}
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
		// A session on another machine opens off the Update goroutine.
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
