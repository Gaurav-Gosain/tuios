package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type listOverlay struct {
	Title        string
	Width        int
	MaxVisible   int
	Search       bool
	Query        string
	SearchLine   func(width int) string
	Count        int
	Selected     int
	Scroll       *int
	EmptyMsg     string
	EmptyHint    overlay.Hint
	Pending      bool
	Hints        []overlay.Hint
	DetailFor    func(width int) []string
	DetailAt     func(x, y int)
	DetailStyled bool
	RenderRow    func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string
	Position     func() (n, of int)
	Unfocused    bool
}

func (m *OS) listOverlayLayout(cfg listOverlay) (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(cfg.Width)
	extra := 1
	if cfg.Search {
		extra += 2
	}
	if cfg.DetailFor != nil {
		if n := len(cfg.DetailFor(width)); n > 0 {
			extra += n + 1
		}
	}
	rows, hints = m.panelBody(cfg.MaxVisible, extra, width, nil, cfg.Hints)
	return width, rows, hints
}

func (m *OS) renderListOverlay(cfg listOverlay) (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Surface

	cfg.Width, cfg.MaxVisible, cfg.Hints = m.listOverlayLayout(cfg)
	scroll := 0
	if cfg.Scroll != nil {
		*cfg.Scroll = scrollWindow(*cfg.Scroll, cfg.Selected, cfg.Count, cfg.MaxVisible)
		scroll = *cfg.Scroll
	}

	var lines []string
	rowYOffset := 0
	if cfg.Search {
		search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil()) +
			overlay.Style(bg).Foreground(pal.Fg).Render(cfg.Query) +
			overlay.Cursor(" ", bg, pal.Fg)
		if cfg.SearchLine != nil {
			search = cfg.SearchLine(cfg.Width)
		}
		lines = append(lines, search, overlay.Rule(cfg.Width, bg, pal))
		rowYOffset = 2
	}

	start := scroll
	end := min(start+cfg.MaxVisible, cfg.Count)
	shown := 0
	for i := start; i < end; i++ {
		st := overlay.RowState{Cursor: i == cfg.Selected, Focused: !cfg.Unfocused}
		rowBg := pal.Ground(st, bg)
		lines = append(lines, pal.Row(cfg.RenderRow(i, i == cfg.Selected, rowBg, pal, cfg.Width), cfg.Width, st, bg))
		shown++
	}
	if cfg.Count == 0 && !cfg.Pending {
		empty := overlay.Empty{Message: cfg.EmptyMsg, Hint: cfg.EmptyHint}
		if empty.Message == "" {
			empty.Message = "Nothing here"
		}
		if empty.Hint.Key == "" {
			empty.Hint = overlay.Hint{Key: "esc", Label: "close"}
		}
		lines = append(lines, empty.Lines(cfg.Width, cfg.MaxVisible, bg, pal)...)
		shown = cfg.MaxVisible
	}
	for shown < cfg.MaxVisible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}
	if cfg.Count > cfg.MaxVisible {
		n, of := cfg.Selected+1, cfg.Count
		if cfg.Position != nil {
			n, of = cfg.Position()
		}
		info := fmt.Sprintf("%d of %d", n, of)
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}
	detailRow := -1
	if cfg.DetailFor != nil {
		if detail := cfg.DetailFor(cfg.Width); len(detail) > 0 {
			lines = append(lines, overlay.Rule(cfg.Width, bg, pal))
			detailRow = len(lines)
			for _, l := range detail {
				if cfg.DetailStyled {
					lines = append(lines, l)
					continue
				}
				lines = append(lines, overlay.Style(bg).Foreground(pal.Fg).Render(l))
			}
		}
	}

	panel := overlay.Panel{
		Title: cfg.Title,
		Width: cfg.Width,
		Body:  strings.Join(lines, "\n"),
		Hints: cfg.Hints,
	}
	content, geo := panel.Render(pal)
	if cfg.DetailAt != nil && detailRow >= 0 {
		cfg.DetailAt(geo.BodyX, geo.BodyY+detailRow)
	}

	rows := make([]overlayRowHit, 0, max(end-start, 0))
	for i := start; i < end; i++ {
		rowY := geo.BodyY + (i - start) + rowYOffset
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
	}
	return content, geo, rows
}

func (m *OS) simpleOverlayPanel(title string, bodyLines []string, hints []overlay.Hint) (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Surface
	width := m.panelWidth(simplePanelWidth)
	var styled []string
	for _, l := range bodyLines {
		for _, wrapped := range wrapPlain(l, width-2) {
			styled = append(styled, overlay.Style(bg).Foreground(pal.FgDim).Render("  "+wrapped))
		}
	}
	panel := overlay.Panel{
		Title: title,
		Width: width,
		Body:  strings.Join(styled, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

const simplePanelWidth = 52

func wrapPlain(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" || (len(s) <= width && ansi.StringWidth(s) <= width) {
		return []string{s}
	}
	var out []string
	var line strings.Builder
	lineW := 0
	flush := func() {
		if lineW > 0 || line.Len() > 0 {
			out = append(out, line.String())
			line.Reset()
			lineW = 0
		}
	}
	for word := range strings.FieldsSeq(s) {
		wordW := ansi.StringWidth(word)
		if wordW > width {
			flush()
			rest := word
			for rest != "" {
				cluster, w := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
				if cluster == "" {
					break
				}
				if lineW+w > width && lineW > 0 {
					flush()
				}
				line.WriteString(cluster)
				lineW += w
				rest = rest[len(cluster):]
			}
			continue
		}
		switch {
		case lineW == 0 && line.Len() == 0:
			line.WriteString(word)
			lineW = wordW
		case lineW+1+wordW <= width:
			line.WriteByte(' ')
			line.WriteString(word)
			lineW += 1 + wordW
		default:
			flush()
			line.WriteString(word)
			lineW = wordW
		}
	}
	flush()
	return out
}

const listOverlayRows = 10

func listRowMarker(selected bool) string {
	if selected {
		return overlay.Sigil()
	}
	return "  "
}

func listRowSpans(width int, marker, left, right string, bg color.Color, pal overlay.Palette) string {
	m := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker)
	ell := overlay.Style(bg).Foreground(pal.FgMute).Render(overlay.Ellipsis())
	avail := width - lipgloss.Width(m) - lipgloss.Width(right) - 1
	if lipgloss.Width(left) > avail {
		left = ansi.Truncate(left, max(avail-lipgloss.Width(ell), 0), "") + ell
	}
	gap := max(width-lipgloss.Width(m)-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return m + left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + right
}

func listRowLine(width int, marker, label, trailing string, labelColor, trailingColor color.Color, bold bool, bg color.Color, pal overlay.Palette) string {
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(labelColor).Bold(bold).Render(label)
	trail := ""
	if trailing != "" {
		trail = overlay.Style(bg).Foreground(trailingColor).Render(trailing)
	}
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(trail), 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + trail
}
