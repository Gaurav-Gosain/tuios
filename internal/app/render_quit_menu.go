package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// quitMenuInnerWidth is the preferred inner width of the quit menu panel.
const quitMenuInnerWidth = 38

// renderQuitMenu draws the quit menu on the shared list-overlay grammar and
// returns the rendered panel, its geometry, and the per-row hit rectangles, so
// hover, click, and click-away routing work exactly as they do for every other
// list overlay.
func (m *OS) renderQuitMenu() (string, overlay.Geometry, []overlayRowHit) {
	items := m.QuitMenuItems
	title := "Quit TUIOS"
	if m.IsDaemonSession && m.SessionName != "" {
		title = "Session: " + printableTitle(m.SessionName)
	}
	return m.renderListOverlay(listOverlay{
		Glyph:      "", // warning
		Title:      title,
		Width:      quitMenuInnerWidth,
		MaxVisible: max(len(items), 1),
		Count:      len(items),
		Selected:   m.QuitMenuSelected,
		Scroll:     &m.QuitMenuScroll,
		EmptyMsg:   "Nothing to quit",
		Hints: []overlay.Hint{
			{Key: "⏎", Label: "run"},
			{Key: "q", Label: "default"},
			{Key: "esc", Label: "cancel"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			it := items[i]
			labelColor := pal.Fg
			switch {
			case it.Warn:
				labelColor = pal.Warn
			case !selected:
				labelColor = pal.FgDim
			}
			return listRowLine(width, listRowMarker(selected), it.Label, it.Key,
				labelColor, pal.FgMute, selected, rowBg, pal)
		},
	})
}
