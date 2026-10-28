// Ported from pele src/diagrams/kanban/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package kanban

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

const (
	COLUMN_GAP = 12
	COLUMN_PAD = 8
	CARD_PAD   = 10
	CARD_GAP   = 8
	META_GAP   = 4
	ICON_GAP   = 6
)

// PRIORITY: Mermaid names five priorities and gives Medium no color. Matching here ignores case. The high
// ones are marked in the critical color and the low ones in the muted one, the nearer to Medium
// the fainter. They are not series colors, as a theme may put those in any order.
// It is a switch rather than a map, which would allocate at init.
func PRIORITY(priority string) (string, bool) {
	switch priority {
	case "very high":
		return `stroke="` + svg.CRITICAL + `"`, true
	case "high":
		return `stroke="` + svg.CRITICAL + `" opacity="0.5"`, true
	case "low":
		return `stroke="var(--_m)" opacity="0.5"`, true
	case "very low":
		return `stroke="var(--_m)"`, true
	}
	return "", false
}

// priorityKey is priority.trim().toLowerCase(). Go lowers U+0130 to "i" where JavaScript
// gives "i" and a combining dot; a key with one matches no priority either way.
func priorityKey(priority string) string {
	if strings.ContainsRune(priority, 'İ') {
		return "İ"
	}
	return strings.ToLower(js.Trim(priority))
}

// fitLabel lays out text for a fixed width. A word too long for a line is broken between characters,
// because a card cannot grow sideways to hold it.
func fitLabel(raw string, markdown bool, measurer text.TextMeasurer, size, max float64, base int) text.Label {
	label := text.LayoutLabel(raw, markdown, measurer, size, max, base)
	if label.Width <= max {
		return label
	}
	var lines [][]text.Span
	var widths []float64
	widest := 0.0
	var line []text.Span
	width := 0.0
	commit := func() {
		lines = append(lines, line)
		widths = append(widths, width)
		widest = math.Max(widest, width)
		line = nil
		width = 0
	}
	for i := range label.Lines {
		if label.Widths[i] <= max {
			line = label.Lines[i]
			width = label.Widths[i]
			commit()
			continue
		}
		for _, span := range label.Lines[i] {
			if span.Icon != "" || span.Math != nil {
				if width > 0 && width+span.Width > max {
					commit()
				}
				line = append(line, span)
				width += span.Width
				continue
			}
			var t strings.Builder
			from := width
			for _, ch := range span.Text {
				w := measurer.Width(string(ch), size, span.Style)
				if width > 0 && width+w > max {
					if t.Len() > 0 {
						line = append(line, text.Span{Text: t.String(), Style: span.Style, Width: width - from})
					}
					commit()
					t.Reset()
					from = 0
				}
				t.WriteRune(ch)
				width += w
			}
			if t.Len() > 0 {
				line = append(line, text.Span{Text: t.String(), Style: span.Style, Width: width - from})
			}
		}
		commit()
	}
	label.Lines = lines
	label.Widths = widths
	label.Width = widest
	label.Height = float64(len(lines)) * label.LineHeight
	if label.Heights != nil {
		return text.WithHeights(label)
	}
	return label
}

// textBlock writes a label with every line starting at x, or ending there, and the first line's box starting at y.
func textBlock(b *strings.Builder, label text.Label, x, y float64, attrs string, icons text.IconResolver, end bool) {
	for i, line := range label.Lines {
		cy := y + text.LineMiddle(label, i)
		if len(line) == 1 && line[0].Icon == "" && line[0].Math == nil && line[0].Style&(text.Sub|text.Sup|text.Underline) == 0 {
			svg.WriteStrings(b, `<text`, svg.TextStyle(attrs, line[0].Style), ` x="`, svg.Num(x), `" y="`, svg.Num(cy+float64(label.Size*0.35)), `"`)
			if end {
				b.WriteString(` text-anchor="end"`)
			}
			svg.WriteStrings(b, `>`, svg.Esc(line[0].Text), `</text>`)
		} else {
			width := label.Widths[i]
			dx := width
			if end {
				dx = -width
			}
			b.WriteString(svg.LabelSvg(text.LabelLine(label, i), x+dx/2, cy, attrs, icons))
		}
	}
}

type Card struct {
	item     *KanbanNode
	label    text.Label
	ticket   text.Label
	assigned text.Label
	// Height of the rows under the text that hold the ticket and the assignee.
	meta   float64
	height float64
}

func renderKanban(model *KanbanDb, config types.Config, options types.RenderOptions) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	line := js.Round(size * 1.5)
	smallLine := js.Round(small * 1.5)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	icons := options.Icons
	has := func(name string) bool { return name != "" && text.IconMarkup(measurer, name) != "" }
	conf, _ := config["kanban"].(map[string]any)
	glyph := float64(small * 1.25)
	// Math can't break across lines, so columns widen to hold the widest expression.
	mathWidth := 0.0
	holds := func(raw string, fontSize, reserved float64) {
		if raw == "" || !strings.Contains(raw, "$$") {
			return
		}
		for _, spans := range text.LayoutLabel(raw, true, measurer, fontSize, math.Inf(1), 0).Lines {
			for _, span := range spans {
				if span.Math != nil {
					mathWidth = math.Max(mathWidth, span.Math.Width+reserved)
				}
			}
		}
	}
	for _, section := range model.sections {
		count := measurer.Width(strconv.Itoa(len(section.items)), small, 0)
		reserved := 2*COLUMN_PAD + 8 + count + 12
		if has(section.icon) {
			reserved += glyph + ICON_GAP
		}
		holds(section.label, size, reserved)
		for _, item := range section.items {
			reserved := float64(2 * (COLUMN_PAD + CARD_PAD))
			iconRoom := 0.0
			if has(item.icon) {
				iconRoom = glyph + ICON_GAP
			}
			holds(item.label, small, reserved+iconRoom)
			holds(item.ticket, small, reserved)
			holds(item.assigned, small, reserved)
		}
	}
	ownWidth := 200.0
	if sectionWidth, ok := conf["sectionWidth"].(float64); ok && sectionWidth > 0 {
		ownWidth = math.Min(math.Max(sectionWidth, 80), 2000)
	}
	ownWidth = math.Max(ownWidth, math.Ceil(mathWidth))
	// A board wider than the width the host has is folded into rows of columns, which then
	// share the row between them.
	room := math.Inf(1)
	if options.MaxWidth != nil && *options.MaxWidth > 0 {
		room = *options.MaxWidth - float64(2*pad)
	}
	count := len(model.sections)
	fits := float64(float64(count)*(ownWidth+COLUMN_GAP))-COLUMN_GAP <= room
	var perRow int
	var columnWidth float64
	if fits {
		perRow = max(count, 1)
		columnWidth = ownWidth
	} else {
		perRow = int(math.Max(1, math.Floor((room+COLUMN_GAP)/(ownWidth+COLUMN_GAP))))
		columnWidth = math.Max(math.Max(math.Min(ownWidth, room), math.Min(math.Floor((room+COLUMN_GAP)/float64(perRow))-COLUMN_GAP, ownWidth*2)), math.Ceil(mathWidth))
	}
	baseUrl, _ := conf["ticketBaseUrl"].(string)
	cardWidth := columnWidth - 2*COLUMN_PAD
	textWidth := cardWidth - 2*CARD_PAD
	links := []types.LinkInfo{}

	sections := model.sections
	titles := make([]text.Label, 0, len(sections))
	countWidths := make([]float64, 0, len(sections))
	cards := make([][]Card, 0, len(sections))
	headHeight := line
	bodyHeights := make([]float64, 0, len(sections))
	for _, section := range sections {
		countWidth := measurer.Width(strconv.Itoa(len(section.items)), small, 0)
		reserved := countWidth + 12
		if has(section.icon) {
			reserved += glyph + ICON_GAP
		}
		title := fitLabel(section.label, true, measurer, size, cardWidth-8-reserved, text.Bold)
		headHeight = math.Max(headHeight, title.Height)
		countWidths = append(countWidths, countWidth)
		titles = append(titles, title)

		list := make([]Card, 0, len(section.items))
		height := 0.0
		for _, item := range section.items {
			iconRoom := 0.0
			if has(item.icon) {
				iconRoom = glyph + ICON_GAP
			}
			label := fitLabel(item.label, true, measurer, small, textWidth-iconRoom, 0)
			ticket := fitLabel(item.ticket, false, measurer, small, textWidth, 0)
			assigned := fitLabel(item.assigned, false, measurer, small, textWidth, 0)
			meta := math.Max(ticket.Height, assigned.Height)
			if ticket.Width+assigned.Width+8 > textWidth {
				meta = ticket.Height + assigned.Height
			}
			cardHeight := 2*CARD_PAD + math.Max(label.Height, smallLine)
			if meta != 0 {
				cardHeight += META_GAP + meta
			}
			list = append(list, Card{item, label, ticket, assigned, meta, cardHeight})
			height += cardHeight + CARD_GAP
		}
		cards = append(cards, list)
		bodyHeights = append(bodyHeights, height)
	}
	headHeight += 2 * COLUMN_PAD
	var rowHeights, rowTops []float64
	for first, y := 0, 0.0; first < len(sections); first += perRow {
		tallest := float64(CARD_GAP)
		for index := first; index < min(first+perRow, len(sections)); index++ {
			tallest = math.Max(tallest, bodyHeights[index])
		}
		rowTops = append(rowTops, y)
		rowHeights = append(rowHeights, headHeight+tallest)
		y += headHeight + tallest + COLUMN_GAP
	}
	boardHeight := 0.0
	if len(rowHeights) > 0 {
		boardHeight = rowTops[len(rowTops)-1] + rowHeights[len(rowHeights)-1]
	}

	title := svg.TitleLabel(model.title, measurer, size, common.TitleRoom(options))
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 16
	}
	boardWidth := math.Max(float64(float64(min(len(sections), perRow))*(columnWidth+COLUMN_GAP))-COLUMN_GAP, 0)
	width := math.Max(boardWidth, title.Width)
	left := pad + (width-boardWidth)/2
	edge := columnWidth - COLUMN_PAD - 4

	var out, body strings.Builder
	for index, section := range sections {
		row := index / perRow
		body.Reset()
		svg.WriteStrings(&body, `<rect width="`, js.Num(columnWidth), `" height="`, num(rowHeights[row]), `" fill="var(--_s)"`, svg.ROUNDED, `/>`)
		textBlock(&body, titles[index], COLUMN_PAD+4, COLUMN_PAD, ` class="pele-cluster-label" font-weight="var(--_hw)"`, icons, false)
		svg.WriteStrings(&body, `<text class="pele-count" x="`, js.Num(edge), `" y="`, num(COLUMN_PAD+line/2+float64(small*0.35)),
			`" text-anchor="end" font-size="`, js.Num(small), `" fill="var(--_m)">`, strconv.Itoa(len(section.items)), `</text>`)
		if has(section.icon) {
			body.WriteString(common.IconSvg(section.icon, edge-countWidths[index]-ICON_GAP-glyph, COLUMN_PAD+(line-glyph)/2, glyph, icons))
		}

		y := headHeight
		for _, card := range cards[index] {
			item := card.item
			priority := priorityKey(item.priority)
			color, colored := PRIORITY(priority)
			var inner strings.Builder
			svg.WriteStrings(&inner, `<rect width="`, js.Num(cardWidth), `" height="`, num(card.height), `" fill="var(--_bg)" stroke="var(--_b)"`, svg.ROUNDED, `/>`)

			if colored {
				svg.WriteStrings(&inner, `<path class="pele-priority" d="M2.5,5V`, num(card.height-5), `" fill="none" `, color, ` stroke-width="3" stroke-linecap="round"/>`)
			}
			textBlock(&inner, card.label, CARD_PAD, CARD_PAD, ` class="pele-label"`, icons, false)
			if has(item.icon) {
				inner.WriteString(common.IconSvg(item.icon, cardWidth-CARD_PAD-glyph, CARD_PAD+(smallLine-glyph)/2, glyph, icons))
			}
			bottom := card.height - CARD_PAD
			if item.ticket != "" {
				top := bottom - card.meta
				href, ok := "", false
				if baseUrl != "" {
					href, ok = util.LinkUrl(util.SafeUrl(strings.Replace(baseUrl, "#TICKET#", item.ticket, 1)), options)
				}
				if !ok {
					textBlock(&inner, card.ticket, CARD_PAD, top, ` class="pele-ticket" fill="var(--_m)"`, icons, false)
				} else {
					svg.WriteStrings(&inner, `<a href="`, svg.Esc(href), `" target="_blank"`, util.RelAttr(options), `>`)
					textBlock(&inner, card.ticket, CARD_PAD, top, ` class="pele-ticket" fill="var(--_c)"`, icons, false)
					inner.WriteString(`</a>`)
					links = append(links, types.LinkInfo{ID: item.id, Href: href, Internal: false})
				}
			}
			if item.assigned != "" {
				textBlock(&inner, card.assigned, cardWidth-CARD_PAD, bottom-card.assigned.Height, ` class="pele-assigned" fill="var(--_m)"`, icons, true)
			}
			class := ""
			if colored {
				class = " pele-priority-" + strings.Replace(priority, " ", "-", 1)
			}
			svg.WriteStrings(&body, `<g class="pele-node pele-card`, class, svg.ClassNames(item.cssClasses), `" data-id="`, svg.EscText(item.id),
				`" transform="translate(`, strconv.Itoa(COLUMN_PAD), `,`, num(y), `)" font-size="`, js.Num(small), `">`, inner.String(), `</g>`)
			y += card.height + CARD_GAP
		}

		svg.WriteStrings(&out, `<g class="pele-cluster pele-column`, svg.ClassNames(section.cssClasses), `" data-id="`, svg.EscText(section.id),
			`" transform="translate(`, num(left+float64(float64(index%perRow)*(columnWidth+COLUMN_GAP))), `,`, num(pad+titleHeight+rowTops[row]), `)">`,
			body.String(), `</g>`)
	}

	totalWidth := math.Ceil(width + float64(2*pad))
	totalHeight := math.Ceil(boardHeight + titleHeight + float64(2*pad))
	var geometry *types.Geometry
	if options.Geometry != nil {
		board := &types.GeoKanban{Title: title.Plain()}
		for index := range sections {
			column := types.GeoColumn{Label: titles[index].Plain()}
			for _, card := range cards[index] {
				column.Cards = append(column.Cards, types.GeoCard{Label: card.label.Plain(), Ticket: card.item.ticket,
					Assigned: card.item.assigned, Priority: js.Trim(card.item.priority)})
			}
			board.Columns = append(board.Columns, column)
		}
		geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Kanban: board}
	}
	return types.Rendered{
		Geometry: geometry,
		SVG: svg.SvgDocument("kanban", totalWidth, totalHeight, size, options, svg.Accessible{},
			svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil), out.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  links,
	}
}
