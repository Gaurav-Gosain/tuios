package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The post-capture preview panel.
//
// The body is the captured cells drawn straight back with the terminal's own
// text rendering, in their exact colours. For the content this is not a
// degraded preview, it is the most faithful one there is: the same renderer
// the content came from is drawing it again. What it cannot show is the pixel
// dressing, and one quiet line says so rather than the panel pretending.
//
// A kitty host gets the same panel with the rendered PNG drawn over the capture
// rows, which are blanked for it so the picture is not laid on top of the same
// capture in text (see screenshot_graphics.go). Sixel gets the text tier and
// nothing else, because sixel cannot delete a placement, which is why the
// launcher icons already skip it.

// overlayKindShot is the preview panel's overlay kind.
const overlayKindShot = "screenshot"

// shotPreviewWidth is the panel's preferred inner width. A capture is usually
// 80 columns, so the panel is sized to show most of one without scrolling and
// still fit an 80-column terminal.
const shotPreviewWidth = 76

// shotPreviewRows is the preferred number of capture rows on screen.
const shotPreviewRows = 18

// screenshotPreviewBody is the body viewport in cells: how much of the capture
// the panel can show at once. Both handlers and the renderer ask this, so the
// scroll clamp and the drawn rows cannot disagree.
func (m *OS) screenshotPreviewBody() (cols, rows int) {
	width := m.panelWidth(shotPreviewWidth)
	// Two cells of body gutter, and the metadata and note lines the body
	// spends on things that are not capture rows.
	cols = max(1, width-2)
	rows = m.panelBodyRows(shotPreviewRows, shotPreviewExtraRows, width, nil, m.shotPreviewHints())
	return cols, max(1, rows)
}

// shotPreviewExtraRows is how many body lines can go to things that are not
// capture rows: the header, the path, the note, the status line, the blank
// after them and the scroll indicator. It is the largest that set gets, not
// the usual one, so a small terminal budgets for the worst case rather than
// having the panel outgrow it.
const shotPreviewExtraRows = 6

// shotPreviewHints is the footer. Every key on it works here or is not drawn:
// c is omitted when nothing would copy, o when this client is not on the
// user's machine, and the reason lands on the status line instead.
func (m *OS) shotPreviewHints() []overlay.Hint {
	hints := []overlay.Hint{{Key: "enter", Label: "done"}}
	if label := m.ShotPreview.CopyLabel; label != "" {
		hints = append(hints, overlay.Hint{Key: "c", Label: label})
	}
	if !m.IsRemoteClient() {
		hints = append(hints, overlay.Hint{Key: "o", Label: "open"})
	}
	hints = append(hints,
		overlay.Hint{Key: "r", Label: "retake"},
		overlay.Hint{Key: "esc", Label: "discard file"},
	)
	return hints
}

// renderScreenshotPreview draws the panel and records the rows it drew.
func (m *OS) renderScreenshotPreview() (string, overlay.Geometry, []overlayRowHit) {
	p := &m.ShotPreview
	pal := theme.UI()
	width := m.panelWidth(shotPreviewWidth)
	bodyCols, bodyRows := m.screenshotPreviewBody()
	hints := m.shotPreviewHints()

	dim := overlay.Style(pal.Surface).Foreground(pal.FgDim)
	mute := overlay.Style(pal.Surface).Foreground(pal.FgMute)

	var lines []string
	lines = append(lines, overlay.Fill(dim.Render("  "+m.shotPreviewHeader()), width, pal.Surface))
	lines = append(lines, overlay.Fill(mute.Render("  "+elideLeft(shortenPath(p.Path), width-4)), width, pal.Surface))
	if p.Note != "" {
		lines = append(lines, overlay.Fill(mute.Render("  "+p.Note), width, pal.Surface))
	}
	if p.Status != "" {
		lines = append(lines, overlay.Fill(mute.Render("  "+p.Status), width, pal.Surface))
	}
	lines = append(lines, overlay.Fill("", width, pal.Surface))

	// The cells are the body, except where the picture is about to be drawn
	// over them.
	//
	// The picture keeps the capture's shape, so it does not fill the body: it
	// leaves a margin on one axis. Drawing cells there too put half a picture
	// and half a wall of text in one panel, which is what a letterboxed
	// placement over a full text tier looks like. Blanking only the rectangle
	// the picture covers is the narrowest answer: every other line of the panel
	// is still cells, and a host that claims kitty graphics and does not
	// deliver loses the capture rows and keeps the header, the path, the note
	// and the footer, which is enough to say what happened and get out.
	_, picRows, hasPicture := m.screenshotPreviewPictureBox()
	rows := m.shotPreviewCells(bodyCols, bodyRows)
	if hasPicture {
		rows = blankPictureRows(rows, picRows)
	}
	for _, row := range rows {
		lines = append(lines, overlay.Fill("  "+row, width, pal.Surface))
	}
	// The scroll line is about the cells. Where the picture is drawn the whole
	// capture is on screen at once, so saying it is not would be a lie.
	if !hasPicture && p.Grid != nil && (p.Grid.Rows > bodyRows || p.Grid.Cols > bodyCols) {
		lines = append(lines, overlay.Fill(mute.Render(
			fmt.Sprintf("  Showing %d of %d rows. Scroll with the wheel or the arrows.",
				min(bodyRows, p.Grid.Rows), p.Grid.Rows)), width, pal.Surface))
	}

	panel := overlay.Panel{
		Glyph: theme.Glyphs().Dot,
		Title: "Screenshot",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

// blankPictureRows empties every body row the picture will be drawn over.
//
// A whole row goes, not the part of it the picture covers. The rows are
// lipgloss output, escape sequences interleaved with text, so cutting one at a
// column means taking its styling apart and risking a broken escape for the few
// columns a letterboxed picture leaves at the side. An empty margin beside the
// picture reads as a margin; a strip of the capture's own text there reads as
// the panel showing the capture twice.
func blankPictureRows(rows []string, picRows int) []string {
	for i := 0; i < len(rows) && i < picRows; i++ {
		rows[i] = ""
	}
	return rows
}

// shotPreviewHeader is the one metadata line: what was written, how big, and
// whether it reached a clipboard.
func (m *OS) shotPreviewHeader() string {
	p := &m.ShotPreview
	size := "0 KB"
	if p.Bytes > 0 {
		size = fmt.Sprintf("%d KB", max(1, p.Bytes/1024))
	}
	cols, rows := 0, 0
	if p.Grid != nil {
		cols, rows = p.Grid.Cols, p.Grid.Rows
	}
	line := fmt.Sprintf("%s  %dx%d cells  %s", strings.ToUpper(string(p.Format)), cols, rows, size)
	if p.Copied != "" {
		line += "  copied to the clipboard"
	}
	return line
}

// elideLeft trims a path from the front, because the end of it is the part
// that says which file this is.
func elideLeft(s string, width int) string {
	if width < 4 || lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width("…"+string(runes)) > width {
		runes = runes[1:]
	}
	return "…" + string(runes)
}

// shotPreviewCells redraws the captured grid as styled terminal text. This is
// the text tier: it runs on a plain xterm and is exact, because the cells are
// the ones the content was drawn from.
func (m *OS) shotPreviewCells(cols, rows int) []string {
	p := &m.ShotPreview
	if p.Grid == nil {
		return nil
	}
	g := p.Grid
	out := make([]string, 0, rows)
	for y := p.Scroll; y < min(p.Scroll+rows, g.Rows); y++ {
		out = append(out, shotRowToLine(g, y, p.ScrollX, cols))
	}
	// Pad so the panel does not change height as the capture scrolls, which
	// would move the footer out from under the pointer.
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

// shotRowToLine styles one grid row into a lipgloss string, merging runs the
// way the ANSI backend does so a wide row is a handful of spans and not one
// span per cell.
func shotRowToLine(g *shot.Grid, y, fromCol, cols int) string {
	var b strings.Builder
	var run strings.Builder
	var style shot.Cell
	have := false

	flush := func() {
		if !have || run.Len() == 0 {
			run.Reset()
			have = false
			return
		}
		b.WriteString(shotCellStyle(g, style).Render(run.String()))
		run.Reset()
		have = false
	}
	drawn := 0
	for x := fromCol; x < g.Cols && drawn < cols; x++ {
		c := g.Cells[y][x]
		if c.Width == 0 {
			continue
		}
		if have && !style.SameStyle(c) {
			flush()
		}
		if !have {
			style, have = c, true
		}
		text := c.Cluster
		if text == "" {
			text = " "
		}
		run.WriteString(text)
		drawn += max(1, int(c.Width))
	}
	flush()
	return b.String()
}

// shotCellStyle turns a resolved cell's style into a lipgloss style. The
// colours are already concrete RGB, so nothing is guessed a second time here.
func shotCellStyle(g *shot.Grid, c shot.Cell) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(shot.Hex(c.FG)))
	if !c.BGDefault {
		s = s.Background(lipgloss.Color(shot.Hex(c.BG)))
	} else {
		s = s.Background(lipgloss.Color(shot.Hex(g.BG)))
	}
	if c.Bold {
		s = s.Bold(true)
	}
	if c.Italic {
		s = s.Italic(true)
	}
	if c.Faint {
		s = s.Faint(true)
	}
	if c.Strike {
		s = s.Strikethrough(true)
	}
	if c.Underline != shot.UnderlineNone {
		s = s.Underline(true)
	}
	return s
}

// shotPreviewImageRow is the body row the capture starts on, which the pixel
// tier places its picture at so the image lands exactly where the cells would.
func (m *OS) shotPreviewImageRow() int {
	row := 2 // the header line and the path
	if m.ShotPreview.Note != "" {
		row++
	}
	if m.ShotPreview.Status != "" {
		row++
	}
	return row + 1 // the blank between the metadata and the capture
}
