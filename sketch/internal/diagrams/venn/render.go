// Ported from pele src/diagrams/venn/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package venn

import (
	"math"
	"slices"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	RADIUS     = 100
	MAX_GROWTH = 1.6
)

var NO_STYLE = svg.ResolveStyle(nil)

type regionLine struct {
	id    string
	hasID bool
	raw   string
	size  float64
	base  int
	label text.Label
}

type Region struct {
	sets   []string
	inside []int
	spot   Spot
	// What is written in the region: its label first, then its texts. The id is that of a text.
	lines []regionLine
	style svg.ResolvedStyle
	size  float64
	// across tells whether its text is written across other circles.
	across bool
}

// keyOf is one key for a list of set ids. Ids may hold any character but a double quote, so a
// list is written with quotes between its ids.
func keyOf(ids []string) string {
	if len(ids) == 1 {
		return ids[0]
	}
	return setsKey(ids)
}

func renderVenn(model *VennDb, _ types.Config, options types.RenderOptions) types.Rendered {
	size := types.Or(options.FontSize, 16)
	small := js.Round(float64(size * 0.875))
	tiny := js.Round(float64(size * 0.8125))
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	circles := layoutCircles(model.subsets)
	index := make(map[string]int, len(circles))
	for i, c := range circles {
		index[c.id] = i
	}

	// Styles for the same targets add up, later declarations winning.
	type declared struct {
		props [][2]string
		at    map[string]int
	}
	styles := map[string]*declared{}
	for _, style := range model.styles {
		key := keyOf(style.targets)
		merged := styles[key]
		if merged == nil {
			merged = &declared{at: map[string]int{}}
			styles[key] = merged
		}
		for _, kv := range style.styles {
			if k, ok := merged.at[kv[0]]; ok {
				merged.props[k][1] = kv[1]
			} else {
				merged.at[kv[0]] = len(merged.props)
				merged.props = append(merged.props, kv)
			}
		}
	}
	styleOf := func(key string) svg.ResolvedStyle {
		d := styles[key]
		if d == nil {
			return NO_STYLE
		}
		list := make([]string, len(d.props))
		for i, kv := range d.props {
			list[i] = kv[0] + ":" + kv[1]
		}
		return svg.ResolveStyle(list)
	}

	// Circles that touch each circle; only those can cut into a region of it.
	neighbors := make([][]int, len(circles))
	for a := 0; a < len(circles); a++ {
		for b := a + 1; b < len(circles); b++ {
			if js.Hypot(circles[a].x-circles[b].x, circles[a].y-circles[b].y) < circles[a].r+circles[b].r {
				neighbors[a] = append(neighbors[a], b)
				neighbors[b] = append(neighbors[b], a)
			}
		}
	}

	// One region for each set and each stated overlap; a later statement about the same sets replaces an earlier one.
	var keys []string
	stated := map[string]VennSubset{}
	for _, subset := range model.subsets {
		key := keyOf(subset.sets)
		if _, ok := stated[key]; !ok {
			keys = append(keys, key)
		}
		stated[key] = subset
	}
	regions := make(map[string]*Region, len(keys))
	order := make([]*Region, 0, len(keys))
	for _, key := range keys {
		subset := stated[key]
		var sets []string
		for _, id := range subset.sets {
			if !containsString(sets, id) {
				sets = append(sets, id)
			}
		}
		inside := make([]int, len(sets))
		for i, id := range sets {
			inside[i] = index[id]
		}
		smallest := inside[0]
		for _, i := range inside {
			if circles[i].r < circles[smallest].r {
				smallest = i
			}
		}
		style := styleOf(key)
		label, hasText := subset.label, subset.hasLabel
		if !hasText && len(subset.sets) == 1 {
			label, hasText = subset.sets[0], true
		}
		base := 0
		if style.Bold {
			base |= text.Bold
		}
		if style.Italic {
			base |= text.Italic
		}
		var outside []int
		for _, i := range neighbors[smallest] {
			if !containsInt(inside, i) {
				outside = append(outside, i)
			}
		}
		spot := findSpot(circles, inside, outside)
		// A region that other circles cover, or all but cover, is written across them instead.
		across := len(outside) > 0 && spot.margin < float64(circles[smallest].r*0.2)
		if across {
			spot = findSpot(circles, inside, nil)
		}
		region := &Region{sets: sets, inside: inside, spot: spot, style: style, size: subset.size, across: across}
		if hasText && label != "" {
			fontSize := small
			if len(sets) == 1 {
				fontSize = size
			}
			region.lines = append(region.lines, regionLine{raw: label, size: fontSize, base: base, label: text.LayoutLabel(label, false, measurer, fontSize, 4000, base)})
		}
		regions[key] = region
		order = append(order, region)
	}
	for _, node := range model.textNodes {
		raw := node.id
		if node.hasLabel {
			raw = node.label
		}
		if region := regions[keyOf(node.sets)]; region != nil {
			region.lines = append(region.lines, regionLine{id: node.id, hasID: true, raw: raw, size: tiny, label: text.LayoutLabel(raw, false, measurer, tiny, 4000, 0)})
		}
	}

	// Lengths so far are in units of the layout. The scale gives the largest circle a set radius,
	// and grows, within limits, until every region holds what is written in it.
	largest := 0.0
	for _, c := range circles {
		if c.r > largest {
			largest = c.r
		}
	}
	scale := 1.0
	if largest > 0 {
		scale = RADIUS / largest
	}
	growth := 1.0
	for _, region := range order {
		if region.spot.margin <= 0 {
			continue
		}
		width, height := 0.0, 0.0
		for _, line := range region.lines {
			width = math.Max(width, line.label.Width)
			height += line.label.Height
		}
		need := js.Hypot(width, height)/2 + 4
		growth = math.Max(growth, math.Min(need/float64(region.spot.margin*scale), MAX_GROWTH))
	}
	scale *= growth

	// What is still too wide for its region is wrapped to it.
	for _, region := range order {
		room := math.Max(float64(float64(region.spot.margin*scale)*1.8), 72)
		for i := range region.lines {
			line := &region.lines[i]
			if line.label.Width > room {
				line.label = text.LayoutLabel(line.raw, false, measurer, line.size, room, line.base)
			}
		}
	}

	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, c := range circles {
		minX = math.Min(minX, float64((c.x-c.r)*scale))
		minY = math.Min(minY, float64((c.y-c.r)*scale))
		maxX = math.Max(maxX, float64((c.x+c.r)*scale))
		maxY = math.Max(maxY, float64((c.y+c.r)*scale))
	}
	for _, region := range order {
		wide, tall := 0.0, 0.0
		for _, line := range region.lines {
			wide = math.Max(wide, line.label.Width)
			tall += line.label.Height
		}
		minX = math.Min(minX, float64(region.spot.x*scale)-wide/2)
		maxX = math.Max(maxX, float64(region.spot.x*scale)+wide/2)
		minY = math.Min(minY, float64(region.spot.y*scale)-tall/2)
		maxY = math.Max(maxY, float64(region.spot.y*scale)+tall/2)
	}
	if len(circles) == 0 {
		minX, minY, maxX, maxY = 0, 0, 0, 0
	}

	titleText := ""
	if model.hasTitle && model.title != "" {
		titleText = normalizeText(model.title)
	}
	title := text.LayoutLabel(titleText, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	inner := math.Max(maxX-minX, title.Width)
	ox := pad + (inner-(maxX-minX))/2 - minX
	oy := pad + titleHeight - minY
	width := math.Ceil(inner + float64(2*pad))
	height := math.Ceil(maxY - minY + titleHeight + float64(2*pad))
	placed := make([]Circle, len(circles))
	for i, c := range circles {
		placed[i] = Circle{id: c.id, x: float64(c.x*scale) + ox, y: float64(c.y*scale) + oy, r: float64(c.r * scale)}
	}

	var content strings.Builder
	content.WriteString(svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	content.WriteString(`<g class="pele-venn-sets">`)
	for i, c := range placed {
		color := svg.SeriesColor(i)
		style := NO_STYLE
		if region := regions[c.id]; region != nil {
			style = region.style
		}
		svg.WriteStrings(&content, `<circle class="pele-venn-set" data-id="`, svg.EscText(c.id), `" cx="`, svg.Num(c.x), `" cy="`, svg.Num(c.y), `" r="`, svg.Num(c.r),
			`" fill="`, color, `" fill-opacity="0.2" stroke="`, color, `"`, style.Shape, `/>`)
	}
	content.WriteString(`</g>`)

	var painted []*Region
	for _, r := range order {
		if len(r.sets) > 1 && r.style.Shape != "" {
			painted = append(painted, r)
		}
	}
	// The fill under a region's text is the last overlap drawn over all of it, or its own circle when the style sets an opacity.
	under := func(region *Region) string {
		if region.across {
			return ""
		}
		fill := ""
		if len(region.sets) == 1 && (strings.Contains(region.style.Shape, `"fill-opacity:`) || strings.Contains(region.style.Shape, `;fill-opacity:`)) {
			fill = region.style.Shape
		}
		for _, other := range painted {
			if !slices.ContainsFunc(other.sets, func(id string) bool { return !slices.Contains(region.sets, id) }) {
				fill = other.style.Shape
			}
		}
		return fill
	}

	var overlaps, labels strings.Builder
	smallText, tinyText := js.Num(small), js.Num(tiny)
	for _, region := range order {
		id := svg.EscText(strings.Join(region.sets, "|"))
		// An overlap is only drawn when a style gives it a look of its own; otherwise the circles show it.
		if len(region.sets) > 1 && region.style.Shape != "" {
			parts := make([]*Circle, len(region.inside))
			for k, i := range region.inside {
				parts[k] = &placed[i]
			}
			if d := regionPath(parts, svg.Num); d != "" {
				svg.WriteStrings(&overlaps, `<path class="pele-venn-overlap" data-id="`, id, `" d="`, d, `" fill="none"`, region.style.Shape, `/>`)
			}
		}
		total := 0.0
		for _, line := range region.lines {
			total += line.label.Height
		}
		if total == 0 {
			continue
		}
		x := float64(region.spot.x*scale) + ox
		y := float64(region.spot.y*scale) + oy - total/2
		fill := under(region)
		svg.WriteStrings(&labels, `<g class="pele-venn-region" data-id="`, id, `">`)
		for _, line := range region.lines {
			own := region.style.Text
			if line.hasID {
				own = styleOf(line.id).Text
			}
			color := svg.Ink(fill, own)
			var attrs string
			if !line.hasID {
				attrs = ` class="pele-label"`
				if line.size != size {
					attrs += ` font-size="` + smallText + `"`
				}
				if color != "" {
					attrs += ` fill="` + color + `"`
				}
				attrs += own
			} else {
				if color == "" {
					color = "var(--_m)"
				}
				attrs = ` class="pele-venn-text" data-id="` + svg.EscText(line.id) + `" font-size="` + tinyText + `" fill="` + color + `"` + own
			}
			labels.WriteString(svg.LabelSvg(line.label, x, y+line.label.Height/2, attrs, nil))
			y += line.label.Height
		}
		labels.WriteString(`</g>`)
	}
	if overlaps.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-venn-overlaps">`, overlaps.String(), `</g>`)
	}
	if labels.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-venn-labels">`, labels.String(), `</g>`)
	}

	rendered := types.Rendered{
		SVG:    svg.SvgDocument("venn", width, height, size, options, svg.Accessible{}, content.String()),
		Width:  width,
		Height: height,
		Links:  []types.LinkInfo{},
	}
	if options.Geometry != nil {
		geo := &types.GeoVenn{Title: title.Plain()}
		for _, region := range order {
			r := types.GeoVennRegion{Sets: region.sets, Size: region.size, X: float64(region.spot.x*scale) + ox, Y: float64(region.spot.y*scale) + oy}
			for _, line := range region.lines {
				t := strings.Join(line.label.Plain(), " ")
				if line.hasID {
					r.Texts = append(r.Texts, t)
				} else {
					r.Label = t
				}
			}
			geo.Regions = append(geo.Regions, r)
		}
		for _, c := range placed {
			geo.Circles = append(geo.Circles, types.GeoVennCircle{Set: c.id, X: c.x, Y: c.y, R: c.r})
		}
		rendered.Geometry = &types.Geometry{Width: width, Height: height, Venn: geo}
	}
	return rendered
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}
