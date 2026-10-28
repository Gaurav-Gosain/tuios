// Ported from pele src/diagrams/pie/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package pie

import (
	"math"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	RADIUS  = 120
	SWATCH  = 12
	ROW     = 26
	GAP     = 32
	OUTSIDE = 28
)

func option(config types.Config, key string) any {
	conf, _ := config["pie"].(map[string]any)
	return conf[key]
}

func point(angle, radius float64) string {
	return svg.Num(js.Sin(angle)*radius) + "," + svg.Num(-js.Cos(angle)*radius)
}

func ring(r float64, sweep string) string {
	num := svg.Num
	return "M0," + num(-r) + "A" + num(r) + "," + num(r) + " 0 1 " + sweep + " 0," + num(r) + "A" + num(r) + "," + num(r) + " 0 1 " + sweep + " 0," + num(-r) + "Z"
}

func slice(start, end, outer, inner float64) string {
	num := svg.Num
	if end-start >= math.Pi*2-1e-9 {
		out := ring(outer, "1")
		if inner > 0 {
			out += ring(inner, "0")
		}
		return out
	}
	large := "0"
	if end-start > math.Pi {
		large = "1"
	}
	if inner <= 0 {
		return "M0,0L" + point(start, outer) + "A" + num(outer) + "," + num(outer) + " 0 " + large + " 1 " + point(end, outer) + "Z"
	}
	return "M" + point(start, outer) + "A" + num(outer) + "," + num(outer) + " 0 " + large + " 1 " + point(end, outer) +
		"L" + point(end, inner) + "A" + num(inner) + "," + num(inner) + " 0 " + large + " 0 " + point(start, inner) + "Z"
}

func renderPie(model *PieModel, config types.Config, options types.RenderOptions) types.Rendered {
	return common.FitWidth(options, RADIUS*2, 140, func(diameter float64, tight bool) types.Rendered {
		return draw(model, config, options, diameter/2, tight)
	})
}

type outsideLabel struct {
	x, y, width float64
}

// clear reports whether a label outside the rim at (x, y) keeps clear of those placed before it.
func clear(placedOutside []outsideLabel, x, y, width, small float64) bool {
	for _, other := range placedOutside {
		if !(math.Abs(x-other.x) >= (width+other.width)/2+2 || math.Abs(y-other.y) >= small+2) {
			return false
		}
	}
	return true
}

// `tight` is set when the chart does not fit the width it has with its legend beside it.
func draw(model *PieModel, config types.Config, options types.RenderOptions, radius float64, tight bool) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	textPosition := 0.75
	if position, ok := option(config, "textPosition").(float64); ok && position >= 0 && position <= 1 {
		textPosition = position
	}
	inner := 0.0
	if hole, ok := option(config, "donutHole").(float64); ok && hole > 0 && hole <= 0.9 {
		inner = hole * radius
	}
	placed := "right"
	if v := option(config, "legendPosition"); v != nil {
		placed = js.String(v)
	}
	legendPosition := placed
	if tight && (placed == "left" || placed == "right") {
		legendPosition = "bottom"
	}

	entries := model.labels
	sum := 0.0
	for _, label := range entries {
		sum += model.values[label]
	}

	legend := make([]text.Label, len(entries))
	for i, label := range entries {
		content := label
		if model.showData {
			content = label + " [" + js.Num(model.values[label]) + "]"
		}
		legend[i] = text.LayoutLabel(content, false, measurer, small, 4000, 0)
	}
	legendWidth := 0.0
	for _, label := range legend {
		legendWidth = math.Max(legendWidth, label.Width)
	}
	if len(legend) > 0 {
		legendWidth += SWATCH + 8
	}
	legendHeight := float64(len(legend) * ROW)

	title := text.LayoutLabel(model.title, false, measurer, size, common.TitleRoom(options), text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 16
	}

	// A slice too thin for its percentage gets the label outside the rim, which needs a margin.
	shares := make([]float64, len(entries))
	for i, label := range entries {
		share := 0.0
		if sum > 0 {
			share = model.values[label] / sum
		}
		if share*100 >= 1 && js.ToFixed0(share*100) != "0" {
			shares[i] = share
		}
	}
	narrow := make([]bool, len(entries))
	margin := 0.0
	for i, share := range shares {
		if share == 0 || share >= 0.5 {
			continue
		}
		chord := 2 * radius * textPosition * js.Sin(share*math.Pi)
		narrow[i] = chord < measurer.Width(js.ToFixed0(share*100)+"%", small, 0)+6
		if narrow[i] {
			margin = OUTSIDE
		}
	}

	side := ""
	if legendPosition == "left" || legendPosition == "right" || legendPosition == "center" {
		side = legendPosition
	}
	stacked := legendPosition == "top" || legendPosition == "bottom"
	diameter := (radius + margin) * 2
	width := diameter
	height := diameter
	if side == "left" || side == "right" {
		if legendWidth > 0 {
			width += GAP + legendWidth
		}
		height = math.Max(height, legendHeight)
	} else if stacked {
		width = math.Max(width, legendWidth)
		if legendHeight > 0 {
			height += 20 + legendHeight
		}
	}
	width = math.Max(width, title.Width)

	cx := pad
	switch side {
	case "left":
		cx += legendWidth + GAP + diameter/2
	case "right":
		cx += diameter / 2
	default:
		cx += width / 2
	}
	bodyTop := pad + titleHeight
	cy := bodyTop
	if legendPosition == "top" {
		cy += legendHeight + 20 + diameter/2
	} else if stacked {
		cy += diameter / 2
	} else {
		cy += height / 2
	}

	slices := ""
	labels := ""
	outside := ""
	angle := 0.0
	rim := radius + OUTSIDE/2
	var placedOutside []outsideLabel
	for index, label := range entries {
		share := shares[index]
		if share == 0 {
			continue
		}
		end := angle + float64(float64(share*math.Pi)*2)
		slices += `<path class="pele-slice" data-id="` + svg.EscText(label) + `" d="` + slice(angle, end, radius, inner) + `" fill="` + svg.SeriesColor(index) + `"/>`
		mid := (angle + end) / 2
		txt := js.ToFixed0(share*100) + "%"
		if narrow[index] {
			width := measurer.Width(txt, small, 0)
			at := mid
			for step := 0; step < 360 && !clear(placedOutside, float64(js.Sin(at)*rim), float64(-js.Cos(at)*rim), width, small); step++ {
				at += 2 / rim
			}
			x := float64(js.Sin(at) * rim)
			y := float64(-js.Cos(at) * rim)
			placedOutside = append(placedOutside, outsideLabel{x, y, width})
			outside += `<text x="` + num(x) + `" y="` + num(y+float64(small*0.35)) + `">` + txt + `</text>`
		} else {
			r := radius * textPosition
			labels += `<text x="` + num(js.Sin(mid)*r) + `" y="` + num(float64(-js.Cos(mid)*r)+float64(small*0.35)) + `">` + txt + `</text>`
		}
		angle = end
	}
	if slices == "" {
		slices = `<circle class="pele-slice" r="` + js.Num(radius) + `" fill="none" stroke="var(--_b)"/>`
	}

	legendOut := ""
	var lx, ly float64
	switch side {
	case "right":
		lx = cx + diameter/2 + GAP
		ly = bodyTop + (height-legendHeight)/2
	case "left":
		lx = pad
		ly = bodyTop + (height-legendHeight)/2
	case "center":
		lx = cx - legendWidth/2
		ly = cy - legendHeight/2
	default:
		lx = pad + (width-legendWidth)/2
		if legendPosition == "top" {
			ly = bodyTop
		} else {
			ly = bodyTop + diameter + 20
		}
	}
	for index, label := range legend {
		y := ly + float64(index*ROW) + ROW/2
		legendOut += `<g class="pele-legend-item" data-id="` + svg.EscText(entries[index]) + `">` +
			`<rect x="` + num(lx) + `" y="` + num(y-SWATCH/2) + `" width="` + js.Int(SWATCH) + `" height="` + js.Int(SWATCH) + `" rx="2" fill="` + svg.SeriesColor(index) + `"/>` +
			svg.LabelSvg(label, lx+SWATCH+8+label.Width/2, y, "", nil) +
			`</g>`
	}

	totalWidth := math.Ceil(width + float64(2*pad))
	totalHeight := math.Ceil(height + titleHeight + float64(2*pad))
	translate := `transform="translate(` + num(cx) + `,` + num(cy) + `)"`
	content := svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil) +
		`<g class="pele-slices" ` + translate + ` stroke="var(--_bg)" stroke-width="1" stroke-linejoin="round">` + slices + `</g>`
	if labels != "" {
		content += `<g class="pele-slice-labels" ` + translate + ` font-size="` + js.Num(small) + `" text-anchor="middle" fill="var(--_bg)">` + labels + `</g>`
	}
	if outside != "" {
		content += `<g class="pele-slice-labels" ` + translate + ` font-size="` + js.Num(small) + `" text-anchor="middle">` + outside + `</g>`
	}
	if legendOut != "" {
		content += `<g class="pele-legend" font-size="` + js.Num(small) + `">` + legendOut + `</g>`
	}

	rendered := types.Rendered{
		SVG:    svg.SvgDocument("pie", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
	if options.Geometry != nil {
		pie := &types.GeoPie{Title: title.Plain(), ShowData: model.showData}
		for _, label := range entries {
			share := 0.0
			if sum > 0 {
				share = model.values[label] / sum
			}
			pie.Slices = append(pie.Slices, types.GeoSlice{Label: text.DecodeEntities(label), Value: js.Num(model.values[label]), Percent: js.ToFixed0(share*100) + "%", Share: share})
		}
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Pie: pie}
	}
	return rendered
}
