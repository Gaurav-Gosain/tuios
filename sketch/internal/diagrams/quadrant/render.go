// Ported from pele src/diagrams/quadrant/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package quadrant

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	GAP     = 4
	CAREFUL = 300
)

type Dot struct {
	cx, cy, reach float64
	text          text.Label
}

func setting(config types.Config, key string) any {
	conf, _ := config["quadrantChart"].(map[string]any)
	return conf[key]
}

func length(config types.Config, key string, fallback, max float64) float64 {
	if value, ok := setting(config, key).(float64); ok && value >= 0 {
		return math.Min(value, max)
	}
	return fallback
}

func hex(color string) string {
	if strings.HasPrefix(color, "#") {
		return color
	}
	return "#" + color
}

func renderQuadrant(model *QuadrantDb, config types.Config, options types.RenderOptions) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	axisSize := js.Round(size * 0.875)
	pointSize := js.Round(size * 0.75)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	natural := js.Round(math.Max(100, length(config, "chartWidth", 500, 4000)))
	width := js.Round(common.CapWidth(options, natural, 280, 0))
	height := js.Round((math.Max(100, length(config, "chartHeight", 500, 4000)) * width) / natural)
	outer := length(config, "quadrantPadding", types.Or(options.Padding, 8), 200)
	xPad := length(config, "xAxisLabelPadding", 5, 200)
	yPad := length(config, "yAxisLabelPadding", 5, 200)
	hasPoints := len(model.points) > 0

	label := func(t *QuadrantText, fontSize, max float64) text.Label {
		if t == nil {
			return text.EMPTY
		}
		return text.LayoutLabel(t.text, t.typ == "markdown", measurer, fontSize, math.Max(max, 40), 0)
	}

	title := text.LayoutLabel(model.title, false, measurer, size, math.Max(width-float64(2*outer), 40), text.Bold)
	titlePad := length(config, "titlePadding", 10, 200)
	titleSpace := 0.0
	if title.Height > 0 {
		titleSpace = title.Height + titlePad
	}

	hasX := (model.xAxisLeft != nil && model.xAxisLeft.text != "") || (model.xAxisRight != nil && model.xAxisRight.text != "")
	hasY := (model.yAxisBottom != nil && model.yAxisBottom.text != "") || (model.yAxisTop != nil && model.yAxisTop.text != "")
	// Mermaid moves the x axis labels below the chart as soon as there are points.
	xBelow := hasPoints || setting(config, "xAxisPosition") == "bottom"
	yRight := setting(config, "yAxisPosition") == "right"

	side := width - float64(2*outer)
	xLeftMax := side
	if model.xAxisRight != nil {
		xLeftMax = side/2 - 16
	}
	xLeft := label(model.xAxisLeft, axisSize, xLeftMax)
	xRight := label(model.xAxisRight, axisSize, side/2-16)
	xHeight := math.Max(xLeft.Height, xRight.Height)
	xSpace := 0.0
	if hasX {
		xSpace = xHeight + xPad
	}
	tall := height - float64(2*outer) - titleSpace - xSpace
	yBottomMax := tall
	if model.yAxisTop != nil {
		yBottomMax = tall/2 - 16
	}
	yBottom := label(model.yAxisBottom, axisSize, yBottomMax)
	yTop := label(model.yAxisTop, axisSize, tall/2-16)
	yHeight := math.Max(yBottom.Height, yTop.Height)
	ySpace := 0.0
	if hasY {
		ySpace = yHeight + yPad
	}

	left := outer
	if !yRight {
		left += ySpace
	}
	top := outer + titleSpace
	if !xBelow {
		top += xSpace
	}
	w := math.Max(0, side-ySpace)
	h := math.Max(0, tall)
	tileW := math.Max(0, (w-GAP)/2)
	tileH := math.Max(0, (h-GAP)/2)

	// Boxes a point label keeps clear of: the gaps between quadrants, quadrant labels, points,
	// and the labels placed before it.
	taken := []float64{left + tileW, top, left + w - tileW, top + h, left, top + tileH, left + w, top + h - tileH}
	isClear := func(from int, x0, y0, x1, y1 float64) bool {
		for k := from; k < len(taken); k += 4 {
			// Empty quadrant labels must not block point labels.
			if taken[k] >= taken[k+2] || taken[k+1] >= taken[k+3] {
				continue
			}
			if x0 < taken[k+2] && x1 > taken[k] && y0 < taken[k+3] && y1 > taken[k+1] {
				return false
			}
		}
		return true
	}

	textTop := length(config, "quadrantTextTopPadding", 5, 200)
	var geo *types.GeoQuadrant
	if options.Geometry != nil {
		geo = &types.GeoQuadrant{Title: title.Plain(), XLow: plainLabel(xLeft), XHigh: plainLabel(xRight), YLow: plainLabel(yBottom), YHigh: plainLabel(yTop)}
	}
	var quadrants strings.Builder
	for k := 0; k < 4; k++ {
		x := left
		if k == 0 || k == 3 {
			x = left + w - tileW
		}
		y := top
		if k >= 2 {
			y = top + h - tileH
		}
		t := label(model.quadrants[k], size, tileW-16)
		if geo != nil {
			geo.Quadrants[k] = plainLabel(t)
		}
		cx := x + tileW/2
		cy := y + tileH/2
		if hasPoints {
			cy = y + textTop + t.Height/2
		}
		taken = append(taken, cx-t.Width/2, cy-t.Height/2, cx+t.Width/2, cy+t.Height/2)
		svg.WriteStrings(&quadrants, `<g class="pele-quadrant" data-id="`, strconv.Itoa(k+1), `">`,
			`<rect x="`, num(x), `" y="`, num(y), `" width="`, num(tileW), `" height="`, num(tileH), `"`, svg.ROUNDED, `/>`,

			svg.LabelSvg(t, cx, cy, ` class="pele-label" fill="var(--_fg)" stroke="none"`, nil), "</g>")
	}

	var axes strings.Builder
	if hasX {
		y := top - xPad - xHeight/2
		if xBelow {
			y = top + h + xPad + xHeight/2
		}
		x := left + xLeft.Width/2
		if xRight.Height > 0 {
			x = left + w/4
		}
		svg.WriteStrings(&axes, svg.LabelSvg(xLeft, x, y, "", nil), svg.LabelSvg(xRight, left+(w*3)/4, y, "", nil))
	}
	if hasY {
		x := left - yPad - yHeight/2
		if yRight {
			x = left + w + yPad + yHeight/2
		}
		turned := func(t text.Label, y float64) {
			if t.Height > 0 {
				svg.WriteStrings(&axes, `<g transform="translate(`, num(x), ",", num(y), `) rotate(-90)">`, svg.LabelSvg(t, 0, 0, "", nil), "</g>")
			}
		}
		y := top + h - yBottom.Width/2
		if yTop.Height > 0 {
			y = top + (h*3)/4
		}
		turned(yBottom, y)
		turned(yTop, top+h/4)
	}

	radius := length(config, "pointRadius", 5, 200)
	textPad := length(config, "pointTextPadding", 5, 200)
	fill := svg.SeriesColor(0)
	var dots []Dot
	var circles []string
	for _, point := range model.points {
		if !(point.x >= 0 && point.x <= 1 && point.y >= 0 && point.y <= 1) {
			continue
		}
		shared, hasShared := model.classes[point.className]
		pick := func(own, class *string) *string {
			if own != nil {
				return own
			}
			if hasShared {
				return class
			}
			return nil
		}
		r := radius
		if point.radius != nil {
			r = *point.radius
		} else if hasShared && shared.radius != nil {
			r = *shared.radius
		}
		r = math.Min(r, 2000)
		color := pick(point.color, shared.color)
		strokeColor := pick(point.strokeColor, shared.strokeColor)
		strokeWidth := pick(point.strokeWidth, shared.strokeWidth)
		var styles []string
		if color != nil && *color != "" {
			styles = append(styles, "fill:"+hex(*color))
		}
		hasStrokeWidth := strokeWidth != nil && *strokeWidth != ""
		hasStrokeColor := strokeColor != nil && *strokeColor != ""
		if hasStrokeWidth {
			if hasStrokeColor {
				styles = append(styles, "stroke:"+hex(*strokeColor))
			}
			styles = append(styles, "stroke-width:"+*strokeWidth)
		}
		cx := left + float64(point.x*w)
		cy := top + float64((1-point.y)*h)
		reach := r
		if hasStrokeWidth {
			reach += math.Min(js.ParseIntPrefix(*strokeWidth, 10), 400) / 2
		}
		dots = append(dots, Dot{cx: cx, cy: cy, reach: reach, text: label(&point.text, pointSize, 200)})
		if geo != nil {
			geo.Points = append(geo.Points, types.GeoQPoint{X: point.x, Y: point.y, Label: plainLabel(dots[len(dots)-1].text)})
		}
		stroke := ""
		if hasStrokeWidth && !hasStrokeColor {
			stroke = ` stroke="` + fill + `"`
		}
		circles = append(circles, `<circle class="pele-point`+svg.ClassNames(point.className)+`" data-id="`+svg.EscText(point.text.text)+
			`" cx="`+num(cx)+`" cy="`+num(cy)+`" r="`+num(r)+`"`+stroke+svg.ResolveStyle(styles).Shape+`/>`)
	}

	// A label goes below its point, or above, right or left of it, or below its quadrant's title,
	// when that is the first place that is inside the chart and free. A second round lets it
	// cross the gaps between quadrants.
	// Large charts skip the search for a free place.
	careful := len(dots) <= CAREFUL
	if careful {
		for _, d := range dots {
			taken = append(taken, d.cx-d.reach, d.cy-d.reach, d.cx+d.reach, d.cy+d.reach)
		}
	}
	var labels strings.Builder
	for _, d := range dots {
		cx, cy, t := d.cx, d.cy, d.text
		halfW := t.Width / 2
		halfH := float64(t.Size*0.6) + (t.Height-t.LineHeight)/2
		away := d.reach + textPad
		inset := math.Min(away, math.Max(0, (tileW-t.Width)/2))
		// The title of the point's quadrant: 0 top right, 1 top left, 2 bottom left, 3 bottom right.
		var quadrant int
		switch {
		case cx < left+w/2 && cy < top+h/2:
			quadrant = 1
		case cx < left+w/2:
			quadrant = 2
		case cy < top+h/2:
			quadrant = 0
		default:
			quadrant = 3
		}
		heading := 8 + 4*quadrant
		below := math.Max(cy+away, taken[heading+3]+textPad) + halfH
		spots := [10]float64{cx, cy + away + halfH, cx, cy - away - halfH, cx + away + halfW, cy, cx - away - halfW, cy, cx, below}
		tx := cx
		ty := spots[1]
		found := false
		for k := 0; k < 20; k += 2 {
			spot := k % 10
			// A label moved below a title keeps clear of the plot's edge.
			margin := 0.0
			if spot == 8 {
				margin = inset
			}
			x := spots[spot]
			if (spot < 4 || spot == 8) && t.Width < w {
				x = math.Min(math.Max(spots[spot], left+halfW+margin), left+w-halfW-margin)
			}
			y := spots[spot+1]
			if x-halfW < left || x+halfW > left+w || y-halfH < top || y+halfH > top+h {
				continue
			}
			from := 8
			if k < 10 {
				from = 0
			}
			free := !careful || isClear(from, x-halfW, y-halfH, x+halfW, y+halfH)
			if !found || free {
				tx = x
				ty = y
				found = true
			}
			if free {
				break
			}
		}
		if careful {
			taken = append(taken, tx-halfW, ty-halfH, tx+halfW, ty+halfH)
		}
		labels.WriteString(svg.LabelSvg(t, tx, ty, ` class="pele-label"`, nil))
	}

	// Mermaid puts each new point under the ones before it.
	parts := []string{
		svg.LabelSvg(title, width/2, titlePad/2+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil),
		`<g class="pele-quadrants" fill="var(--_s)" stroke="var(--_b)">` + quadrants.String() + `</g>`,
	}
	if axes.Len() > 0 {
		parts = append(parts, `<g class="pele-axis-labels" font-size="`+js.Num(axisSize)+`" fill="var(--_m)">`+axes.String()+`</g>`)
	}
	if len(dots) > 0 {
		var b strings.Builder
		svg.WriteStrings(&b, `<g class="pele-points" fill="`, fill, `">`)
		for k := len(circles) - 1; k >= 0; k-- {
			b.WriteString(circles[k])
		}
		svg.WriteStrings(&b, `</g><g class="pele-point-labels" font-size="`, js.Num(pointSize), `">`, labels.String(), `</g>`)
		parts = append(parts, b.String())
	}

	var geometry *types.Geometry
	if geo != nil {
		geometry = &types.Geometry{Width: width, Height: height, Quadrant: geo}
	}
	return types.Rendered{
		Geometry: geometry,
		SVG:      svg.SvgDocument("quadrantChart", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, parts...),
		Width:    width,
		Height:   height,
		Links:    []types.LinkInfo{},
	}
}

// plainLabel is a label as one line of text. Not part of pele: it is for a
// text drawing (sketch/text).
func plainLabel(l text.Label) string { return strings.Join(l.Plain(), " ") }
