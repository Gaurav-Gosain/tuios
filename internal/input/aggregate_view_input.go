package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleAggregateViewInput handles keyboard input when the aggregate view is open.
func handleAggregateViewInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	items := o.GetAggregateViewItems()
	filtered := app.FilterAggregateViewItems(items, o.AggregateViewQuery)

	switch msg.String() {
	case "esc", "ctrl+c":
		o.ShowAggregateView = false
		o.AggregateViewQuery = ""
		o.AggregateViewSelected = 0
		o.AggregateViewScroll = 0
		return o, nil

	case "enter":
		o.AggregateViewJump(o.AggregateViewSelected)
		return o, nil

	// The selection moves and the renderer scrolls to keep it in view, which is
	// how every other list overlay works. This used to carry a hardcoded twelve
	// rows of its own, so on any screen not showing twelve the list scrolled at
	// the wrong time or not at all.
	case "up", "ctrl+p":
		if o.AggregateViewSelected > 0 {
			o.AggregateViewSelected--
		}
		return o, nil

	case "down", "ctrl+n":
		if o.AggregateViewSelected < len(filtered)-1 {
			o.AggregateViewSelected++
		}
		return o, nil

	case "backspace":
		if len(o.AggregateViewQuery) > 0 {
			o.AggregateViewQuery = o.AggregateViewQuery[:len(o.AggregateViewQuery)-1]
			o.AggregateViewSelected = 0
			o.AggregateViewScroll = 0
		}
		return o, nil

	case "ctrl+u":
		o.AggregateViewQuery = ""
		o.AggregateViewSelected = 0
		o.AggregateViewScroll = 0
		return o, nil

	default:
		if msg.String() == "space" {
			o.AggregateViewQuery += " "
			o.AggregateViewSelected = 0
			o.AggregateViewScroll = 0
		} else if msg.Text != "" {
			o.AggregateViewQuery += msg.Text
			o.AggregateViewSelected = 0
			o.AggregateViewScroll = 0
		}
		return o, nil
	}
}
