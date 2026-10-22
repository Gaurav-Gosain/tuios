// Ported from pele src/diagrams/treemap/render.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

package treemap

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const HEADER = 28
const SIDE = 6
const LINE = 1.25

func option(config types.Config, key string, fallback, min, max float64) float64 {
	section, _ := config["treemap"].(map[string]any)
	if value, ok := section[key].(float64); ok && value >= min {
		return math.Min(value, max)
	}
	return fallback
}

// hasFill is /[";]fill:/.test(shape).
func hasFill(shape string) bool {
	return strings.Contains(shape, `"fill:`) || strings.Contains(shape, ";fill:")
}

func renderTreemap(model *TreemapModel, config types.Config, options types.RenderOptions) types.Rendered {
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	tiny := js.Round(size * 0.75)
	least := js.Round(size * 0.5625)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	treemap, _ := config["treemap"].(map[string]any)
	pad := 0.0
	if options.Padding != nil {
		pad = *options.Padding
	} else {
		pad = option(config, "diagramPadding", 8, 0, 2000)
	}

	// Mermaid sizes the diagram as ten times `nodeWidth` by ten times `nodeHeight`.
	width := js.Round(common.CapWidth(options, option(config, "nodeWidth", 64, 1, 400)*10, 200, 2*pad))
	height := js.Round(option(config, "nodeHeight", 40, 1, 400) * 10)
	showValues := treemap["showValues"] != false
	spec := ","
	if s, ok := treemap["valueFormat"].(string); ok {
		spec = s
	}
	format := formatter(spec)
	boxes := layoutTreemap(model.roots, width, height, Insets{gap: option(config, "padding", 4, 0, 200), header: HEADER, side: SIDE})

	title := text.LayoutLabel(model.title, false, measurer, size, common.TitleRoom(options), text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 8
	}
	left := pad + math.Max(0, (title.Width-width)/2)
	top := pad + titleHeight

	var geo *types.GeoTreemap
	if options.Geometry != nil {
		geo = &types.GeoTreemap{Title: title.Plain(), Width: width, Height: height}
	}
	var sections, leaves, t strings.Builder
	for _, b := range boxes {
		node := b.node
		style := svg.ResolveStyle(node.cssCompiledStyles)
		color := svg.SeriesColor(b.series)
		x := left + b.x
		y := top + b.y
		name := js.Trim(text.DecodeEntities(node.name))
		value := ""
		if showValues {
			value = format(b.value)
		}
		if geo != nil {
			depth := 0
			for p := b.parent; p != nil; p = p.parent {
				depth++
			}
			geo.Boxes = append(geo.Boxes, types.GeoTreeBox{X: b.x, Y: b.y, W: b.w, H: b.h, Label: name, Value: value, Section: node.section, Depth: depth})
		}
		t.Reset()

		if node.section {
			if b.h >= HEADER {
				room := b.w - 2*SIDE - 4
				valueWidth := 0.0
				if value != "" {
					valueWidth = measurer.Width(value, tiny, 0)
				}
				withValue := valueWidth > 0 && room-valueWidth-12 >= 3*small
				max := room
				if withValue {
					max = room - valueWidth - 12
				}
				label, ok := common.FitText(name, max, small, small, measurer, text.Bold)
				baseline := y + HEADER/2 + 1
				if ok {
					svg.WriteStrings(&t, `<text x="`, svg.Num(x+SIDE+2), `" y="`, svg.Num(baseline+float64(small*0.35)), `" font-weight="var(--_hw)"`, style.Text, `>`, svg.Esc(label.Text), `</text>`)
				}
				if withValue {
					svg.WriteStrings(&t, `<text class="pele-value" x="`, svg.Num(x+b.w-SIDE-2), `" y="`, svg.Num(baseline+float64(tiny*0.35)), `" font-size="`, js.Num(tiny),
						`" text-anchor="end" fill="var(--_m)"`, style.Text, `>`, svg.Esc(value), `</text>`)
				}
			}
			// A section is a tint of its color, unless a class gives it a fill of its own.
			tint := ` fill-opacity="0.12"`
			if hasFill(style.Shape) {
				tint = ""
			}
			svg.WriteStrings(&sections, `<g class="pele-node pele-section`, svg.ClassNames(node.classSelector), `" data-id="`, svg.EscText(node.name), `">`,
				`<rect x="`, svg.Num(x), `" y="`, svg.Num(y), `" width="`, svg.Num(b.w), `" height="`, svg.Num(b.h), `" fill="`, color, `"`,
				svg.Rounded(tint+` stroke="`+color+`"`+style.Shape), `/>`, t.String(), `</g>`)
			continue
		}

		room := b.w - 8
		tallest := math.Min(size, math.Floor((b.h-4)/LINE))
		var label common.Fitted
		ok := false
		if tallest >= least {
			label, ok = common.FitText(name, room, tallest, least, measurer, 0)
		}
		if ok {
			cx := svg.Num(x + b.w/2)
			cy := y + b.h/2
			valueSize := math.Max(least, js.Round(label.Size*0.8))
			lines := (label.Size + valueSize) * LINE
			if value != "" && lines <= b.h-4 && measurer.Width(value, valueSize, 0) <= room {
				first := cy - lines/2 + (label.Size*LINE)/2
				second := cy + lines/2 - (valueSize*LINE)/2
				svg.WriteStrings(&t, `<text x="`, cx, `" y="`, svg.Num(first+float64(label.Size*0.35)), `" font-size="`, svg.Num(label.Size), `"`, style.Text, `>`, svg.Esc(label.Text), `</text>`,
					`<text class="pele-value" x="`, cx, `" y="`, svg.Num(second+float64(valueSize*0.35)), `" font-size="`, svg.Num(valueSize), `"`, style.Text, `>`, svg.Esc(value), `</text>`)
			} else {
				svg.WriteStrings(&t, `<text x="`, cx, `" y="`, svg.Num(cy+float64(label.Size*0.35)), `" font-size="`, svg.Num(label.Size), `"`, style.Text, `>`, svg.Esc(label.Text), `</text>`)
			}
		}
		svg.WriteStrings(&leaves, `<g class="pele-node pele-leaf`, svg.ClassNames(node.classSelector), `" data-id="`, svg.EscText(node.name), `">`,
			`<rect x="`, svg.Num(x), `" y="`, svg.Num(y), `" width="`, svg.Num(b.w), `" height="`, svg.Num(b.h), `" fill="`, color, `"`,
			svg.Rounded(style.Shape), `/>`, t.String(), `</g>`)

	}

	totalWidth := math.Ceil(math.Max(width, title.Width) + float64(2*pad))
	totalHeight := math.Ceil(height + titleHeight + float64(2*pad))
	var content strings.Builder
	content.WriteString(svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	if sections.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-sections" font-size="`, js.Num(small), `">`, sections.String(), `</g>`)
	}
	if leaves.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-leaves" text-anchor="middle" fill="var(--_bg)">`, leaves.String(), `</g>`)
	}

	rendered := types.Rendered{
		SVG:    svg.SvgDocument("treemap", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
	if geo != nil {
		var walk func(nodes []*TreemapNode, depth int)
		walk = func(nodes []*TreemapNode, depth int) {
			for _, node := range nodes {
				value := ""
				if showValues {
					value = format(sumOf(node))
				}
				geo.Tree = append(geo.Tree, types.GeoTreeBox{Label: js.Trim(text.DecodeEntities(node.name)), Value: value, Section: node.section, Depth: depth})
				if len(geo.Tree) < 100_000 {
					walk(node.children, depth+1)
				}
			}
		}
		walk(model.roots, 0)
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Treemap: geo}
	}
	return rendered
}

// sumOf is the value layoutTreemap gives a node's box: a leaf's own value, or the leaves under a
// section added in the same order. Not in pele.
func sumOf(node *TreemapNode) float64 {
	value := newBox(node, nil, 0).value
	for i := len(node.children) - 1; i >= 0; i-- {
		value += sumOf(node.children[i])
	}
	return value
}
