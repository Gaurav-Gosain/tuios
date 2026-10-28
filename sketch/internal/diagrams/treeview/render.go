// Ported from pele src/diagrams/treeview/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package treeview

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	ICON_GAP           = 6
	DESC_GAP           = 16
	GUIDE_INSET        = 7
	ELBOW              = 4
	HIGHLIGHT_OVERHANG = 4
)

func setting(config types.Config, key string, fallback, min, max float64) float64 {
	section, _ := config["treeView"].(map[string]any)
	if value, ok := section[key].(float64); ok && value >= min && value <= max {
		return value
	}
	return fallback
}

type treeSettingsOf struct{ paddingX, paddingY, rowIndent, lineThickness float64 }

// treeSettings are Mermaid's layout options, with Pele's defaults.
func treeSettings(config types.Config) treeSettingsOf {
	return treeSettingsOf{
		paddingX:      setting(config, "paddingX", 5, 0, 100),
		paddingY:      setting(config, "paddingY", 2, 0, 100),
		rowIndent:     setting(config, "rowIndent", 15, 0, 400),
		lineThickness: setting(config, "lineThickness", 1, 0.25, 12),
	}
}

type Row struct {
	node        *TreeNode
	depth       int
	x           float64
	cy          float64
	icon        string
	text        string
	labelX      float64
	highlighted bool
	label       *text.Label
	description *text.Label
	top         float64
	height      float64
}

func renderTreeView(model *TreeViewModel, config types.Config, options types.RenderOptions) types.Rendered {
	return common.Tighten(options, func(tight bool) types.Rendered { return draw(model, config, options, tight) })
}

type pendingNode struct {
	node  *TreeNode
	depth int
	slot  bool
}

type nodeDepth struct {
	node  *TreeNode
	depth int
}

func draw(model *TreeViewModel, config types.Config, options types.RenderOptions, tight bool) types.Rendered {
	size := types.Or(options.FontSize, 16)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	icons := iconConfig(config)
	mono := options.HasMonospace(types.MonospaceFiles)
	family := ""
	if mono {
		family = ` font-family="` + svg.FONT_MONO + `"`
	}
	maxWidth := types.Or(options.MaxWidth, 320)

	s := treeSettings(config)
	paddingX, paddingY, rowIndent, lineThickness := s.paddingX, s.paddingY, s.rowIndent, s.lineThickness
	maxDepth := 0
	if tight {
		depths := make([]nodeDepth, 0, model.count)
		for _, node := range model.root.children {
			depths = append(depths, nodeDepth{node, 0})
		}
		for i := 0; i < len(depths); i++ {
			node, depth := depths[i].node, depths[i].depth
			maxDepth = max(maxDepth, depth)
			for _, child := range node.children {
				depths = append(depths, nodeDepth{child, depth + 1})
			}
		}
	}
	step := rowIndent + paddingX
	if tight {
		step = math.Min(rowIndent+paddingX, math.Max(2, (maxWidth-120)/math.Max(1, float64(maxDepth))))
	}
	rowHeight := js.Round(size*1.5) + float64(2*paddingY)
	iconSize := size

	titleRoom := 4000.0
	if tight {
		titleRoom = common.TitleRoom(options)
	}
	title := text.LayoutLabel(model.title, false, measurer, size, titleRoom, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}

	// Siblings line up: when one entry of a folder has an icon, the others leave room for one.
	hasIcon := func(list []*TreeNode) bool {
		for _, child := range list {
			if getNodeIcon(child, icons) != "" {
				return true
			}
		}
		return false
	}
	rows := make([]Row, 0, model.count)
	// rowOf holds each node's row by its id.
	rowOf := make([]int, model.count)
	var pending []pendingNode
	push := func(list []*TreeNode, depth int) {
		slot := hasIcon(list)
		for i := len(list) - 1; i >= 0; i-- {
			pending = append(pending, pendingNode{list[i], depth, slot})
		}
	}
	push(model.root.children, 0)
	labelRight := 0.0
	anyHighlight := false
	rowsHeight := 0.0
	for len(pending) > 0 {
		p := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		node, depth, slot := p.node, p.depth, p.slot
		x := float64(float64(depth) * step)
		label := text.DecodeEntities(node.name)
		labelX := x
		if slot {
			labelX = x + (iconSize + ICON_GAP)
		}
		room := math.Max(40, maxWidth-float64(2*pad)-float64(2*HIGHLIGHT_OVERHANG)-labelX)
		style := 0
		if node.nodeType == "directory" {
			style = text.Bold
		}
		if mono {
			style |= text.Mono
		}
		var literal, description *text.Label
		if tight {
			l := common.LiteralLabel(label, measurer, size, room, style)
			literal = &l
			if node.description != "" {
				d := common.LiteralLabel(text.DecodeEntities(node.description), measurer, size, room, text.Italic)
				description = &d
			}
		}
		var width float64
		if literal != nil {
			width = literal.Width
		} else {
			width = measurer.Width(label, size, style)
		}
		if labelX+width > labelRight {
			labelRight = labelX + width
		}
		highlighted := false
		if node.cssClass != "" {
			for _, name := range strings.FieldsFunc(node.cssClass, js.IsSpace) {
				if name == "highlight" {
					highlighted = true
				}
			}
		}
		if highlighted {
			anyHighlight = true
		}
		height := rowHeight
		var cy float64
		if tight {
			lh, dh := 0.0, 0.0
			if literal != nil {
				lh = literal.Height
			}
			if description != nil {
				dh = description.Height
			}
			height = math.Max(rowHeight, lh+dh+float64(2*paddingY))
			cy = rowsHeight + (paddingY + math.Max(iconSize, lh)/2)
		} else {
			cy = rowsHeight + height/2
		}
		rows = append(rows, Row{node: node, depth: depth, x: x, cy: cy, icon: getNodeIcon(node, icons), text: label, labelX: labelX,
			highlighted: highlighted, label: literal, description: description, top: rowsHeight, height: height})
		rowsHeight += height
		rowOf[node.id] = len(rows) - 1
		push(node.children, depth+1)
	}

	descX := labelRight + DESC_GAP
	width := labelRight
	for i := range rows {
		row := &rows[i]
		if row.node.description == "" {
			continue
		}
		var w float64
		if row.description != nil {
			w = row.description.Width
		} else {
			w = measurer.Width(text.DecodeEntities(row.node.description), size, text.Italic)
		}
		if tight {
			width = math.Max(width, row.labelX+w)
		} else {
			width = math.Max(width, descX+w)
		}
	}
	width = math.Max(width, title.Width)
	inset := pad
	if anyHighlight {
		inset = pad + HIGHLIGHT_OVERHANG
	}
	top := pad + titleHeight
	totalWidth := math.Ceil(width + float64(2*inset))
	totalHeight := math.Ceil(top + rowsHeight + pad)

	var guides strings.Builder
	for i := range rows {
		row := &rows[i]
		children := row.node.children
		if len(children) == 0 {
			continue
		}
		gx := inset + row.x + GUIDE_INSET
		tick := math.Max(gx, inset+row.x+step-paddingX)
		last := top + rows[rowOf[children[len(children)-1].id]].cy
		r := math.Min(ELBOW, tick-gx)
		svg.WriteStrings(&guides, `<path d="M`, svg.Num(gx), ",", svg.Num(top+row.top+row.height-2), "V", svg.Num(last-r))
		if r > 0 {
			svg.WriteStrings(&guides, "Q", svg.Num(gx), ",", svg.Num(last), " ", svg.Num(gx+r), ",", svg.Num(last), "H", svg.Num(tick))
		}
		for i := 0; i < len(children)-1 && tick > gx; i++ {
			svg.WriteStrings(&guides, "M", svg.Num(gx), ",", svg.Num(top+rows[rowOf[children[i].id]].cy), "H", svg.Num(tick))
		}
		guides.WriteString(`"/>`)
	}

	baseline := float64(size * 0.35)
	var body strings.Builder
	var geo *types.GeoTreeView
	if options.Geometry != nil {
		geo = &types.GeoTreeView{Title: title.Plain(), Rows: make([]types.GeoTreeRow, 0, len(rows))}
	}
	for i := range rows {
		row := &rows[i]
		node := row.node
		cy := top + row.cy
		directory := node.nodeType == "directory"
		kind := "file"
		if directory {
			kind = "dir"
		}
		svg.WriteStrings(&body, `<g class="pele-node pele-tree-`, kind, svg.ClassNames(node.cssClass), `" data-id="`, svg.EscText(node.name), `">`)
		if row.highlighted {
			x := inset + row.x - HIGHLIGHT_OVERHANG
			svg.WriteStrings(&body, `<rect class="pele-tree-highlight" x="`, svg.Num(x), `" y="`, svg.Num(top+row.top+1), `" width="`,
				svg.Num(totalWidth-inset+HIGHLIGHT_OVERHANG-x), `" height="`, svg.Num(row.height-2), `" fill="var(--_c)" fill-opacity="0.14"`, svg.ROUNDED, `/>`)

		}
		if row.icon != "" {
			name := ""
			if strings.HasPrefix(row.icon, BUILTIN_PACK+":") {
				name = row.icon[len(BUILTIN_PACK)+1:]
			}
			g := glyph(name)
			if g == "" {
				if directory {
					g = glyph("folder")
				} else {
					g = glyph("file")
				}
			}
			drawn := ""
			if options.Icons != nil {
				drawn = options.Icons(row.icon)
			}
			if drawn == "" {
				drawn = `<path d="` + g + `" stroke="var(--_m)" stroke-width="1.5"/>`
			}
			svg.WriteStrings(&body, `<svg class="pele-icon" data-icon="`, svg.EscText(row.icon), `" x="`, svg.Num(inset+row.x), `" y="`, svg.Num(cy-iconSize/2),
				`" width="`, svg.Num(iconSize), `" height="`, svg.Num(iconSize),
				`" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">`, drawn, `</svg>`)
		}
		weight := ""
		if directory {
			weight = ` font-weight="var(--_hw)"`
		}
		if row.label != nil {
			body.WriteString(common.LeftLabelSvg(*row.label, inset+row.labelX, top+row.top+paddingY, ` class="pele-label"`+weight+family))
		} else if row.text != "" {
			svg.WriteStrings(&body, `<text class="pele-label" x="`, svg.Num(inset+row.labelX), `" y="`, svg.Num(cy+baseline), `"`, weight, family, ` xml:space="preserve">`, svg.Esc(row.text), `</text>`)
		}
		if row.description != nil {
			lh := 0.0
			if row.label != nil {
				lh = row.label.Height
			}
			body.WriteString(common.LeftLabelSvg(*row.description, inset+row.labelX, top+row.top+paddingY+lh, ` class="pele-tree-description" font-style="italic" fill="var(--_m)"`))
		} else if node.description != "" {
			svg.WriteStrings(&body, `<text class="pele-tree-description" x="`, svg.Num(inset+descX), `" y="`, svg.Num(cy+baseline),
				`" font-style="italic" fill="var(--_m)" xml:space="preserve">`, svg.Esc(text.DecodeEntities(node.description)), `</text>`)
		}
		body.WriteString(`</g>`)
		if geo != nil {
			geo.Rows = append(geo.Rows, types.GeoTreeRow{Depth: row.depth, Name: row.text, Directory: directory, Icon: row.icon,
				Description: text.DecodeEntities(node.description), Highlight: row.highlighted})
		}
	}

	var content strings.Builder
	content.WriteString(svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	if guides.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-tree-guides" fill="none" stroke="var(--_b)" stroke-width="`, svg.Num(lineThickness), `">`, guides.String(), `</g>`)
	}
	content.WriteString(body.String())

	rendered := types.Rendered{
		SVG:    svg.SvgDocument("treeView", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
	if geo != nil {
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, TreeView: geo}
	}
	return rendered
}
