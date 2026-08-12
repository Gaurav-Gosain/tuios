package app

import (
	"image/color"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	workspaceSwitcherWidth = 44
	// workspaceSwitcherRows is the requested row count, shared with the wheel so
	// it scrolls by the same page the renderer draws.
	workspaceSwitcherRows = 9
)

// renderWorkspaceSwitcher renders the workspace switcher on the shared overlay
// grammar, returning the panel, geometry and hit rows.
func (m *OS) renderWorkspaceSwitcher() (string, overlay.Geometry, []overlayRowHit) {
	filtered := FilterWorkspaceItems(m.WorkspaceSwitcherItems, m.WorkspaceSwitcherQuery)
	if len(filtered) > 0 {
		m.WorkspaceSwitcherSelected = clampInt(m.WorkspaceSwitcherSelected, 0, len(filtered)-1)
	}

	return m.renderListOverlay(listOverlay{
		Glyph:      "",
		Title:      "Workspaces",
		Width:      workspaceSwitcherWidth,
		MaxVisible: workspaceSwitcherRows,
		Search:     true,
		Query:      m.WorkspaceSwitcherQuery,
		Count:      len(filtered),
		Selected:   m.WorkspaceSwitcherSelected,
		Scroll:     &m.WorkspaceSwitcherScroll,
		EmptyMsg:   "No workspace matches",
		Hints: []overlay.Hint{
			{Key: "⏎", Label: "go"},
			{Key: "ctrl+r", Label: "rename"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return workspaceSwitcherRow(filtered[i], selected, rowBg, pal, width)
		},
	})
}

// workspaceSwitcherRow draws one workspace: its number, the name when it has
// one, and its pane count. The number leads every row because it is the
// workspace's identity and the key its jump bindings use, so it has to be
// readable whether or not the workspace has been named.
func workspaceSwitcherRow(w WorkspaceItem, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(panePlural(w.Panes))
	if w.IsCurrent {
		right = overlay.Style(rowBg).Foreground(pal.Success).Render("current  ") + right
	}

	numColor := pal.FgMute
	if selected {
		numColor = pal.Accent
	}
	left := overlay.Style(rowBg).Foreground(numColor).Bold(true).Render(strconv.Itoa(w.Number))
	if w.Name != "" {
		labelColor := pal.FgDim
		if selected {
			labelColor = pal.Fg
		}
		left += overlay.Style(rowBg).Foreground(labelColor).Bold(selected).
			Render("  " + overlay.Truncate(printableTitle(w.Name), max(width-12, 1)))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
}
