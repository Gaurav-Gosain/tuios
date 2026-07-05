package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) renderLayoutPicker() string {
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

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#fbbf24")).
		Bold(true).
		Background(bg)

	if m.LayoutPickerMode == "save" {
		lines = append(lines, padLine(titleStyle.Render("Save Layout"), paletteWidth))
	} else {
		lines = append(lines, padLine(titleStyle.Render("Load Layout"), paletteWidth))
	}

	// Separator
	sepStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#4b5563")).
		Background(bg)
	lines = append(lines, sepStyle.Render(strings.Repeat("─", paletteWidth)))

	if m.LayoutPickerMode == "save" {
		// Save mode: show input for layout name
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

		inputLine := promptStyle.Render("Name: ") + queryStyle.Render(m.LayoutSaveBuffer) + cursorStyle.Render("_")
		lines = append(lines, padLine(inputLine, paletteWidth))

		hintStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280")).
			Background(bg)
		lines = append(lines, padLine(hintStyle.Render("  Press Enter to save, Esc to cancel"), paletteWidth))
	} else {
		// Load mode: show search and list
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

		searchLine := promptStyle.Render("> ") + queryStyle.Render(m.LayoutPickerQuery) + cursorStyle.Render("_")
		lines = append(lines, padLine(searchLine, paletteWidth))

		lines = append(lines, sepStyle.Render(strings.Repeat("─", paletteWidth)))

		filtered := FilterLayoutTemplates(m.LayoutPickerItems, m.LayoutPickerQuery)

		if len(filtered) == 0 {
			emptyStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6b7280")).
				Background(bg)
			lines = append(lines, padLine(emptyStyle.Render("  No saved layouts"), paletteWidth))
		} else {
			start := m.LayoutPickerScroll
			end := min(start+maxVisible, len(filtered))

			nameStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#d1d5db")).
				Background(bg)
			nameSelectedStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Bold(true).
				Background(lipgloss.Color("#374151"))
			detailStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6b7280")).
				Background(bg)
			detailSelectedStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9ca3af")).
				Background(lipgloss.Color("#374151"))
			selectedBg := lipgloss.NewStyle().Background(lipgloss.Color("#374151"))

			for i := start; i < end; i++ {
				item := filtered[i]
				isSelected := i == m.LayoutPickerSelected

				detail := fmt.Sprintf("%d windows", len(item.Windows))
				if item.AutoTiling {
					detail += " [tiling]"
				}

				nameMaxWidth := paletteWidth - lipgloss.Width(detail) - 7
				name := item.Name
				if lipgloss.Width(name) > nameMaxWidth {
					name = name[:nameMaxWidth-3] + "..."
				}

				middlePadding := max(paletteWidth-lipgloss.Width(name)-lipgloss.Width(detail)-7, 1)

				var line string
				if isSelected {
					padStr := selectedBg.Render(strings.Repeat(" ", middlePadding))
					line = selectedBg.Render("  ") +
						nameSelectedStyle.Render(name) +
						padStr +
						detailSelectedStyle.Render(detail) +
						selectedBg.Render("  ")
				} else {
					bgStyle := lipgloss.NewStyle().Background(bg)
					padStr := bgStyle.Render(strings.Repeat(" ", middlePadding))
					line = bgStyle.Render("  ") +
						nameStyle.Render(name) +
						padStr +
						detailStyle.Render(detail) +
						bgStyle.Render("  ")
				}
				lines = append(lines, padLine(line, paletteWidth))
			}

			if len(filtered) > maxVisible {
				infoStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("#6b7280")).
					Background(bg)
				scrollInfo := fmt.Sprintf("  %d layouts", len(filtered))
				lines = append(lines, padLine(infoStyle.Render(scrollInfo), paletteWidth))
			}
		}

		// Hints
		hintStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280")).
			Background(bg)
		lines = append(lines, sepStyle.Render(strings.Repeat("─", paletteWidth)))
		lines = append(lines, padLine(hintStyle.Render("  Enter: apply  d: delete  Esc: close"), paletteWidth))
	}

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(getBorder()).
		BorderForeground(theme.HelpBorder()).
		Padding(1, 2).
		Background(bg).
		Render(content)
}
