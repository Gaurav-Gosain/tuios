// Ported from pele src/diagrams/git/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package git

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

const (
	DOT  = 7
	STEP = 44
	LANE = 44
	// CURVE is shorter than a step, so a curve between lanes never runs through a commit on a lane it crosses.
	CURVE     = 36
	GAP       = 8
	LABEL_GAP = 20
	TAIL      = 20
	NOTCH     = 7
	TAG_GAP   = 6
	HALF      = math.Sqrt2 / 2
)

var CLASSES = [...]string{"normal", "reverse", "highlight", "merge", "cherry-pick"}

type View struct {
	commit *GitCommit
	index  int
	lane   int
	m      float64
	label  *text.Label
	tags   []text.Label
}

func flag(config types.Config, key string, fallback bool) bool {
	git, _ := config["gitGraph"].(map[string]any)
	if value, ok := git[key].(bool); ok {
		return value
	}
	return fallback
}

func tagWidth(tag text.Label) float64 {
	return NOTCH + 4 + tag.Width + 6
}

func symbol(b *strings.Builder, typ int, x, y float64, color string) {
	num := svg.Num
	if typ == HIGHLIGHT {
		svg.WriteStrings(b, `<rect x="`, num(x-DOT-1), `" y="`, num(y-DOT-1), `" width="16" height="16" rx="2" fill="`, color, `"/>`)
		return
	}
	at := `cx="` + num(x) + `" cy="` + num(y) + `"`
	if typ == MERGE {
		svg.WriteStrings(b, `<circle `, at, ` r="6" fill="var(--_bg)" stroke="`, color, `" stroke-width="2"/><circle `, at, ` r="2.5" fill="`, color, `"/>`)
		return
	}
	svg.WriteStrings(b, `<circle `, at, ` r="7" fill="`, color, `"/>`)
	if typ == REVERSE {
		svg.WriteStrings(b, `<path d="M`, num(x-3), `,`, num(y-3), `l6,6M`, num(x-3), `,`, num(y+3), `l6,-6" fill="none" stroke="var(--_bg)" stroke-width="1.5" stroke-linecap="round"/>`)
	}
	if typ == CHERRY_PICK {
		svg.WriteStrings(b, `<path d="M`, num(x-2.6), `,`, num(y+1), `L`, num(x+0.8), `,`, num(y-4.2), `L`, num(x+2.6), `,`, num(y+1), `" fill="none" stroke="var(--_bg)" stroke-linejoin="round"/>`,
			`<circle cx="`, num(x-2.6), `" cy="`, num(y+2.2), `" r="1.8" fill="var(--_bg)"/>`,
			`<circle cx="`, num(x+2.6), `" cy="`, num(y+2.2), `" r="1.8" fill="var(--_bg)"/>`)
	}
}

func renderGit(model *GitModel, config types.Config, options types.RenderOptions) types.Rendered {
	if model.direction != "LR" {
		return draw(model, config, options, model.direction)
	}
	return common.TurnToFit(options, func(down bool) types.Rendered {
		if down {
			return draw(model, config, options, "TB")
		}
		return draw(model, config, options, "LR")
	})
}

func draw(model *GitModel, config types.Config, options types.RenderOptions, direction string) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.75)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	showBranches := flag(config, "showBranches", true)
	showCommitLabel := flag(config, "showCommitLabel", true)
	rotate := flag(config, "rotateCommitLabel", true)
	parallel := flag(config, "parallelCommits", false)
	vertical := direction != "LR"
	flip := direction == "BT"

	laneOf := make(map[string]int, len(model.lanes))
	for index, name := range model.lanes {
		laneOf[name] = index
	}
	laneCount := len(model.lanes)
	medium := js.Round(size * 0.875)
	// Mermaid writes branch names, tags, and commit ids as plain text, so tags in them show as written.
	names := make([]text.Label, laneCount)
	for i, name := range model.lanes {
		if !showBranches {
			name = ""
		}
		names[i] = common.PlainLabel(name, measurer, medium, 4000, 0)
	}

	sorted := make([]*GitCommit, 0, len(model.commits))
	for _, commit := range model.commits {
		sorted = append(sorted, commit)
	}
	slices.SortFunc(sorted, func(a, b *GitCommit) int { return a.seq - b.seq })
	views := make([]*View, 0, len(sorted))
	viewStore := make([]View, len(sorted))
	byId := make(map[string]*View, len(sorted))
	for _, commit := range sorted {
		labelled := showCommitLabel && commit.typ != CHERRY_PICK && (commit.typ != MERGE || commit.customId)
		var tags []text.Label
		for _, tag := range commit.tags {
			label := common.PlainLabel(tag, measurer, small, 4000, 0)
			if len(label.Lines) > 0 {
				tags = append(tags, label)
			}
		}
		view := &viewStore[len(views)]
		*view = View{commit: commit, index: len(views), lane: laneOf[commit.branch], tags: tags}
		if labelled {
			label := common.PlainLabel(commit.id, measurer, small, 4000, 0)
			view.label = &label
		}
		views = append(views, view)
		byId[commit.id] = view
	}

	// Room each commit needs: along the main axis on either side, and across it towards the
	// previous lane (`lo`) and the next one (`hi`). Tags sit on one side of a lane and ids on the other.
	half := make([]float64, len(views))
	tagHalf := make([]float64, len(views))
	lo := make([]float64, laneCount)
	hi := make([]float64, laneCount)
	for i := range lo {
		lo[i], hi[i] = DOT, DOT
	}
	for k, view := range views {
		label, tags := view.label, view.tags
		tagsMain, tagsCross := 0.0, 0.0
		for _, tag := range tags {
			w := tagWidth(tag)
			if vertical {
				tagsMain = math.Max(tagsMain, tag.Height)
				tagsCross += w + 4
			} else {
				tagsMain = math.Max(tagsMain, w)
				tagsCross += tag.Height + 3
			}
		}
		labelMain, labelCross := 0.0, 0.0
		if label != nil && len(label.Lines) > 0 {
			if rotate {
				labelCross = 3 + float64(HALF*(label.Width+label.Height))
			} else {
				if vertical {
					labelMain = label.Height
					labelCross = 6 + label.Width
				} else {
					labelMain = label.Width
					labelCross = 6 + label.Height
				}
			}
		}
		half[k] = math.Max(math.Max(DOT, tagsMain/2), labelMain/2)
		tagHalf[k] = tagsMain / 2
		tagSide := float64(DOT)
		if len(tags) > 0 {
			tagSide += TAG_GAP + tagsCross
		}
		labelSide := DOT + labelCross
		lane := view.lane
		if vertical {
			lo[lane] = math.Max(lo[lane], labelSide)
			hi[lane] = math.Max(hi[lane], tagSide)
		} else {
			lo[lane] = math.Max(lo[lane], tagSide)
			hi[lane] = math.Max(hi[lane], labelSide)
		}
	}

	// In a left-to-right graph the tags stand over their commit. A connector that comes down from
	// a lane above joins the commit's lane this far before the commit, clear of the tags, and the
	// commit is placed far enough along to leave room for that.
	lead := make([]float64, len(views))
	if !vertical {
		for k, view := range views {
			if tagHalf[k] == 0 {
				continue
			}
			for _, id := range view.commit.parents {
				if parent := byId[id]; parent != nil && parent.lane < view.lane {
					lead[k] = tagHalf[k] + 4
				}
			}
		}
	}
	space := func(p, k int) float64 {
		s := math.Max(STEP, half[p]+half[k]+GAP)
		if lead[k] > 0 {
			return math.Max(s, half[p]+CURVE+lead[k])
		}
		return math.Max(s, 0)
	}

	for k, view := range views {
		if !parallel {
			if k > 0 {
				view.m = views[k-1].m + space(k-1, k)
			}
			continue
		}
		// Mermaid's `parallelCommits`: a commit sits one step after its furthest parent, so branches
		// that grow side by side share positions instead of taking turns.
		m := 0.0
		for _, id := range view.commit.parents {
			parent := byId[id]
			if parent == nil || parent.index >= k {
				continue
			}
			m = math.Max(m, parent.m+space(parent.index, k))
		}
		view.m = m
	}
	end := 0.0
	for _, view := range views {
		end = math.Max(end, view.m)
	}

	cross := make([]float64, laneCount)
	for i := 0; i < laneCount; i++ {
		name := names[i]
		reach := 0.0
		if len(name.Lines) > 0 {
			if vertical {
				reach = name.Width/2 + 8
			} else {
				reach = name.Height/2 + 2
			}
		}
		lo[i] = math.Max(lo[i], reach)
		hi[i] = math.Max(hi[i], reach)
		if i > 0 {
			cross[i] = cross[i-1] + math.Max(LANE, hi[i-1]+lo[i]+GAP)
		}
	}

	px := func(m, c float64) float64 {
		if vertical {
			return c
		}
		return m
	}
	py := func(m, c float64) float64 {
		if !vertical {
			return c
		}
		if flip {
			return -m
		}
		return m
	}
	point := func(b *strings.Builder, m, c float64) {
		svg.WriteNum(b, px(m, c))
		b.WriteByte(',')
		svg.WriteNum(b, py(m, c))
	}

	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	grow := func(x0, y0, x1, y1 float64) {
		if x0 < minX {
			minX = x0
		}
		if y0 < minY {
			minY = y0
		}
		if x1 > maxX {
			maxX = x1
		}
		if y1 > maxY {
			maxY = y1
		}
	}

	var branches strings.Builder
	if showBranches {
		for i := 0; i < laneCount; i++ {
			name := names[i]
			color := svg.SeriesColor(i)
			c := cross[i]
			w := name.Width + 16
			h := name.Height + 4
			along := w
			if vertical {
				along = h
			}
			from := -LABEL_GAP - along
			if len(name.Lines) == 0 {
				from = -LABEL_GAP
			}
			cx := px(from+along/2, c)
			cy := py(from+along/2, c)
			grow(px(-LABEL_GAP, c), py(-LABEL_GAP, c), px(-LABEL_GAP, c), py(-LABEL_GAP, c))
			grow(px(end+TAIL, c), py(end+TAIL, c), px(end+TAIL, c), py(end+TAIL, c))
			svg.WriteStrings(&branches, `<g class="pele-branch" data-id="`, svg.EscText(model.lanes[i]), `">`, `<path class="pele-lane" d="M`)
			point(&branches, -LABEL_GAP, c)
			branches.WriteString("L")
			point(&branches, end+TAIL, c)
			svg.WriteStrings(&branches, `" stroke="`, color, `" stroke-dasharray="2 4"/>`)
			if len(name.Lines) > 0 {
				grow(cx-w/2, cy-h/2, cx+w/2, cy+h/2)
				svg.WriteStrings(&branches, `<rect x="`, num(cx-w/2), `" y="`, num(cy-h/2), `" width="`, num(w), `" height="`, num(h), `" fill="`, color, `"`, svg.ROUNDED, `/>`,

					svg.LabelSvg(name, cx, cy, ` class="pele-label" fill="var(--_bg)"`, nil))
			}
			branches.WriteString("</g>")
		}
	}

	// Positions of the commits on each lane, to tell whether a connector may run along it.
	taken := make([][]float64, laneCount)
	perLane := make([]int, laneCount)
	for _, view := range views {
		perLane[view.lane]++
	}
	for lane, n := range perLane {
		taken[lane] = make([]float64, 0, n)
	}
	for _, view := range views {
		taken[view.lane] = append(taken[view.lane], view.m)
	}
	if parallel {
		for _, list := range taken {
			slices.Sort(list)
		}
	}
	busy := func(lane int, from, to float64) bool {
		list := taken[lane]
		low, high := 0, len(list)
		for low < high {
			mid := (low + high) >> 1
			if list[mid] <= from {
				low = mid + 1
			} else {
				high = mid
			}
		}
		return low < len(list) && list[low] < to
	}

	var curves, lines strings.Builder
	curves.Grow(160 * len(views))
	lines.Grow(100 * len(views))
	for _, view := range views {
		commit := view.commit
		c2 := cross[view.lane]
		m2 := view.m
		for j, id := range commit.parents {
			parent := byId[id]
			if parent == nil || parent == view {
				continue
			}
			c1 := cross[parent.lane]
			m1 := parent.m
			if parent.lane == view.lane {
				lines.WriteString(`<path class="pele-edge" d="M`)
				point(&lines, m1, c1)
				lines.WriteString("L")
				point(&lines, m2, c2)
				svg.WriteStrings(&lines, `" stroke="`, svg.SeriesColor(view.lane), `"/>`)
				continue
			}
			merged := j > 0 && commit.typ == MERGE
			picked := j > 0 && !merged
			class := ""
			if merged {
				class = " pele-edge-merge"
			} else if picked {
				class = " pele-edge-cherry-pick"
			}
			d := &curves
			svg.WriteStrings(d, `<path class="pele-edge`, class, `" d="M`)
			point(d, m1, c1)
			curve := func(a1, b1, a2, b2, a3, b3 float64) {
				d.WriteString("C")
				point(d, a1, b1)
				d.WriteString(" ")
				point(d, a2, b2)
				d.WriteString(" ")
				point(d, a3, b3)
			}
			if m2 <= m1 {
				mid := (m1 + m2) / 2
				curve(mid, c1, mid, c2, m2, c2)
			} else if !merged && !busy(view.lane, m1, m2) {
				// Leaves the parent at once and runs along the child's lane.
				s := math.Min(CURVE, m2-m1)
				curve(m1+s/2, c1, m1+s/2, c2, m1+s, c2)
				d.WriteString("L")
				point(d, m2, c2)
			} else {
				// Turns in just before the child, or before its tags when it comes from their side.
				to := m2
				if parent.lane < view.lane && m2-lead[view.index] > m1 {
					to = m2 - lead[view.index]
				}
				s := math.Min(CURVE, to-m1)
				along := c1
				if !merged || busy(parent.lane, m1, m2) {
					// Neither lane is free to run along. Between two lanes, past the ids of one and before
					// the tags of the next, a strip stays empty, and the connector takes that.
					if c1 > c2 {
						along = c2 + hi[view.lane] + GAP/2
					} else {
						along = c2 - lo[view.lane] - GAP/2
					}
					s = math.Min(CURVE, (to-m1)/2)
					curve(m1+s/2, c1, m1+s/2, along, m1+s, along)
				}
				d.WriteString("L")
				point(d, to-s, along)
				curve(to-s/2, along, to-s/2, c2, to, c2)
				if to < m2 {
					d.WriteString("L")
					point(d, m2, c2)
				}
			}
			colorLane := view.lane
			if merged {
				colorLane = parent.lane
			}
			svg.WriteStrings(&curves, `" stroke="`, svg.SeriesColor(colorLane), `"`)
			if picked {
				curves.WriteString(` stroke-dasharray="4 3"`)
			}
			curves.WriteString(`/>`)
		}
	}

	var commits strings.Builder
	commits.Grow(400 * len(views))
	for _, view := range views {
		commit, label, tags := view.commit, view.label, view.tags
		x := px(view.m, cross[view.lane])
		y := py(view.m, cross[view.lane])
		typ := commit.typ
		if commit.customType >= 0 {
			typ = commit.customType
		}
		grow(x-DOT-1, y-DOT-1, x+DOT+1, y+DOT+1)
		merge := ""
		if commit.typ == MERGE && typ != MERGE {
			merge = " pele-commit-merge"
		}
		class := CLASSES[0]
		if typ >= 0 && typ < len(CLASSES) {
			class = CLASSES[typ]
		}
		svg.WriteStrings(&commits, `<g class="pele-commit pele-commit-`, class, merge, `" data-id="`, svg.EscText(commit.id), `">`)
		body := &commits
		// Merges and cherry-picks carry a message Mermaid makes up; only a written one is worth showing.
		if commit.typ < MERGE && commit.message != "" {
			svg.WriteStrings(body, `<title>`, svg.EscText(commit.message), `</title>`)
		}
		symbol(body, typ, x, y, svg.SeriesColor(view.lane))

		if label != nil && len(label.Lines) > 0 {
			w := label.Width
			h := label.Height
			back := func(cx, cy float64) {
				svg.WriteStrings(body, `<rect x="`, num(cx-w/2-2), `" y="`, num(cy-h/2+2), `" width="`, num(w+4), `" height="`, num(h-4), `" rx="3" fill="var(--_bg)"/>`)
			}
			if rotate {
				// The text ends beside the commit and trails back the way the history came.
				k := (HALF * h) / 2
				ax, ay := x+3, y+DOT+3+k
				if vertical {
					ax, ay = x-DOT-3-k, y
				}
				reach := float64(HALF*w) + k
				if flip {
					grow(ax-reach, ay-reach, ax+k, ay+k)
				} else {
					grow(ax-reach, ay-k, ax+k, ay+reach)
				}
				angle := "-45"
				if flip {
					angle = "45"
				}
				svg.WriteStrings(body, `<g class="pele-commit-label" transform="rotate(`, angle, ` `, num(ax), ` `, num(ay), `)">`)
				back(ax-w/2, ay)
				svg.WriteStrings(body, svg.LabelSvg(*label, ax-w/2, ay, "", nil), `</g>`)
			} else {
				cx, cy := x, y+DOT+6+h/2
				if vertical {
					cx, cy = x-DOT-6-w/2, y
				}
				grow(cx-w/2, cy-h/2, cx+w/2, cy+h/2)
				body.WriteString(`<g class="pele-commit-label">`)
				back(cx, cy)
				svg.WriteStrings(body, svg.LabelSvg(*label, cx, cy, "", nil), `</g>`)
			}
		}

		// Beside the lane the tags form a row; above it they stack, the first one furthest out.
		offset := float64(DOT + TAG_GAP)
		t, stepT := len(tags)-1, -1
		if vertical {
			t, stepT = 0, 1
		}
		for ; t >= 0 && t < len(tags); t += stepT {
			tag := tags[t]
			w := tagWidth(tag)
			h := tag.Height
			left, cy := x-w/2, y-offset-h/2
			if vertical {
				left, cy = x+offset, y
				offset += w + 4
			} else {
				offset += h + 3
			}
			grow(left, cy-h/2, left+w, cy+h/2)
			svg.WriteStrings(body, `<g class="pele-tag">`,
				`<path d="M`, num(left), `,`, num(cy), `L`, num(left+NOTCH), `,`, num(cy-h/2), `H`, num(left+w), `V`, num(cy+h/2), `H`, num(left+NOTCH), `Z" fill="var(--_s)" stroke="var(--_b)" stroke-linejoin="round"/>`,
				`<circle cx="`, num(left+NOTCH), `" cy="`, num(cy), `" r="1.5" fill="var(--_b)"/>`,
				svg.LabelSvg(tag, left+NOTCH+4+tag.Width/2, cy, ` fill="var(--_fg)"`, nil),
				`</g>`)
		}

		commits.WriteString(`</g>`)
	}

	if minX > maxX {
		grow(0, 0, 0, 0)
	}
	title := text.LayoutLabel(model.title, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 16
	}
	inner := math.Max(maxX-minX, title.Width)
	width := math.Ceil(inner + float64(2*pad))
	height := math.Ceil(maxY - minY + titleHeight + float64(2*pad))
	dx := pad - minX + (inner-(maxX-minX))/2
	dy := pad + titleHeight - minY

	var content strings.Builder
	svg.WriteStrings(&content, svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil),
		`<g transform="translate(`, num(dx), `,`, num(dy), `)">`)
	if branches.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-branches" fill="none" font-size="`, js.Num(medium), `">`, branches.String(), `</g>`)
	}
	if curves.Len() > 0 || lines.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-edges" fill="none" stroke-width="2" stroke-linecap="round">`, curves.String(), lines.String(), `</g>`)
	}
	if commits.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-commits" font-size="`, js.Num(small), `" fill="var(--_m)">`, commits.String(), `</g>`)
	}
	content.WriteString(`</g>`)

	var geometry *types.Geometry
	if options.Geometry != nil {
		g := &types.GeoGit{Title: title.Plain()}
		for _, name := range names {
			g.Lanes = append(g.Lanes, strings.Join(name.Plain(), " "))
		}
		for _, view := range views {
			typ := view.commit.typ
			if view.commit.customType >= 0 {
				typ = view.commit.customType
			}
			c := types.GeoCommit{Lane: view.lane, Pos: view.m, Kind: CLASSES[0]}
			if typ >= 0 && typ < len(CLASSES) {
				c.Kind = CLASSES[typ]
			}
			if view.label != nil {
				c.Label = view.label.Plain()
			}
			for _, tag := range view.tags {
				c.Tags = append(c.Tags, tag.Plain())
			}
			for _, id := range view.commit.parents {
				if parent := byId[id]; parent != nil && parent != view {
					c.Parents = append(c.Parents, parent.index)
				}
			}
			g.Commits = append(g.Commits, c)
		}
		geometry = &types.Geometry{Width: width, Height: height, Git: g}
	}
	return types.Rendered{
		Geometry: geometry,
		SVG:      svg.SvgDocument("gitGraph", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:    width,
		Height:   height,
		Links:    []types.LinkInfo{},
	}
}
