package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// Seams the tests reach for. Production code calls the functions these wrap.

// prefixMenuBindings is prefixMenuGroups as one list.
func (m *OS) prefixMenuBindings() []config.Keybinding {
	var out []config.Keybinding
	for _, g := range m.prefixMenuGroups() {
		out = append(out, g.Bindings...)
	}
	return out
}

// peakColumn is the column the light is brightest at on one row.
//
// The centre is a position along the sweep's own axis, not a column: for a
// diagonal the two differ by the row's share of the lean, and for a vertical
// sweep there is no column to speak of. Anything that wants to know where the
// light is on a given row has to go through this rather than reading centre.
func (b copyFlashBand) peakColumn(row int) float64 {
	if b.vertical {
		return 0
	}
	return b.centre + float64(row)*b.slope
}

// rows is how many rows the block covers.
func (b copyFlashBox) rows() int { return b.bottom - b.top + 1 }

// baseFacts is the whole set for a report with no model behind it.
func baseFacts(where string) []CrashFact {
	return append(crashHeadFacts(where), crashMachineFacts()...)
}

// occludersAbove is the on-screen rectangle of every window drawn over this
// one. A window that is not visible cannot cover anything, whatever geometry it
// is still carrying from the last time it was.
func occludersAbove(
	windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) []cellRect {
	return occludersAboveInto(nil, windowZ, allWindows, excludeWindowID)
}

// LineWidthFallbackCount reports how many times the width fast path has
// deferred to the reference implementation. Exposed for the render performance
// guards in the test suite.
func LineWidthFallbackCount() uint64 { return lineWidthFallbacks.Load() }

// navParseStyled parses one capture with a parser of its own. The load uses
// a navParser for each machine instead.
func navParseStyled(content string, ink navInk, keep int) (plain, styled []string) {
	p := newNavParser(ink)
	defer p.Close()
	return p.parse(content, keep)
}

// getWindowTitle returns the display name for a window, truncated to fit within maxWidth.
// Returns empty string if title should be hidden or doesn't fit.
// position is the window's 1-based place in its workspace, used by the {index}
// placeholder of appearance.window_title_format.
func getWindowTitle(window *terminal.Window, position int, maxWidth int, s *config.Settings) string {
	return windowTitleText(window, window.AgentState, position, maxWidth, s)
}

// scratchIndex is the index of a pane of the built-in scratch group, or -1.
func (m *OS) scratchIndex() int {
	return m.scratchIndexNamed(scratchName)
}

// Get retrieves a cached style or builds and caches it if not found.
// This is the main entry point for cached style access.
func (sc *StyleCache) Get(cell *uv.Cell, isCursor bool) lipgloss.Style {
	return sc.getEntry(cell, isCursor).style
}

// tapeIndicatorStatus exposes the current indicator status for tests and
// callers that need to assert what the passive surface is showing. The bool is
// false when no indicator is active.
func (m *OS) tapeIndicatorStatus() (trust.Status, bool) {
	if !m.tapeDetect.indicator.active {
		return trust.StatusUntrusted, false
	}
	return m.tapeDetect.indicator.status, true
}
