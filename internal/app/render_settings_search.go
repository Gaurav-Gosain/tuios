package app

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// settingsRowExtra is what a row shows beyond itself when it is a search
// result.
type settingsRowExtra struct {
	// match is the byte offsets of the label the query matched, lit in the
	// accent the palette and the launcher light theirs in.
	match []int
	// tag is a quiet word after the label: the tab the row lives on.
	tag string
}

// settingsSearchLine is the search line at the top of the body: the query
// being typed on the left, and how many rows it found on the right.
func (m *OS) settingsSearchLine(found, width int, pal overlay.Palette) string {
	bg := pal.Surface
	query := m.settingsSearch.query
	left := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())
	if query == "" {
		left += overlay.Cursor(" ", bg, pal.Fg) +
			overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render(" name, key, value or description")
	} else {
		left += overlay.Style(bg).Foreground(pal.Fg).Render(query) + overlay.Cursor(" ", bg, pal.Fg)
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

// settingsSearchEmpty is the line a search that found nothing shows in place
// of the rows.
func settingsSearchEmpty(query string, width int, pal overlay.Palette) string {
	msg := "No settings match"
	if strings.TrimSpace(query) == "" {
		msg = "Type to search every tab"
	}
	return overlay.Style(pal.Surface).Foreground(pal.FgDim).Italic(true).
		Render("  " + overlay.Truncate(msg, max(width-2, 1)))
}

// settingsSearchDescription is the description box while searching. Its first
// line is where the selected row lives: its config key, with the characters
// the query matched lit when the key is what matched, and its tab. The rest is
// the row's description, as the tab view shows it.
func (m *OS) settingsSearchDescription(items []settingItem, hits []settingsHit, cats []settingsCategory, width, n int, pal overlay.Palette) []string {
	if len(items) == 0 || n <= 0 {
		return settingsDescription("", width, n, pal)
	}
	item, hit := items[m.SettingsSelected], hits[m.SettingsSelected]
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
		out = append(out, settingsDescription(item.Desc, width, n-1, pal)...)
	}
	return out
}
