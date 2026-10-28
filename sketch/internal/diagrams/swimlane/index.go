// Ported from pele src/diagrams/swimlane/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package swimlane draws swimlane diagrams: flowcharts whose top-level subgraphs are
// lanes that run the length of the diagram.
package swimlane

import (
	"strings"

	"tuios.dev/sketch/internal/diagrams/flowchart"
	"tuios.dev/sketch/internal/layout"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const defaultLane = "__swimlane_default__"

type variant struct{}

func (variant) Type() string { return "swimlane" }

// Prepare puts nodes outside every lane in one unnamed lane, as in Mermaid. Lanes all run
// the same way, so a `direction` inside a subgraph has no effect.
func (variant) Prepare(graph *flowchart.FlowGraph) {
	loose := false
	for _, node := range graph.Nodes {
		if node.IsGroup {
			node.Dir = ""
		} else if node.ParentID == nil {
			loose = true
		}
	}
	if !loose {
		return
	}
	lane := defaultLane
	for _, node := range graph.Nodes {
		if !node.IsGroup && node.ParentID == nil {
			node.ParentID = &lane
		}
	}
	graph.Nodes = append(graph.Nodes, &flowchart.GraphNode{
		ID:                defaultLane,
		LabelType:         "text",
		Shape:             "rect",
		IsGroup:           true,
		CssStyles:         []string{},
		CssCompiledStyles: []string{},
	})
}

func (variant) Layout(nodes []*layout.CNode, edges []*layout.CEdge, dir layout.Dir, opt layout.LayeredOptions, tight bool) layout.CompoundResult {
	pad := float64(layout.LanePad)
	if tight {
		pad = 12
	}
	return layout.LaneLayout(nodes, edges, dir, opt, pad)
}

// DrawGroup draws a lane: an outline the length of the diagram, with its title in a band at the start.
func (variant) DrawGroup(c *layout.CNode, label text.Label, style svg.ResolvedStyle, classes string, id string, dir layout.Dir, icons text.IconResolver) (string, bool) {
	left := c.X - c.W/2
	top := c.Y - c.H/2
	sideways := dir == "LR" || dir == "RL"
	cx, cy := c.X, top+c.PadTop/2
	if sideways {
		cx, cy = left+c.PadTop/2, c.Y
	}
	txt := svg.LabelSvg(label, cx, cy, ` class="pele-cluster-label"`+svg.InkFill(style, "var(--_m)")+style.Text, icons)
	bw, bh := c.W, c.PadTop
	if sideways {
		bw, bh = c.PadTop, c.H
	}
	var b strings.Builder
	b.Grow(320 + len(txt) + len(classes) + len(id) + len(style.Shape))
	svg.WriteStrings(&b, `<g class="pele-cluster pele-lane`, classes, `" data-id="`, id, `"><rect x="`)
	svg.WriteNum(&b, left)
	b.WriteString(`" y="`)
	svg.WriteNum(&b, top)
	b.WriteString(`" width="`)
	svg.WriteNum(&b, c.W)
	b.WriteString(`" height="`)
	svg.WriteNum(&b, c.H)
	b.WriteString(`" fill="none" stroke="var(--_b)"/><rect x="`)
	svg.WriteNum(&b, left)
	b.WriteString(`" y="`)
	svg.WriteNum(&b, top)
	b.WriteString(`" width="`)
	svg.WriteNum(&b, bw)
	b.WriteString(`" height="`)
	svg.WriteNum(&b, bh)
	svg.WriteStrings(&b, `" fill="var(--_a)" stroke="var(--_b)"`, style.Shape, `/>`)
	if sideways && txt != "" {
		b.WriteString(`<g transform="rotate(-90 `)
		svg.WriteNum(&b, cx)
		b.WriteByte(' ')
		svg.WriteNum(&b, cy)
		svg.WriteStrings(&b, `)">`, txt, `</g>`)
	} else {
		b.WriteString(txt)
	}
	b.WriteString(`</g>`)
	return b.String(), true
}

// FilledBox is the part of a lane that takes its fill, as [left, top, right, bottom]: the title band.
func (variant) FilledBox(c *layout.CNode, dir layout.Dir) [4]float64 {
	left := c.X - c.W/2
	top := c.Y - c.H/2
	if dir == "LR" || dir == "RL" {
		return [4]float64{left, top, left + c.PadTop, top + c.H}
	}
	return [4]float64{left, top, left + c.W, top + c.PadTop}
}

// Swimlane is the swimlane diagram type: a flowchart whose top-level subgraphs are lanes,
// with the same syntax and the same model.
var Swimlane = &types.Diagram{
	Type:  "swimlane",
	Math:  true,
	Parse: flowchart.Flowchart.Parse,
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return flowchart.RenderFlowchart(model.(*flowchart.FlowDb), config, options, variant{})
	},
}
