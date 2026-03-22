package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleScrollbackSearchInput handles keyboard input when the scrollback search bar is open.
func handleScrollbackSearchInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	switch keyStr {
	case "esc":
		// Close search, restore scroll position
		o.ShowScrollbackSearch = false
		o.ScrollbackSearchQuery = ""
		o.ScrollbackSearchMatches = nil
		o.ScrollbackSearchCurrent = 0
		// Return to live output
		if window := o.GetFocusedWindow(); window != nil {
			window.ScrollbackOffset = 0
			window.ScrollbackMode = false
			window.InvalidateCache()
		}
		return o, nil

	case "enter":
		// Close search but keep the scroll position (stay at the match)
		o.ShowScrollbackSearch = false
		o.ScrollbackSearchQuery = ""
		o.ScrollbackSearchMatches = nil
		o.ScrollbackSearchCurrent = 0
		if window := o.GetFocusedWindow(); window != nil {
			window.InvalidateCache()
		}
		return o, nil

	case "backspace":
		if len(o.ScrollbackSearchQuery) > 0 {
			o.ScrollbackSearchQuery = o.ScrollbackSearchQuery[:len(o.ScrollbackSearchQuery)-1]
			o.ScrollbackSearchMatches = o.SearchScrollback(o.ScrollbackSearchQuery)
			o.ScrollbackSearchCurrent = 0
			if len(o.ScrollbackSearchMatches) > 0 {
				// Jump to last match (most recent, at bottom)
				o.ScrollbackSearchCurrent = len(o.ScrollbackSearchMatches) - 1
				o.ScrollToSearchMatch(o.ScrollbackSearchCurrent)
			}
		}
		return o, nil

	case "ctrl+n", "down":
		// Next match
		if len(o.ScrollbackSearchMatches) > 0 {
			o.ScrollbackSearchCurrent = (o.ScrollbackSearchCurrent + 1) % len(o.ScrollbackSearchMatches)
			o.ScrollToSearchMatch(o.ScrollbackSearchCurrent)
		}
		return o, nil

	case "ctrl+p", "up":
		// Previous match
		if len(o.ScrollbackSearchMatches) > 0 {
			o.ScrollbackSearchCurrent--
			if o.ScrollbackSearchCurrent < 0 {
				o.ScrollbackSearchCurrent = len(o.ScrollbackSearchMatches) - 1
			}
			o.ScrollToSearchMatch(o.ScrollbackSearchCurrent)
		}
		return o, nil

	case "ctrl+u":
		// Clear the query
		o.ScrollbackSearchQuery = ""
		o.ScrollbackSearchMatches = nil
		o.ScrollbackSearchCurrent = 0
		return o, nil

	default:
		// Accept printable characters
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.ScrollbackSearchQuery += keyStr
			o.ScrollbackSearchMatches = o.SearchScrollback(o.ScrollbackSearchQuery)
			o.ScrollbackSearchCurrent = 0
			if len(o.ScrollbackSearchMatches) > 0 {
				// Jump to last match (most recent, at bottom)
				o.ScrollbackSearchCurrent = len(o.ScrollbackSearchMatches) - 1
				o.ScrollToSearchMatch(o.ScrollbackSearchCurrent)
			}
			return o, nil
		}

		return o, nil
	}
}
