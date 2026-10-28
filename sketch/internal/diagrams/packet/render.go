// Ported from pele src/diagrams/packet/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package packet

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

func renderPacket(model *PacketModel, config types.Config, options types.RenderOptions) types.Rendered {
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(model, config, options, down) })
}

// pow2 is 2 ** k for a whole k.
func pow2(k float64) float64 {
	if math.IsNaN(k) || math.IsInf(k, 0) || k > 2000 || k < -2000 {
		return math.Pow(2, k)
	}
	return math.Ldexp(1, int(k))
}

type fitted struct {
	f  common.Fitted
	ok bool
}

func draw(model *PacketModel, config types.Config, options types.RenderOptions, down bool) types.Rendered {
	size := types.Or(options.FontSize, 16)
	labelSize := js.Round(size * 0.875)
	bitSize := js.Round(size * 0.625)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	packet, _ := config["packet"].(map[string]any)
	rowHeight := packetOption(config, "rowHeight", 32, 1, 400)
	bitWidth := packetOption(config, "bitWidth", 32, 1, 400)
	paddingX := packetOption(config, "paddingX", 5, 0, 400)
	paddingY := packetOption(config, "paddingY", 5, 0, 400)
	showBits := packet["showBits"] != false
	descending := packet["bitOrder"] == "descending"
	bitsPerRow := model.bitsPerRow
	rows := model.rows
	if down {
		maxWidth := types.Or(options.MaxWidth, 320)
		count := math.Max(1, math.Floor((maxWidth-float64(2*pad))/math.Min(bitWidth, 18)))
		// A byte is the shortest row. Below that every label is one character to a line, and a long
		// packet is a row to a bit.
		bitsPerRow = math.Min(bitsPerRow, math.Max(8, pow2(math.Floor(js.Log2(count)))))
		last := 0.0
		if n := len(model.rows); n > 0 {
			if lastRow := model.rows[n-1]; len(lastRow) > 0 {
				last = lastRow[len(lastRow)-1].end
			}
		}
		if math.Ceil((last+1)/bitsPerRow) > 10000 {
			bitsPerRow = model.bitsPerRow
		}
		bitWidth = math.Min(bitWidth, math.Max(18, (maxWidth-float64(2*pad))/bitsPerRow))
		// The model has split each field at its own rows. Its pieces are joined again first, so that
		// a field is only split where one of the new rows ends. A piece counts one bit short.
		piece := func(block *PacketBlock) bool { return block.bits == block.end-block.start }
		var whole []PacketBlock
		for _, row := range model.rows {
			for _, block := range row {
				if n := len(whole); n > 0 {
					prev := &whole[n-1]
					if piece(prev) && piece(&block) && prev.label == block.label && block.start == prev.end+1 && math.Mod(block.start, model.bitsPerRow) == 0 {
						prev.end = block.end
						prev.bits = prev.end - prev.start
						continue
					}
				}
				whole = append(whole, block)
			}
		}
		rows = nil
		for _, block := range whole {
			for start := block.start; start <= block.end; {
				end := math.Min(block.end, float64((math.Floor(start/bitsPerRow)+1)*bitsPerRow)-1)
				index := int(math.Floor(start / bitsPerRow))
				for len(rows) <= index {
					rows = append(rows, nil)
				}
				piece := block
				piece.start, piece.end = start, end
				rows[index] = append(rows[index], piece)
				start = end + 1
			}
		}
	}
	var labels [][]text.Label
	if down {
		labels = make([][]text.Label, len(rows))
		for r, row := range rows {
			labels[r] = make([]text.Label, len(row))
			for b, block := range row {
				room := math.Max(1, float64((block.end-block.start+1)*bitWidth)-math.Min(paddingX, bitWidth-1)-6)
				labels[r][b] = common.LiteralLabel(js.Trim(text.DecodeEntities(block.label)), measurer, labelSize, room, 0)
			}
		}
	}
	// Bit numbers sit above each row and need room of their own.
	numbers := 0.0
	if showBits {
		numbers = bitSize + 4
	}
	rowHeights := make([]float64, len(rows))
	for r := range rows {
		height := rowHeight
		if down {
			for b := range rows[r] {
				height = math.Max(height, labels[r][b].Height+10)
			}
		}
		rowHeights[r] = height
	}
	tops := make([]float64, 0, len(rows))
	rowsHeight := 0.0
	for _, height := range rowHeights {
		tops = append(tops, rowsHeight)
		rowsHeight += numbers + height + paddingY
	}
	if len(rows) > 0 {
		rowsHeight -= paddingY
	}

	gap := math.Min(paddingX, bitWidth-1)
	width := 0.0
	if len(rows) > 0 {
		width = float64(bitsPerRow*bitWidth) - gap
	}
	titleRoom := 4000.0
	if down {
		titleRoom = common.TitleRoom(options)
	}
	title := text.LayoutLabel(model.title, false, measurer, size, titleRoom, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height
		if rowsHeight > 0 {
			titleHeight += 12
		}
	}

	// The pieces of a field split across rows share its label, so each width is fitted once.
	fits := map[string]map[float64]fitted{}
	fit := func(label string, max float64) fitted {
		byWidth := fits[label]
		if byWidth == nil {
			byWidth = map[float64]fitted{}
			fits[label] = byWidth
		}
		if got, ok := byWidth[max]; ok {
			return got
		}
		f, ok := common.FitText(js.Trim(text.DecodeEntities(label)), max, labelSize, js.Round(size*0.5), measurer, 0)
		byWidth[max] = fitted{f, ok}
		return byWidth[max]
	}

	var geo *types.GeoPacket
	if options.Geometry != nil {
		geo = &types.GeoPacket{Title: title.Plain(), BitsPerRow: int(math.Min(bitsPerRow, 4096)), Descending: descending, ShowBits: showBits}
	}
	left := pad + math.Max(0, (title.Width-width)/2)
	var fields, bits strings.Builder
	for index, row := range rows {
		y := pad + tops[index] + numbers
		rowHeight := rowHeights[index]
		for b, block := range row {
			count := block.end - block.start + 1
			first := math.Mod(block.start, bitsPerRow)
			x := left + float64(first*bitWidth)
			if descending {
				x = left + float64((bitsPerRow-first-count)*bitWidth)
			}
			w := float64(count*bitWidth) - gap
			svg.WriteStrings(&fields, `<g class="pele-node" data-id="`, js.Num(block.start), `-`, js.Num(block.end), `">`,
				`<rect x="`, svg.Num(x), `" y="`, svg.Num(y), `" width="`, svg.Num(w), `" height="`, svg.Num(rowHeight), `" fill="var(--_s)" stroke="var(--_b)"`, svg.ROUNDED, `/>`)

			if down {
				fields.WriteString(svg.LabelSvg(labels[index][b], x+w/2, y+rowHeight/2, "", nil))
			} else if t := fit(block.label, w-6); t.ok && t.f.Size <= rowHeight {
				svg.WriteStrings(&fields, `<text x="`, svg.Num(x+w/2), `" y="`, svg.Num(y+rowHeight/2+float64(t.f.Size*0.35)), `"`)
				if t.f.Size != labelSize {
					svg.WriteStrings(&fields, ` font-size="`, svg.Num(t.f.Size), `"`)
				}
				svg.WriteStrings(&fields, `>`, svg.Esc(t.f.Text), `</text>`)
			}
			fields.WriteString(`</g>`)
			if geo != nil {
				geo.Fields = append(geo.Fields, types.GeoField{Row: index, Start: block.start, End: block.end, Label: js.Trim(text.DecodeEntities(block.label))})
			}
			if !showBits {
				continue
			}
			leading, trailing := block.start, block.end
			if descending {
				leading, trailing = block.end, block.start
			}
			if count == 1 {
				svg.WriteStrings(&bits, `<text x="`, svg.Num(x+w/2), `" y="`, svg.Num(y-4), `" text-anchor="middle">`, js.Num(leading), `</text>`)
			} else {
				svg.WriteStrings(&bits, `<text x="`, svg.Num(x), `" y="`, svg.Num(y-4), `">`, js.Num(leading), `</text>`,
					`<text x="`, svg.Num(x+w), `" y="`, svg.Num(y-4), `" text-anchor="end">`, js.Num(trailing), `</text>`)
			}
		}
	}

	totalWidth := math.Ceil(math.Max(width, title.Width) + float64(2*pad))
	totalHeight := math.Ceil(rowsHeight + titleHeight + float64(2*pad))
	var content strings.Builder
	if fields.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-packet-fields" font-size="`, js.Num(labelSize), `" text-anchor="middle">`, fields.String(), `</g>`)
	}
	if bits.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-packet-bits" font-size="`, js.Num(bitSize), `" fill="var(--_m)">`, bits.String(), `</g>`)
	}
	// Mermaid puts a packet's title under it.
	content.WriteString(svg.LabelSvg(title, totalWidth/2, totalHeight-pad-title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))

	rendered := types.Rendered{
		SVG:    svg.SvgDocument("packet", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
	if geo != nil {
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Packet: geo}
	}
	return rendered
}
