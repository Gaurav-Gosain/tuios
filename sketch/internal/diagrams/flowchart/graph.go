// Ported from pele src/diagrams/flowchart/graph.ts at 8aff183be57021b533f319a29341a529be934b08.

package flowchart

import (
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type GraphNode struct {
	ID string
	// Label is nil for none.
	Label             *string
	LabelType         LabelType
	Shape             string
	IsGroup           bool
	ParentID          *string
	CssStyles         []string
	CssCompiledStyles []string
	CssClasses        string
	Dir               string
	Link              *string
	LinkTarget        *string
	Tooltip           *string
	Icon              string
	Pos               string
	Img               string
	AssetWidth        *float64
	AssetHeight       *float64
	Constraint        string
	// ColorIndex is nil for none.
	ColorIndex *int
	Metadata   map[string]types.YamlValue
}

type GraphEdge struct {
	ID                string
	IsUserDefinedId   bool
	Start             string
	End               string
	Type              string
	Label             string
	LabelType         LabelType
	Thickness         *string
	Pattern           *string
	Minlen            *int
	ArrowTypeStart    string
	ArrowTypeEnd      string
	CssCompiledStyles []string
	Style             []string
	Classes           string
	Animate           *bool
	Animation         *string
	Curve             string
}

type FlowGraph struct {
	Nodes []*GraphNode
	Edges []*GraphEdge
}

func shapeOfVertex(vertex *FlowNode) string {
	if vertex.Img != "" {
		return "imageSquare"
	}
	if vertex.Icon != "" {
		switch vertex.Form {
		case "circle":
			return "iconCircle"
		case "square":
			return "iconSquare"
		case "rounded":
			return "iconRounded"
		}
		return "icon"
	}
	switch vertex.Type {
	case "square", "":
		return "squareRect"
	case "round":
		return "roundedRect"
	}
	return vertex.Type
}

func arrows(typ *string) (string, string) {
	if typ != nil {
		switch *typ {
		case "arrow_point", "arrow_circle", "arrow_cross":
			return "none", *typ
		case "double_arrow_point", "double_arrow_circle", "double_arrow_cross":
			single := strings.Replace(*typ, "double_", "", 1)
			return single, single
		}
	}
	return "none", "arrow_point"
}

func isCollapsedMeta(m map[string]types.YamlValue) bool {
	v, ok := m["view"].(string)
	return ok && v == "collapsed"
}

// buildFlowGraph resolves a parsed flowchart into the nodes and edges that are actually drawn:
// group membership, collapsed subgraphs, class styles and edge defaults.
func buildFlowGraph(model *FlowDb, defaultCurve string) FlowGraph {
	var nodes []*GraphNode
	var edges []*GraphEdge
	subgraphs := model.Subgraphs

	compiled := func(classNames []string) []string {
		out := []string{}
		for _, name := range classNames {
			def, ok := model.Classes.Get(name)
			if !ok {
				continue
			}
			for _, s := range def.Styles {
				out = append(out, js.Trim(s))
			}
			for _, s := range def.TextStyles {
				out = append(out, js.Trim(s))
			}
		}
		return out
	}

	subgraphParent := map[string]string{}
	for _, sg := range subgraphs {
		for _, childID := range sg.Nodes {
			if _, ok := model.Subgraph(childID); ok {
				subgraphParent[childID] = sg.ID
			}
		}
	}

	declarationIndex := map[string]int{}
	childrenOf := map[string][]string{}
	for _, sg := range subgraphs {
		parent, ok := subgraphParent[sg.ID]
		if !ok {
			continue
		}
		childrenOf[parent] = append(childrenOf[parent], sg.ID)
	}
	nextIndex := 0
	for _, sg := range subgraphs {
		if _, ok := subgraphParent[sg.ID]; ok {
			continue
		}
		pending := []string{sg.ID}
		for len(pending) > 0 {
			id := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if _, ok := declarationIndex[id]; ok {
				continue
			}
			declarationIndex[id] = nextIndex
			nextIndex++
			kids := childrenOf[id]
			for i := len(kids) - 1; i >= 0; i-- {
				pending = append(pending, kids[i])
			}
		}
	}

	isCollapsed := func(id string) bool {
		sg, ok := model.Subgraph(id)
		return ok && isCollapsedMeta(sg.Metadata)
	}
	anyCollapsed := false
	for _, sg := range subgraphs {
		if isCollapsedMeta(sg.Metadata) {
			anyCollapsed = true
		}
	}
	// The outermost collapsed subgraph around each subgraph, resolved once per chain of parents.
	// "" is none.
	outermost := map[string]string{}
	outermostCollapsed := func(id string) string {
		var chain []string
		seen := map[string]bool{}
		current, has := id, true
		for has {
			if _, done := outermost[current]; done || seen[current] {
				break
			}
			seen[current] = true
			chain = append(chain, current)
			current, has = subgraphParent[current]
		}
		result := ""
		if has {
			result = outermost[current]
		}
		for i := len(chain) - 1; i >= 0; i-- {
			if result == "" && isCollapsed(chain[i]) {
				result = chain[i]
			}
			outermost[chain[i]] = result
		}
		return result
	}

	hidden := map[string]bool{}
	collapsedInto := map[string]string{}
	if anyCollapsed {
		for _, sg := range subgraphs {
			ancestor := outermostCollapsed(sg.ID)
			if ancestor == "" {
				continue
			}
			if sg.ID != ancestor {
				hidden[sg.ID] = true
				collapsedInto[sg.ID] = ancestor
			}
			for _, childID := range sg.Nodes {
				if childID == ancestor {
					continue
				}
				hidden[childID] = true
				collapsedInto[childID] = ancestor
			}
		}
	}

	parentOf := map[string]string{}
	groups := map[string]bool{}
	for i := len(subgraphs) - 1; i >= 0; i-- {
		sg := subgraphs[i]
		if hidden[sg.ID] {
			continue
		}
		if len(sg.Nodes) > 0 {
			groups[sg.ID] = true
		}
		for _, id := range sg.Nodes {
			parentOf[id] = sg.ID
		}
	}
	parentPtr := func(id string) *string {
		if p, ok := parentOf[id]; ok {
			return &p
		}
		return nil
	}

	byID := map[string]*GraphNode{}
	for i := len(subgraphs) - 1; i >= 0; i-- {
		sg := subgraphs[i]
		if hidden[sg.ID] {
			continue
		}
		collapsed := isCollapsedMeta(sg.Metadata)
		title := sg.Title
		shape := "rect"
		if collapsed {
			shape = "collapsedGroup"
		}
		node := &GraphNode{
			ID:                sg.ID,
			Label:             &title,
			LabelType:         sg.LabelType,
			ParentID:          parentPtr(sg.ID),
			CssStyles:         []string{},
			CssCompiledStyles: compiled(sg.Classes),
			CssClasses:        strings.Join(sg.Classes, " "),
			Shape:             shape,
			Dir:               sg.Dir,
			IsGroup:           !collapsed,
		}
		if idx, ok := declarationIndex[sg.ID]; ok {
			node.ColorIndex = &idx
		}
		if !collapsed {
			node.Metadata = sg.Metadata
		}
		nodes = append(nodes, node)
		byID[sg.ID] = node
	}

	for _, vertex := range model.Nodes.Values() {
		if hidden[vertex.ID] {
			continue
		}
		if existing, ok := byID[vertex.ID]; ok {
			existing.CssStyles = vertex.Styles
			existing.CssCompiledStyles = compiled(vertex.Classes)
			existing.CssClasses = strings.Join(vertex.Classes, " ")
			continue
		}
		isGroup := groups[vertex.ID]
		shape := "rect"
		if !isGroup {
			shape = shapeOfVertex(vertex)
		}
		var tooltip *string
		if t, ok := model.Tooltips[vertex.ID]; ok {
			kept := t
			tooltip = &kept
		}
		node := &GraphNode{
			ID:                vertex.ID,
			Label:             vertex.Text,
			LabelType:         vertex.LabelType,
			ParentID:          parentPtr(vertex.ID),
			CssStyles:         vertex.Styles,
			CssCompiledStyles: compiled(append([]string{"default", "node"}, vertex.Classes...)),
			CssClasses:        "default " + strings.Join(vertex.Classes, " "),
			Dir:               vertex.Dir,
			Link:              vertex.Link,
			LinkTarget:        vertex.LinkTarget,
			Tooltip:           tooltip,
			Icon:              vertex.Icon,
			Pos:               vertex.Pos,
			Img:               vertex.Img,
			AssetWidth:        vertex.AssetWidth,
			AssetHeight:       vertex.AssetHeight,
			Constraint:        vertex.Constraint,
			IsGroup:           isGroup,
			Shape:             shape,
		}
		nodes = append(nodes, node)
	}

	for index, raw := range model.Edges {
		start := raw.Start
		if v, ok := collapsedInto[raw.Start]; ok {
			start = v
		}
		end := raw.End
		if v, ok := collapsedInto[raw.End]; ok {
			end = v
		}
		_, cs := collapsedInto[raw.Start]
		_, ce := collapsedInto[raw.End]
		if start == end && (cs || ce) {
			continue
		}

		arrowStart, arrowEnd := arrows(raw.Type)
		styles := append([]string{}, model.DefaultEdgeStyle...)
		if raw.Style != nil {
			styles = append(styles, raw.Style...)
		}
		invisible := raw.Stroke != nil && *raw.Stroke == "invisible"
		plain := invisible || (raw.Type != nil && *raw.Type == "arrow_open")
		id := raw.ID
		if id == "" {
			id = "L_" + start + "_" + end + "_" + js.Int(index)
		}
		typ := "normal"
		if raw.Type != nil {
			typ = *raw.Type
		}
		classes := "edge-thickness-normal edge-pattern-solid flowchart-link"
		if invisible {
			classes = ""
		}
		e := &GraphEdge{
			ID:                id,
			IsUserDefinedId:   raw.IsUserDefinedId,
			Start:             start,
			End:               end,
			Type:              typ,
			Label:             raw.Text,
			LabelType:         raw.LabelType,
			Thickness:         raw.Stroke,
			Pattern:           raw.Stroke,
			Minlen:            raw.Length,
			Classes:           classes,
			ArrowTypeStart:    arrowStart,
			ArrowTypeEnd:      arrowEnd,
			CssCompiledStyles: compiled(raw.Classes),
			Style:             styles,
			Animate:           raw.Animate,
			Animation:         raw.Animation,
			Curve:             firstNonEmpty(raw.Interpolate, model.DefaultInterpolate, defaultCurve),
		}
		if plain {
			e.ArrowTypeStart = "none"
			e.ArrowTypeEnd = "none"
		}
		edges = append(edges, e)
	}

	return FlowGraph{Nodes: nodes, Edges: edges}
}

func firstNonEmpty(list ...string) string {
	for _, s := range list {
		if s != "" {
			return s
		}
	}
	return ""
}
