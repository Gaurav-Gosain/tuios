package app

import (
	"fmt"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// PointerShape represents a CSS cursor shape name for OSC 22.
type PointerShape string

const (
	PointerDefault    PointerShape = "default"
	PointerGrab       PointerShape = "grab"
	PointerGrabbing   PointerShape = "grabbing"
	PointerEWResize   PointerShape = "ew-resize"
	PointerNSResize   PointerShape = "ns-resize"
	PointerNWSEResize PointerShape = "nwse-resize"
	PointerNESWResize PointerShape = "nesw-resize"
)

// currentPointer tracks the last shape to avoid redundant writes.
var currentPointer PointerShape

// SetPointerShape writes an OSC 22 sequence to change the mouse pointer.
func SetPointerShape(shape PointerShape) {
	if shape == currentPointer {
		return
	}
	currentPointer = shape
	fmt.Fprintf(os.Stdout, "\x1b]22;%s\x1b\\", string(shape))
}

// ResetPointerShape sets the pointer back to default.
func ResetPointerShape() {
	SetPointerShape(PointerDefault)
}

// UpdatePointerForPosition sets the pointer shape based on what the mouse
// is hovering over: window borders, corners, separator lines, title bars.
func (m *OS) UpdatePointerForPosition(x, y int) {
	if m.Dragging || m.Resizing {
		return
	}

	// Check dock area
	topMargin := m.GetTopMargin()
	if config.DockbarPosition == "top" && y < topMargin {
		SetPointerShape(PointerDefault)
		return
	}
	if config.DockbarPosition == "bottom" && y >= topMargin+m.GetUsableHeight() {
		SetPointerShape(PointerDefault)
		return
	}

	// In tiled mode with shared borders, check separator lines
	if m.AutoTiling && config.SharedBorders && !m.UseScrollingLayout {
		tree := m.GetOrCreateBSPTree()
		if tree != nil {
			bounds := layout.Rect{X: 0, Y: topMargin, W: m.GetRenderWidth(), H: m.GetUsableHeight()}
			for _, s := range tree.CollectSplits(bounds) {
				if s.Vertical && x == s.Pos && y >= s.From && y <= s.To {
					SetPointerShape(PointerEWResize)
					return
				}
				if !s.Vertical && y == s.Pos && x >= s.From && x <= s.To {
					SetPointerShape(PointerNSResize)
					return
				}
			}
		}
	}

	// Find window under cursor (topmost by Z)
	topIdx := -1
	topZ := -1
	for i, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}
		if x >= win.X && x < win.X+win.Width && y >= win.Y && y < win.Y+win.Height {
			if win.Z > topZ {
				topZ = win.Z
				topIdx = i
			}
		}
	}

	if topIdx == -1 {
		SetPointerShape(PointerDefault)
		return
	}

	win := m.Windows[topIdx]

	// Tiled windows with shared borders have no individual border
	if win.Tiled && config.SharedBorders {
		SetPointerShape(PointerDefault)
		return
	}

	borderOff := win.BorderOffset()
	if borderOff == 0 {
		SetPointerShape(PointerDefault)
		return
	}

	onLeft := x == win.X
	onRight := x == win.X+win.Width-1
	onTop := y == win.Y
	onBottom := y == win.Y+win.Height-1

	// Corners → diagonal resize
	if (onLeft && onTop) || (onRight && onBottom) {
		SetPointerShape(PointerNWSEResize)
		return
	}
	if (onRight && onTop) || (onLeft && onBottom) {
		SetPointerShape(PointerNESWResize)
		return
	}

	// Vertical edges → horizontal resize
	if onLeft || onRight {
		SetPointerShape(PointerEWResize)
		return
	}

	// Top border → grab (title bar)
	if onTop {
		SetPointerShape(PointerGrab)
		return
	}

	// Bottom border → vertical resize
	if onBottom {
		SetPointerShape(PointerNSResize)
		return
	}

	SetPointerShape(PointerDefault)
}
