package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	// contextMenuMinWidth is the narrowest inner width worth laying a menu out
	// at. Below this a row has nowhere to put a marker, an icon and a label.
	contextMenuMinWidth = 16
	// contextMenuMaxWidth caps the menu on a wide screen. A context menu is a
	// short list of short labels; letting it grow to the width of the widest
	// hint would make a floating panel out of what should read as a small popup.
	contextMenuMaxWidth = 36
	// contextMenuGap is the minimum run of spaces between a label and its hint.
	contextMenuGap = 2
)

// menuTitle is the header a context menu draws. Menu titles are window and
// session names, which are foreign data, so the sanitizer sits here rather than
// in each builder: measuring and drawing then agree, and a new menu cannot ship
// an unlaundered header.
func (cm *ContextMenu) menuTitle() string { return printableTitle(cm.Title) }

// contextMenuWidth returns the inner content width to lay the menu out at: wide
// enough for its widest row, capped, and then fitted to the screen so the panel
// never draws past the right-hand edge.
func (m *OS) contextMenuWidth(cm *ContextMenu) int {
	widest := lipgloss.Width(cm.menuTitle()) + 2 // the title chip is padded
	for _, it := range cm.Items {
		if it.Sep {
			continue
		}
		w := lipgloss.Width(contextMenuRowLeft(it)) + lipgloss.Width(it.Hint)
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

// contextMenuRowLeft is the left-hand part of a row: the selection marker, the
// icon, and the label, unstyled. It exists so the width measurement and the
// renderer cannot disagree about how much room the left side takes.
func contextMenuRowLeft(it ContextMenuItem) string {
	icon := ""
	if it.Icon != "" && !config.UseASCIIOnly {
		icon = it.Icon + " "
	}
	return "  " + icon + it.Label
}

// renderContextMenu renders the open context menu and returns the panel and its
// geometry. It returns an empty string when no menu is open.
func (m *OS) renderContextMenu() (string, overlay.Geometry) {
	cm := m.ContextMenu
	if cm == nil {
		return "", overlay.Geometry{}
	}

	pal := theme.UI()
	bg := pal.Surface
	width := m.contextMenuWidth(cm)

	// A menu with more rows than the screen is tall scrolls rather than drawing
	// past the bottom edge. Rows are dropped from the view, never from the menu,
	// so no action becomes unreachable: arrow navigation walks the whole list
	// and the window follows the selection.
	start, visible := m.contextMenuRows(cm)
	cm.ScrollFrom = start

	lines := make([]string, 0, visible)
	for i := start; i < start+visible; i++ {
		it := cm.Items[i]
		if it.Sep {
			lines = append(lines, overlay.Rule(width, bg, pal))
			continue
		}
		lines = append(lines, m.contextMenuRow(it, i == cm.Selected, width, bg, pal))
	}

	panel := overlay.Panel{
		Title: cm.menuTitle(),
		Width: width,
		Body:  strings.Join(lines, "\n"),
	}
	return panel.Render(pal)
}

// contextMenuRows returns the first item to draw and how many fit, clamping the
// menu's scroll offset so the selected row stays in view.
//
// The row budget is the screen height less the panel's own chrome. Overlays
// that ignore this draw their lower rows past the bottom of the screen, where
// the terminal discards them.
func (m *OS) contextMenuRows(cm *ContextMenu) (start, visible int) {
	count := len(cm.Items)
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return 0, count // size not known yet; draw the lot
	}
	visible = min(count, max(rh-panelChromeRows, 1))
	cm.Scroll = scrollWindow(cm.Scroll, cm.Selected, count, visible)
	return cm.Scroll, visible
}

// contextMenuRow renders one runnable row, filled to the panel width so the
// selection highlight spans it.
//
// A dimmed row is drawn a step down from the live rows and never takes the
// highlight, even if the selection somehow points at it: the colors are the only
// thing telling the user the action is unavailable, so they do not get
// overridden. It is FgDim rather than FgMute because unavailable still has to be
// readable, and FgMute measured 1.81:1 on the panel's Surface.
func (m *OS) contextMenuRow(it ContextMenuItem, selected bool, width int, bg color.Color, pal overlay.Palette) string {
	rowBg := bg
	if selected && !it.Dim {
		rowBg = pal.RowSel
	}

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
		marker = "› "
		if config.UseASCIIOnly {
			marker = "> "
		}
	}

	left := overlay.Style(rowBg).Foreground(pal.Accent).Bold(true).Render(marker)
	if it.Icon != "" && !config.UseASCIIOnly {
		left += overlay.Style(rowBg).Foreground(iconColor).Render(it.Icon + " ")
	}

	// The label gives up room before the hint does: the hint is what the user
	// came for when they are learning the keyboard, and it is already short.
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
		return overlay.Fill(left, width, rowBg)
	}
	hint := overlay.Style(rowBg).Foreground(hintColor).Render(it.Hint)
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(hint), 1)
	return overlay.Fill(left+overlay.Style(rowBg).Render(strings.Repeat(" ", gap))+hint, width, rowBg)
}

// contextMenuOrigin places the menu against its anchor.
//
// The menu opens down and to the right of the pointer, which is where a user
// expects it. When that would run past an edge it flips to the other side of
// the anchor rather than being nudged along it, so the pointer stays on a
// corner of the menu instead of landing in the middle of a row the user did not
// aim at. The clamp afterwards is a backstop for a menu taller or wider than
// the screen itself, where neither side fits and drawing off the edge is the
// only other option.
func (m *OS) contextMenuOrigin(cm *ContextMenu, geo overlay.Geometry) (int, int) {
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()

	x := cm.AnchorX
	if x+geo.Width > rw {
		x = cm.AnchorX - geo.Width + 1
	}
	y := cm.AnchorY
	if y+geo.Height > rh {
		y = cm.AnchorY - geo.Height + 1
	}

	x = max(min(x, rw-geo.Width), 0)
	y = max(min(y, rh-geo.Height), 0)
	return x, y
}

// placeContextMenu draws the menu as a layer and records where it landed, so
// the mouse handlers can map a screen cell to a row without redoing the layout.
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
