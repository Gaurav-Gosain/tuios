package layout

import "slices"

// The master ratio's range and its default, in percent. The master-stack
// tiler clamps to the same range, and it matches the resize_width_N actions,
// which go from 10 to 90 percent.
const (
	MasterRatioMin     = 10
	MasterRatioMax     = 90
	MasterRatioDefault = 50
)

// Where the master-stack layout puts the master panes. Left is the layout as it
// was before the position could change. Center puts the masters in a middle
// column and deals the stack to the right and the left in turn, the way
// xmonad's ThreeColMid and Hyprland's center orientation do.
const (
	MasterPositionLeft   = "left"
	MasterPositionRight  = "right"
	MasterPositionTop    = "top"
	MasterPositionBottom = "bottom"
	MasterPositionCenter = "center"
)

// MasterPositions is the accepted set, in the order cycle_master_position
// steps through them.
var MasterPositions = []string{
	MasterPositionLeft,
	MasterPositionRight,
	MasterPositionTop,
	MasterPositionBottom,
	MasterPositionCenter,
}

// ValidMasterPosition is pos when it is one of MasterPositions, and left
// otherwise.
func ValidMasterPosition(pos string) string {
	if IsMasterPosition(pos) {
		return pos
	}
	return MasterPositionLeft
}

// IsMasterPosition reports whether pos is one of MasterPositions.
func IsMasterPosition(pos string) bool {
	return slices.Contains(MasterPositions, pos)
}

// The default size of a new window, in cells.
const (
	DefaultWindowWidth  = 20
	DefaultWindowHeight = 5
)
