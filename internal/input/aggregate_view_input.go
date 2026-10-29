package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleAggregateViewInput handles keyboard input when the aggregate view is open.
func handleAggregateViewInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// The selection moves and the renderer scrolls to keep it in view, which is
	// how every other list overlay works. A row count of its own would scroll
	// at the wrong time on any screen not showing exactly that many rows.
	if listKey(o, commandKey(msg), false, listPage, o.AggregateViewMove) {
		return o, nil
	}
	switch commandKey(msg) {
	case "esc", "ctrl+c":
		o.ShowAggregateView = false
		o.AggregateViewQuery = ""
		o.AggregateViewSelected = 0
		o.AggregateViewScroll = 0
		return o, nil

	case "enter":
		o.AggregateViewJump(o.AggregateViewSelected)
		return o, nil

	default:
		if changed, _ := editFilterQuery(msg, &o.AggregateViewQuery, true); changed {
			o.AggregateViewSelected = 0
			o.AggregateViewScroll = 0
		}
		return o, nil
	}
}
