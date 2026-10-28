// Ported from pele src/diagrams/eventmodeling/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package eventmodeling

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

// series is the series slot for each kind of entity. A UI has none and is drawn plain.
func series(kind EntityKind) (int, bool) {
	switch kind {
	case "command":
		return 0, true
	case "event":
		return 1, true
	case "readmodel":
		return 2, true
	case "processor":
		return 6, true
	}
	return 0, false
}

const (
	BOX_PAD_X     = 12
	BOX_PAD_Y     = 10
	BOX_MIN_WIDTH = 96
	BOX_GAP       = 16
	STAGGER       = 80
	LANE_PAD      = 12
	LANE_GAP      = 8
	DATA_COLUMNS  = 48
	NOTE_COLUMNS  = 80
	CARD_GAP      = 8
)

type Placed struct {
	box  *EmBox
	name text.Label
	data []string
	x, y float64
	w, h float64
}

type Band struct {
	lane  *EmLane
	label text.Label
	right float64
	y, h  float64
}

// hardWrap splits text into lines of at most `columns` UTF-16 units, keeping its own line breaks.
// A cut inside a surrogate pair leaves each half alone, which UTF-8 writes as U+FFFD.
func hardWrap(raw string, columns int) []string {
	var out []string
	for _, line := range strings.Split(text.DecodeEntities(raw), "\n") {
		// Count code points, as Array.from does, so an emoji is never split.
		var chars []rune
		if js.Len(line) > columns {
			chars = []rune(line)
		}
		if chars == nil || len(chars) <= columns {
			out = append(out, line)
			continue
		}
		for i := 0; i < len(chars); i += columns {
			out = append(out, string(chars[i:min(i+columns, len(chars))]))
		}
	}
	return out
}

func writeLines(b *strings.Builder, rows []string, x, y, step float64, attrs string) {
	open := false
	for i, row := range rows {
		if row == "" {
			continue
		}
		if !open {
			svg.WriteStrings(b, "<text", attrs, ` xml:space="preserve">`)
			open = true
		}
		svg.WriteStrings(b, `<tspan x="`, svg.Num(x), `" y="`, svg.Num(y+float64(float64(i)*step)), `">`, svg.Esc(row), `</tspan>`)
	}
	if open {
		b.WriteString(`</text>`)
	}
}

func renderEventModel(model *EventModelingModel, config types.Config, options types.RenderOptions) types.Rendered {
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(model, config, options, down) })
}

func draw(model *EventModelingModel, _ types.Config, options types.RenderOptions, down bool) types.Rendered {
	var rendered types.Rendered
	if down {
		rendered = verticalEventModel(model, options)
	} else {
		rendered = across(model, options)
	}
	if options.Geometry != nil {
		rendered.Geometry = &types.Geometry{Width: rendered.Width, Height: rendered.Height, EventModel: geometry(model, options)}
	}
	return rendered
}

func across(model *EventModelingModel, options types.RenderOptions) types.Rendered {
	size := types.Or(options.FontSize, 16)
	nameSize := js.Round(float64(size * 0.875))
	small := js.Round(float64(size * 0.8125))
	mono := js.Round(float64(size * 0.75))
	monoStep := js.Round(float64(mono * 1.4))
	smallStep := js.Round(float64(small * 1.5))
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	widest := func(rows []string, fontSize float64, style int) float64 {
		width := 0.0
		for _, row := range rows {
			width = math.Max(width, measurer.Width(row, fontSize, style))
		}
		return width
	}

	bands := make([]*Band, len(model.lanes))
	bandOf := func(lane *EmLane) *Band {
		for _, band := range bands {
			if band.lane == lane {
				return band
			}
		}
		return nil
	}
	labelColumn := 0.0
	for i, lane := range model.lanes {
		label := text.LayoutLabel(lane.label, false, measurer, small, 4000, text.Bold)
		labelColumn = math.Max(labelColumn, label.Width)
		bands[i] = &Band{lane: lane, label: label, right: math.Inf(-1)}
	}
	labelColumn += 2 * LANE_PAD

	// Time runs left to right. A frame in a new lane starts under the end of the one before it,
	// so the arrow between them can drop straight down; a frame in the same lane follows on.
	placed := make([]*Placed, 0, len(model.boxes))
	var previous *Placed
	for _, box := range model.boxes {
		band := bandOf(box.lane)
		name := text.LayoutLabel(box.name, false, measurer, nameSize, 4000, text.Bold)
		var data []string
		if box.data != "" {
			data = hardWrap(box.data, DATA_COLUMNS)
		}
		w := math.Max(BOX_MIN_WIDTH, math.Max(name.Width, widest(data, mono, text.Mono))+2*BOX_PAD_X)
		h := 2*BOX_PAD_Y + name.Height
		if len(data) > 0 {
			h += 6 + float64(float64(len(data))*monoStep)
		}
		x := 0.0
		if previous != nil {
			stagger := previous.x + math.Max(previous.w/2, previous.w-STAGGER)
			if previous.box.lane == box.lane {
				x = band.right + BOX_GAP
			} else {
				x = math.Max(stagger, band.right+BOX_GAP)
			}
		}
		item := &Placed{box: box, name: name, data: data, x: x, w: w, h: h}
		band.right = x + w
		band.h = math.Max(band.h, h)
		placed = append(placed, item)
		previous = item
	}
	byBox := func(box *EmBox) *Placed {
		for _, item := range placed {
			if item.box == box {
				return item
			}
		}
		return nil
	}

	contentRight := 0.0
	for _, item := range placed {
		contentRight = math.Max(contentRight, item.x+item.w)
	}
	laneWidth := labelColumn + contentRight + LANE_PAD

	title := text.LayoutLabel(model.title, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	left := pad
	top := pad + titleHeight

	y := top
	var lanes strings.Builder
	smallText := js.Num(small)
	for _, band := range bands {
		band.y = y
		band.h += 2 * LANE_PAD
		svg.WriteStrings(&lanes, `<g class="pele-cluster pele-em-lane" data-id="`, svg.EscText(band.lane.label), `">`,
			`<rect x="`, svg.Num(left), `" y="`, svg.Num(y), `" width="`, svg.Num(laneWidth), `" height="`, svg.Num(band.h), `" fill="var(--_s)"`, svg.ROUNDED, `/>`,
			svg.LabelSvg(band.label, left+LANE_PAD+band.label.Width/2, y+band.h/2, ` class="pele-cluster-label" font-size="`+smallText+`" font-weight="var(--_hw)" fill="var(--_m)"`, nil),
			`</g>`)
		y += band.h + LANE_GAP
	}
	bottom := top
	if len(bands) > 0 {
		bottom = y - LANE_GAP
	}

	var nodes strings.Builder
	nameText, monoText := js.Num(nameSize), js.Num(mono)
	for _, item := range placed {
		band := bandOf(item.box.lane)
		item.x += left + labelColumn
		item.y = band.y + (band.h-item.h)/2
		frame := `x="` + svg.Num(item.x) + `" y="` + svg.Num(item.y) + `" width="` + svg.Num(item.w) + `" height="` + svg.Num(item.h) + `"`
		reset := ""
		if item.box.reset {
			reset = " pele-em-reset"
		}
		svg.WriteStrings(&nodes, `<g class="pele-node pele-em-`, item.box.kind, reset, `" data-id="`, svg.EscText(item.box.id), `">`)
		if s, ok := series(item.box.kind); ok {
			svg.WriteStrings(&nodes, `<rect `, frame, ` fill="var(--_bg)"`, svg.ROUNDED, `/><rect `, frame, ` fill="`, svg.SeriesColor(s), `" fill-opacity="0.22" stroke="`, svg.SeriesColor(s), `"`, svg.ROUNDED, `/>`)
		} else {
			svg.WriteStrings(&nodes, `<rect `, frame, ` fill="var(--_bg)" stroke="var(--_b)"`, svg.ROUNDED, `/>`)
		}
		nameY := item.y + BOX_PAD_Y + item.name.Height/2
		nodes.WriteString(svg.LabelSvg(item.name, item.x+item.w/2, nameY, ` class="pele-label" font-size="`+nameText+`" font-weight="var(--_hw)"`, nil))
		writeLines(&nodes, item.data, item.x+BOX_PAD_X, item.y+BOX_PAD_Y+item.name.Height+6+monoStep/2+float64(mono*0.35), monoStep,
			` class="pele-em-data" font-family="`+svg.FONT_MONO+`" font-size="`+monoText+`"`)
		nodes.WriteString(`</g>`)
	}

	var edges strings.Builder
	trim := svg.MarkerTrim("arrow_point")
	for _, relation := range model.relations {
		s := byBox(relation.source)
		t := byBox(relation.target)
		if s == nil || t == nil || s == t {
			continue
		}
		svg.WriteStrings(&edges, `<g class="pele-edge" data-id="`, svg.EscText(relation.source.id), "-", svg.EscText(relation.target.id), `"><path d="`)
		var head strings.Builder
		if s.box.lane == t.box.lane {
			// Along the lane, from the side of one box to the side of the other.
			forward := t.x >= s.x
			sx, tx := s.x, t.x+t.w
			if forward {
				sx, tx = s.x+s.w, t.x
			}
			sy := s.y + s.h/2
			ty := t.y + t.h/2
			dir := -1.0
			if tx >= sx {
				dir = 1
			}
			svg.WriteStrings(&edges, "M", svg.Num(sx), ",", svg.Num(sy), "L", svg.Num(tx-float64(dir*trim)), ",", svg.Num(ty))
			svg.WriteMarker(&head, "arrow_point", tx, ty, dir, 0, "var(--_l)")
		} else {
			// Between lanes, straight down or up where the boxes overlap, else in an S.
			down := t.y > s.y
			from := math.Max(s.x, t.x)
			to := math.Min(s.x+s.w, t.x+t.w)
			inset := func(w float64) float64 { return math.Min(24, w/4) }
			var sx, tx float64
			if to-from >= 16 {
				sx = (from + to) / 2
				tx = sx
			} else if t.x > s.x {
				sx = s.x + s.w - inset(s.w)
				tx = t.x + inset(t.w)
			} else {
				sx = s.x + inset(s.w)
				tx = t.x + t.w - inset(t.w)
			}
			sy, ty, dir := s.y, t.y+t.h, -1.0
			if down {
				sy, ty, dir = s.y+s.h, t.y, 1
			}
			end := ty - float64(dir*trim)
			mid := (sy + end) / 2
			if sx == tx {
				svg.WriteStrings(&edges, "M", svg.Num(sx), ",", svg.Num(sy), "V", svg.Num(end))
			} else {
				svg.WriteStrings(&edges, "M", svg.Num(sx), ",", svg.Num(sy), "C", svg.Num(sx), ",", svg.Num(mid), " ", svg.Num(tx), ",", svg.Num(mid), " ", svg.Num(tx), ",", svg.Num(end))
			}
			svg.WriteMarker(&head, "arrow_point", tx, ty, 0, dir, "var(--_l)")
		}
		svg.WriteStrings(&edges, `" fill="none" stroke="var(--_l)"/>`, head.String(), `</g>`)
	}

	// Notes and given/when/then specifications, listed under the lanes with the frame they belong to.
	names := map[string]string{}
	for _, box := range model.boxes {
		names[box.id] = box.name
	}
	var cards strings.Builder
	anyCard := false
	cardsRight := 0.0
	card := func(kind, heading, frame string, rows []string) {
		headText := heading + " · " + frame
		if name, ok := names[frame]; ok {
			headText += " " + name
		}
		head := text.LayoutLabel(headText, false, measurer, small, 4000, text.Bold)
		w := math.Max(head.Width, widest(rows, small, 0)) + 2*BOX_PAD_X
		h := 2*BOX_PAD_Y + head.Height + float64(float64(len(rows))*smallStep)
		cy := bottom
		if anyCard || len(bands) != 0 {
			cy = bottom + CARD_GAP
		}
		anyCard = true
		svg.WriteStrings(&cards, `<g class="pele-em-`, kind, `" data-id="`, svg.EscText(frame), `">`,
			`<rect x="`, svg.Num(left), `" y="`, svg.Num(cy), `" width="`, svg.Num(w), `" height="`, svg.Num(h), `" fill="var(--_bg)" stroke="var(--_b)"`, svg.ROUNDED, `/>`,
			svg.LabelSvg(head, left+BOX_PAD_X+head.Width/2, cy+BOX_PAD_Y+head.Height/2, ` font-size="`+smallText+`" font-weight="var(--_hw)"`, nil))
		writeLines(&cards, rows, left+BOX_PAD_X, cy+BOX_PAD_Y+head.Height+smallStep/2+float64(small*0.35), smallStep, ` font-size="`+smallText+`"`)
		cards.WriteString(`</g>`)
		bottom = cy + h
		cardsRight = math.Max(cardsRight, w)
	}
	for _, note := range model.notes {
		card("note", "Note", note.frame, hardWrap(note.text, NOTE_COLUMNS))
	}
	for _, spec := range model.specifications {
		var rows []string
		for _, row := range specRows(spec) {
			rows = append(rows, hardWrap(row, NOTE_COLUMNS)...)
		}
		card("specification", "Given / When / Then", spec.frame, rows)
	}

	width := math.Max(math.Max(0, cardsRight), title.Width)
	if len(bands) > 0 {
		width = math.Max(math.Max(laneWidth, cardsRight), title.Width)
	}
	totalWidth := math.Ceil(width + float64(2*pad))
	totalHeight := math.Ceil(bottom + pad)
	var content strings.Builder
	content.WriteString(svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	content.WriteString(lanes.String())
	content.WriteString(edges.String())
	content.WriteString(nodes.String())
	content.WriteString(cards.String())
	return types.Rendered{
		SVG:    svg.SvgDocument("eventmodeling", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
}

// specRows are the lines of a given/when/then specification.
func specRows(spec EmSpecification) []string {
	rows := []string{"Given  " + strings.Join(spec.given, ", ")}
	if len(spec.when) > 0 {
		rows = append(rows, "When  "+strings.Join(spec.when, ", "))
	}
	return append(rows, "Then  "+strings.Join(spec.then, ", "))
}

const (
	RAIL_GAP   = 8
	CARD_MIN   = 120
	ARROW_ROOM = 20
)

type placedCard struct {
	y, h, w float64
	index   int
	lane    float64
}

func verticalEventModel(model *EventModelingModel, options types.RenderOptions) types.Rendered {
	size := types.Or(options.FontSize, 16)
	small := js.Round(float64(size * 0.875))
	mono := js.Round(float64(size * 0.75))
	pad := types.Or(options.Padding, 8)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	room := math.Max(160, types.Or(options.MaxWidth, 320)-float64(2*pad))
	// A relation that skips a card runs down a rail beside the cards. Relations into one card
	// share a rail; others whose runs overlap take the next rail out, so that no two follow the
	// same line.
	order := func(box *EmBox) int { return slices.Index(model.boxes, box) }
	type run struct {
		target   *EmBox
		from, to int
	}
	var runs []*run
	runOf := func(target *EmBox) *run {
		for _, r := range runs {
			if r.target == target {
				return r
			}
		}
		return nil
	}
	// A relation between neighbouring cards passes the lower card's lane heading.
	passed := map[*EmBox]bool{}
	for _, relation := range model.relations {
		s, t := order(relation.source), order(relation.target)
		if s < 0 || t < 0 || s == t {
			continue
		}
		if abs(s-t) < 2 {
			passed[model.boxes[max(s, t)]] = true
			continue
		}
		if r := runOf(relation.target); r != nil {
			r.from = min(r.from, s)
			r.to = max(r.to, s)
		} else {
			runs = append(runs, &run{relation.target, min(s, t), max(s, t)})
		}
	}
	railOf := map[*EmBox]int{}
	var railEnds []int
	sorted := slices.Clone(runs)
	slices.SortStableFunc(sorted, func(a, b *run) int { return a.from - b.from })
	for _, r := range sorted {
		rail := slices.IndexFunc(railEnds, func(end int) bool { return end < r.from })
		if rail < 0 {
			rail = len(railEnds)
			railEnds = append(railEnds, 0)
		}
		railEnds[rail] = r.to
		railOf[r.target] = rail
	}
	gutter := 24 + float64(RAIL_GAP*max(0, len(railEnds)-1))
	// Rails are never shared to save room: past what the width allows, the drawing is wider.
	cardWidth := math.Max(CARD_MIN, room-gutter)
	textRoom := cardWidth - 2*BOX_PAD_X
	title := text.LayoutLabel(model.title, false, measurer, size, room, text.Bold)
	y := pad
	if title.Height != 0 {
		y = pad + (title.Height + 12)
	}
	bottom := pad + title.Height
	width := math.Max(title.Width, cardWidth+gutter)
	var nodes, edges, cards strings.Builder
	placed := map[*EmBox]placedCard{}
	literal := func(raw string, fontSize float64, style int) text.Label {
		return common.LiteralLabel(text.DecodeEntities(raw), measurer, fontSize, textRoom, style)
	}
	smallText, monoText := js.Num(small), js.Num(mono)
	joined := func(label text.Label) []string {
		out := make([]string, len(label.Lines))
		for i, line := range label.Lines {
			var b strings.Builder
			for _, span := range line {
				b.WriteString(span.Text)
			}
			out[i] = b.String()
		}
		return out
	}
	for index, box := range model.boxes {
		laneRoom := textRoom
		if passed[box] {
			laneRoom = textRoom - ARROW_ROOM
		}
		lane := common.BreakLabel(text.LayoutLabel(box.lane.label, false, measurer, small, laneRoom, text.Bold), measurer, laneRoom)
		name := text.LayoutLabel(box.name, false, measurer, small, textRoom-36, text.Bold)
		var data *text.Label
		if box.data != "" {
			d := literal(box.data, mono, text.Mono)
			data = &d
		}
		dataW := 0.0
		if data != nil {
			dataW = data.Width
		}
		w := math.Max(math.Max(cardWidth, lane.Width+2*BOX_PAD_X), math.Max(name.Width+2*BOX_PAD_X+36, dataW+2*BOX_PAD_X))
		headH := lane.Height + 12
		h := 2*BOX_PAD_Y + name.Height
		if data != nil {
			h += data.Height + 6
		}
		cy := y + headH
		frame := `x="` + svg.Num(pad) + `" y="` + svg.Num(cy) + `" width="` + svg.Num(w) + `" height="` + svg.Num(h) + `"`
		reset := ""
		if box.reset {
			reset = " pele-em-reset"
		}
		svg.WriteStrings(&nodes, `<g class="pele-cluster pele-em-lane" data-id="`, svg.EscText(box.lane.label), `">`,
			common.LeftLabelSvg(lane, pad+BOX_PAD_X, y, ` class="pele-cluster-label" font-size="`+smallText+`" font-weight="var(--_hw)" fill="var(--_m)"`),
			`<g class="pele-node pele-em-`, box.kind, reset, `" data-id="`, svg.EscText(box.id), `"><rect `, frame, ` fill="var(--_bg)" stroke="var(--_b)"`, svg.ROUNDED, `/>`)
		if s, ok := series(box.kind); ok {
			svg.WriteStrings(&nodes, `<rect `, frame, ` fill="`, svg.SeriesColor(s), `" fill-opacity="0.22" stroke="`, svg.SeriesColor(s), `"`, svg.ROUNDED, `/>`)
		}
		svg.WriteStrings(&nodes, svg.LabelSvg(name, pad+BOX_PAD_X+name.Width/2, cy+BOX_PAD_Y+name.Height/2, ` class="pele-label" font-size="`+smallText+`" font-weight="var(--_hw)"`, nil),
			`<text class="pele-em-frame" x="`, svg.Num(pad+w-BOX_PAD_X), `" y="`, svg.Num(cy+BOX_PAD_Y+small), `" text-anchor="end" font-size="`, monoText, `" fill="var(--_m)">`, svg.Esc(box.id), `</text>`)
		if data != nil {
			writeLines(&nodes, joined(*data), pad+BOX_PAD_X, cy+BOX_PAD_Y+name.Height+6+data.LineHeight/2+float64(mono*0.35), data.LineHeight,
				` class="pele-em-data" font-family="`+svg.FONT_MONO+`" font-size="`+monoText+`"`)
		}
		nodes.WriteString(`</g></g>`)
		placed[box] = placedCard{y: cy, h: h, w: w, index: index, lane: lane.Width}
		width = math.Max(width, w+gutter)
		bottom = cy + h
		y = bottom + 28
	}
	trim := svg.MarkerTrim("arrow_point")
	for _, relation := range model.relations {
		s, sok := placed[relation.source]
		t, tok := placed[relation.target]
		if !sok || !tok || relation.source == relation.target {
			continue
		}
		down := t.y > s.y
		dir, sy, ty := -1.0, s.y, t.y+t.h
		if down {
			dir, sy, ty = 1, s.y+s.h, t.y
		}
		svg.WriteStrings(&edges, `<g class="pele-edge" data-id="`, svg.EscText(relation.source.id), "-", svg.EscText(relation.target.id), `"><path d="`)
		var head strings.Builder
		if abs(s.index-t.index) == 1 {
			// The lower card's lane heading sits in the gap, and wraps to leave the arrow room beside it.
			lower := s
			if down {
				lower = t
			}
			cx := pad + math.Max(math.Min(s.w, t.w)/2, BOX_PAD_X+lower.lane+16)
			svg.WriteStrings(&edges, "M", svg.Num(cx), ",", svg.Num(sy), "V", svg.Num(ty-float64(dir*trim)))
			svg.WriteMarker(&head, "arrow_point", cx, ty, 0, dir, "var(--_l)")
		} else {
			rail := pad + width - gutter + 12 + float64(RAIL_GAP*railOf[relation.target])
			// A card can send one relation and receive another: they leave above and arrive below its middle.
			from, to := s.y+s.h/2-5, t.y+t.h/2+5
			svg.WriteStrings(&edges, "M", svg.Num(pad+s.w), ",", svg.Num(from), "H", svg.Num(rail), "V", svg.Num(to), "H", svg.Num(pad+t.w+trim))
			svg.WriteMarker(&head, "arrow_point", pad+t.w, to, -1, 0, "var(--_l)")
		}
		svg.WriteStrings(&edges, `" fill="none" stroke="var(--_l)"/>`, head.String(), `</g>`)
	}
	names := map[string]string{}
	for _, box := range model.boxes {
		names[box.id] = box.name
	}
	card := func(kind, heading, frame, raw string) {
		headText := heading + " · " + frame
		if name, ok := names[frame]; ok {
			headText += " " + name
		}
		head := text.LayoutLabel(headText, false, measurer, small, textRoom, text.Bold)
		body := literal(raw, small, 0)
		w := math.Max(cardWidth, math.Max(head.Width+2*BOX_PAD_X, body.Width+2*BOX_PAD_X))
		h := 2*BOX_PAD_Y + head.Height + body.Height
		svg.WriteStrings(&cards, `<g class="pele-em-`, kind, `" data-id="`, svg.EscText(frame), `"><rect x="`, svg.Num(pad), `" y="`, svg.Num(y), `" width="`, svg.Num(w), `" height="`, svg.Num(h), `" fill="var(--_bg)" stroke="var(--_b)"`, svg.ROUNDED, `/>`,
			svg.LabelSvg(head, pad+BOX_PAD_X+head.Width/2, y+BOX_PAD_Y+head.Height/2, ` font-size="`+smallText+`" font-weight="var(--_hw)"`, nil))
		writeLines(&cards, joined(body), pad+BOX_PAD_X, y+BOX_PAD_Y+head.Height+body.LineHeight/2+float64(small*0.35), body.LineHeight, ` font-size="`+smallText+`"`)
		cards.WriteString(`</g>`)
		bottom = y + h
		y = bottom + CARD_GAP
		width = math.Max(width, w)
	}
	for _, note := range model.notes {
		card("note", "Note", note.frame, note.text)
	}
	for _, spec := range model.specifications {
		card("specification", "Given / When / Then", spec.frame, strings.Join(specRows(spec), "\n"))
	}
	totalWidth := math.Ceil(width + float64(2*pad))
	height := math.Ceil(bottom + pad)
	content := svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil) + edges.String() + nodes.String() + cards.String()
	return types.Rendered{
		SVG:    svg.SvgDocument("eventmodeling", totalWidth, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content),
		Width:  totalWidth,
		Height: height,
		Links:  []types.LinkInfo{},
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// geometry is the model as lists, for a drawing in text. Not part of pele.
func geometry(model *EventModelingModel, options types.RenderOptions) *types.GeoEventModel {
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	geo := &types.GeoEventModel{Title: text.LayoutLabel(model.title, false, measurer, 16, math.Inf(1), text.Bold).Plain()}
	for _, lane := range model.lanes {
		geo.Lanes = append(geo.Lanes, lane.label)
	}
	for _, box := range model.boxes {
		data := []string(nil)
		if box.data != "" {
			data = strings.Split(text.DecodeEntities(box.data), "\n")
		}
		geo.Boxes = append(geo.Boxes, types.GeoFrame{ID: box.id, Name: box.name, Kind: box.kind, Lane: slices.Index(model.lanes, box.lane), Reset: box.reset, Data: data})
	}
	for _, relation := range model.relations {
		geo.Relations = append(geo.Relations, [2]int{slices.Index(model.boxes, relation.source), slices.Index(model.boxes, relation.target)})
	}
	names := map[string]string{}
	for _, box := range model.boxes {
		names[box.id] = box.name
	}
	head := func(heading, frame string) string {
		if name, ok := names[frame]; ok {
			return heading + " · " + frame + " " + name
		}
		return heading + " · " + frame
	}
	for _, note := range model.notes {
		geo.Notes = append(geo.Notes, types.GeoEventCard{Head: head("Note", note.frame), Lines: strings.Split(text.DecodeEntities(note.text), "\n")})
	}
	for _, spec := range model.specifications {
		geo.Notes = append(geo.Notes, types.GeoEventCard{Head: head("Given / When / Then", spec.frame), Lines: specRows(spec)})
	}
	return geo
}
