package app

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type settingsRowExtra struct {
	match []int
	tag   string
}

func (m *OS) settingsSearchLine(found, width int, pal overlay.Palette) string {
	bg := pal.Surface
	query := m.settingsSearch.query.String()
	left := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())
	if query == "" {
		left += overlay.Cursor(" ", bg, pal.Fg) +
			overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render(" name, key, value or description")
	} else {
		before, at, after := m.settingsSearch.query.Parts()
		left += overlay.FieldLine(before, at, after, max(width-lipgloss.Width(left)-14, 1), bg, pal.Fg)
	}

	count := ""
	switch {
	case query == "":
	case found == 0:
		count = "no matches"
	case found == 1:
		count = "1 match"
	default:
		count = strconv.Itoa(found) + " matches"
	}
	right := overlay.Style(bg).Foreground(pal.FgMute).Render(count)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return overlay.Fill(left, width, bg)
	}
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + right
}

func settingsSearchEmpty(query string, width int, pal overlay.Palette) string {
	msg := "No settings match"
	if strings.TrimSpace(query) == "" {
		msg = "Type to search every tab"
	}
	return overlay.Style(pal.Surface).Foreground(pal.FgDim).Italic(true).
		Render("  " + overlay.Truncate(msg, max(width-2, 1)))
}

func (m *OS) settingsSearchDescription(items []settingItem, hits []settingsHit, cats []settingsCategory, width, n int, pal overlay.Palette) []string {
	if len(items) == 0 || n <= 0 {
		return settingsDescription("", width, n, pal)
	}
	item, hit := items[m.settingsList.Cursor], hits[m.settingsList.Cursor]
	bg := pal.Surface

	where := cats[hit.cat].Name + " tab"
	key := item.Path
	var match []int
	if hit.field == settingsFieldKey {
		match = hit.pos
	}
	var first string
	if key != "" {
		first = launcherRowName(overlay.Truncate(key, max(width-4-lipgloss.Width(where), 1)), match, bg, pal.FgDim, false, pal) +
			overlay.Style(bg).Foreground(pal.FgMute).Render("  "+where)
	} else {
		first = overlay.Style(bg).Foreground(pal.FgMute).Render(where)
	}
	out := []string{overlay.Style(bg).Render("  ") + first}
	if n > 1 {
		out = append(out, settingsDescription(joinSentences(item.Desc, m.settingsDefaultNote(item)), width, n-1, pal)...)
	}
	return out
}

func joinSentences(text, note string) string {
	switch {
	case note == "":
		return text
	case text == "":
		return note
	}
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text + " " + note
}
