package app

import "github.com/Gaurav-Gosain/tuios/internal/overlay"

// Overlay panels are laid out at a preferred size that suits a desktop terminal
// and then fitted to the screen they are actually drawn on. A small terminal,
// 51x37 say, is narrower and shorter than every panel wants to be, and a panel
// that ignores that draws its right-hand side off the edge of the screen where
// it cannot be read or scrolled to.
//
// The helpers here are the single place that arithmetic lives. Every panel asks
// for the width and row count it prefers and takes what fits; at a desktop size
// the preference always wins, so nothing about the desktop rendering changes.
const (
	// minPanelRows is the fewest body rows a scrolling panel is squeezed to.
	// Below this the panel stops being a list and the user cannot tell where
	// they are in it.
	minPanelRows = 3

	// panelChromeRows counts the rows an overlay panel always spends on chrome:
	// the top pad, the title, the blank below it, and the bottom pad.
	panelChromeRows = 4
)

// panelWidth returns the inner content width to build a panel at, given the
// width it would prefer. The screen wins when it is narrower.
func (m *OS) panelWidth(preferred int) int {
	return overlay.FitWidth(preferred, m.GetRenderWidth())
}

// panelBodyRows returns how many scrolling body rows a panel can show. extra is
// the number of body lines the panel spends on things that are not rows (a
// search input, a rule, a scroll indicator, a description line); tabs and hints
// are measured from the panel itself so their wrapped height is accounted for.
func (m *OS) panelBodyRows(preferred, extra, width int, tabs []string, hints []overlay.Hint) int {
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return preferred // size not known yet; the caller's preference stands
	}
	return max(min(preferred, rh-panelChrome(extra, width, tabs, hints)), minPanelRows)
}

// panelChrome is every row of a panel that is not a body row.
func panelChrome(extra, width int, tabs []string, hints []overlay.Hint) int {
	chrome := panelChromeRows + extra
	if len(tabs) > 0 {
		chrome += overlay.TabRowCount(tabs, width) + 2 // rule + blank
	}
	if len(hints) > 0 {
		chrome += 2 + overlay.HintRowCount(hints, width) // blank + rule + hints
	}
	return chrome
}

// panelBody fits both the row count and the footer to the screen height. On a
// screen too short to hold the minimum body and the footer both, the footer
// goes: the keys it names are still on the keyboard, whereas a body squeezed
// below three rows stops being a list.
func (m *OS) panelBody(preferred, extra, width int, tabs []string, hints []overlay.Hint) (int, []overlay.Hint) {
	rows := m.panelBodyRows(preferred, extra, width, tabs, hints)
	rh := m.GetRenderHeight()
	if rh <= 0 || len(hints) == 0 || rows+panelChrome(extra, width, tabs, hints) <= rh {
		return rows, hints
	}
	return m.panelBodyRows(preferred, extra, width, tabs, nil), nil
}

// scrollWindow clamps a scroll offset so that a list of count items showing
// visible rows keeps selected in view, and returns the offset to render from.
func scrollWindow(scroll, selected, count, visible int) int {
	if count <= visible {
		return 0
	}
	scroll = clampInt(scroll, 0, count-visible)
	if selected < scroll {
		scroll = selected
	}
	if selected >= scroll+visible {
		scroll = selected - visible + 1
	}
	return clampInt(scroll, 0, count-visible)
}
