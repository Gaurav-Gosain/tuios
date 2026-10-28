// Ported from pele src/diagrams/cynefin/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package cynefin

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

type quadrant struct {
	name     DomainName
	col, row float64
	color    int
}

// QUADRANTS are [domain, column, row, series color].
var QUADRANTS = [...]quadrant{
	{"complex", 0, 0, 0},
	{"complicated", 1, 0, 1},
	{"chaotic", 0, 1, 3},
	{"clear", 1, 1, 2},
}

const CONFUSION_COLOR = 4

// meta is [name, decision model, kind of practice].
func meta(name DomainName) [3]string {
	switch name {
	case "complex":
		return [3]string{"Complex", "Probe → Sense → Respond", "Emergent Practices"}
	case "complicated":
		return [3]string{"Complicated", "Sense → Analyse → Respond", "Good Practices"}
	case "clear":
		return [3]string{"Clear", "Sense → Categorise → Respond", "Best Practices"}
	case "chaotic":
		return [3]string{"Chaotic", "Act → Sense → Respond", "Novel Practices"}
	}
	return [3]string{"Confusion", "", "Disorder"}
}

const (
	MAX_CONFUSION_ITEMS = 3
	ITEM_MAX_WIDTH      = 200
	ITEM_PAD_X          = 10
	ITEM_PAD_Y          = 4
	ITEM_GAP            = 6
	HEAD_GAP            = 10
	MARGIN              = 20
	CLEARANCE           = 10
)

type Badge struct {
	text     string
	label    text.Label
	w, h     float64
	overflow bool
}

type Block struct {
	name      DomainName
	title     text.Label
	subtitles []text.Label
	badges    []Badge
	w, h      float64
	cx, cy    float64
}

func setting(config types.Config, key string, fallback, min, max float64) float64 {
	section, _ := config["cynefin"].(map[string]any)
	if value, ok := section[key].(float64); ok && value >= min && value <= max {
		return value
	}
	return fallback
}

func sq(x float64) float64 { return float64(x * x) }

func renderCynefin(model *CynefinModel, config types.Config, options types.RenderOptions) types.Rendered {
	size := types.Or(options.FontSize, 16)
	small := js.Round(float64(size * 0.8125))
	itemSize := js.Round(float64(size * 0.875))
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	icons := options.Icons
	section, _ := config["cynefin"].(map[string]any)
	showDescriptions := section["showDomainDescriptions"] != false
	amplitude := setting(config, "boundaryAmplitude", 8, 0, 50)
	seed := resolveSeed(setting(config, "seed", 0, -1e15, 1e15), types.Or(options.IDPrefix, "pele"))
	itemLine := js.Round(float64(itemSize * 1.5))

	badge := func(t string, overflow bool) Badge {
		label := text.LayoutLabel(t, false, measurer, itemSize, ITEM_MAX_WIDTH, 0)
		return Badge{text: t, label: label, w: label.Width + 2*ITEM_PAD_X, h: math.Max(label.Height, itemLine) + 2*ITEM_PAD_Y, overflow: overflow}
	}

	block := func(name DomainName) Block {
		m := meta(name)
		title := text.LayoutLabel(m[0], false, measurer, size, 4000, text.Bold)
		var subtitles []text.Label
		if showDescriptions {
			for _, t := range [2]string{m[1], m[2]} {
				if t != "" {
					subtitles = append(subtitles, text.LayoutLabel(t, false, measurer, small, 4000, text.Italic))
				}
			}
		}
		var items []string
		if d := model.domains[name]; d != nil {
			items = d.items
		}
		shown := len(items)
		if name == "confusion" && len(items) > MAX_CONFUSION_ITEMS {
			shown = MAX_CONFUSION_ITEMS
		}
		var badges []Badge
		for i := 0; i < shown; i++ {
			badges = append(badges, badge(items[i], false))
		}
		if shown < len(items) {
			badges = append(badges, badge("+"+strconv.Itoa(len(items)-shown)+" more", true))
		}
		w := title.Width
		h := title.Height
		for _, s := range subtitles {
			w = math.Max(w, s.Width)
			h += s.Height
		}
		if len(badges) > 0 {
			h += HEAD_GAP - ITEM_GAP
		}
		for _, b := range badges {
			w = math.Max(w, b.w)
			h += ITEM_GAP + b.h
		}
		return Block{name: name, title: title, subtitles: subtitles, badges: badges, w: w, h: h}
	}

	var quadrants [4]Block
	for i, q := range QUADRANTS {
		quadrants[i] = block(q.name)
	}
	confusion := block("confusion")
	rx := math.Max(88, float64(confusion.w*0.71)+8)
	ry := math.Max(64, float64(confusion.h*0.71)+6)

	quadW := setting(config, "width", 640, 100, 10000) / 2
	quadH := setting(config, "height", 440, 100, 10000) / 2
	for _, b := range quadrants {
		quadW = math.Max(quadW, b.w+2*MARGIN)
		quadH = math.Max(quadH, b.h+2*MARGIN)
	}
	// A block sits in the middle of its quadrant, so its inner corner must stay clear of the ellipse.
	for _, b := range quadrants {
		dx := (quadW - b.w) / 2
		dy := (quadH - b.h) / 2
		if sq(dx/(rx+CLEARANCE))+sq(dy/(ry+CLEARANCE)) >= 1 {
			continue
		}
		wider := b.w + float64(2*(rx+CLEARANCE))
		taller := b.h + float64(2*(ry+CLEARANCE))
		if float64(wider*quadH) <= float64(quadW*taller) {
			quadW = math.Max(quadW, wider)
		} else {
			quadH = math.Max(quadH, taller)
		}
	}

	title := text.LayoutLabel(model.title, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	width := 2 * quadW
	height := 2 * quadH
	x0 := pad
	y0 := pad + titleHeight
	cx := x0 + quadW
	cy := y0 + quadH
	right := x0 + width
	bottom := y0 + height

	fold := FOLD
	fold.segments, fold.pinEnd = 4, true
	west := HORIZON
	west.segments, west.pinEnd = 4, true
	east := HORIZON
	east.segments, east.pinStart = 4, true
	foldC := wave(y0, cy, cx, seed, amplitude, fold)
	westC := wave(x0, cx, cy, seed+100, amplitude, west)
	eastC := wave(cx, right, cy, seed+200, amplitude, east)
	drop := cliff(cx, cy, bottom, float64(width*0.03))
	at := func(b *strings.Builder, curve []float64, end bool) {
		if end {
			svg.WriteStrings(b, svg.Num(curve[len(curve)-2]), ",", svg.Num(curve[len(curve)-1]))
		} else {
			svg.WriteStrings(b, svg.Num(curve[0]), ",", svg.Num(curve[1]))
		}
	}

	region := func(name DomainName) string {
		var b strings.Builder
		switch name {
		case "complex":
			svg.WriteStrings(&b, "M", svg.Num(x0), ",", svg.Num(y0), "L")
			at(&b, foldC, false)
			forward(&b, foldC)
			backward(&b, westC)
		case "complicated":
			b.WriteString("M")
			at(&b, foldC, false)
			svg.WriteStrings(&b, "L", svg.Num(right), ",", svg.Num(y0), "L")
			at(&b, eastC, true)
			backward(&b, eastC)
			backward(&b, foldC)
		case "chaotic":
			b.WriteString("M")
			at(&b, westC, false)
			forward(&b, westC)
			forward(&b, drop)
			svg.WriteStrings(&b, "L", svg.Num(x0), ",", svg.Num(bottom))
		case "clear":
			b.WriteString("M")
			at(&b, eastC, false)
			forward(&b, eastC)
			svg.WriteStrings(&b, "L", svg.Num(right), ",", svg.Num(bottom), "L")
			at(&b, drop, true)
			backward(&b, drop)
		}
		b.WriteString("Z")
		return b.String()
	}

	var content strings.Builder
	content.WriteString(svg.LabelSvg(title, x0+width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	content.WriteString(`<g class="pele-cynefin-regions">`)
	for _, q := range QUADRANTS {
		svg.WriteStrings(&content, `<path class="pele-cynefin-region" data-id="`, q.name, `" d="`, region(q.name), `" fill="`, svg.SeriesColor(q.color), `" fill-opacity="0.14"/>`)
	}
	content.WriteString(`</g>`)
	content.WriteString(`<g class="pele-cynefin-boundaries" fill="none" stroke="var(--_b)" stroke-dasharray="6 3"><path d="`)
	curvePath(&content, foldC)
	content.WriteString(`"/><path d="`)
	curvePath(&content, westC)
	content.WriteString(`"/><path d="`)
	curvePath(&content, eastC)
	content.WriteString(`"/><path class="pele-cynefin-cliff" d="`)
	curvePath(&content, drop)
	content.WriteString(`" stroke="var(--_l)" stroke-width="2" stroke-dasharray="none" stroke-linecap="round"/></g>`)
	oval := ellipsePath(cx, cy, rx, ry)
	svg.WriteStrings(&content, `<path d="`, oval, `" fill="var(--_bg)"/>`,
		`<path class="pele-cynefin-region" data-id="confusion" d="`, oval, `" fill="`, svg.SeriesColor(CONFUSION_COLOR), `" fill-opacity="0.18" stroke="var(--_b)" stroke-dasharray="4 2"/>`)

	smallText := js.Num(small)
	itemText := js.Num(itemSize)
	drawBlock := func(out *strings.Builder, b *Block) {
		y := b.cy - b.h/2
		svg.WriteStrings(out, `<g class="pele-cynefin-domain" data-id="`, b.name, `">`,
			svg.LabelSvg(b.title, b.cx, y+b.title.Height/2, ` class="pele-label" font-weight="var(--_hw)"`, nil))
		y += b.title.Height
		for _, s := range b.subtitles {
			out.WriteString(svg.LabelSvg(s, b.cx, y+s.Height/2, ` class="pele-cynefin-subtitle" font-size="`+smallText+`" font-style="italic" fill="var(--_m)"`, nil))
			y += s.Height
		}
		y += HEAD_GAP - ITEM_GAP
		for _, item := range b.badges {
			y += ITEM_GAP
			out.WriteString(`<g class="pele-node`)
			if item.overflow {
				out.WriteString(` pele-cynefin-overflow"`)
			} else {
				svg.WriteStrings(out, `" data-id="`, svg.EscText(item.text), `"`)
			}
			svg.WriteStrings(out, `><rect x="`, svg.Num(b.cx-item.w/2), `" y="`, svg.Num(y), `" width="`, svg.Num(item.w), `" height="`, svg.Num(item.h),
				`" fill="var(--_bg)" stroke="var(--_b)"`)
			attrs := ` class="pele-label" font-size="` + itemText + `"`
			if item.overflow {
				out.WriteString(` stroke-dasharray="3 2"`)
				attrs += ` fill="var(--_m)"`
			}
			svg.WriteStrings(out, svg.ROUNDED, `/>`, svg.LabelSvg(item.label, b.cx, y+item.h/2, attrs, icons), `</g>`)

			y += item.h
		}
		out.WriteString(`</g>`)
	}

	blocks := map[DomainName]*Block{}
	for index, q := range QUADRANTS {
		b := &quadrants[index]
		b.cx = x0 + float64(quadW*(q.col+0.5))
		b.cy = y0 + float64(quadH*(q.row+0.5))
		blocks[q.name] = b
		drawBlock(&content, b)
	}
	confusion.cx = cx
	confusion.cy = cy
	blocks["confusion"] = &confusion
	drawBlock(&content, &confusion)

	inside := func(b *Block, x, y float64) bool {
		if b == &confusion {
			return sq((x-cx)/(rx+4))+sq((y-cy)/(ry+4)) <= 1
		}
		return math.Abs(x-b.cx) <= b.w/2+8 && math.Abs(y-b.cy) <= b.h/2+8
	}

	var edges strings.Builder
	seen := map[string]int{}
	for _, transition := range model.transitions {
		from := blocks[transition.from]
		to := blocks[transition.to]
		if from == nil || to == nil || from == to {
			continue
		}
		key := transition.to + " " + transition.from
		if transition.from < transition.to {
			key = transition.from + " " + transition.to
		}
		repeat := seen[key]
		seen[key] = repeat + 1

		dx := to.cx - from.cx
		dy := to.cy - from.cy
		length := js.Or(js.Hypot(dx, dy), 1)
		mx := (from.cx + to.cx) / 2
		my := (from.cy + to.cy) / 2
		// Curves bow away from the middle of the diagram, which keeps them off the ellipse.
		outward := -1.0
		if float64(-dy*(mx-cx))+float64(dx*(my-cy)) >= 0 {
			outward = 1
		}
		nx := float64((-dy / length) * outward)
		ny := float64((dx / length) * outward)
		// A diagonal runs through the middle, so it bows far enough to pass the ellipse.
		diagonal := from != &confusion && to != &confusion && math.Abs(dx) > 1 && math.Abs(dy) > 1
		var bow float64
		if diagonal {
			bow = 2 * (js.Hypot(float64(rx*nx), float64(ry*ny)) + 16)
		} else {
			bow = float64(length * 0.15)
		}
		bow += float64(repeat * 56)
		px := mx + float64(nx*bow)
		py := my + float64(ny*bow)
		point := func(t float64) (float64, float64) {
			u := 1 - t
			uu, ut2, tt := float64(u*u), float64(float64(2*u)*t), float64(t*t)
			return float64(uu*from.cx) + float64(ut2*px) + float64(tt*to.cx), float64(uu*from.cy) + float64(ut2*py) + float64(tt*to.cy)
		}
		insideAt := func(b *Block, t float64) bool {
			x, y := point(t)
			return inside(b, x, y)
		}
		crossing := func(b *Block, lo, hi float64, leaving bool) float64 {
			for i := 0; i < 16; i++ {
				mid := (lo + hi) / 2
				if insideAt(b, mid) == leaving {
					lo = mid
				} else {
					hi = mid
				}
			}
			return (lo + hi) / 2
		}
		const STEPS = 48
		i := 1
		for i <= STEPS && insideAt(from, float64(i)/STEPS) {
			i++
		}
		if i > STEPS {
			continue
		}
		t0 := crossing(from, float64(i-1)/STEPS, float64(i)/STEPS, true)
		for i <= STEPS && !insideAt(to, float64(i)/STEPS) {
			i++
		}
		t1 := 1.0
		if i <= STEPS {
			t1 = crossing(to, float64(i-1)/STEPS, float64(i)/STEPS, false)
		}

		sx, sy := point(t0)
		ex, ey := point(t1)
		blossom := func(a, b, c float64) float64 {
			return float64(float64((1-t0)*(1-t1))*a) + float64(float64(float64((1-t0)*t1)+float64(t0*(1-t1)))*b) + float64(float64(t0*t1)*c)
		}
		qx := blossom(from.cx, px, to.cx)
		qy := blossom(from.cy, py, to.cy)
		el := js.Or(js.Hypot(ex-qx, ey-qy), 1)
		ux := (ex - qx) / el
		uy := (ey - qy) / el
		svg.WriteStrings(&edges, `<g class="pele-edge" data-id="`, transition.from, "-", transition.to, `">`)
		var head strings.Builder
		svg.WriteMarker(&head, "arrow_point", ex, ey, ux, uy, "var(--_l)")
		trim := svg.MarkerTrim("arrow_point")
		ex -= float64(ux * trim)
		ey -= float64(uy * trim)
		svg.WriteStrings(&edges, `<path d="M`, svg.Num(sx), ",", svg.Num(sy), "Q", svg.Num(qx), ",", svg.Num(qy), " ", svg.Num(ex), ",", svg.Num(ey),
			`" fill="none" stroke="var(--_l)"/>`, head.String())

		if transition.label != "" {
			label := text.LayoutLabel(transition.label, false, measurer, small, ITEM_MAX_WIDTH, 0)
			lx, ly := point((t0 + t1) / 2)
			away := float64(math.Abs(nx)*label.Width)/2 + float64(math.Abs(ny)*label.Height)/2 + 6
			half := label.Width/2 + 4
			tx := math.Min(math.Max(lx+float64(nx*away), x0+half), math.Max(right-half, x0+half))
			edges.WriteString(svg.LabelSvg(label, tx, ly+float64(ny*away), ` class="pele-edge-label" font-size="`+smallText+`"`, icons))
		}
		edges.WriteString(`</g>`)
	}
	if edges.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-edges">`, edges.String(), `</g>`)
	}

	totalWidth := math.Ceil(math.Max(width, title.Width) + float64(2*pad))
	totalHeight := math.Ceil(height + titleHeight + float64(2*pad))
	rendered := types.Rendered{
		SVG:    svg.SvgDocument("cynefin", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
	if options.Geometry != nil {
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Cynefin: geometry(model, title, measurer, showDescriptions)}
	}
	return rendered
}

// geometry is the model as lists, for a drawing in text. Not part of pele.
func geometry(model *CynefinModel, title text.Label, measurer text.TextMeasurer, descriptions bool) *types.GeoCynefin {
	plain := func(s string) string {
		return strings.Join(text.LayoutLabel(s, false, measurer, 16, math.Inf(1), 0).Plain(), " ")
	}
	geo := &types.GeoCynefin{Title: title.Plain()}
	for _, name := range [...]DomainName{"complex", "complicated", "chaotic", "clear", "confusion"} {
		m := meta(name)
		d := types.GeoDomain{Name: m[0]}
		if descriptions {
			d.Model, d.Practice = m[1], m[2]
		}
		if domain := model.domains[name]; domain != nil {
			for _, item := range domain.items {
				d.Items = append(d.Items, plain(item))
			}
		}
		geo.Domains = append(geo.Domains, d)
	}
	for _, t := range model.transitions {
		geo.Transitions = append(geo.Transitions, types.GeoTransition{From: meta(t.from)[0], To: meta(t.to)[0], Label: plain(t.label)})
	}
	return geo
}
