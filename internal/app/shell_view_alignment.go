package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// normalizeShellContent changes only the client's drawing of a daemon-backed
// shell. A narrow rail and the center pane may have different leading empty
// guest rows after shell startup or reflow. Preserve the daemon's grid and
// scrollback, and shift at most two *empty* rows out of the visible content;
// the bottom rows become empty instead. The guest cursor and mouse coordinates
// use DisplayRowOffset to refer to the original, unmodified grid.
func (m *OS) normalizeShellContent(window *terminal.Window, content string) string {
	window.DisplayRowOffset = 0
	if window.PTYID == "" || window.Terminal == nil || window.ForegroundCmd != "" ||
		window.IsAltScreen() || window.CopyModeVisible() || window.ScrollbackOffset > 0 ||
		m.sessionView.on || window.ContentHeight() < 4 {
		return content
	}
	// The writer may be processing a burst. Never hold up a frame to measure
	// its leading cells; when the burst ends the next frame can normalize it.
	if !window.TryRLockIO() {
		return content
	}
	// Stream-owned grids may briefly retain their old dimensions during a
	// resize. Do not mistake the as-yet-unrendered rows for shell whitespace.
	if window.Terminal.Width() != window.ContentWidth() || window.Terminal.Height() != window.ContentHeight() {
		window.RUnlockIO()
		return content
	}
	offset := 0
	for y := 0; y < min(window.ContentHeight(), 4); y++ {
		visible := false
		for x := 0; x < window.ContentWidth(); x++ {
			cell := window.Terminal.CellAt(x, y)
			if cell != nil && (strings.TrimSpace(cell.Content) != "" || !isNilColor(cell.Style.Bg)) {
				visible = true
				break
			}
		}
		if visible {
			offset = max(0, y-1)
			break
		}
	}
	window.RUnlockIO()
	if offset == 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) != window.ContentHeight() {
		return content
	}
	window.DisplayRowOffset = offset
	// Each row from renderTerminal has already been shaped to the pane's
	// content width. Shift complete styled rows, not bytes or terminal cells.
	// Box rendering then adds its normal left/right and bottom borders.
	shifted := append([]string(nil), lines[offset:]...)
	for len(shifted) < len(lines) {
		shifted = append(shifted, strings.Repeat(" ", window.ContentWidth()))
	}
	return strings.Join(shifted, "\n")
}
