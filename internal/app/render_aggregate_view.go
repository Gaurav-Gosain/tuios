package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) renderAggregateView() string {
	items := m.GetAggregateViewItems()
	filtered := FilterAggregateViewItems(items, m.AggregateViewQuery)
	groups := GetAggregateWorkspaceGroups(filtered, m.CurrentWorkspace)

	// Dimensions
	totalWidth := m.GetRenderWidth() * 4 / 5
	if totalWidth < 80 {
		totalWidth = min(m.GetRenderWidth()-4, 80)
	}
	treeWidth := totalWidth*2/5 - 2
	previewWidth := totalWidth - treeWidth - 5
	totalHeight := m.GetRenderHeight() * 3 / 4
	if totalHeight < 15 {
		totalHeight = min(m.GetRenderHeight()-4, 15)
	}

	selectedFlatIdx := m.AggregateViewSelected

	// Adjust scroll to keep selected visible
	maxTreeLines := max(totalHeight-3, 5)
	if selectedFlatIdx < m.AggregateViewScroll {
		m.AggregateViewScroll = selectedFlatIdx
	}
	if selectedFlatIdx >= m.AggregateViewScroll+maxTreeLines {
		m.AggregateViewScroll = selectedFlatIdx - maxTreeLines + 1
	}

	// === Build tree content as plain text lines ===
	type treeRow struct {
		text     string
		selected bool
	}
	var treeRows []treeRow
	var selectedItem *AggregateViewItem
	flatIdx := 0

	for gi := range groups {
		g := &groups[gi]

		// Workspace header
		attached := ""
		if g.IsCurrent {
			attached = " (attached)"
		}
		wsHeader := fmt.Sprintf("Workspace %d: %d windows%s", g.Workspace+1, g.WindowCount, attached)
		treeRows = append(treeRows, treeRow{text: wsHeader})

		// Window entries
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

	// Fallback if nothing found via loop
	if selectedItem == nil && selectedFlatIdx >= 0 && selectedFlatIdx < len(filtered) {
		selectedItem = &filtered[selectedFlatIdx]
	}

	// Render tree lines with lipgloss styles
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	selectedStyle := lipgloss.NewStyle().Reverse(true)
	normalStyle := lipgloss.NewStyle()
	dimStyle := lipgloss.NewStyle().Faint(true)

	var treeContent strings.Builder

	// Header / filter
	query := m.AggregateViewQuery
	if query != "" {
		treeContent.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("Filter: ") + query + "\n")
	} else {
		treeContent.WriteString(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Choose Window (%d total)", len(items))) + "\n")
	}

	if len(filtered) == 0 {
		treeContent.WriteString(dimStyle.Render("(no matching windows)") + "\n")
	}

	// Visible rows with scrolling
	startRow := 0
	// Find which tree row corresponds to the scroll offset
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
			// Find the workspace header before this row
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
		if strings.HasPrefix(r.text, "Workspace") {
			treeContent.WriteString(headerStyle.Render(r.text) + "\n")
		} else if r.selected {
			treeContent.WriteString(selectedStyle.Render(r.text) + "\n")
		} else {
			treeContent.WriteString(normalStyle.Render(r.text) + "\n")
		}
		linesRendered++
	}

	treeContent.WriteString(dimStyle.Render("up/down:nav  Enter:jump  Esc:close"))

	// === Build preview content ===
	var previewContent strings.Builder

	if selectedItem != nil && selectedItem.Window != nil && selectedItem.Window.Terminal != nil {
		w := selectedItem.Window
		w.RLockIO()
		raw := w.Terminal.String()
		w.RUnlockIO()

		previewContent.WriteString(lipgloss.NewStyle().Bold(true).Render(selectedItem.Title) +
			dimStyle.Render(fmt.Sprintf(" [%dx%d]", w.Width, w.Height)) + "\n")
		previewContent.WriteString(dimStyle.Render(strings.Repeat("─", previewWidth)) + "\n")

		lines := strings.Split(raw, "\n")
		previewLines := max(totalHeight-4, 3)
		start := 0
		if len(lines) > previewLines {
			start = len(lines) - previewLines
		}
		for i := start; i < len(lines) && i < start+previewLines; i++ {
			line := lines[i]
			// Truncate by visible length (accounting for ANSI in terminal output)
			if len(line) > previewWidth*3 { // rough byte limit
				line = line[:previewWidth*3]
			}
			previewContent.WriteString(line + "\n")
		}
	} else if selectedItem != nil {
		previewContent.WriteString(lipgloss.NewStyle().Bold(true).Render(selectedItem.Title) + "\n")
		previewContent.WriteString(dimStyle.Render("(no content)") + "\n")
	}

	// === Layout with lipgloss ===
	treePane := lipgloss.NewStyle().
		Width(treeWidth).
		Height(totalHeight).
		Render(treeContent.String())

	previewPane := lipgloss.NewStyle().
		Width(previewWidth).
		Height(totalHeight).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("8")).
		PaddingLeft(1).
		Render(previewContent.String())

	combined := lipgloss.JoinHorizontal(lipgloss.Top, treePane, previewPane)

	return lipgloss.NewStyle().
		Width(totalWidth).
		Border(getBorder()).
		BorderForeground(theme.HelpBorder()).
		Padding(0, 1).
		Background(lipgloss.Color("0")).
		Render(combined)
}
