// Ported from pele src/diagrams/sequence/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package sequence

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

// Arrow heads.
const (
	Head_None = iota
	Head_Arrow
	Head_Cross
	Head_Open
	Head_HalfSolid
	Head_HalfStick
)

type LineStyle struct {
	dotted bool
	head   int
	// Where the heads are: at the receiver, at the sender only (the reverse arrows), or at both.
	atStart bool
	atEnd   bool
	bottom  bool
}

var STYLES = func() map[int]*LineStyle {
	s := map[int]*LineStyle{}
	style := func(typ int, dotted bool, head int, ends int, bottom bool) {
		s[typ] = &LineStyle{dotted: dotted, head: head, atStart: ends != 1, atEnd: ends != 0, bottom: bottom}
	}
	style(LINETYPE_SOLID, false, Head_Arrow, 1, false)
	style(LINETYPE_DOTTED, true, Head_Arrow, 1, false)
	style(LINETYPE_SOLID_CROSS, false, Head_Cross, 1, false)
	style(LINETYPE_DOTTED_CROSS, true, Head_Cross, 1, false)
	style(LINETYPE_SOLID_OPEN, false, Head_None, 1, false)
	style(LINETYPE_DOTTED_OPEN, true, Head_None, 1, false)
	style(LINETYPE_SOLID_POINT, false, Head_Open, 1, false)
	style(LINETYPE_DOTTED_POINT, true, Head_Open, 1, false)
	style(LINETYPE_BIDIRECTIONAL_SOLID, false, Head_Arrow, 2, false)
	style(LINETYPE_BIDIRECTIONAL_DOTTED, true, Head_Arrow, 2, false)
	for _, dotted := range []bool{false, true} {
		d := 0
		if dotted {
			d = 10
		}
		style(LINETYPE_SOLID_TOP+d, dotted, Head_HalfSolid, 1, false)
		style(LINETYPE_SOLID_BOTTOM+d, dotted, Head_HalfSolid, 1, true)
		style(LINETYPE_STICK_TOP+d, dotted, Head_HalfStick, 1, false)
		style(LINETYPE_STICK_BOTTOM+d, dotted, Head_HalfStick, 1, true)
		style(LINETYPE_SOLID_ARROW_TOP_REVERSE+d, dotted, Head_HalfSolid, 0, false)
		style(LINETYPE_SOLID_ARROW_BOTTOM_REVERSE+d, dotted, Head_HalfSolid, 0, true)
		style(LINETYPE_STICK_ARROW_TOP_REVERSE+d, dotted, Head_HalfStick, 0, false)
		style(LINETYPE_STICK_ARROW_BOTTOM_REVERSE+d, dotted, Head_HalfStick, 0, true)
	}
	return s
}()

var FRAME_KIND = map[int]string{
	LINETYPE_LOOP_START:     "loop",
	LINETYPE_ALT_START:      "alt",
	LINETYPE_OPT_START:      "opt",
	LINETYPE_PAR_START:      "par",
	LINETYPE_PAR_OVER_START: "par",
	LINETYPE_CRITICAL_START: "critical",
	LINETYPE_BREAK_START:    "break",
	LINETYPE_RECT_START:     "rect",
}

const (
	MSG_PAD    = 12
	SELF_W     = 28
	SELF_H     = 22
	ACT_W      = 10
	ACT_STEP   = 4
	ACT_MIN    = 14
	NOTE_GAP   = 10
	NOTE_OVER  = 16
	FRAME_PAD  = 20
	FRAME_NEST = 10
	TAB_H      = 20
	FRAME_TEXT = 240
	BADGE_H    = 16
	CENTRAL_R  = 4
	DASH       = ` stroke-dasharray="4 4"`
)

type Numbering struct {
	// index is the number each message line carries, whether or not it is shown. NaN for everything else.
	index []float64
	shown []bool
	// visible says whether numbers are on after the last statement.
	visible bool
}

func isLineMessage(typ int) bool {
	_, ok := STYLES[typ]
	return ok
}

// numberMessages counts message lines the way Mermaid's renderer does while it draws.
func numberMessages(messages []*SeqMessage, always bool) Numbering {
	index := make([]float64, len(messages))
	shown := make([]bool, len(messages))
	current := 1.0
	step := 1.0
	visible := false
	for i, m := range messages {
		index[i] = math.NaN()
		shown[i] = false
		if m.Type == LINETYPE_AUTONUMBER {
			spec := m.Numbering
			current = js.Or(spec.Start, current)
			step = js.Or(spec.Step, step)
			visible = spec.Visible
		} else if isLineMessage(m.Type) {
			index[i] = current
			shown[i] = visible || always
			current = js.Round((current+step)*100) / 100
		}
	}
	return Numbering{index, shown, visible}
}

type column struct {
	id          string
	actor       *SeqActor
	kind        string
	label       text.Label
	size        ActorSize
	x           float64
	createAt    int
	destroyAt   int
	top         float64
	lifeStart   float64
	lifeEnd     float64
	activations []float64
}

const (
	V_Line = iota
	V_Self
	V_Note
)

type view struct {
	kind  int
	a     int
	b     int
	label text.Label
	x     float64
	w     float64
}

type seqFrame struct {
	kind          string
	x0            float64
	x1            float64
	top           float64
	title         text.Label
	tabW          float64
	fill          string
	dividers      []int
	dividerY      []float64
	dividerLabels []text.Label
}

type boxRun struct {
	box   *SeqBox
	first int
	last  int
	title text.Label
	grow  float64
}

var NO_LABEL = text.Label{}

func option(conf types.Config, key string, fallback float64) float64 {
	if v, ok := conf[key].(float64); ok && v >= 0 && v <= 2000 {
		return v
	}
	return fallback
}

func half(x, y, dir, up float64, stick bool, color string) string {
	bx := svg.Num(x - float64(dir*svg.ARROW))
	if stick {
		return `<path class="pele-marker" d="M` + svg.Num(x) + `,` + svg.Num(y) + `L` + bx + `,` + svg.Num(y+float64(up*5)) + `"/>`
	}
	return `<path class="pele-marker" d="M` + svg.Num(x) + `,` + svg.Num(y) + `L` + bx + `,` + svg.Num(y+float64(up*4.5)) + `V` + svg.Num(y) + `Z" fill="` + color + `" stroke="none"/>`
}

// head draws the head whose tip is at (x, y) and points along dir. `up` is the side a half head sits on.
func head(typ int, x, y, dir, up float64, color string) string {
	switch typ {
	case Head_Arrow:
		return svg.Marker("arrow_point", x, y, dir, 0, color)
	case Head_Cross:
		return svg.Marker("arrow_cross", x, y, dir, 0, color)
	case Head_Open:
		return `<path class="pele-marker" d="M` + svg.Num(x-float64(dir*svg.ARROW)) + `,` + svg.Num(y-4.5) + `L` + svg.Num(x) + `,` + svg.Num(y) + `L` + svg.Num(x-float64(dir*svg.ARROW)) + `,` + svg.Num(y+4.5) + `" stroke-linejoin="round"/>`
	case Head_HalfSolid:
		return half(x, y, dir, up, false, color)
	case Head_HalfStick:
		return half(x, y, dir, up, true, color)
	}
	return ""
}

func trim(typ int) float64 {
	switch typ {
	case Head_Arrow:
		return svg.ARROW - 1
	case Head_Cross:
		return 4
	}
	return 0
}

func renderSequence(db *SeqDb, config types.Config, options types.RenderOptions) types.Rendered {
	return common.Tighten(options, func(tight bool) types.Rendered { return draw(db, config, options, tight, 0, true) })
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// monoMeasurer measures text in the monospace font.
type monoMeasurer struct{ text.TextMeasurer }

func (m monoMeasurer) Width(t string, s float64, style int) float64 {
	return m.TextMeasurer.Width(t, s, style|text.Mono)
}

func draw(db *SeqDb, config types.Config, options types.RenderOptions, tight bool, hold float64, byNeed bool) types.Rendered {
	conf, _ := config["sequence"].(map[string]any)
	if conf == nil {
		conf = types.Config{}
	}
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	tiny := js.Round(size * 0.75)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	monospaced := options.HasMonospace(types.MonospaceMessages)
	messageMeasurer := measurer
	if monospaced {
		messageMeasurer = monoMeasurer{measurer}
	}
	icons := options.Icons
	pad := types.Or(options.Padding, 8)
	mirror := true
	if v, ok := conf["mirrorActors"].(bool); ok && !v {
		mirror = false
	}
	rightAngles, _ := conf["rightAngles"].(bool)
	inf := math.Inf(1)
	tightOr := func(v float64) float64 {
		if tight {
			return v
		}
		return inf
	}
	actorMargin := math.Min(option(conf, "actorMargin", 40), tightOr(8))
	minW := math.Min(option(conf, "width", 64), tightOr(48))
	minH := option(conf, "height", js.Round(size*1.5)+20)
	wrapLimit := inf
	if tight {
		maxW := 320.0
		if options.MaxWidth != nil {
			maxW = *options.MaxWidth
		}
		wrapLimit = math.Max(32, (maxW-float64(2*pad))/math.Max(1, float64(db.Actors.Len()))-actorMargin-24)
	}
	wrapW := math.Min(option(conf, "width", 160), wrapLimit)
	// Mermaid's default of 35 corresponds to Pele's default gap. Other values move it by the difference.
	msgGap := math.Max(4, 14+option(conf, "messageMargin", 35)-35)
	notePad := math.Min(option(conf, "noteMargin", 10), tightOr(6))
	boxPad := math.Min(option(conf, "boxMargin", 10), tightOr(6))
	align := 0.0
	switch conf["messageAlign"] {
	case "left":
		align = -1
	case "right":
		align = 1
	}

	msgPad, noteOver, framePad, frameNest := float64(MSG_PAD), float64(NOTE_OVER), float64(FRAME_PAD), float64(FRAME_NEST)
	if tight {
		msgPad, noteOver, framePad, frameNest = 6, 8, 12, 6
	}
	messages := db.Messages
	count := len(messages)
	showNumbers, _ := conf["showSequenceNumbers"].(bool)
	numbering := numberMessages(messages, showNumbers)
	textBy := func(raw string, fontSize float64, wrap bool, width float64, by text.TextMeasurer) text.Label {
		max := inf
		if tight || wrap {
			max = width
		}
		label := text.LayoutLabel(raw, false, by, fontSize, max, 0)
		if tight {
			return common.BreakLabel(label, by, math.Max(120, width))
		}
		return label
	}
	textLabel := func(raw string, fontSize float64, wrap bool, width float64) text.Label {
		return textBy(raw, fontSize, wrap, width, measurer)
	}
	messageText := func(raw string, wrap bool, width float64) text.Label {
		return textBy(raw, small, wrap, width, messageMeasurer)
	}

	var used map[string]bool
	if hide, _ := conf["hideUnusedParticipants"].(bool); hide {
		used = map[string]bool{}
		for _, m := range messages {
			if m.From != nil {
				used[*m.From] = true
			}
			if m.To != nil {
				used[*m.To] = true
			}
		}
	}

	nextLine := make([]int, count+1)
	nextLine[count] = -1
	for i := count - 1; i >= 0; i-- {
		if isLineMessage(messages[i].Type) {
			nextLine[i] = i
		} else {
			nextLine[i] = nextLine[i+1]
		}
	}

	var cols []*column
	colOf := map[string]int{}
	for _, id := range db.Actors.keys {
		actor := db.Actors.m[id]
		if used != nil && !used[id] {
			continue
		}
		kind := actorKind(actor.Type)
		label := textLabel(actor.Description, size, actor.Wrap, wrapW)
		createAt := -1
		if created, ok := db.CreatedActors[id]; ok {
			createAt = nextLine[min(created, count)]
		}
		if createAt >= 0 && deref(messages[createAt].To) != id {
			createAt = -1
		}
		destroyAt := -1
		if destroyed, ok := db.DestroyedActors[id]; ok {
			destroyAt = nextLine[min(destroyed, count)]
		}
		if destroyAt >= 0 && deref(messages[destroyAt].To) != id && deref(messages[destroyAt].From) != id {
			destroyAt = -1
		}
		colOf[id] = len(cols)
		padX := float64(PAD_X)
		if tight {
			padX = 8
		}
		cols = append(cols, &column{
			id:        id,
			actor:     actor,
			kind:      kind,
			label:     label,
			size:      actorSize(kind, label, minW, minH, padX),
			createAt:  createAt,
			destroyAt: destroyAt,
			lifeEnd:   -1,
		})
	}
	last := len(cols) - 1

	// Widths between lifelines. Every message and note says how far apart the lifelines it touches must be.
	var needKeys []int
	needs := map[int]float64{}
	need := func(lo, hi int, width float64) {
		if lo < 0 || hi > last {
			return
		}
		key := lo*len(cols) + hi
		if _, ok := needs[key]; !ok {
			needKeys = append(needKeys, key)
		}
		if needs[key] < width {
			needs[key] = width
		}
	}
	badgeWidth := func(i int) float64 {
		if numbering.shown[i] {
			return math.Max(BADGE_H, measurer.Width(js.Num(numbering.index[i]), tiny, 0)+8)
		}
		return 0
	}

	views := make([]*view, count)
	sized := make([]map[float64]text.Label, count)
	sizedText := func(i int, room float64) text.Label {
		known := sized[i]
		if known == nil {
			if room == wrapW {
				return views[i].label
			}
			known = map[float64]text.Label{wrapW: views[i].label}
			sized[i] = known
		}
		label, ok := known[room]
		if !ok {
			label = messageText(messages[i].Message, true, room)
			known[room] = label
		}
		return label
	}
	for i, m := range messages {
		if m.From == nil || m.To == nil || m.Numbering != nil {
			continue
		}
		a, okA := colOf[*m.From]
		b, okB := colOf[*m.To]
		if !okA || !okB {
			continue
		}
		if m.Type == LINETYPE_NOTE {
			label := textLabel(m.Message, small, m.Wrap, wrapW)
			w := label.Width + float64(2*notePad)
			if m.Placement == PLACEMENT_LEFTOF {
				need(a-1, a, w+2*NOTE_GAP+ACT_W)
			} else if m.Placement == PLACEMENT_RIGHTOF {
				need(a, a+1, w+2*NOTE_GAP+ACT_W)
			} else if a == b {
				need(a-1, a, w/2+NOTE_GAP)
				need(a, a+1, w/2+NOTE_GAP)
			} else {
				need(min(a, b), max(a, b), w-float64(2*noteOver))
			}
			views[i] = &view{kind: V_Note, a: a, b: b, label: label, w: w}
		} else if isLineMessage(m.Type) {
			label := messageText(m.Message, m.Wrap, wrapW)
			badge := badgeWidth(i)
			if a == b {
				need(a, a+1, SELF_W+8+label.Width+16+ACT_W+badge)
				views[i] = &view{kind: V_Self, a: a, b: b, label: label}
			} else {
				created := 0.0
				if cols[b].createAt == i {
					created = cols[b].size.W / 2
				}
				central := 0.0
				if m.CentralConnection != 0 {
					central = 4 * CENTRAL_R
				}
				lw := label.Width
				if tight {
					lw = math.Min(label.Width, math.Max(16, wrapW-16))
				}
				need(min(a, b), max(a, b), lw+float64(2*msgPad)+ACT_W+badge+created+central)
				views[i] = &view{kind: V_Line, a: a, b: b, label: label}
			}
		}
	}

	var runs []*boxRun
	for i := 0; i <= last; i++ {
		box := cols[i].actor.Box
		var run *boxRun
		if len(runs) > 0 {
			run = runs[len(runs)-1]
		}
		if box != nil && run != nil && run.box == box && run.last == i-1 {
			run.last = i
		} else if box != nil {
			runs = append(runs, &boxRun{box: box, first: i, last: i, title: textLabel(deref(box.Name), small, box.Wrap, wrapW)})
		}
	}

	gaps := make([]float64, max(last, 0))
	for i := 0; i < last; i++ {
		left := cols[i]
		right := cols[i+1]
		boxed := 0.0
		if left.actor.Box != right.actor.Box {
			if left.actor.Box != nil {
				boxed += boxPad
			}
			if right.actor.Box != nil {
				boxed += boxPad
			}
		}
		gaps[i] = math.Max(left.size.W/2+right.size.W/2+actorMargin+boxed, needs[i*len(cols)+i+1])
	}
	// A message across several lifelines widens each gap it crosses by an equal share of what is missing.
	var spans [][3]float64
	for _, key := range needKeys {
		lo := key / len(cols)
		hi := key % len(cols)
		if hi-lo > 1 {
			spans = append(spans, [3]float64{float64(lo), float64(hi), needs[key]})
		}
	}
	slices.SortStableFunc(spans, func(p, q [3]float64) int {
		d := (p[1] - p[0]) - (q[1] - q[0])
		if d == 0 {
			d = p[0] - q[0]
		}
		switch {
		case d < 0:
			return -1
		case d > 0:
			return 1
		}
		return 0
	})
	for _, s := range spans {
		lo, hi, width := int(s[0]), int(s[1]), s[2]
		total := 0.0
		for i := lo; i < hi; i++ {
			total += gaps[i]
		}
		if total >= width {
			continue
		}
		share := (width - total) / float64(hi-lo)
		for i := lo; i < hi; i++ {
			gaps[i] += share
		}
	}
	place := func() {
		for i := 1; i <= last; i++ {
			cols[i].x = cols[i-1].x + gaps[i-1]
		}
	}
	place()
	for _, run := range runs {
		inner := cols[run.last].x + cols[run.last].size.W/2 - (cols[run.first].x - cols[run.first].size.W/2)
		run.grow = math.Max(0, (run.title.Width-inner)/2)
		if run.grow == 0 {
			continue
		}
		if run.first > 0 {
			gaps[run.first-1] += run.grow
		}
		if run.last < last {
			gaps[run.last] += run.grow
		}
	}

	minX := inf
	maxX := math.Inf(-1)
	extend := func(x0, x1 float64) {
		if x0 < minX {
			minX = x0
		}
		if x1 > maxX {
			maxX = x1
		}
	}
	boxEdges := func(run *boxRun) (float64, float64) {
		return cols[run.first].x - cols[run.first].size.W/2 - boxPad - run.grow, cols[run.last].x + cols[run.last].size.W/2 + boxPad + run.grow
	}
	frames := make([]*seqFrame, count)
	layout := func() {
		place()
		minX = inf
		maxX = math.Inf(-1)
		for _, col := range cols {
			extend(col.x-col.size.W/2, col.x+col.size.W/2)
		}
		for _, run := range runs {
			extend(boxEdges(run))
		}

		// Horizontal extents of every block, from what it holds.
		var open []*seqFrame
		spanAll := func() (float64, float64) {
			if last < 0 {
				return 0, 120
			}
			return cols[0].x - framePad, cols[last].x + framePad
		}
		for i, m := range messages {
			typ := m.Type
			frameKind, isFrame := FRAME_KIND[typ]
			v := views[i]
			if isFrame {
				fr := &seqFrame{kind: frameKind, x0: inf, x1: math.Inf(-1), title: NO_LABEL, dividers: []int{i}}
				if frameKind == "rect" && m.Numbering == nil {
					fr.fill = m.Message
				}
				frames[i] = fr
				open = append(open, fr)
			} else if typ == LINETYPE_ALT_ELSE || typ == LINETYPE_PAR_AND || typ == LINETYPE_CRITICAL_OPTION {
				if len(open) > 0 {
					top := open[len(open)-1]
					top.dividers = append(top.dividers, i)
				}
			} else if typ == LINETYPE_LOOP_END || typ == LINETYPE_ALT_END || typ == LINETYPE_OPT_END || typ == LINETYPE_PAR_END ||
				typ == LINETYPE_CRITICAL_END || typ == LINETYPE_BREAK_END || typ == LINETYPE_RECT_END {
				if len(open) == 0 {
					continue
				}
				fr := open[len(open)-1]
				open = open[:len(open)-1]
				frames[i] = fr
				if fr.x0 > fr.x1 {
					fr.x0, fr.x1 = spanAll()
				}
				if fr.kind != "rect" {
					fr.tabW = measurer.Width(fr.kind, tiny, text.Bold) + 14
					// A block grows to the right to give its texts up to FRAME_TEXT of width before they wrap.
					for k, d := range fr.dividers {
						raw := messages[d].Message
						tab := 0.0
						if k == 0 {
							tab = fr.tabW + 8
						}
						room := fr.x1 - fr.x0 - 16 - tab
						label := NO_LABEL
						if raw != "" {
							limit := float64(FRAME_TEXT)
							if tight {
								limit = math.Min(FRAME_TEXT, wrapW)
							}
							label = textLabel("["+raw+"]", small, true, math.Max(room, limit))
						}
						if label.Width > room {
							fr.x1 += label.Width - room
						}
						if k == 0 {
							fr.title = label
						} else {
							fr.dividerLabels = append(fr.dividerLabels, label)
						}
					}
				}
				extend(fr.x0, fr.x1)
				if len(open) > 0 {
					parent := open[len(open)-1]
					parent.x0 = math.Min(parent.x0, fr.x0-frameNest)
					parent.x1 = math.Max(parent.x1, fr.x1+frameNest)
				}
			} else if v != nil {
				a := cols[v.a]
				b := cols[v.b]
				var x0, x1 float64
				if v.kind == V_Note {
					if m.Placement == PLACEMENT_LEFTOF {
						v.x = a.x - NOTE_GAP - v.w
					} else if m.Placement == PLACEMENT_RIGHTOF {
						v.x = a.x + NOTE_GAP
					} else if v.a == v.b {
						v.x = a.x - v.w/2
					} else {
						lo := math.Min(a.x, b.x) - noteOver
						hi := math.Max(a.x, b.x) + noteOver
						if tight || m.Wrap {
							v.label = textLabel(m.Message, small, true, math.Max(wrapW, hi-lo-float64(2*notePad)))
						}
						v.w = math.Max(hi-lo, v.label.Width+float64(2*notePad))
						v.x = (lo + hi - v.w) / 2
					}
					x0 = v.x
					x1 = v.x + v.w
				} else if v.kind == V_Self {
					x0 = a.x
					x1 = a.x + ACT_W + SELF_W + 8 + v.label.Width + badgeWidth(i)
				} else {
					x0 = math.Min(a.x, b.x)
					x1 = math.Max(a.x, b.x)
					if tight || m.Wrap {
						v.label = sizedText(i, math.Max(wrapW, x1-x0-float64(2*msgPad)-ACT_W))
					}
					if b.createAt == i {
						if b.x > a.x {
							x1 += b.size.W / 2
						} else {
							x0 -= b.size.W / 2
						}
					}
				}
				extend(x0, x1)
				if len(open) > 0 {
					fr := open[len(open)-1]
					fr.x0 = math.Min(fr.x0, x0-framePad)
					fr.x1 = math.Max(fr.x1, x1+framePad)
				}
			}
		}
	}
	layout()

	// Widen gaps only if it reduces message wrapping.
	grown := false
	uneven := false
	if tight && options.MaxWidth != nil {
		spare := math.Floor(*options.MaxWidth) - math.Ceil(maxX-minX+float64(2*pad)) - hold
		stretch := make([]int32, len(cols))
		want := make([]float64, len(gaps))
		var wrapped []int
		lines := 0
		for i := 0; spare > 0 && i < len(views); i++ {
			v := views[i]
			if v == nil || v.kind != V_Line {
				continue
			}
			natural := messageText(messages[i].Message, false, inf)
			if len(v.label.Lines) <= len(natural.Lines) {
				continue
			}
			wrapped = append(wrapped, i)
			lines += len(v.label.Lines)
			lo := min(v.a, v.b)
			hi := max(v.a, v.b)
			stretch[lo]++
			stretch[hi]--
			short := (natural.Width - (cols[hi].x - cols[lo].x - float64(2*msgPad) - ACT_W)) / float64(hi-lo)
			for g := lo; g < hi; g++ {
				want[g] = math.Max(want[g], short)
			}
		}
		var active int32
		crossed := 0.0
		for i := range gaps {
			active += stretch[i]
			if active > 0 {
				stretch[i] = 1
			} else {
				stretch[i] = 0
			}
			crossed += float64(stretch[i])
		}
		if crossed > 0 {
			before := slices.Clone(gaps)
			spread := func(share func(g int) float64) []int {
				for g := range gaps {
					gaps[g] = before[g] + share(g)
				}
				layout()
				counts := make([]int, len(wrapped))
				for k, i := range wrapped {
					counts[k] = len(views[i].label.Lines)
				}
				return counts
			}
			sum := func(counts []int) int {
				t := 0
				for _, n := range counts {
					t += n
				}
				return t
			}
			even := func(g int) float64 { return float64(float64(stretch[g])*spare) / crossed }
			counts := spread(even)
			missing := 0.0
			for _, w := range want {
				missing += w
			}
			// Allow for rounding error.
			needsShare := func(g int) float64 { return float64(want[g]*(spare-0.01)) / missing }
			// Reducing wrapping requires at least one gap wider than the even split.
			some := false
			for g := range gaps {
				if needsShare(g) > even(g) {
					some = true
					break
				}
			}
			if byNeed && crossed > 1 && missing > 0 && some {
				first := counts
				counts = spread(needsShare)
				uneven = sum(counts) < sum(first)
				for k, n := range counts {
					if n > first[k] {
						uneven = false
					}
				}
				if !uneven {
					counts = spread(even)
				}
			}
			grown = sum(counts) < lines
			if !grown {
				copy(gaps, before)
				layout()
			}
		}
	}

	// Vertical placement, top to bottom, drawing as it goes.
	boxTitleH := 0.0
	for _, run := range runs {
		boxTitleH = math.Max(boxTitleH, run.title.Height)
	}
	boxTop := 0.0
	if len(runs) > 0 {
		boxTop = boxPad
		if boxTitleH > 0 {
			boxTop += boxTitleH + 4
		}
	}
	rowH := 0.0
	for _, col := range cols {
		if col.createAt < 0 {
			rowH = math.Max(rowH, col.size.H)
		}
	}
	headBottom := boxTop + rowH
	for _, col := range cols {
		col.top = headBottom - col.size.H
		col.lifeStart = headBottom
	}

	xs := make([]float64, len(cols))
	for i, col := range cols {
		xs[i] = col.x
	}
	// crosses is whether a lifeline runs between two x positions, where text would be struck through.
	crosses := func(x0, x1 float64) bool {
		lo, hi := 0, len(xs)
		for lo < hi {
			mid := (lo + hi) >> 1
			if xs[mid] <= x0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo < len(xs) && xs[lo] < x1
	}
	var stack []*seqFrame
	inks := map[string]string{}
	// inkOf is "" for TS undefined.
	inkOf := func(fill string) string {
		if fill == "" {
			return ""
		}
		color, ok := inks[fill]
		if !ok {
			color = svg.Ink(svg.ResolveStyle([]string{"fill:" + fill}).Shape, "")
			inks[fill] = color
		}
		return color
	}
	inkOn := func(fill string) string {
		color := inkOf(fill)
		if color != "" {
			return ` fill="` + color + `"`
		}
		return ""
	}
	// Prefer the innermost rect's fill, then a containing box's.
	fillUnder := func(l, r float64) string {
		for k := len(stack) - 1; k >= 0; k-- {
			if stack[k].kind == "rect" {
				return stack[k].fill
			}
		}
		for _, run := range runs {
			x0, x1 := boxEdges(run)
			if l >= x0 && r <= x1 {
				return run.box.Fill
			}
		}
		return ""
	}
	under := func(l, r float64) string { return inkOn(fillUnder(l, r)) }
	backed := func(label text.Label, cx, cy float64, frame bool) string {
		if label.Width == 0 {
			return ""
		}
		l := cx - label.Width/2
		plate := crosses(l, l+label.Width)
		back := ""
		if plate {
			back = `<rect x="` + svg.Num(l-3) + `" y="` + svg.Num(cy-label.Height/2+2) + `" width="` + svg.Num(label.Width+6) + `" height="` + svg.Num(label.Height-4) + `" rx="3" fill="var(--_bg)" stroke="none"/>`
		}
		fill := ""
		if !plate {
			fill = under(l, l+label.Width)
		}
		attrs := fill
		if frame {
			if fill == "" {
				fill = ` fill="var(--_m)"`
			}
			attrs = ` class="pele-frame-label"` + fill + ` stroke="none"`
		}
		return back + svg.LabelSvg(label, cx, cy, attrs, icons)
	}
	leftEdge := func(col *column, extra int) float64 {
		if len(col.activations)+extra > 0 {
			return col.x - ACT_W/2
		}
		return col.x
	}
	rightEdge := func(col *column, extra int) float64 {
		depth := len(col.activations) + extra
		if depth > 0 {
			return col.x + ACT_W/2 + float64(float64(depth-1)*ACT_STEP)
		}
		return col.x
	}

	// Shaded blocks and activations close from the inside out. They are written in reverse, so that
	// the inner ones end up on top.
	var rectsOut, activationsOut []string
	var framesOut, edgesOut, labelsOut, notesOut, actorsOut, crossesOut strings.Builder
	// A message takes about 300 bytes of markup.
	edgesOut.Grow(len(views) * 304)
	endActivation := func(col *column, end float64) float64 {
		start := col.activations[len(col.activations)-1]
		col.activations = col.activations[:len(col.activations)-1]
		bottom := math.Max(end, start+ACT_MIN)
		x := col.x - ACT_W/2 + float64(float64(len(col.activations))*ACT_STEP)
		extend(x, x+ACT_W)
		activationsOut = append(activationsOut, `<rect class="pele-activation" data-id="`+svg.EscText(col.id)+`" x="`+svg.Num(x)+`" y="`+svg.Num(start)+
			`" width="10" height="`+svg.Num(bottom-start)+`" rx="2"/>`)
		return bottom
	}
	// destroy: the lifeline of a destroyed participant ends in a cross a little below the message that ends it.
	destroy := func(col *column, lineY float64) float64 {
		cx := col.x
		cy := lineY + 16
		col.lifeEnd = cy
		crossesOut.WriteString(`<path class="pele-destroy" data-id="` + svg.EscText(col.id) + `" d="M` + svg.Num(cx-6) + `,` + svg.Num(cy-6) + `L` + svg.Num(cx+6) + `,` + svg.Num(cy+6) +
			`M` + svg.Num(cx+6) + `,` + svg.Num(cy-6) + `L` + svg.Num(cx-6) + `,` + svg.Num(cy+6) + `"/>`)
		return cy + 6
	}
	actorSvg := func(col *column, top float64, footer bool) string {
		cls := ""
		if c, ok := col.actor.Properties["class"].(string); ok {
			cls = svg.ClassNames(c)
		}
		figureInk := ""
		if isFigure(col.kind) && col.actor.Box != nil {
			figureInk = inkOn(col.actor.Box.Fill)
		}
		f := ""
		if footer {
			f = " pele-actor-footer"
		}
		return `<g class="pele-node pele-actor pele-actor-` + col.kind + f + cls + `" data-id="` + svg.EscText(col.id) + `">` +
			drawActor(col.kind, col.x, top, col.size, col.label, icons, figureInk) + `</g>`
	}
	badge := func(i int, cx, cy, w float64) string {
		return `<g class="pele-sequence-number"><rect x="` + svg.Num(cx-w/2) + `" y="` + svg.Num(cy-BADGE_H/2) + `" width="` + svg.Num(w) + `" height="16" rx="8" fill="var(--_l)" stroke="none"/><text x="` +
			svg.Num(cx) + `" y="` + svg.Num(cy+float64(tiny*0.35)) + `" text-anchor="middle" font-size="` + js.Num(tiny) + `" fill="var(--_bg)" stroke="none">` + svg.Esc(js.Num(numbering.index[i])) + `</text></g>`
	}

	geo := options.Geometry != nil
	var gMessages []types.GeoMessage
	var gNotes []types.GeoBox
	var gBlocks []types.GeoBlock
	headOf := func(h int, at bool) bool { return at && h != Head_None }

	y := headBottom + 6
	for i, m := range messages {
		typ := m.Type
		v := views[i]
		fr := frames[i]

		if typ == LINETYPE_ACTIVE_START {
			if m.From != nil {
				if c, ok := colOf[*m.From]; ok {
					cols[c].activations = append(cols[c].activations, y)
				}
			}
		} else if typ == LINETYPE_ACTIVE_END {
			if m.From != nil {
				if c, ok := colOf[*m.From]; ok && len(cols[c].activations) > 0 {
					y = math.Max(y, endActivation(cols[c], y))
				}
			}
		} else if fr != nil && fr.dividers[0] == i {
			stack = append(stack, fr)
			if fr.kind == "rect" {
				fr.top = y + 8
				y = fr.top
			} else {
				fr.top = y + 12
				y = fr.top + math.Max(TAB_H, fr.title.Height+2)
			}
		} else if fr != nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			bottom := y + 12
			x := svg.Num(fr.x0)
			w := svg.Num(fr.x1 - fr.x0)
			if fr.kind == "rect" {
				fill := ""
				if fr.fill != "" {
					fill = svg.ResolveStyle([]string{"fill:" + fr.fill}).Shape
				}
				opacity := ""
				if fill == "" {
					opacity = ` fill-opacity="0.5"`
				}
				rectsOut = append(rectsOut, `<rect class="pele-rect" x="`+x+`" y="`+svg.Num(fr.top)+`" width="`+w+`" height="`+svg.Num(bottom-fr.top)+`"`+svg.Rounded(opacity+fill)+`/>`)
			} else {
				inner := `<rect x="` + x + `" y="` + svg.Num(fr.top) + `" width="` + w + `" height="` + svg.Num(bottom-fr.top) + `" fill="none"` + svg.ROUNDED + `/>`
				for k, dy := range fr.dividerY {
					label := fr.dividerLabels[k]
					inner += `<path d="M` + x + `,` + svg.Num(dy) + `H` + svg.Num(fr.x1) + `"` + DASH + `/>` +
						backed(label, fr.x0+8+label.Width/2, dy+2+label.Height/2, true)
				}
				inner += `<rect x="` + x + `" y="` + svg.Num(fr.top) + `" width="` + svg.Num(fr.tabW) + `" height="20" fill="var(--_s)"` + svg.ROUNDED + `/>` +
					`<text class="pele-frame-kind" x="` + svg.Num(fr.x0+fr.tabW/2) + `" y="` + svg.Num(fr.top+TAB_H/2+float64(tiny*0.35)) +
					`" text-anchor="middle" font-size="` + js.Num(tiny) + `" font-weight="var(--_hw)" fill="var(--_m)" stroke="none">` + fr.kind + `</text>` +
					backed(fr.title, fr.x0+fr.tabW+8+fr.title.Width/2, fr.top+math.Max(TAB_H, fr.title.Height)/2, true)
				framesOut.WriteString(`<g class="pele-frame pele-frame-` + fr.kind + `">` + inner + `</g>`)
			}
			if geo {
				gb := types.GeoBlock{Kind: fr.kind, Title: fr.title.Plain(), X0: fr.x0, X1: fr.x1, Top: fr.top, Bottom: bottom, Dividers: append([]float64{}, fr.dividerY...)}
				for _, l := range fr.dividerLabels {
					gb.DividerLabels = append(gb.DividerLabels, l.Plain())
				}
				gBlocks = append(gBlocks, gb)
			}
			y = bottom
		} else if typ == LINETYPE_ALT_ELSE || typ == LINETYPE_PAR_AND || typ == LINETYPE_CRITICAL_OPTION {
			if len(stack) > 0 {
				current := stack[len(stack)-1]
				label := current.dividerLabels[len(current.dividerY)]
				current.dividerY = append(current.dividerY, y+12)
				y += 12
				if label.Height > 0 {
					y += label.Height
				}
			}
		} else if v != nil && v.kind == V_Note {
			a := cols[v.a]
			h := v.label.Height + float64(2*math.Max(4, notePad-4))
			top := y + 10
			x := v.x
			if m.Placement == PLACEMENT_RIGHTOF {
				x += rightEdge(a, 0) - a.x
			} else if m.Placement == PLACEMENT_LEFTOF {
				x += leftEdge(a, 0) - a.x
			}
			extend(x, x+v.w)
			notesOut.WriteString(`<g class="pele-note" data-id="i` + svg.EscText(m.ID) + `">` +
				`<rect x="` + svg.Num(x) + `" y="` + svg.Num(top) + `" width="` + svg.Num(v.w) + `" height="` + svg.Num(h) + `"` + svg.ROUNDED + `/>` +
				svg.LabelSvg(v.label, x+v.w/2, top+h/2, ` class="pele-label" stroke="none" fill="var(--_fg)"`, icons) +
				`</g>`)
			if geo {
				gNotes = append(gNotes, types.GeoBox{X: x, Y: top, W: v.w, H: h, Label: v.label.Plain()})
			}
			y = top + h
		} else if v != nil {
			st := STYLES[typ]
			a := cols[v.a]
			b := cols[v.b]
			label := v.label
			bw := badgeWidth(i)
			// The activation a `+` starts belongs to this message, so the arrow stops at its edge.
			starting := 0
			for k := i + 1; k < count; k++ {
				t := messages[k].Type
				if t == LINETYPE_ACTIVE_START {
					if deref(messages[k].From) == deref(m.To) && messages[k].From != nil && m.To != nil {
						starting++
					}
				} else if t != LINETYPE_ACTIVE_END && t != LINETYPE_CENTRAL_CONNECTION && t != LINETYPE_CENTRAL_CONNECTION_REVERSE {
					break
				}
			}
			up := -1.0
			if st.bottom {
				up = 1
			}
			attrs := ""
			if st.dotted {
				attrs = DASH
			}
			cc := m.CentralConnection
			circleTo := cc == LINETYPE_CENTRAL_CONNECTION || cc == LINETYPE_CENTRAL_CONNECTION_DUAL
			circleFrom := cc == LINETYPE_CENTRAL_CONNECTION_REVERSE || cc == LINETYPE_CENTRAL_CONNECTION_DUAL
			line := ""
			color := "var(--_l)"
			circle := func(cx, cy float64) string {
				return `<circle class="pele-central" cx="` + svg.Num(cx) + `" cy="` + svg.Num(cy) + `" r="4" fill="` + color + `" stroke="none"/>`
			}
			tailAtEnd := st.atStart && !st.atEnd
			headTrim := trim(st.head)
			inner := ""
			var lineY float64

			if v.kind == V_Self {
				top := y + msgGap + math.Max(0, (label.Height-SELF_H)/2)
				bottom := top + SELF_H
				x0 := rightEdge(a, 0)
				if circleFrom {
					x0 = a.x + CENTRAL_R
				}
				x1 := rightEdge(a, starting)
				if circleTo {
					x1 = a.x + CENTRAL_R
				}
				out := math.Max(x0, x1) + SELF_W + bw/2
				line = inkOf(fillUnder(a.x, out))
				if line != "" {
					color = line
				}
				if circleFrom {
					inner += circle(a.x, top)
				}
				if circleTo {
					inner += circle(a.x, bottom)
				}
				if bw > 0 {
					if tailAtEnd {
						inner += badge(i, x1, bottom, bw)
						x1 += bw / 2
					} else {
						inner += badge(i, x0, top, bw)
						x0 += bw / 2
					}
				}
				// The loop leaves to the right and comes back heading left, which turns a half head over.
				heads := ""
				if st.atStart {
					heads += head(st.head, x0, top, -1, up, color)
				}
				if st.atEnd {
					heads += head(st.head, x1, bottom, -1, -up, color)
				}
				fromX := x0
				if st.atStart {
					fromX += headTrim
				}
				toX := x1
				if st.atEnd {
					toX += headTrim
				}
				from := svg.Num(fromX)
				to := svg.Num(toX)
				var d string
				if rightAngles {
					d = `M` + from + `,` + svg.Num(top) + `H` + svg.Num(out) + `V` + svg.Num(bottom) + `H` + to
				} else {
					d = `M` + from + `,` + svg.Num(top) + `H` + svg.Num(out-6) + `A6,6 0 0 1 ` + svg.Num(out) + `,` + svg.Num(top+6) + `V` + svg.Num(bottom-6) + `A6,6 0 0 1 ` + svg.Num(out-6) + `,` + svg.Num(bottom) + `H` + to
				}
				inner = `<path d="` + d + `"` + attrs + `/>` + heads + inner
				extend(a.x, out+8+label.Width)
				if label.Width > 0 {
					svg.WriteStrings(&labelsOut, `<g class="pele-edge-label" data-id="i`, svg.EscText(m.ID), `">`, svg.LabelSvg(label, out+8+label.Width/2, (top+bottom)/2, under(out+8, out+8+label.Width), icons), `</g>`)
				}
				if geo {
					gMessages = append(gMessages, types.GeoMessage{From: v.a, To: v.b, Y: top, Y2: bottom, X0: x0, X1: x1, Self: true, Dotted: st.dotted,
						HeadStart: headOf(st.head, st.atStart), HeadEnd: headOf(st.head, st.atEnd), Label: label.Plain(), LabelX: out + 8 + label.Width/2, LabelY: (top + bottom) / 2})
				}
				// A label taller than the loop reaches below it, so the next row starts under the label.
				lineY = math.Max(bottom, (top+bottom)/2+label.Height/2)
			} else {

				dir := -1.0
				if b.x > a.x {
					dir = 1
				}
				creating := b.createAt == i
				line = inkOf(fillUnder(math.Min(a.x, b.x), math.Max(a.x, b.x)))
				if line != "" {
					color = line
				}
				lift := 6.0
				if label.Height > 0 {
					lift = label.Height + 2
				}
				if creating {
					lift = math.Max(lift, b.size.H/2-msgGap+6)
				} else {
					lift = math.Max(lift, 0)
				}
				lineY = y + msgGap + lift
				var x0, x1 float64
				if dir > 0 {
					x0 = rightEdge(a, 0)
					x1 = leftEdge(b, starting)
				} else {
					x0 = leftEdge(a, 0)
					x1 = rightEdge(b, starting)
				}
				if creating {
					b.top = lineY - b.size.H/2
					b.lifeStart = b.top + b.size.H
					if isFigure(b.kind) {
						x1 = b.x - float64(dir*18)
					} else {
						x1 = b.x - float64(dir*(b.size.W/2+1))
					}
				}
				if circleFrom {
					inner += circle(a.x, lineY)
					x0 = a.x + float64(dir*CENTRAL_R)
				}
				if circleTo {
					inner += circle(b.x, lineY)
					x1 = b.x - float64(dir*CENTRAL_R)
				}
				if bw > 0 {
					// The number sits on the lifeline at the tail of the arrow, past a connection circle if there is one.
					if tailAtEnd {
						cx := x1
						if circleTo {
							cx = x1 - float64(dir*(bw/2+2))
						}
						inner += badge(i, cx, lineY, bw)
						x1 = cx - (dir*bw)/2
					} else {
						cx := x0
						if circleFrom {
							cx = x0 + float64(dir*(bw/2+2))
						}
						inner += badge(i, cx, lineY, bw)
						x0 = cx + (dir*bw)/2
					}
				}
				heads := ""
				if st.atStart {
					heads += head(st.head, x0, lineY, -dir, up*dir, color)
				}
				if st.atEnd {
					heads += head(st.head, x1, lineY, dir, up*dir, color)
				}
				from := x0
				if st.atStart {
					from += float64(dir * headTrim)
				}
				to := x1
				if st.atEnd {
					to -= float64(dir * headTrim)
				}
				inner = `<path d="M` + svg.Num(from) + `,` + svg.Num(lineY) + `H` + svg.Num(to) + `"` + attrs + `/>` + heads + inner
				if label.Width > 0 {
					l := math.Min(x0, x1)
					r := math.Max(x0, x1)
					cx := (l + r) / 2
					if align < 0 {
						cx = l + msgPad + label.Width/2
					} else if align > 0 {
						cx = r - msgPad - label.Width/2
					}
					extend(cx-label.Width/2, cx+label.Width/2)
					svg.WriteStrings(&labelsOut, `<g class="pele-edge-label" data-id="i`, svg.EscText(m.ID), `">`, backed(label, cx, lineY-4-label.Height/2, false), `</g>`)
				}
				if geo {
					gm := types.GeoMessage{From: v.a, To: v.b, Y: lineY, X0: x0, X1: x1, Dotted: st.dotted,
						HeadStart: headOf(st.head, st.atStart), HeadEnd: headOf(st.head, st.atEnd), Label: label.Plain()}
					if label.Width > 0 {
						l, r := math.Min(x0, x1), math.Max(x0, x1)
						gm.LabelX = (l + r) / 2
						if align < 0 {
							gm.LabelX = l + msgPad + label.Width/2
						} else if align > 0 {
							gm.LabelX = r - msgPad - label.Width/2
						}
						gm.LabelY = lineY - 4 - label.Height/2
					}
					gMessages = append(gMessages, gm)
				}
				if creating {
					y = lineY + b.size.H/2 - 6
				}
			}
			stroke := ""
			if line != "" {
				stroke = ` stroke="` + line + `"`
			}
			svg.WriteStrings(&edgesOut, `<g class="pele-edge pele-message" data-id="i`, svg.EscText(m.ID), `"`, stroke, `>`, inner, `</g>`)
			y = math.Max(y, lineY)
			if a.destroyAt == i {
				y = math.Max(y, destroy(a, lineY))
			}
			if b.destroyAt == i && b != a {
				y = math.Max(y, destroy(b, lineY))
			}
		}
	}

	y += 20
	for _, col := range cols {
		for len(col.activations) > 0 {
			endActivation(col, y)
		}
	}
	bottom := y
	if mirror {
		footH := 0.0
		for _, col := range cols {
			if col.lifeEnd >= 0 {
				continue
			}
			footH = math.Max(footH, col.size.H)
			actorsOut.WriteString(actorSvg(col, y, true))
		}
		bottom = y + footH
	}
	var lifelines strings.Builder
	for _, col := range cols {
		actorsOut.WriteString(actorSvg(col, col.top, false))
		end := y
		if col.lifeEnd >= 0 {
			end = col.lifeEnd
		}
		if end > col.lifeStart {
			lifelines.WriteString(`<path class="pele-lifeline" data-id="` + svg.EscText(col.id) + `" d="M` + svg.Num(col.x) + `,` + svg.Num(col.lifeStart) + `V` + svg.Num(end) + `"/>`)
		}
	}

	var boxesOut strings.Builder
	for _, run := range runs {
		x0, x1 := boxEdges(run)
		fill := ""
		if run.box.Fill != "transparent" {
			fill = svg.ResolveStyle([]string{"fill:" + run.box.Fill}).Shape
		}
		dataID := ""
		if run.box.Name != nil && *run.box.Name != "" {
			dataID = ` data-id="` + svg.EscText(*run.box.Name) + `"`
		}
		opacity := ""
		if fill == "" {
			opacity = ` fill-opacity="0.5"`
		}
		boxInk := inkOn(run.box.Fill)
		if boxInk == "" {
			boxInk = ` fill="var(--_m)"`
		}
		boxesOut.WriteString(`<g class="pele-cluster pele-box"` + dataID + `>` +
			`<rect x="` + svg.Num(x0) + `" y="0" width="` + svg.Num(x1-x0) + `" height="` + svg.Num(bottom+boxPad) + `" fill="var(--_a)" stroke="var(--_b)"` + svg.Rounded(opacity+fill) + `/>` +

			svg.LabelSvg(run.title, (x0+x1)/2, boxPad/2+2+run.title.Height/2, ` class="pele-cluster-label"`+boxInk, icons) +
			`</g>`)
	}
	if len(runs) > 0 {
		bottom += boxPad
	}

	minY := 0.0
	titleOut := ""
	room := common.TitleRoom(options)
	titleMax := math.Inf(1)
	if tight {
		titleMax = room
	}
	plainTitle := svg.TitleLabel(db.Title, measurer, size, titleMax)
	title := plainTitle
	if tight {
		title = common.BreakLabel(plainTitle, measurer, math.Max(120, room))
	}
	if minX > maxX {
		extend(0, math.Max(title.Width, 40))
	}
	if title.Width > 0 {
		cx := (minX + maxX) / 2
		extend(cx-title.Width/2, cx+title.Width/2)
		minY = -title.Height - 12
		titleOut = svg.LabelSvg(title, cx, minY+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, icons)
	}

	width := math.Ceil(maxX - minX + float64(2*pad))
	// Retry with equal shares or room for activation notes and rounding.
	if grown && width > *options.MaxWidth {
		next := hold
		if !uneven {
			if hold > 0 {
				next = inf
			} else {
				next = width - math.Floor(*options.MaxWidth)
			}
		}
		return draw(db, config, options, tight, next, false)
	}
	height := math.Ceil(bottom - minY + float64(2*pad))
	smallStr := js.Num(small)
	content := make([]string, 0, 32)
	content = append(content, `<g transform="translate(`+svg.Num(pad-minX)+`,`+svg.Num(pad-minY)+`)">`, titleOut)
	if boxesOut.Len() > 0 {
		content = append(content, `<g class="pele-boxes" font-size="`+smallStr+`">`, boxesOut.String(), `</g>`)
	}
	if len(rectsOut) > 0 {
		slices.Reverse(rectsOut)
		content = append(content, `<g class="pele-rects" fill="var(--_a)">`)
		content = append(content, rectsOut...)
		content = append(content, `</g>`)
	}
	if lifelines.Len() > 0 {
		content = append(content, `<g class="pele-lifelines" fill="none" stroke="var(--_b)">`, lifelines.String(), `</g>`)
	}
	if len(activationsOut) > 0 {
		slices.Reverse(activationsOut)
		content = append(content, `<g class="pele-activations" fill="var(--_s)" stroke="var(--_b)">`)
		content = append(content, activationsOut...)
		content = append(content, `</g>`)
	}
	if framesOut.Len() > 0 {
		content = append(content, `<g class="pele-frames" font-size="`+smallStr+`" stroke="var(--_b)">`, framesOut.String(), `</g>`)
	}
	if edgesOut.Len() > 0 {
		content = append(content, `<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round">`, edgesOut.String(), `</g>`)
	}
	if crossesOut.Len() > 0 {
		content = append(content, `<g class="pele-destroys" fill="none" stroke="var(--_l)" stroke-width="1.5" stroke-linecap="round">`, crossesOut.String(), `</g>`)
	}
	if labelsOut.Len() > 0 {
		family := ""
		if monospaced {
			family = ` font-family="` + svg.FONT_MONO + `"`
		}
		content = append(content, `<g class="pele-edge-labels" font-size="`+smallStr+`"`+family+`>`, labelsOut.String(), `</g>`)
	}
	if notesOut.Len() > 0 {
		content = append(content, `<g class="pele-notes" font-size="`+smallStr+`" fill="var(--_a)" stroke="var(--_b)">`, notesOut.String(), `</g>`)
	}
	if actorsOut.Len() > 0 {
		content = append(content, `<g class="pele-nodes">`, actorsOut.String(), `</g>`)
	}
	content = append(content, `</g>`)
	out := svg.SvgDocument("sequence", width, height, size, options, svg.Accessible{AccTitle: db.AccTitle, AccDescr: db.AccDescr}, content...)
	rendered := types.Rendered{SVG: out, Width: width, Height: height, Links: []types.LinkInfo{}}
	if geo {
		ox, oy := pad-minX, pad-minY
		g := &types.Geometry{Width: width, Height: height}
		for _, col := range cols {
			end := y
			if col.lifeEnd >= 0 {
				end = col.lifeEnd
			}
			foot := -1.0
			if mirror && col.lifeEnd < 0 {
				foot = y + oy
			}
			g.Actors = append(g.Actors, types.GeoActor{ID: col.id, Kind: col.kind, Label: col.label.Plain(), X: col.x + ox, Top: col.top + oy, W: col.size.W, H: col.size.H,
				Foot: foot, LifeStart: col.lifeStart + oy, LifeEnd: end + oy})
		}
		for _, m := range gMessages {
			m.X0, m.X1, m.LabelX = m.X0+ox, m.X1+ox, m.LabelX+ox
			m.Y, m.Y2, m.LabelY = m.Y+oy, m.Y2+oy, m.LabelY+oy
			g.Messages = append(g.Messages, m)
		}
		for _, n := range gNotes {
			n.X, n.Y = n.X+ox, n.Y+oy
			g.Notes = append(g.Notes, n)
		}
		for _, b := range gBlocks {
			b.X0, b.X1, b.Top, b.Bottom = b.X0+ox, b.X1+ox, b.Top+oy, b.Bottom+oy
			for k := range b.Dividers {
				b.Dividers[k] += oy
			}
			g.Blocks = append(g.Blocks, b)
		}
		rendered.Geometry = g
	}
	return rendered
}
