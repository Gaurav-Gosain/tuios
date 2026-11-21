package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	contextMenuMinWidth = 16
	contextMenuMaxWidth = 36
	contextMenuGap      = 2
)

func (cm *ContextMenu) menuTitle() string { return printableTitle(cm.Title) }

func (m *OS) contextMenuWidth(cm *ContextMenu) int {
	widest := lipgloss.Width(cm.menuTitle()) + 2
	for _, it := range cm.Items {
		if it.Sep {
			continue
		}
		w := lipgloss.Width(contextMenuRowLeft(it, &m.Settings)) + lipgloss.Width(it.Hint)
		if it.Hint != "" {
			w += contextMenuGap
		}
		if w > widest {
			widest = w
		}
	}
	widest = max(min(widest, contextMenuMaxWidth), contextMenuMinWidth)
	return m.panelWidth(widest)
}

func contextMenuRowLeft(it ContextMenuItem, s *config.Settings) string {
	icon := ""
	if it.Icon != "" && !s.NerdFontsOff() {
		icon = it.Icon + " "
	}
	return "  " + icon + it.Label
}

func (m *OS) renderContextMenu() (string, overlay.Geometry) {
	cm := m.ContextMenu
	if cm == nil {
		return "", overlay.Geometry{}
	}

	pal := m.overlayUI()
	bg := pal.Surface
	width := m.contextMenuWidth(cm)

	start, visible := m.contextMenuRows(cm)
	cm.ScrollFrom = start

	lines := make([]string, 0, visible)
	for i := start; i < start+visible; i++ {
		it := cm.Items[i]
		if it.Sep {
			lines = append(lines, overlay.Rule(width, bg, pal))
			continue
		}
		lines = append(lines, m.contextMenuRow(it, i == cm.List.Cursor, width, bg, pal))
	}

	panel := overlay.Panel{
		Title: cm.menuTitle(),
		Width: width,
		Body:  strings.Join(lines, "\n"),
	}
	return panel.Render(pal)
}

func (m *OS) contextMenuRows(cm *ContextMenu) (start, visible int) {
	count := len(cm.Items)
	if m.GetRenderHeight() <= 0 {
		return 0, count
	}
	visible = m.contextMenuVisible(count)
	cm.List.Scroll = listnav.Scroll(cm.List.Scroll, cm.List.Cursor, count, visible)
	return cm.List.Scroll, visible
}

func (m *OS) contextMenuVisible(count int) int {
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return count
	}
	return min(count, max(rh-panelChromeRows, 1))
}

func (m *OS) contextMenuRow(it ContextMenuItem, selected bool, width int, bg color.Color, pal overlay.Palette) string {
	st := overlay.RowState{Cursor: selected && !it.Dim, Focused: true}
	rowBg := pal.Ground(st, bg)

	labelColor := pal.Fg
	iconColor := pal.AccentBright
	hintColor := pal.FgDim
	switch {
	case it.Dim:
		labelColor, iconColor, hintColor = pal.FgDim, pal.FgDim, pal.FgDim
	case it.Warn:
		labelColor, iconColor = pal.Warn, pal.Warn
	}

	marker := "  "
	if selected && !it.Dim {
		marker = chevron(&m.Settings) + " "
	}

	left := overlay.Style(rowBg).Foreground(pal.Accent).Bold(true).Render(marker)
	if it.Icon != "" && !m.Settings.NerdFontsOff() {
		left += overlay.Style(rowBg).Foreground(iconColor).Render(it.Icon + " ")
	}

	hintW := lipgloss.Width(it.Hint)
	if hintW > 0 {
		hintW += contextMenuGap
	}
	labelRoom := max(width-lipgloss.Width(left)-hintW, 1)
	left += overlay.Style(rowBg).
		Foreground(labelColor).
		Bold(selected && !it.Dim).
		Render(overlay.Truncate(it.Label, labelRoom))

	if it.Hint == "" {
		return pal.Row(left, width, st, bg)
	}
	hint := overlay.Style(rowBg).Foreground(hintColor).Render(it.Hint)
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(hint), 1)
	return pal.Row(left+overlay.Style(rowBg).Render(strings.Repeat(" ", gap))+hint, width, st, bg)
}

func (m *OS) contextMenuOrigin(cm *ContextMenu, geo overlay.Geometry) (int, int) {
	screen := overlay.Rect{X1: m.GetRenderWidth(), Y1: m.GetRenderHeight()}
	x, y, _ := overlay.Place(overlay.PointRect(cm.AnchorX, cm.AnchorY), geo.Width, geo.Height, screen,
		overlay.Placement{Side: overlay.SideBelow, Gap: -1, FlipAlign: true})
	return x, y
}

func (m *OS) placeContextMenu(layers []*lipgloss.Layer) []*lipgloss.Layer {
	cm := m.ContextMenu
	if cm == nil {
		return layers
	}
	content, geo := m.renderContextMenu()
	if content == "" {
		return layers
	}
	x, y := m.contextMenuOrigin(cm, geo)

	cm.BoundsX, cm.BoundsY = x, y
	cm.BoundsW, cm.BoundsH = geo.Width, geo.Height
	cm.FirstRowY = y + geo.BodyY
	cm.ItemH = 1

	return append(layers, lipgloss.NewLayer(content).
		X(x).Y(y).Z(config.ZIndexContextMenu).ID("context-menu"))
}

func chevron(s *config.Settings) string {
	if s.UseASCIIOnly {
		return ">"
	}
	return "›"
}
