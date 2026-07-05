package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) renderSessionSwitcher() string {
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
	lines = append(lines, padLine(titleStyle.Render("Sessions"), paletteWidth))

	// Separator
	sepStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#4b5563")).
		Background(bg)
	lines = append(lines, sepStyle.Render(strings.Repeat("─", paletteWidth)))

	// Check if in daemon mode
	if !m.IsDaemonSession || m.DaemonClient == nil {
		msgStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280")).
			Background(bg)
		lines = append(lines, padLine(msgStyle.Render("  Session management requires daemon mode."), paletteWidth))
		lines = append(lines, padLine(msgStyle.Render("  Start with: tuios new"), paletteWidth))

		content := strings.Join(lines, "\n")
		return lipgloss.NewStyle().
			Border(getBorder()).
			BorderForeground(theme.HelpBorder()).
			Padding(1, 2).
			Background(bg).
			Render(content)
	}

	// Delete confirmation overlay  - takes over the switcher content
	if m.SessionSwitcherConfirmDelete != "" {
		warnStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f87171")).
			Bold(true).
			Background(bg)
		textStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#d1d5db")).
			Background(bg)
		confirmHintStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280")).
			Background(bg)
		lines = append(lines, padLine(warnStyle.Render("  Delete session?"), paletteWidth))
		lines = append(lines, padLine(textStyle.Render("  '"+m.SessionSwitcherConfirmDelete+"'"), paletteWidth))
		lines = append(lines, padLine(textStyle.Render(""), paletteWidth))
		lines = append(lines, padLine(confirmHintStyle.Render("  [y] yes  [n] no  [esc] cancel"), paletteWidth))

		content := strings.Join(lines, "\n")
		return lipgloss.NewStyle().
			Border(getBorder()).
			BorderForeground(theme.HelpBorder()).
			Padding(1, 2).
			Background(bg).
			Render(content)
	}

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

	searchLine := promptStyle.Render("> ") + queryStyle.Render(m.SessionSwitcherQuery) + cursorStyle.Render("_")
	lines = append(lines, padLine(searchLine, paletteWidth))

	// Separator
	lines = append(lines, sepStyle.Render(strings.Repeat("─", paletteWidth)))

	// Filter items
	filtered := FilterSessionItems(m.SessionSwitcherItems, m.SessionSwitcherQuery)

	// Error message
	if m.SessionSwitcherError != "" {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#dc2626")).
			Background(bg)
		lines = append(lines, padLine(errStyle.Render("  "+m.SessionSwitcherError), paletteWidth))
	}

	// Results
	if len(filtered) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6b7280")).
			Background(bg)
		if m.SessionSwitcherQuery != "" {
			lines = append(lines, padLine(emptyStyle.Render("  No match  - Enter to create '"+m.SessionSwitcherQuery+"'"), paletteWidth))
		} else {
			lines = append(lines, padLine(emptyStyle.Render("  No sessions found"), paletteWidth))
		}
	} else {
		start := m.SessionSwitcherScroll
		end := min(start+maxVisible, len(filtered))

		nameStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#d1d5db")).
			Background(bg)
		nameSelectedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Background(lipgloss.Color("#374151"))
		currentStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ade80")).
			Background(bg)
		currentSelectedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ade80")).
			Bold(true).
			Background(lipgloss.Color("#374151"))
		selectedBg := lipgloss.NewStyle().Background(lipgloss.Color("#374151"))

		for i := start; i < end; i++ {
			item := filtered[i]
			isSelected := i == m.SessionSwitcherSelected

			name := item.Name
			currentTag := ""
			if item.IsCurrent {
				currentTag = " (current)"
			}

			if isSelected {
				var line string
				line = selectedBg.Render("  ") +
					nameSelectedStyle.Render(name)
				if currentTag != "" {
					line += currentSelectedStyle.Render(currentTag)
				}
				padding := paletteWidth - lipgloss.Width(name) - lipgloss.Width(currentTag) - 4
				if padding > 0 {
					line += selectedBg.Render(strings.Repeat(" ", padding))
				}
				line += selectedBg.Render("  ")
				lines = append(lines, padLine(line, paletteWidth))
			} else {
				bgStyle := lipgloss.NewStyle().Background(bg)
				var line string
				line = bgStyle.Render("  ") +
					nameStyle.Render(name)
				if currentTag != "" {
					line += currentStyle.Render(currentTag)
				}
				padding := paletteWidth - lipgloss.Width(name) - lipgloss.Width(currentTag) - 4
				if padding > 0 {
					line += bgStyle.Render(strings.Repeat(" ", padding))
				}
				line += bgStyle.Render("  ")
				lines = append(lines, padLine(line, paletteWidth))
			}
		}

		// Show scroll indicator if needed
		if len(filtered) > maxVisible {
			infoStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6b7280")).
				Background(bg)
			scrollInfo := fmt.Sprintf("  %d sessions", len(filtered))
			lines = append(lines, padLine(infoStyle.Render(scrollInfo), paletteWidth))
		}
	}

	// Footer hint
	lines = append(lines, sepStyle.Render(strings.Repeat("─", paletteWidth)))
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6b7280")).
		Background(bg)
	lines = append(lines, padLine(hintStyle.Render("enter: switch/create | ctrl+d: delete | esc: close"), paletteWidth))

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(getBorder()).
		BorderForeground(theme.HelpBorder()).
		Padding(1, 2).
		Background(bg).
		Render(content)
}
