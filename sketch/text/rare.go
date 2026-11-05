package text

import sketch "tuios.dev/sketch/internal/engine"

// more draws the types that draw does not name. It is nil until Rare is called,
// so a program that never draws those types does not link their drawers.
var more func(d *sketch.Diagram, layout *sketch.Layout, opt Options, ro sketch.RenderOptions, squeeze bool) (*canvas, error)

// Rare adds the text drawers of the diagram types sketch/core does not parse. A
// program that draws those types calls it once before drawing. Call it once,
// before any drawing starts: it sets a package variable that drawing reads.
func Rare() { more = drawRare }

func drawRare(d *sketch.Diagram, layout *sketch.Layout, opt Options, ro sketch.RenderOptions, squeeze bool) (*canvas, error) {
	switch d.Type {
	case "block", "architecture", "requirement":
		return drawFlowchart(layout, opt, squeeze)
	case "usecase", "agentflow":
		return narrower(d, layout, opt, squeeze, drawFlowchart)
	case "swimlane":
		return drawSwimlane(layout, opt, squeeze)
	case "c4":
		return drawC4(d, layout, opt, ro, squeeze)
	case "packet":
		return drawPacket(layout, opt)
	case "radar":
		return drawRadar(layout, opt)
	case "treemap":
		return drawTreemap(layout, opt)
	case "info":
		return drawInfo(layout, opt)
	case "treeView":
		return drawTreeView(layout, opt)
	case "ishikawa":
		return drawIshikawa(layout, opt)
	case "cynefin":
		return drawCynefin(layout, opt)
	case "eventmodeling":
		return drawEventModel(layout, opt)
	case "venn":
		return drawVenn(layout, opt)
	case "wardley":
		return drawWardley(layout, opt)
	case "railroad":
		return drawRailroad(layout, opt)
	case "timeline":
		return drawTimeline(layout, opt)
	case "xychart":
		return drawXY(layout, opt)
	case "quadrantChart":
		return drawQuadrant(layout, opt)
	case "journey":
		return drawJourney(layout, opt)
	case "kanban":
		return drawKanban(layout, opt)
	case "gitGraph":
		return drawGit(layout, opt)
	case "sankey":
		return drawSankey(layout, opt)
	}
	return nil, ErrUnsupported
}
