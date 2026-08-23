package app

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// SetPointerShape writes an OSC 22 sequence to change the mouse pointer.
//
// The sequence goes through writeHostSequence rather than os.Stdout: stdout is
// the right terminal only for a local run, while an SSH- or web-served client
// reads a different handle entirely, and the bytes used to land on the server's
// own console. writeHostSequence already routes to the per-mode host output.
func (m *OS) SetPointerShape(shape PointerShape) {
	if shape == m.currentPointer {
		return
	}
	m.currentPointer = shape
	m.writeHostSequence(fmt.Appendf(nil, "\x1b]22;%s\x1b\\", string(shape)))
}

// ResetPointerShape sets the pointer back to default.
func (m *OS) ResetPointerShape() {
	m.SetPointerShape(PointerDefault)
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
		m.SetPointerShape(PointerDefault)
		return
	}
	if config.DockbarPosition == "bottom" && y >= topMargin+m.GetUsableHeight() {
		m.SetPointerShape(PointerDefault)
		return
	}

	// In tiled mode with shared borders, check separator lines
	if m.panesBorderless() {
		// The same lines the separator overlay draws, so the resize cursor is
		// only offered over a divider that is really there.
		for _, s := range m.separatorSplits() {
			if s.Vertical && x == s.Pos && y >= s.From && y <= s.To {
				m.SetPointerShape(PointerEWResize)
				return
			}
			if !s.Vertical && y == s.Pos && x >= s.From && x <= s.To {
				m.SetPointerShape(PointerNSResize)
				return
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
		m.SetPointerShape(PointerDefault)
		return
	}

	win := m.Windows[topIdx]

	// A pane with no border of its own has no edge to offer, whatever the
	// setting says: BorderOffset is the pane's own answer, and the divider
	// between borderless panes was already handled above.
	borderOff := win.BorderOffset()
	if borderOff == 0 {
		m.SetPointerShape(PointerDefault)
		return
	}

	onLeft := x == win.X
	onRight := x == win.X+win.Width-1
	onTop := y == win.Y
	onBottom := y == win.Y+win.Height-1

	// Corners → diagonal resize
	if (onLeft && onTop) || (onRight && onBottom) {
		m.SetPointerShape(PointerNWSEResize)
		return
	}
	if (onRight && onTop) || (onLeft && onBottom) {
		m.SetPointerShape(PointerNESWResize)
		return
	}

	// Vertical edges → horizontal resize
	if onLeft || onRight {
		m.SetPointerShape(PointerEWResize)
		return
	}

	// Top border → grab (title bar)
	if onTop {
		m.SetPointerShape(PointerGrab)
		return
	}

	// Bottom border → vertical resize
	if onBottom {
		m.SetPointerShape(PointerNSResize)
		return
	}

	m.SetPointerShape(PointerDefault)
}
