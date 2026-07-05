package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) renderCommandPalette() string {
	items := GetCommandPaletteItems()
	filtered := FilterCommandPalette(items, m.CommandPaletteQuery)

	paletteWidth := 58
	maxVisible := 10

	bg := lipgloss.Color("#1a1a2a")

	padLine := func(s string, targetWidth int) string {
		currentWidth := lipgloss.Width(s)
		if currentWidth < targetWidth {
			s += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", targetWidth-currentWidth))
		}
		return s
	}

	var lines []string

	// Search input line
	promptStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#fbbf24")).
		Bold(true).
		Background(bg)
	queryStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(bg)
	cursorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#fbbf24")).
		Background(bg)

	searchLine := promptStyle.Render("> ") + queryStyle.Render(m.CommandPaletteQuery) + cursorStyle.Render("_")
	lines = append(lines, padLine(searchLine, paletteWidth))

	// Separator
	sepStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#4b5563")).
		Background(bg)
	lines = append(lines, sepStyle.Render(strings.Repeat("─", paletteWidth)))

	// Results
	if len(filtered) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280")).
			Background(bg)
		lines = append(lines, padLine(emptyStyle.Render("  No matching commands"), paletteWidth))
	} else {
		start := m.CommandPaletteScroll
		end := min(start+maxVisible, len(filtered))

		nameStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#d1d5db")).
			Background(bg)
		nameSelectedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Background(lipgloss.Color("#374151"))
		shortcutStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280")).
			Background(bg)
		shortcutSelectedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9ca3af")).
			Background(lipgloss.Color("#374151"))
		categoryStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280")).
			Background(bg)
		categorySelectedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9ca3af")).
			Background(lipgloss.Color("#374151"))
		selectedBg := lipgloss.NewStyle().Background(lipgloss.Color("#374151"))

		for i := start; i < end; i++ {
			item := filtered[i]
			isSelected := i == m.CommandPaletteSelected

			catTag := "[" + item.Category + "]"
			// Calculate available space for the name
			shortcutLen := lipgloss.Width(item.Shortcut)
			catLen := lipgloss.Width(catTag)
			// prefix "  " (2) + category + " " (1) + name + padding + shortcut + "  " (2)
			nameMaxWidth := paletteWidth - shortcutLen - catLen - 7
			name := item.Name
			if lipgloss.Width(name) > nameMaxWidth {
				name = name[:nameMaxWidth-3] + "..."
			}

			// Build the padded middle section
			nameRendered := lipgloss.Width(name)
			catRendered := lipgloss.Width(catTag)
			middlePadding := max(paletteWidth-nameRendered-shortcutLen-catRendered-7, 1)

			var line string
			if isSelected {
				padStr := selectedBg.Render(strings.Repeat(" ", middlePadding))
				line = selectedBg.Render("  ") +
					categorySelectedStyle.Render(catTag) +
					selectedBg.Render(" ") +
					nameSelectedStyle.Render(name) +
					padStr +
					shortcutSelectedStyle.Render(item.Shortcut) +
					selectedBg.Render("  ")
			} else {
				bgStyle := lipgloss.NewStyle().Background(bg)
				padStr := bgStyle.Render(strings.Repeat(" ", middlePadding))
				line = bgStyle.Render("  ") +
					categoryStyle.Render(catTag) +
					bgStyle.Render(" ") +
					nameStyle.Render(name) +
					padStr +
					shortcutStyle.Render(item.Shortcut) +
					bgStyle.Render("  ")
			}
			lines = append(lines, padLine(line, paletteWidth))
		}

		// Show scroll indicator if needed
		if len(filtered) > maxVisible {
			infoStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6b7280")).
				Background(bg)
			scrollInfo := fmt.Sprintf("  %d of %d commands", len(filtered), len(items))
			lines = append(lines, padLine(infoStyle.Render(scrollInfo), paletteWidth))
		}
	}

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(getBorder()).
		BorderForeground(theme.HelpBorder()).
		Padding(1, 2).
		Background(bg).
		Render(content)
}
