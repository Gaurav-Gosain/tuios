package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

var fuzzInput InputHandler

func SetFuzzInput(h InputHandler) { fuzzInput = h }

func (m *OS) prefixMenuBindings() []config.Keybinding {
	var out []config.Keybinding
	for _, g := range m.prefixMenuGroups() {
		out = append(out, g.Bindings...)
	}
	return out
}

func (b copyFlashBand) peakColumn(row int) float64 {
	if b.vertical {
		return 0
	}
	return b.centre + float64(row)*b.slope
}

func (b copyFlashBox) rows() int { return b.bottom - b.top + 1 }

func baseFacts(where string) []CrashFact {
	return append(crashHeadFacts(where), crashMachineFacts()...)
}

func occludersAbove(
	windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) []cellRect {
	return occludersAboveInto(nil, windowZ, allWindows, excludeWindowID)
}

func LineWidthFallbackCount() uint64 { return lineWidthFallbacks.Load() }

func navParseStyled(content string, ink navInk, keep int) (plain, styled []string) {
	p := newNavParser(ink)
	defer p.Close()
	return p.parse(content, keep)
}

func getWindowTitle(window *terminal.Window, position int, maxWidth int, s *config.Settings) string {
	return windowTitleText(window, window.AgentState, position, maxWidth, s)
}

func (m *OS) scratchIndex() int {
	return m.scratchIndexNamed(scratchName)
}

func (sc *StyleCache) Get(cell *uv.Cell, isCursor bool) lipgloss.Style {
	return sc.getEntry(cell, isCursor).style
}

func (m *OS) tapeIndicatorStatus() (trust.Status, bool) {
	if !m.tapeDetect.indicator.active {
		return trust.StatusUntrusted, false
	}
	return m.tapeDetect.indicator.status, true
}
