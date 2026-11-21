package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/units"
	"tuios.dev/shot"
)

const overlayKindShot = "screenshot"

const shotPreviewWidth = 76

const shotPreviewRows = 18

func (m *OS) screenshotPreviewBody() (cols, rows int) {
	width := m.panelWidth(shotPreviewWidth)
	cols = max(1, width-2)
	rows = m.panelBodyRows(shotPreviewRows, shotPreviewExtraRows, width, nil, m.shotPreviewHints())
	return cols, max(1, rows)
}

const shotPreviewExtraRows = 6

func (m *OS) shotPreviewHints() []overlay.Hint {
	p := &m.ShotPreview
	hints := []overlay.Hint{{Key: overlay.EnterGlyph, Label: "done"}}
	if !p.Pending {
		if p.CopyLabel != "" {
			hints = append(hints, overlay.Hint{Key: "c", Label: p.CopyLabel})
		}
		if !m.IsRemoteClient() {
			hints = append(hints, overlay.Hint{Key: "o", Label: "open"})
		}
	}
	hints = append(hints,
		overlay.Hint{Key: "r", Label: "retake"},
		overlay.Hint{Key: "esc", Label: "discard"},
	)
	return hints
}

func (m *OS) shotPreviewMetaLines() []string {
	p := &m.ShotPreview
	out := []string{m.shotPreviewHeader()}
	if p.Pending {
		return append(out, p.Status)
	}
	out = append(out, truncateLeft(shortenPath(p.Path), m.panelWidth(shotPreviewWidth)-4))
	if p.Note != "" {
		out = append(out, p.Note)
	}
	if p.Status != "" {
		out = append(out, p.Status)
	}
	return out
}

func (m *OS) renderScreenshotPreview() (string, overlay.Geometry, []overlayRowHit) {
	p := &m.ShotPreview
	pal := m.overlayUI()
	width := m.panelWidth(shotPreviewWidth)
	bodyCols, bodyRows := m.screenshotPreviewBody()
	hints := m.shotPreviewHints()

	dim := overlay.Style(pal.Surface).Foreground(pal.FgDim)
	mute := overlay.Style(pal.Surface).Foreground(pal.FgMute)

	var lines []string
	for i, meta := range m.shotPreviewMetaLines() {
		style := mute
		if i == 0 {
			style = dim
		}
		lines = append(lines, overlay.Fill(style.Render("  "+meta), width, pal.Surface))
	}
	lines = append(lines, overlay.Fill("", width, pal.Surface))

	_, _, picRows, hasPicture := m.screenshotPreviewPictureBox()
	rows := m.shotPreviewCells(bodyCols, bodyRows)
	if hasPicture {
		rows = pictureBodyRows(picRows)
	}
	gutter := mute.Render("  ")
	for _, row := range rows {
		lines = append(lines, overlay.Fill(gutter+row, width, pal.Surface))
	}
	if !hasPicture && p.Grid != nil && (p.Grid.Rows > bodyRows || p.Grid.Cols > bodyCols) {
		lines = append(lines, overlay.Fill(mute.Render(
			fmt.Sprintf("  Showing %d of %d rows. Scroll with the wheel or the arrows.",
				min(bodyRows, p.Grid.Rows), p.Grid.Rows)), width, pal.Surface))
	}

	panel := overlay.Panel{
		Title: "Screenshot",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

func pictureBodyRows(picRows int) []string {
	return make([]string, max(0, picRows))
}

func (m *OS) shotPreviewHeader() string {
	p := &m.ShotPreview
	cols, rows := 0, 0
	if p.Grid != nil {
		cols, rows = p.Grid.Cols, p.Grid.Rows
	}
	line := fmt.Sprintf("%s  %dx%d cells", strings.ToUpper(string(p.Format)), cols, rows)
	if p.Bytes > 0 {
		line += "  " + units.Bytes(int64(p.Bytes))
	}
	return line
}

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
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

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

func (m *OS) shotPreviewImageRow() int {
	return len(m.shotPreviewMetaLines()) + 1
}
