// Ported from pele src/diagrams/radar/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package radar

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const SWATCH = 12
const ROW = 26
const GAP = 24

type Point struct {
	x, y float64
}

func option(config types.Config, key string, fallback, min, max float64) float64 {
	section, _ := config["radar"].(map[string]any)
	if value, ok := section[key].(float64); ok && value >= min {
		return math.Min(value, max)
	}
	return fallback
}

func relativeRadius(value, min, max, radius float64) float64 {
	clipped := math.Min(math.Max(value, min), max)
	scaled := radius * (clipped - min)
	// Keep the original rounding unless multiplication overflows.
	if !math.IsInf(scaled, 0) && !math.IsNaN(scaled) {
		return scaled / (max - min)
	}
	return radius * ((clipped - min) / (max - min))
}

// closedRoundCurve is a closed Catmull-Rom spline through the points, as cubic Bézier segments.
func closedRoundCurve(points []Point, tension float64) string {
	num := svg.Num
	n := len(points)
	var d strings.Builder
	svg.WriteStrings(&d, "M", num(points[0].x), ",", num(points[0].y))
	for i := 0; i < n; i++ {
		p0 := points[(i-1+n)%n]
		p1 := points[i]
		p2 := points[(i+1)%n]
		p3 := points[(i+2)%n]
		svg.WriteStrings(&d, " C", num(p1.x+float64((p2.x-p0.x)*tension)), ",", num(p1.y+float64((p2.y-p0.y)*tension)),
			" ", num(p2.x-float64((p3.x-p1.x)*tension)), ",", num(p2.y-float64((p3.y-p1.y)*tension)), " ", num(p2.x), ",", num(p2.y))
	}
	d.WriteString(" Z")
	return d.String()
}

func polygon(points []Point) string {
	var out strings.Builder
	for _, p := range points {
		if out.Len() > 0 {
			out.WriteByte(' ')
		}
		svg.WriteStrings(&out, svg.Num(p.x), ",", svg.Num(p.y))
	}
	return out.String()
}

func renderRadar(model *RadarModel, config types.Config, options types.RenderOptions) types.Rendered {
	natural := option(config, "width", 320, 1, 4000)
	return common.FitWidth(options, natural, 160, func(width float64, tight bool) types.Rendered {
		return draw(model, config, options, width, (option(config, "height", 320, 1, 4000)*width)/natural, tight)
	})
}

// draw: `tight` is set when the chart does not fit the width it has with its legend beside it.
func draw(model *RadarModel, config types.Config, options types.RenderOptions, width, height float64, tight bool) types.Rendered {
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	axisScale := option(config, "axisScaleFactor", 1, 0, 10)
	labelFactor := option(config, "axisLabelFactor", 1.05, 0, 10)
	tension := option(config, "curveTension", 0.17, 0, 1)
	radius := math.Min(width, height) / 2

	axes, curves := model.axes, model.curves
	ticks, graticule, min := model.options.ticks, model.options.graticule, model.options.min
	count := float64(len(axes))
	unit := make([]Point, len(axes))
	for i := range axes {
		angle := (2*math.Pi*float64(i))/count - math.Pi/2
		unit[i] = Point{js.Cos(angle), js.Sin(angle)}
	}

	max := model.options.max
	if !model.options.hasMax {
		max = math.Inf(-1)
		for _, curve := range curves {
			for _, value := range curve.entries {
				if value > max {
					max = value
				}
			}
		}
	}

	// The chart is drawn around the origin. Margins reserve space; labels and the legend widen it.
	x0 := -width/2 - option(config, "marginLeft", 16, 0, 2000)
	x1 := width/2 + option(config, "marginRight", 16, 0, 2000)
	y0 := -height/2 - option(config, "marginTop", 16, 0, 2000)
	y1 := height/2 + option(config, "marginBottom", 16, 0, 2000)

	var grid strings.Builder
	ring := make([]Point, len(unit))
	for i := 0.0; i < ticks; i++ {
		r := math.Min(radius, (radius*(i+1))/ticks)
		if graticule == "circle" {
			svg.WriteStrings(&grid, `<circle r="`, svg.Num(r), `"/>`)
		} else {
			for k, u := range unit {
				ring[k] = Point{u.x * r, u.y * r}
			}
			svg.WriteStrings(&grid, `<polygon points="`, polygon(ring), `"/>`)
		}
	}

	var spokes, labels strings.Builder
	for i, u := range unit {
		reach := radius * axisScale
		svg.WriteStrings(&spokes, "M0,0L", svg.Num(u.x*reach), ",", svg.Num(u.y*reach))
		label := text.LayoutLabel(axes[i].label, false, measurer, small, 4000, 0)
		at := float64(radius*labelFactor) + 6
		// Each label hangs off its axis on the side away from the center.
		cx := float64(u.x * at)
		if u.x > 0.01 {
			cx += label.Width / 2
		} else if u.x < -0.01 {
			cx += -label.Width / 2
		} else {
			cx += 0
		}
		cy := float64(u.y * at)
		if u.y > 0.01 {
			cy += label.Height / 2
		} else if u.y < -0.01 {
			cy += -label.Height / 2
		} else {
			cy += 0
		}
		svg.WriteStrings(&labels, `<g class="pele-axis-label" data-id="`, svg.EscText(axes[i].name), `">`, svg.LabelSvg(label, cx, cy, "", nil), `</g>`)
		x0 = math.Min(x0, cx-label.Width/2)
		x1 = math.Max(x1, cx+label.Width/2)
		y0 = math.Min(y0, cy-label.Height/2)
		y1 = math.Max(y1, cy+label.Height/2)
	}

	var shapes strings.Builder
	for index, curve := range curves {
		// Mermaid leaves out a curve that does not have a value for every axis.
		if len(curve.entries) != len(axes) {
			continue
		}
		points := make([]Point, len(curve.entries))
		for i, value := range curve.entries {
			r := relativeRadius(value, min, max, radius)
			safe := r
			if math.IsNaN(r) || math.IsInf(r, 0) {
				safe = 0
			}
			points[i] = Point{unit[i].x * safe, unit[i].y * safe}
		}
		color := svg.SeriesColor(index)
		attrs := ` class="pele-curve" data-id="` + svg.EscText(curve.name) + `" fill="` + color + `" stroke="` + color + `"`
		if graticule == "circle" {
			svg.WriteStrings(&shapes, `<path`, attrs, ` d="`, closedRoundCurve(points, tension), `"/>`)
		} else {
			svg.WriteStrings(&shapes, `<polygon`, attrs, ` points="`, polygon(points), `"/>`)
		}
	}

	var legend []text.Label
	if model.options.showLegend {
		legend = make([]text.Label, len(curves))
		for i, curve := range curves {
			legend[i] = text.LayoutLabel(curve.label, false, measurer, small, 4000, 0)
		}
	}
	legendWidth := 0.0
	for _, label := range legend {
		legendWidth = math.Max(legendWidth, label.Width)
	}
	legendHeight := float64(float64(len(legend)) * ROW)
	legendX := x1 + GAP
	legendTop := -legendHeight / 2
	if tight {
		legendX = -(SWATCH + 8 + legendWidth) / 2
		legendTop = y1 + GAP/2
	}
	if len(legend) > 0 && tight {
		x0 = math.Min(x0, legendX)
		x1 = math.Max(x1, -legendX)
		y1 = legendTop + legendHeight
	} else if len(legend) > 0 {
		x1 = legendX + SWATCH + 8 + legendWidth
		y0 = math.Min(y0, -legendHeight/2)
		y1 = math.Max(y1, legendHeight/2)
	}
	var legendOut strings.Builder
	for index, label := range legend {
		y := legendTop + float64(float64(index)*ROW) + ROW/2
		color := svg.SeriesColor(index)
		svg.WriteStrings(&legendOut, `<g class="pele-legend-item" data-id="`, svg.EscText(curves[index].name), `">`,
			`<rect x="`, svg.Num(legendX), `" y="`, svg.Num(y-SWATCH/2), `" width="`, js.Num(SWATCH), `" height="`, js.Num(SWATCH), `" rx="2" fill="`, color, `" fill-opacity="0.4" stroke="`, color, `"/>`,
			svg.LabelSvg(label, legendX+SWATCH+8+label.Width/2, y, "", nil),
			`</g>`)
	}

	title := text.LayoutLabel(model.title, false, measurer, size, common.TitleRoom(options), text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 8
	}
	bodyWidth := math.Max(x1-x0, title.Width)
	totalWidth := math.Ceil(bodyWidth + float64(2*pad))
	totalHeight := math.Ceil(y1 - y0 + titleHeight + float64(2*pad))

	var content strings.Builder
	svg.WriteStrings(&content, svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil),
		`<g transform="translate(`, svg.Num(pad+(bodyWidth-(x1-x0))/2-x0), `,`, svg.Num(pad+titleHeight-y0), `)">`)
	if grid.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-graticule" fill="none" stroke="var(--_a)">`, grid.String(), `</g>`)
	}
	if spokes.Len() > 0 {
		svg.WriteStrings(&content, `<path class="pele-axes" d="`, spokes.String(), `" fill="none" stroke="var(--_b)"/>`)
	}
	if shapes.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-curves" fill-opacity="0.2" stroke-width="1.5" stroke-linejoin="round">`, shapes.String(), `</g>`)
	}
	if labels.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-axis-labels" font-size="`, js.Num(small), `">`, labels.String(), `</g>`)
	}
	if legendOut.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-legend" font-size="`, js.Num(small), `">`, legendOut.String(), `</g>`)
	}
	content.WriteString(`</g>`)

	rendered := types.Rendered{
		SVG:    svg.SvgDocument("radar", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
	if options.Geometry != nil {
		geo := &types.GeoRadar{Title: title.Plain(), Min: min, Max: max, Ticks: radarTicks(ticks), Polygon: graticule != "circle"}
		for _, axis := range axes {
			geo.Axes = append(geo.Axes, axis.label)
		}
		for _, curve := range curves {
			if len(curve.entries) == len(axes) {
				geo.Curves = append(geo.Curves, types.GeoCurve{Label: curve.label, Values: curve.entries})
			}
		}
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Radar: geo}
	}
	return rendered
}

// radarTicks is the count of graticule rings for a text drawing: the ticks
// option, from 0 to 20.
func radarTicks(ticks float64) int {
	if !(ticks >= 1) {
		return 0
	}
	return int(math.Min(ticks, 20))
}
