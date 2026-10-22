// Ported from pele src/diagrams/sankey/render.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

package sankey

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	LABEL_GAP = 6
	VALUE_GAP = 15
)

func numberOption(conf map[string]any, key string, fallback, min, max float64) float64 {
	if value, ok := conf[key].(float64); ok && value >= min {
		return math.Min(value, max)
	}
	return fallback
}

func textOption(conf map[string]any, key string) string {
	switch value := conf[key].(type) {
	case string:
		return value
	case float64:
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			return js.Num(value)
		}
	}
	return ""
}

// rounded: two decimals, as Mermaid shows values; numbers too large for that are left as they are.
func rounded(value float64) float64 {
	if math.Abs(value) < 1e15 {
		return js.Round(value*100) / 100
	}
	return value
}

// fillStyle is a colour from the configuration, as an inline style that overrides the series colour.
func fillStyle(color any) string {
	if s, ok := color.(string); ok {
		return svg.ResolveStyle([]string{"fill:" + s}).Shape
	}
	return ""
}

func renderSankey(model *SankeyDb, config types.Config, options types.RenderOptions) types.Rendered {
	conf, ok := config["sankey"].(map[string]any)
	if !ok {
		conf = map[string]any{}
	}
	return common.FitWidth(options, numberOption(conf, "width", 600, 1, 10000), 160, func(chartWidth float64, _ bool) types.Rendered {
		return draw(model, conf, options, chartWidth)
	})
}

func draw(model *SankeyDb, conf map[string]any, options types.RenderOptions, chartWidth float64) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, "")
	}
	pad := types.Or(options.Padding, 8)

	chartHeight := numberOption(conf, "height", 400, 1, 10000)
	nodeWidth := math.Min(numberOption(conf, "nodeWidth", 10, 1, 200), chartWidth)
	showValues := conf["showValues"] != false
	prefix := textOption(conf, "prefix")
	suffix := textOption(conf, "suffix")
	outlined := conf["labelStyle"] == "outlined"
	alignment := "justify"
	if a, ok := conf["nodeAlignment"].(string); ok && (a == "left" || a == "right" || a == "center") {
		alignment = a
	}
	linkColor, ok := conf["linkColor"].(string)
	if !ok {
		linkColor = "gradient"
	}

	padding := numberOption(conf, "nodePadding", 12, 0, 200)
	if showValues {
		padding += VALUE_GAP
	}
	nodes, links := layoutSankey(model, LayoutOptions{
		width:       chartWidth,
		height:      chartHeight,
		nodeWidth:   nodeWidth,
		nodePadding: padding,
		labelHeight: small * 1.15,
		alignment:   alignment,
	})

	custom := map[string]string{}
	if colors, ok := conf["nodeColors"].(map[string]any); ok {
		for id, color := range colors {
			if style := fillStyle(color); style != "" {
				custom[id] = style
			}
		}
	}
	nodeStyle := func(i int) string { return custom[model.nodes[i].id] }

	// Labels sit beside the bars, on the side that faces the middle of the chart.
	central := 0
	most := 0.0
	for _, node := range nodes {
		if node.value > most {
			most = node.value
			central = node.layer
		}
	}
	minX, maxX := 0.0, chartWidth
	minY, maxY := 0.0, chartHeight
	var labelsOut, nodesOut strings.Builder
	for i, node := range nodes {
		id := model.nodes[i].id
		height := math.Max(node.height, 1)
		svg.WriteStrings(&nodesOut, `<rect class="pele-sankey-node" data-id="`, svg.EscText(id), `" x="`, num(node.x), `" y="`, num(node.y), `" width="`, num(nodeWidth),
			`" height="`, num(height), `" rx="`, num(math.Min(2, height/2)), `" fill="`, svg.SeriesColor(i), `"`, nodeStyle(i), `/>`)

		value := ""
		if showValues {
			value = prefix + js.Num(rounded(node.value)) + suffix
		}
		after := node.x < chartWidth/2
		if outlined {
			after = node.layer >= central
		}
		label := id
		if value != "" {
			label = id + " " + value
		}
		textWidth := measurer.Width(label, small, 0)
		x := node.x - LABEL_GAP
		if after {
			x = node.x + nodeWidth + LABEL_GAP
		}
		y := node.y + node.height/2
		if after {
			minX = math.Min(minX, x)
			maxX = math.Max(maxX, x+textWidth)
		} else {
			minX = math.Min(minX, x-textWidth)
			maxX = math.Max(maxX, x)
		}
		minY = math.Min(minY, y-float64(small*0.75))
		maxY = math.Max(maxY, y+float64(small*0.75))
		anchor := "end"
		if after {
			anchor = "start"
		}
		position := ` x="` + num(x) + `" y="` + num(y+float64(small*0.35)) + `" text-anchor="` + anchor + `"`
		svg.WriteStrings(&labelsOut, `<g class="pele-label" data-id="`, svg.EscText(id), `">`)
		if outlined {
			svg.WriteStrings(&labelsOut, `<text`, position, ` fill="none" stroke="var(--_bg)" stroke-width="3" stroke-linejoin="round">`, svg.Esc(id))
			if value != "" {
				svg.WriteStrings(&labelsOut, " ", svg.Esc(value))
			}
			labelsOut.WriteString(`</text>`)
		}
		svg.WriteStrings(&labelsOut, `<text`, position, `>`, svg.Esc(id))
		if value != "" {
			svg.WriteStrings(&labelsOut, ` <tspan class="pele-sankey-value" fill="var(--_m)">`, svg.Esc(value), `</tspan>`)
		}
		labelsOut.WriteString(`</text></g>`)
	}

	fixed := ""
	if linkColor != "gradient" && linkColor != "source" && linkColor != "target" {
		fixed = fillStyle(linkColor)
	}
	var linksOut strings.Builder
	for _, link := range links {
		// Without definitions there are no gradients, so a gradient link takes its source's colour.
		end := link.source
		if linkColor == "target" {
			end = link.target
		}
		half := math.Max(link.thickness, 1) / 2
		x0 := nodes[link.source].x + nodeWidth
		x1 := nodes[link.target].x
		mid := num((x0 + x1) / 2)
		from := svg.EscText(model.nodes[link.source].id)
		to := svg.EscText(model.nodes[link.target].id)
		style := fixed
		if style == "" {
			style = nodeStyle(end)
		}
		svg.WriteStrings(&linksOut, `<path class="pele-sankey-link" data-id="`, from, `-&gt;`, to, `" d="M`, num(x0), `,`, num(link.sourceY-half),
			`C`, mid, `,`, num(link.sourceY-half), ` `, mid, `,`, num(link.targetY-half), ` `, num(x1), `,`, num(link.targetY-half),
			`L`, num(x1), `,`, num(link.targetY+half),
			`C`, mid, `,`, num(link.targetY+half), ` `, mid, `,`, num(link.sourceY+half), ` `, num(x0), `,`, num(link.sourceY+half),
			`Z" fill="`, svg.SeriesColor(end), `"`, style, `>`,
			`<title>`, from, ` → `, to, `: `, svg.EscText(prefix), js.Num(rounded(link.value)), svg.EscText(suffix), `</title></path>`)
	}

	title := text.LayoutLabel(model.title, false, measurer, size, common.TitleRoom(options), text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	inner := math.Max(maxX-minX, title.Width)
	ox := pad - minX + (inner-(maxX-minX))/2
	oy := pad + titleHeight - minY
	width := math.Ceil(inner + float64(2*pad))
	height := math.Ceil(maxY - minY + titleHeight + float64(2*pad))

	var geometry *types.Geometry
	if options.Geometry != nil {
		// Not part of pele: the diagram as lists, for a text drawing (sketch/text).
		g := &types.GeoSankey{Title: title.Plain()}
		for i, node := range nodes {
			value := ""
			if showValues {
				value = prefix + js.Num(rounded(node.value)) + suffix
			}
			g.Nodes = append(g.Nodes, types.GeoFlowNode{Label: model.nodes[i].id, Value: value, Layer: node.layer, Y: node.y + node.height/2})
		}
		for _, link := range links {
			g.Flows = append(g.Flows, types.GeoFlow{Source: link.source, Target: link.target, Value: prefix + js.Num(rounded(link.value)) + suffix,
				SourceY: link.sourceY, TargetY: link.targetY})
		}
		geometry = &types.Geometry{Width: width, Height: height, Sankey: g}
	}
	return types.Rendered{
		Geometry: geometry,
		SVG: svg.SvgDocument("sankey", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr},
			svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil),
			`<g transform="translate(`+num(ox)+`,`+num(oy)+`)">`,
			`<g class="pele-sankey-links" fill-opacity="0.4">`, linksOut.String(), `</g>`,
			`<g class="pele-sankey-nodes">`, nodesOut.String(), `</g>`,
			`<g class="pele-labels" font-size="`+js.Num(small)+`">`, labelsOut.String(), `</g>`,
			`</g>`),
		Width:  width,
		Height: height,
		Links:  []types.LinkInfo{},
	}
}
