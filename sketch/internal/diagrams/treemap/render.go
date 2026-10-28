// Ported from pele src/diagrams/treemap/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package treemap

import (
	"math"
	"slices"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const HEADER = 28
const SHORT_HEADER = 18
const SIDE = 6
const LINE = 1.25

func titlePart(raw string) string { return js.Trim(svg.Esc(js.Trim(text.DecodeEntities(raw)))) }

func leafCount(roots []*TreemapNode) int {
	count := 0
	stack := slices.Clone(roots)
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node.section {
			stack = append(stack, node.children...)
		} else if !math.IsNaN(node.value) && !math.IsInf(node.value, 0) && node.value > 0 {
			count++
		}
	}
	return count
}

// corners caps corners at a quarter of a small box's shorter side; 4px is the default radius.
func corners(attrs string, b *Box) string {
	limit := math.Min(b.w, b.h) / 4
	if limit < 4 {
		return strings.Replace(svg.Rounded(attrs), "rx:var(--_r)", "rx:min(var(--_r),"+svg.Num(limit)+"px)", 1)
	}
	return svg.Rounded(attrs)
}

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
	// Reduce gaps and insets as average leaf area shrinks.
	crowd := math.Min(1, math.Max(0, math.Log10((width*height)/math.Max(1, float64(leafCount(model.roots)))/800)))
	boxes := layoutTreemap(model.roots, width, height, Insets{
		gap:    option(config, "padding", js.Round(2+float64(2*crowd)), 0, 200),
		header: HEADER,
		short:  SHORT_HEADER,
		side:   js.Round(3 + float64(3*crowd)),
	})

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
		// Include headerless ancestors in tooltips because their children cover them.
		own := titlePart(node.name)
		var path []string
		if own != "" {
			path = append(path, own)
		}
		for p := b.parent; p != nil && !p.headed; p = p.parent {
			if section := titlePart(p.node.name); section != "" {
				path = slices.Insert(path, 0, section)
			}
		}
		full := strings.Join(path, " › ")
		// tooltip takes the text drawn in the box, if any.
		tooltip := func(drawn string, ok bool) string {
			if full == "" || (ok && drawn == name && full == own) {
				return ""
			}
			return "<title>" + full + "</title>"
		}
		rect := func(w *strings.Builder) {
			svg.WriteStrings(w, `<rect x="`, svg.Num(x), `" y="`, svg.Num(y), `" width="`, svg.Num(b.w), `" height="`, svg.Num(b.h), `" fill="`, color, `"`)
		}
		// Inline font sizes override SVG attributes, so truncate instead of shrinking.
		fixed := style.FontSize
		textStyle := 0
		if style.Bold {
			textStyle |= text.Bold
		}
		if style.Italic {
			textStyle |= text.Italic
		}
		ink := svg.InkFill(style, "")

		if node.section {
			drawn, drawnOK := "", false
			if b.headed {
				short := b.header < HEADER
				labelSize := tiny
				if !short {
					labelSize = small
				}
				valueSize := tiny
				if fixed != 0 {
					labelSize, valueSize = fixed, fixed
				}
				room := b.w - 2*SIDE - 4
				valueWidth := 0.0
				if value != "" {
					valueWidth = measurer.Width(value, valueSize, textStyle)
				}
				withValue := !short && valueWidth > 0 && room-valueWidth-12 >= float64(3*labelSize)
				max := room
				if withValue {
					max = room - valueWidth - 12
				}
				var label common.Fitted
				ok := false
				if float64(labelSize*LINE) < b.header {
					label, ok = common.FitText(name, max, labelSize, labelSize, measurer, textStyle|text.Bold)
				}
				baseline := y + b.header/2 + 1
				drawn, drawnOK = label.Text, ok
				if ok {
					sizeAttr := ""
					if short {
						sizeAttr = ` font-size="` + js.Num(labelSize) + `"`
					}
					svg.WriteStrings(&t, `<text x="`, svg.Num(x+SIDE+2), `" y="`, svg.Num(baseline+float64(labelSize*0.35)), `" font-weight="var(--_hw)"`, sizeAttr, ink, style.Text, `>`, svg.Esc(label.Text), `</text>`)
				}
				if withValue && float64(valueSize*LINE) <= HEADER {
					valueInk := ink
					if valueInk == "" {
						valueInk = ` fill="var(--_m)"`
					}
					svg.WriteStrings(&t, `<text class="pele-value" x="`, svg.Num(x+b.w-SIDE-2), `" y="`, svg.Num(baseline+float64(valueSize*0.35)), `" font-size="`, js.Num(tiny),
						`" text-anchor="end"`, valueInk, style.Text, `>`, svg.Esc(value), `</text>`)
				}
			}
			// A section is a tint of its color, unless a class gives it a fill of its own. Headerless
			// sections draw no box, so their children blend into the grid.
			tint := ` fill-opacity="0.12"`
			if hasFill(style.Shape) {
				tint = ""
			}
			svg.WriteStrings(&sections, `<g class="pele-node pele-section`, svg.ClassNames(node.classSelector), `" data-id="`, svg.EscText(node.name), `">`, tooltip(drawn, drawnOK))
			if b.headed {
				rect(&sections)
				svg.WriteStrings(&sections, corners(tint+` stroke="`+color+`"`+style.Shape, b), `/>`)
			} else {
				svg.WriteStrings(&sections, `<rect x="`, svg.Num(x), `" y="`, svg.Num(y), `" width="`, svg.Num(b.w), `" height="`, svg.Num(b.h), `" fill="none"/>`)
			}
			svg.WriteStrings(&sections, t.String(), `</g>`)
			continue
		}

		room := b.w - 8
		smallest := least
		var tallest float64
		if fixed == 0 {
			tallest = math.Min(size, math.Floor((b.h-4)/LINE))
		} else {
			smallest = fixed
			if float64(fixed*LINE) <= b.h-4 {
				tallest = fixed
			}
		}
		var label common.Fitted
		ok := false
		if tallest >= smallest {
			label, ok = common.FitText(name, room, tallest, smallest, measurer, textStyle)
		}
		if ok {
			cx := svg.Num(x + b.w/2)
			cy := y + b.h/2
			valueSize := math.Max(least, js.Round(label.Size*0.8))
			if fixed != 0 {
				valueSize = fixed
			}
			lines := (label.Size + valueSize) * LINE
			if value != "" && lines <= b.h-4 && measurer.Width(value, valueSize, textStyle) <= room {
				first := cy - lines/2 + (label.Size*LINE)/2
				second := cy + lines/2 - (valueSize*LINE)/2
				svg.WriteStrings(&t, `<text x="`, cx, `" y="`, svg.Num(first+float64(label.Size*0.35)), `" font-size="`, svg.Num(label.Size), `"`, ink, style.Text, `>`, svg.Esc(label.Text), `</text>`,
					`<text class="pele-value" x="`, cx, `" y="`, svg.Num(second+float64(valueSize*0.35)), `" font-size="`, svg.Num(valueSize), `"`, ink, style.Text, `>`, svg.Esc(value), `</text>`)
			} else {
				svg.WriteStrings(&t, `<text x="`, cx, `" y="`, svg.Num(cy+float64(label.Size*0.35)), `" font-size="`, svg.Num(label.Size), `"`, ink, style.Text, `>`, svg.Esc(label.Text), `</text>`)
			}
		}
		svg.WriteStrings(&leaves, `<g class="pele-node pele-leaf`, svg.ClassNames(node.classSelector), `" data-id="`, svg.EscText(node.name), `">`, tooltip(label.Text, ok))
		rect(&leaves)
		svg.WriteStrings(&leaves, corners(style.Shape, b), `/>`, t.String(), `</g>`)
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
