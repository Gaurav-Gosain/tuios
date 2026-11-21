package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lrstanley/go-nf/glyphs/fa"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type dockButton struct {
	label  string
	action string
	nerd   string
	ascii  string
	hint   string
}

var dockButtons = [...]dockButton{
	{"Menu", config.OpenMenuPrefix + "start", fa.Bars.String(), "=", "Open the menu"},
	{"New", "new_window", fa.Plus.String(), "+", "Open a new terminal"},
	{"Split", "split_vertical", fa.Columns.String(), "|", "Split this pane side by side"},
	{"Close", "close_window", fa.Times.String(), "x", "Close this pane"},
	{"Help", "toggle_help", fa.Question.String(), "?", "Show help"},
	{"Settings", "open_settings", fa.Cog.String(), "*", "Open settings"},
}

const dockButtonMenu = 0

func (m *OS) dockButtonRow() []int {
	first := dockButtonMenu + 1
	if m.Settings.Simple {
		first = dockButtonMenu
	}
	row := make([]int, 0, len(dockButtons))
	for i := first; i < len(dockButtons); i++ {
		row = append(row, i)
	}
	return row
}

const dockButtonMore = len(dockButtons)

type dockButtonsFit struct {
	compact bool
	shown   int
}

func (m *OS) dockButtonIcon(i int) string {
	if i == dockButtonMore {
		if m.Settings.NerdFontsOff() {
			return ">"
		}
		return fa.EllipsisH.String()
	}
	if m.Settings.NerdFontsOff() {
		return dockButtons[i].ascii
	}
	return dockButtons[i].nerd
}

func (m *OS) renderDockButtons(fit dockButtonsFit) (string, []dockCustomHit) {
	pal := m.groundUI()
	var b strings.Builder
	var hits []dockCustomHit
	x := 0
	draw := func(i int, text string) {
		bg, fg := pal.Card, theme.Readable(pal.Fg, pal.Card)
		if m.dockButtonHover == i+1 {
			bg, fg = pal.Accent, theme.Readable(pal.PillFg, pal.Accent)
		}
		st := lipgloss.NewStyle().Foreground(fg)
		if !overlay.IsNoColor(bg) {
			st = st.Background(bg)
		}
		if x > 0 {
			b.WriteString(" ")
			x++
		}
		cell := st.Render(" " + text + " ")
		w := lipgloss.Width(cell)
		hits = append(hits, dockCustomHit{X0: x, X1: x + w, Name: config.DockComponentButtons, Button: i + 1})
		b.WriteString(cell)
		x += w
	}
	row := m.dockButtonRow()
	for _, i := range row[:min(fit.shown, len(row))] {
		if fit.compact {
			draw(i, m.dockButtonIcon(i))
		} else {
			draw(i, dockButtons[i].label)
		}
	}
	if fit.shown < len(row) {
		draw(dockButtonMore, m.dockButtonIcon(dockButtonMore))
	}
	m.dockButtonsShown = fit.shown
	return b.String(), hits
}

func (m *OS) dockButtonsWidth(fit dockButtonsFit) int {
	if !m.dockPlan.Has(config.DockComponentButtons) {
		return 0
	}
	shown := m.dockButtonsShown
	text, _ := m.renderDockButtons(fit)
	m.dockButtonsShown = shown
	return lipgloss.Width(text)
}

// DockButtonAt returns the button covering the absolute cell (x, y): an index
// into the row, dockButtonMore for the more button, or -1.
func (m *OS) DockButtonAt(x, y int) int {
	for _, h := range m.dockCustomHits {
		if h.Button > 0 && y == h.Y && x >= h.X0 && x < h.X1 {
			return h.Button - 1
		}
	}
	return -1
}

// PressDockButton answers a click on button i. It returns the action to run,
// or "" when the click opened the more menu instead.
func (m *OS) PressDockButton(i, x, y int) string {
	if i == dockButtonMenu {
		cm := &ContextMenu{Title: "Menu", Target: CtxTargetDock, WindowIndex: -1, AnchorX: x, AnchorY: y, ItemH: 1}
		cm.Items = m.menu("start", 0)
		cm.home()
		m.ContextMenu = cm
		return ""
	}
	if i >= 0 && i < len(dockButtons) {
		return dockButtons[i].action
	}
	if i != dockButtonMore {
		return ""
	}
	cm := &ContextMenu{Title: "More", Target: CtxTargetDock, WindowIndex: -1, AnchorX: x, AnchorY: y, ItemH: 1}
	row := m.dockButtonRow()
	for _, j := range row[min(m.dockButtonsShown, len(row)):] {
		cm.Items = append(cm.Items, m.item(m.dockButtonIcon(j), dockButtons[j].label, dockButtons[j].action, false))
	}
	cm.home()
	m.ContextMenu = cm
	return ""
}

// DockButtonHoverAt points the hover at the button under (x, y) and arms its
// label. It reports whether the pointer is on a button.
func (m *OS) DockButtonHoverAt(x, y int) bool {
	x, y = m.ScreenPoint(x, y)
	i := m.DockButtonAt(x, y)
	m.dockButtonHover = i + 1
	if i < 0 {
		if m.Tooltip.Source == tooltipDockButton {
			m.tooltipClear()
		}
		return false
	}
	m.tooltipTrack(tooltipDockButton, i)
	return true
}

func dockButtonTooltipText(i int) string {
	if i == dockButtonMore {
		return "More buttons"
	}
	return dockButtons[i].hint
}

func (m *OS) renderDockButtonTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipDockButton) {
		return nil
	}
	m.Tooltip.Shown = true
	for _, h := range m.dockCustomHits {
		if h.Button != m.Tooltip.Key+1 {
			continue
		}
		label := tooltipLabel(dockButtonTooltipText(m.Tooltip.Key), m.GetRenderWidth(), theme.UI())
		return tooltipLayer(label, overlay.Rect{X0: h.X0, Y0: h.Y, X1: h.X1, Y1: h.Y + 1}, m.dockTooltipSide(), m.screenRect(), "dock-button-tooltip")
	}
	return nil
}
