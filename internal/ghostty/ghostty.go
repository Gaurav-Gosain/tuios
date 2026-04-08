// Package ghostty provides Go bindings for libghostty-vt, Ghostty's
// headless VT terminal emulator. Used in daemon mode for high-performance
// terminal state management with built-in dirty tracking.
//
// Build requires libghostty-vt.a (from ghostty's zig build system).
// Set CGO_CFLAGS and CGO_LDFLAGS to point to the ghostty include/lib dirs.
package ghostty

/*
#cgo CFLAGS: -I/home/gaurav/dev/ghostty/zig-out/include
#cgo LDFLAGS: -L/home/gaurav/dev/ghostty/zig-out/lib -lghostty-vt -lm -lpthread -lc

#include <ghostty/vt.h>
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// Terminal wraps a libghostty-vt terminal instance.
type Terminal struct {
	term  C.GhosttyTerminal
	render C.GhosttyRenderState
}

// NewTerminal creates a new ghostty terminal with the given dimensions.
func NewTerminal(cols, rows int) (*Terminal, error) {
	var term C.GhosttyTerminal
	opts := C.GhosttyTerminalOptions{
		cols:           C.uint16_t(cols),
		rows:           C.uint16_t(rows),
		max_scrollback: C.size_t(10000),
	}

	result := C.ghostty_terminal_new(nil, &term, opts)
	if result != 0 {
		return nil, fmt.Errorf("ghostty_terminal_new failed: %d", result)
	}

	// Create render state for incremental dirty tracking
	var render C.GhosttyRenderState
	result = C.ghostty_render_state_new(nil, &render)
	if result != 0 {
		C.ghostty_terminal_free(term)
		return nil, fmt.Errorf("ghostty_render_state_new failed: %d", result)
	}

	return &Terminal{term: term, render: render}, nil
}

// Free releases the terminal and render state resources.
func (t *Terminal) Free() {
	if t.render != nil {
		C.ghostty_render_state_free(t.render)
		t.render = nil
	}
	if t.term != nil {
		C.ghostty_terminal_free(t.term)
		t.term = nil
	}
}

// Write feeds VT-encoded bytes to the terminal for processing.
func (t *Terminal) Write(data []byte) {
	if len(data) == 0 || t.term == nil {
		return
	}
	C.ghostty_terminal_vt_write(
		t.term,
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
	)
}

// Resize changes the terminal dimensions.
func (t *Terminal) Resize(cols, rows, cellWidthPx, cellHeightPx int) error {
	result := C.ghostty_terminal_resize(
		t.term,
		C.uint16_t(cols),
		C.uint16_t(rows),
		C.uint32_t(cellWidthPx),
		C.uint32_t(cellHeightPx),
	)
	if result != 0 {
		return fmt.Errorf("ghostty_terminal_resize failed: %d", result)
	}
	return nil
}

// DirtyState represents the type of dirty tracking.
type DirtyState int

const (
	DirtyFalse   DirtyState = 0
	DirtyPartial DirtyState = 1
	DirtyFull    DirtyState = 2
)

// UpdateRenderState snapshots the terminal state for rendering.
// Returns the dirty state (false/partial/full).
func (t *Terminal) UpdateRenderState() DirtyState {
	if t.render == nil || t.term == nil {
		return DirtyFalse
	}
	result := C.ghostty_render_state_update(t.render, t.term)
	if result != 0 {
		return DirtyFalse
	}

	var dirty C.GhosttyRenderStateDirty
	C.ghostty_render_state_get(
		t.render,
		C.GHOSTTY_RENDER_STATE_DATA_DIRTY,
		unsafe.Pointer(&dirty),
	)
	return DirtyState(dirty)
}

// CursorInfo holds cursor position and visibility.
type CursorInfo struct {
	X, Y    int
	Visible bool
}

// GetCursor returns the cursor position from the render state.
func (t *Terminal) GetCursor() CursorInfo {
	var info CursorInfo
	if t.render == nil {
		return info
	}

	var x, y C.uint16_t
	var visible C.bool
	var hasValue C.bool

	C.ghostty_render_state_get(t.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_X, unsafe.Pointer(&x))
	C.ghostty_render_state_get(t.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_Y, unsafe.Pointer(&y))
	C.ghostty_render_state_get(t.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VISIBLE, unsafe.Pointer(&visible))
	C.ghostty_render_state_get(t.render, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_HAS_VALUE, unsafe.Pointer(&hasValue))

	if bool(hasValue) {
		info.X = int(x)
		info.Y = int(y)
	}
	info.Visible = bool(visible)
	return info
}

// GetDimensions returns cols and rows from the render state.
func (t *Terminal) GetDimensions() (cols, rows int) {
	if t.render == nil {
		return 0, 0
	}
	var c, r C.uint16_t
	C.ghostty_render_state_get(t.render, C.GHOSTTY_RENDER_STATE_DATA_COLS, unsafe.Pointer(&c))
	C.ghostty_render_state_get(t.render, C.GHOSTTY_RENDER_STATE_DATA_ROWS, unsafe.Pointer(&r))
	return int(c), int(r)
}
