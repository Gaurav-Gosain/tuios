// Ported from pele src/diagrams/sankey/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package sankey

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	LABEL_GAP  = 6
	VALUE_GAP  = 15
	LABEL_ROOM = 1.8
	CAREFUL    = 300
)

// contentId is two 32-bit hashes, so different gradients practically never share an id.
// It hashes UTF-16 code units, as charCodeAt gives them.
func contentId(text string) string {
	a := uint32(0x811c9dc5)
	b := uint32(5381)
	for _, c := range utf16.Encode([]rune(text)) {
		a = (a ^ uint32(c)) * 0x01000193
		b = b*33 + uint32(c)
	}
	return strconv.FormatUint(uint64(a), 36) + strconv.FormatUint(uint64(b), 36)
}

// fillOf is /^ style="fill:(.*);"$/.exec(style)?.[1]: the colour of a node's style, or false.
func fillOf(style string) (string, bool) {
	const head, foot = ` style="fill:`, `;"`
	if len(style) < len(head)+len(foot) || !strings.HasPrefix(style, head) || !strings.HasSuffix(style, foot) {
		return "", false
	}
	fill := style[len(head) : len(style)-len(foot)]
	// `.` matches no line terminator.
	if strings.ContainsAny(fill, "\n\r\u2028\u2029") {
		return "", false
	}
	return fill, true
}

// wordsOnly is /^[\w-]+$/.test(text).
func wordsOnly(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// sankeyLabel is where a node's label sits.
type sankeyLabel struct {
	value string
	width float64
	after bool
	left  float64
	y     float64
}

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
	natural := numberOption(conf, "width", 600, 1, 10000)
	return common.FitWidth(options, natural, 160, func(chartWidth float64, _ bool) types.Rendered {
		return draw(model, conf, options, chartWidth, natural)
	})
}

func draw(model *SankeyDb, conf map[string]any, options types.RenderOptions, chartWidth, natural float64) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	least := js.Round(size * 0.625)
	// Labels shrink with a chart narrowed to fit, as they would if the whole picture were scaled down.
	small := math.Max(least, js.Round(size*0.875*math.Min(1, chartWidth/natural)))
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	chartHeight := numberOption(conf, "height", 400, 1, 10000)
	nodeWidth := math.Min(numberOption(conf, "nodeWidth", 10, 1, 200), chartWidth)
	showValues := conf["showValues"] != false
	prefix := text.DecodeEntities(textOption(conf, "prefix"))
	suffix := text.DecodeEntities(textOption(conf, "suffix"))
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
	lay := func(labelSize float64) ([]PlacedNode, []PlacedLink) {
		return layoutSankey(model, LayoutOptions{
			width:       chartWidth,
			height:      chartHeight,
			nodeWidth:   nodeWidth,
			nodePadding: padding,
			labelHeight: labelSize * 1.15,
			alignment:   alignment,
		})
	}
	nodes, links := lay(small)
	// Labels also shrink with the most crowded column, so each keeps about LABEL_ROOM lines.
	rows := map[int]int{}
	fullest := 1
	for _, node := range nodes {
		count := rows[node.layer] + 1
		rows[node.layer] = count
		if count > fullest {
			fullest = count
		}
	}
	crowded := math.Max(least, js.Round(chartHeight/float64(fullest)/LABEL_ROOM))
	if crowded < small {
		small = crowded
		nodes, links = lay(small)
	}
	lineHeight := small * 1.5

	custom := map[string]string{}
	if colors, ok := conf["nodeColors"].(map[string]any); ok {
		for id, color := range colors {
			if style := fillStyle(color); style != "" {
				custom[id] = style
			}
		}
	}
	nodeStyle := func(i int) string { return custom[model.nodes[i].id] }
	// Match the whole fill to preserve semicolons in escaped entities.
	nodeColor := func(i int) string {
		if fill, ok := fillOf(nodeStyle(i)); ok {
			return fill
		}
		return svg.SeriesColor(i)
	}

	// Labels sit beside the bars, on the side that faces the middle of the chart unless that
	// side is crowded.
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
	labels := make([]*sankeyLabel, len(nodes))
	names := make([]string, len(nodes))
	for i, node := range nodes {
		name := text.DecodeEntities(model.nodes[i].id)
		names[i] = name
		value := ""
		if showValues {
			value = prefix + js.Num(rounded(node.value)) + suffix
		}
		measured := name
		if value != "" {
			measured = name + " " + value
		}
		width := measurer.Width(measured, small, 0)
		after := node.x < chartWidth/2
		if outlined {
			after = node.layer >= central
		}
		x := node.x - LABEL_GAP
		if after {
			x = node.x + nodeWidth + LABEL_GAP
		}
		y := node.y + node.height/2
		left := x - width
		if after {
			minX = math.Min(minX, x)
			maxX = math.Max(maxX, x+width)
			left = x
		} else {
			minX = math.Min(minX, x-width)
			maxX = math.Max(maxX, x)
		}
		minY = math.Min(minY, y-float64(small*0.75))
		maxY = math.Max(maxY, y+float64(small*0.75))
		labels[i] = &sankeyLabel{value: value, width: width, after: after, left: left, y: y}
	}
	// A label moves at most one line; retain two bands on each side.
	bands := map[float64][]*sankeyLabel{}
	if len(nodes) <= CAREFUL {
		for _, label := range labels {
			band := math.Floor(label.y / lineHeight)
			for _, key := range [5]float64{band - 2, band - 1, band, band + 1, band + 2} {
				bands[key] = append(bands[key], label)
			}
		}
	}
	var labelsOut, nodesOut strings.Builder
	for i, node := range nodes {
		id := model.nodes[i].id
		name := names[i]
		height := math.Max(node.height, 1)
		svg.WriteStrings(&nodesOut, `<rect class="pele-sankey-node" data-id="`, svg.EscText(id), `" x="`, num(node.x), `" y="`, num(node.y), `" width="`, num(nodeWidth),
			`" height="`, num(height), `" rx="`, num(math.Min(2, height/2)), `" fill="`, svg.SeriesColor(i), `"`, nodeStyle(i), `/>`)

		label := labels[i]
		overlapsLabel := func(left, y float64) bool {
			for _, other := range bands[math.Floor(y/lineHeight)] {
				if other != label && math.Abs(other.y-y) < lineHeight && left < other.left+other.width+LABEL_GAP && left+label.width > other.left-LABEL_GAP {
					return true
				}
			}
			return false
		}
		// Try the other side, then an adjacent line when both sides are crowded.
		if len(nodes) <= CAREFUL && overlapsLabel(label.left, label.y) {
		place:
			for _, dy := range [3]float64{0, -lineHeight, lineHeight} {
				for _, after := range [2]bool{label.after, !label.after} {
					left := node.x - LABEL_GAP - label.width
					if after {
						left = node.x + nodeWidth + LABEL_GAP
					}
					y := label.y + dy
					if overlapsLabel(left, y) || left < minX || left+label.width > maxX || y-float64(small*0.75) < minY || y+float64(small*0.75) > maxY {
						continue
					}
					blocked := false
					for j, bar := range nodes {
						if j != i && left < bar.x+nodeWidth+LABEL_GAP && left+label.width > bar.x-LABEL_GAP && y-float64(small*0.75) < bar.y+bar.height && y+float64(small*0.75) > bar.y {
							blocked = true
							break
						}
					}
					if blocked {
						continue
					}
					// A label stays nearer its own bar than any other in the column, so it can't name that one.
					own := math.Max(0, math.Max(node.y-y, y-node.y-node.height))
					for j, bar := range nodes {
						if j != i && bar.x == node.x && math.Max(0, math.Max(bar.y-y, y-bar.y-bar.height)) <= own {
							blocked = true
							break
						}
					}
					if blocked {
						continue
					}
					label.left = left
					label.y = y
					label.after = after
					break place
				}
			}
		}
		value, after, y := label.value, label.after, label.y
		x := node.x - LABEL_GAP
		if after {
			x = node.x + nodeWidth + LABEL_GAP
		}
		anchor := "end"
		if after {
			anchor = "start"
		}
		position := ` x="` + num(x) + `" y="` + num(y+float64(small*0.35)) + `" text-anchor="` + anchor + `"`
		svg.WriteStrings(&labelsOut, `<g class="pele-label" data-id="`, svg.EscText(id), `">`)
		if outlined {
			svg.WriteStrings(&labelsOut, `<text`, position, ` fill="none" stroke="var(--_bg)" stroke-width="3" stroke-linejoin="round">`, svg.Esc(name))
			if value != "" {
				svg.WriteStrings(&labelsOut, " ", svg.Esc(value))
			}
			labelsOut.WriteString(`</text>`)
		}
		svg.WriteStrings(&labelsOut, `<text`, position, `>`, svg.Esc(name))
		if value != "" {
			svg.WriteStrings(&labelsOut, ` <tspan class="pele-sankey-value" fill="var(--_m)">`, svg.Esc(value), `</tspan>`)
		}
		labelsOut.WriteString(`</text></g>`)
	}

	fixed := ""
	if linkColor != "gradient" && linkColor != "source" && linkColor != "target" {
		fixed = fillStyle(linkColor)
	}
	// Without an idPrefix diagrams share gradient ids, and Chrome then uses a hidden or differently
	// themed copy's gradient. Other characters would end the url() and void the whole fill.
	idPrefix := types.Or(options.IDPrefix, "")
	gradient := linkColor == "gradient" && wordsOnly(idPrefix)
	var gradientIds []string
	gradients := map[string]string{}
	var linksOut strings.Builder
	for _, link := range links {
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
		paint := `fill="` + svg.SeriesColor(end) + `"` + style
		if gradient {
			// Ids come from the gradient itself, so diagrams sharing a prefix agree on them. The colour
			// after the url() is the fallback.
			stops := `x1="` + num(x0) + `" x2="` + num(x1) + `"><stop offset="0" stop-color="` + nodeColor(link.source) + `"/>` +
				`<stop offset="1" stop-color="` + nodeColor(link.target) + `"/>`
			id := idPrefix + "-sankey-" + contentId(stops)
			if _, seen := gradients[id]; !seen {
				gradientIds = append(gradientIds, id)
			}
			gradients[id] = `<linearGradient id="` + id + `" gradientUnits="userSpaceOnUse" ` + stops + `</linearGradient>`
			paint = `fill="url(#` + id + `) ` + nodeColor(link.source) + `"`
		}
		svg.WriteStrings(&linksOut, `<path class="pele-sankey-link" data-id="`, from, `-&gt;`, to, `" d="M`, num(x0), `,`, num(link.sourceY-half),
			`C`, mid, `,`, num(link.sourceY-half), ` `, mid, `,`, num(link.targetY-half), ` `, num(x1), `,`, num(link.targetY-half),
			`L`, num(x1), `,`, num(link.targetY+half),
			`C`, mid, `,`, num(link.targetY+half), ` `, mid, `,`, num(link.sourceY+half), ` `, num(x0), `,`, num(link.sourceY+half),
			`Z" `, paint, `>`,
			`<title>`, from, ` → `, to, `: `, svg.Esc(prefix), js.Num(rounded(link.value)), svg.Esc(suffix), `</title></path>`)
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

	defs := ""
	if len(gradientIds) > 0 {
		var b strings.Builder
		b.WriteString(`<defs>`)
		for _, id := range gradientIds {
			b.WriteString(gradients[id])
		}
		b.WriteString(`</defs>`)
		defs = b.String()
	}

	var geometry *types.Geometry
	if options.Geometry != nil {
		// Not part of pele: the diagram as lists, for a text drawing (sketch/text).
		g := &types.GeoSankey{Title: title.Plain()}
		for i, node := range nodes {
			value := ""
			if showValues {
				value = prefix + js.Num(rounded(node.value)) + suffix
			}
			g.Nodes = append(g.Nodes, types.GeoFlowNode{Label: names[i], Value: value, Layer: node.layer, Y: node.y + node.height/2})
		}
		for _, link := range links {
			g.Flows = append(g.Flows, types.GeoFlow{Source: link.source, Target: link.target, Value: prefix + js.Num(rounded(link.value)) + suffix, Size: link.value,
				SourceY: link.sourceY, TargetY: link.targetY})
		}
		geometry = &types.Geometry{Width: width, Height: height, Sankey: g}
	}
	return types.Rendered{
		Geometry: geometry,
		SVG: svg.SvgDocument("sankey", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr},
			svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil),
			`<g transform="translate(`+num(ox)+`,`+num(oy)+`)">`,
			defs,
			`<g class="pele-sankey-links" fill-opacity="0.4">`, linksOut.String(), `</g>`,
			`<g class="pele-sankey-nodes">`, nodesOut.String(), `</g>`,
			`<g class="pele-labels" font-size="`+js.Num(small)+`">`, labelsOut.String(), `</g>`,
			`</g>`),
		Width:  width,
		Height: height,
		Links:  []types.LinkInfo{},
	}
}
