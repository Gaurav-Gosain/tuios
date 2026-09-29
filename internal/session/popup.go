package session

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A popup is a floating pane that runs one command and closes when the command
// exits. The size the caller asks for travels with the window; the rectangle it
// resolves to does not, for the reason the zoom box does not (see
// WindowState.Zoomed): the box is measured against one client's content region,
// and a peer of another size has to measure it against its own. So the daemon
// keeps the request as the caller wrote it and every client resolves it here.

const (
	// PopupDefaultWidth and PopupDefaultHeight are the size a popup takes when
	// the caller names none. They are shares rather than cells so the default
	// reads the same on a phone-sized terminal and on a wall.
	PopupDefaultWidth  = "80%"
	PopupDefaultHeight = "60%"

	// PopupMinWidth and PopupMinHeight are the smallest box a popup is given
	// when there is room for it. A smaller region overrides them: see
	// ResolvePopupSize.
	PopupMinWidth  = 10
	PopupMinHeight = 3
)

// ParsePopupSize reads one --width or --height value. A bare number is cells, a
// number with a trailing percent sign is a share of the region the popup sits
// in. An empty spec is not a value and is reported as such, so a caller can tell
// "the user said nothing" from "the user said 0".
func ParsePopupSize(spec string) (value int, percent bool, err error) {
	return config.ParseBoxSize(spec)
}

// ValidatePopupSize reports why a size spec cannot be used, or nil. An empty
// spec is allowed: it means the default.
func ValidatePopupSize(spec string) error {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	_, _, err := ParsePopupSize(spec)
	return err
}

// ResolvePopupSize turns one size spec into cells, measured against extent, the
// width or height of the region the popup goes in. An empty or unreadable spec
// falls back to the default, because a client draws what it has rather than
// refusing to draw.
//
// A request larger than the region gives up the space, not the region: the
// popup is cut down to the region rather than drawn outside it. The floor works
// the same way. PopupMinWidth is a floor only while the region can hold it, so a
// region narrower than the floor yields a popup as wide as the region and never
// one that overhangs it.
func ResolvePopupSize(spec, fallback string, extent, floor int) int {
	if extent <= 0 {
		return 0
	}
	value, percent, err := ParsePopupSize(spec)
	if err != nil {
		value, percent, err = ParsePopupSize(fallback)
		if err != nil {
			return extent
		}
	}
	size := value
	if percent {
		size = extent * value / 100
	}
	size = min(size, extent)
	size = max(size, min(floor, extent))
	return max(size, 1)
}

// popupContentSize is the size a popup's command gets before any client has
// placed it: the box a client resolves (see popupRect in internal/app) less
// the border, measured against the session's size less its agreed chrome
// reserve. That is the content region every client lays panes out in, so the
// placement that follows does not change the size.
func popupContentSize(sessW, sessH int, r LayoutReserve, width, height string) (cols, rows int) {
	regionW := max(sessW-r.Left-r.Right, 1)
	regionH := max(sessH-r.Top-r.Bottom, 1)
	boxW := ResolvePopupSize(width, PopupDefaultWidth, regionW, PopupMinWidth)
	boxH := ResolvePopupSize(height, PopupDefaultHeight, regionH, PopupMinHeight)
	return max(boxW-2, 1), max(boxH-2, 1)
}
