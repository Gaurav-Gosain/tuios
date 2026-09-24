package input

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleAggregateViewInput handles keyboard input when the aggregate view is open.
func handleAggregateViewInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// The selection moves and the renderer scrolls to keep it in view, which is
	// how every other list overlay works. This used to carry a hardcoded twelve
	// rows of its own, so on any screen not showing twelve the list scrolled at
	// the wrong time or not at all.
	if listKey(msg.String(), false, listPage, o.AggregateViewMove) {
		return o, nil
	}
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

	case "backspace":
		if len(o.AggregateViewQuery) > 0 {
			// A rune at a time. Taking a byte off splits a multi-byte character
			// and leaves invalid UTF-8 in the query.
			_, size := utf8.DecodeLastRuneInString(o.AggregateViewQuery)
			o.AggregateViewQuery = o.AggregateViewQuery[:len(o.AggregateViewQuery)-size]
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
