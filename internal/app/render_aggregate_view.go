package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// renderAggregateView renders the all-windows tree + live preview overlay. It
// keeps its two-pane layout (the preview shows raw window output) but is themed
// with the shared palette and returns geometry so it can be dragged and
// dismissed like the other overlays.
func (m *OS) renderAggregateView() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	items := m.GetAggregateViewItems()
	filtered := FilterAggregateViewItems(items, m.AggregateViewQuery)
	groups := GetAggregateWorkspaceGroups(filtered, m.CurrentWorkspace)

	totalWidth := m.GetRenderWidth() * 4 / 5
	if totalWidth < 80 {
		totalWidth = min(m.GetRenderWidth()-4, 80)
	}
	treeWidth := totalWidth*2/5 - 2
	previewWidth := totalWidth - treeWidth - 5
	// Two panes need room for two panes. On a narrow screen the tree column comes
	// out around sixteen cells, which wraps every row it holds and shows a preview
	// too narrow to read, so the preview is dropped and the tree takes the lot.
	showPreview := previewWidth >= 24 && treeWidth >= 24
	if !showPreview {
		treeWidth = totalWidth - 4 // the box's own border and padding
		previewWidth = 0
	}
	totalHeight := m.GetRenderHeight() * 3 / 4
	if totalHeight < 15 {
		totalHeight = min(m.GetRenderHeight()-4, 15)
	}

	selectedFlatIdx := m.AggregateViewSelected
	maxTreeLines := max(totalHeight-3, 5)
	if selectedFlatIdx < m.AggregateViewScroll {
		m.AggregateViewScroll = selectedFlatIdx
	}
	if selectedFlatIdx >= m.AggregateViewScroll+maxTreeLines {
		m.AggregateViewScroll = selectedFlatIdx - maxTreeLines + 1
	}

	type treeRow struct {
		text     string
		selected bool
	}
	var treeRows []treeRow
	var selectedItem *AggregateViewItem
	flatIdx := 0

	for gi := range groups {
		g := &groups[gi]
		attached := ""
		if g.IsCurrent {
			attached = " (attached)"
		}
		treeRows = append(treeRows, treeRow{text: fmt.Sprintf("Workspace %d: %d windows%s", g.Workspace, g.WindowCount, attached)})

		for ii := range g.Items {
			item := &g.Items[ii]
			selected := flatIdx == selectedFlatIdx
			if selected {
				selectedItem = item
			}

			title := item.Title
			maxTitle := max(treeWidth-18, 10)
			if len(title) > maxTitle {
				title = title[:maxTitle-3] + "..."
			}
			mark := " "
			if item.IsFocused {
				mark = "*"
			}
			flags := ""
			if item.IsMinimized {
				flags = " [min]"
			}
			if item.IsFloating {
				flags += " [float]"
			}
			dims := fmt.Sprintf("[%dx%d]", item.Width, item.Height)
			line := fmt.Sprintf("  %d: %s%s %s%s", item.WindowIndex, title, mark, dims, flags)
			treeRows = append(treeRows, treeRow{text: line, selected: selected})
			flatIdx++
		}
	}

	if selectedItem == nil && selectedFlatIdx >= 0 && selectedFlatIdx < len(filtered) {
		selectedItem = &filtered[selectedFlatIdx]
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(pal.Accent)
	selectedStyle := lipgloss.NewStyle().Background(pal.RowSel).Foreground(pal.Fg).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(pal.FgDim)
	dimStyle := lipgloss.NewStyle().Foreground(pal.FgMute)

	var treeContent strings.Builder
	if query := m.AggregateViewQuery; query != "" {
		treeContent.WriteString(lipgloss.NewStyle().Foreground(pal.AccentBright).Bold(true).Render("Filter ") +
			normalStyle.Render(truncateString(query, max(treeWidth-7, 1))) + "\n")
	} else {
		header := fmt.Sprintf("Choose window (%d total)", len(items))
		if treeWidth < lipgloss.Width(header) {
			header = fmt.Sprintf("%d windows", len(items))
		}
		treeContent.WriteString(lipgloss.NewStyle().Bold(true).Foreground(pal.Fg).Render(truncateString(header, treeWidth)) + "\n")
	}
	if len(filtered) == 0 {
		treeContent.WriteString(dimStyle.Italic(true).Render(truncateString("(no matching windows)", treeWidth)) + "\n")
	}

	startRow := 0
	windowRowIdx := 0
	for ri, r := range treeRows {
		if !r.selected && windowRowIdx < m.AggregateViewScroll && !strings.HasPrefix(r.text, "Workspace") {
			windowRowIdx++
			continue
		}
		if strings.HasPrefix(r.text, "Workspace") {
			continue
		}
		if windowRowIdx >= m.AggregateViewScroll {
			for si := ri; si >= 0; si-- {
				if strings.HasPrefix(treeRows[si].text, "Workspace") {
					startRow = si
					break
				}
			}
			break
		}
		windowRowIdx++
	}

	linesRendered := 0
	for ri := startRow; ri < len(treeRows) && linesRendered < maxTreeLines; ri++ {
		r := treeRows[ri]
		// The tree is a fixed-width column: a row longer than it would be
		// wrapped by lipgloss onto a second line, pushing the pane and the box
		// around it past the height they were given.
		text := truncateString(r.text, treeWidth)
		switch {
		case strings.HasPrefix(r.text, "Workspace"):
			treeContent.WriteString(headerStyle.Render(text) + "\n")
		case r.selected:
			treeContent.WriteString(selectedStyle.Render(text) + "\n")
		default:
			treeContent.WriteString(normalStyle.Render(text) + "\n")
		}
		linesRendered++
	}

	var previewContent strings.Builder
	if selectedItem != nil && selectedItem.Window != nil && selectedItem.Window.Terminal != nil {
		w := selectedItem.Window
		w.RLockIO()
		raw := w.Terminal.String()
		w.RUnlockIO()

		previewContent.WriteString(lipgloss.NewStyle().Bold(true).Foreground(pal.Fg).Render(truncateString(selectedItem.Title, max(previewWidth-12, 4))) +
			dimStyle.Render(fmt.Sprintf(" [%dx%d]", w.Width, w.Height)) + "\n")
		previewContent.WriteString(dimStyle.Render(strings.Repeat("─", previewWidth)) + "\n")

		lines := strings.Split(raw, "\n")
		previewLines := max(totalHeight-4, 3)
		start := 0
		if len(lines) > previewLines {
			start = len(lines) - previewLines
		}
		for i := start; i < len(lines) && i < start+previewLines; i++ {
			// Live window output, measured in cells rather than bytes: a row
			// wider than the pane wraps and grows the box past its own height.
			previewContent.WriteString(truncateToWidth(lines[i], previewWidth) + "\n")
		}
	} else if selectedItem != nil {
		previewContent.WriteString(lipgloss.NewStyle().Bold(true).Foreground(pal.Fg).Render(truncateString(selectedItem.Title, previewWidth)) + "\n")
		previewContent.WriteString(dimStyle.Render("(no content)") + "\n")
	}

	hintText := "↑↓ navigate   ⏎ jump   esc close"
	if treeWidth < lipgloss.Width(hintText) {
		hintText = "↑↓ ⏎ esc"
	}
	hint := dimStyle.Render(truncateString(hintText, treeWidth))
	treeContent.WriteString(hint)

	treePane := lipgloss.NewStyle().Width(treeWidth).Height(totalHeight).Render(treeContent.String())
	combined := treePane
	if showPreview {
		previewPane := lipgloss.NewStyle().
			Width(previewWidth).Height(totalHeight).
			BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(pal.FgMute).
			PaddingLeft(1).Render(previewContent.String())
		combined = lipgloss.JoinHorizontal(lipgloss.Top, treePane, previewPane)
	}

	// A solid lipgloss box (whose Background lipgloss keeps intact across the
	// inner fg-only styles) rather than the manual-fill panel, so the tree/live
	// preview do not develop transparent holes.
	box := lipgloss.NewStyle().
		Width(totalWidth).
		Border(getBorder()).
		BorderForeground(pal.Accent).
		Background(pal.Surface).
		Padding(0, 1).
		Render(combined)

	w, h := lipgloss.Width(box), lipgloss.Height(box)
	geo := overlay.Geometry{Width: w, Height: h, TitleBar: overlay.Rect{X0: 0, Y0: 0, X1: w, Y1: 1}}
	return box, geo, nil
}
