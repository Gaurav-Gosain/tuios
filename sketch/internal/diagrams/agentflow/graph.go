// Ported from pele src/diagrams/agentflow/graph.ts at 8aff183be57021b533f319a29341a529be934b08.

package agentflow

import (
	"strings"

	"tuios.dev/sketch/internal/js"
)

type GraphNode struct {
	ID string
	// Label is nil for none.
	Label     *string
	LabelType string
	Shape     string
	IsGroup   bool
	// ParentID is the container's id where HasParent says there is one: an id can be "".
	ParentID          string
	HasParent         bool
	CssStyles         []string
	CssCompiledStyles []string
	CssClasses        string
	Dir               *string
	Link              *string
	LinkTarget        *string
	Tooltip           string
	// ColorIndex is the palette slot of a container, nil for none.
	ColorIndex *int
	// Kind is what a node is, which picks its palette slot; "" for none.
	Kind string
}

type GraphEdge struct {
	ID          string
	UserDefined bool
	Start       string
	End         string
	Type        string
	Label       string
	LabelType   string
	Thickness   string
	// Minlen is -1 for none.
	Minlen            int
	ArrowTypeStart    string
	ArrowTypeEnd      string
	CssCompiledStyles []string
	Style             []string
	Classes           string
	// Curve is "" for none.
	Curve string
}

type AgentGraph struct {
	Nodes []*GraphNode
	Edges []*GraphEdge
}

// orderedSet is a JavaScript Set of strings: it iterates in the order things were added. Not in pele.
type orderedSet struct {
	order []string
	has   map[string]bool
}

func (s *orderedSet) add(k string) {
	if !s.has[k] {
		s.has[k] = true
		s.order = append(s.order, k)
	}
}

func viewOf(m Metadata) any { return m["view"] }

// buildAgentGraph resolves a parsed agentflow into the nodes and edges that are drawn: flow membership, collapsed
// flows, shapes reduced to the ones agentflow has, class styles and palette slots. Shape and
// containment problems are reported on the model as diagnostics.
func buildAgentGraph(db *AgentflowDb, paletteLength int, defaultCurve string) *AgentGraph {
	nodes := make([]*GraphNode, 0, len(db.Subgraphs)+len(db.Nodes.keys))
	edges := make([]*GraphEdge, 0, len(db.Edges))
	subgraphs := db.Subgraphs

	compiled := func(classNames []string) []string {
		var out []string
		for _, name := range classNames {
			def, ok := db.Classes.get(name)
			if !ok {
				continue
			}
			out = append(out, def.Styles...)
			out = append(out, def.TextStyles...)
		}
		for k, s := range out {
			out[k] = js.Trim(s)
		}
		return out
	}

	// Everything inside a collapsed flow is hidden, and stands for its outermost collapsed flow.
	// Mermaid walks the whole inside of each collapsed flow, in the order the flows closed, and the
	// last walk to reach a thing wins. A thing has one container, so a later walk can only enter an
	// earlier one through its root: it stops there, and the root's owner is followed afterwards.
	hidden := orderedSet{has: map[string]bool{}}
	owner := map[string]string{}
	walked := map[string]bool{}
	for _, sg := range subgraphs {
		if viewOf(sg.Metadata) != "collapsed" || hidden.has[sg.ID] {
			continue
		}
		pending := []string{sg.ID}
		for len(pending) > 0 {
			id := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			sub := db.subgraph(id)
			if sub == nil {
				continue
			}
			for _, childID := range sub.Nodes {
				if childID == sg.ID || hidden.has[childID] {
					continue
				}
				hidden.add(childID)
				owner[childID] = sg.ID
				if !walked[childID] {
					pending = append(pending, childID)
				}
			}
		}
		walked[sg.ID] = true
	}
	collapsedInto := map[string]string{}
	for _, id := range hidden.order {
		path := []string{id}
		top := owner[id]
		for {
			known, isKnown := collapsedInto[top]
			up, hasUp := known, isKnown
			if !isKnown {
				up, hasUp = owner[top]
			}
			if !hasUp {
				break
			}
			if !isKnown {
				path = append(path, top)
			}
			top = up
			if isKnown {
				break
			}
		}
		for _, p := range path {
			collapsedInto[p] = top
		}
	}

	position := make(map[string]int, len(subgraphs))
	for i, sg := range subgraphs {
		position[sg.ID] = i
	}

	// Two flows can each name the other. The nesting that would close the loop is dropped.
	parentOf := map[string]string{}
	closesLoop := func(childID, parentID string, at int) bool {
		// Only a flow handled earlier in this pass can already be above the parent.
		childAt, ok := position[childID]
		if !ok || childAt < at {
			return false
		}
		current, has := parentID, true
		for hops := 0; has && hops <= len(subgraphs); hops++ {
			if current == childID {
				return true
			}
			current, has = parentOf[current]
		}
		return false
	}
	for i := len(subgraphs) - 1; i >= 0; i-- {
		sg := subgraphs[i]
		if hidden.has[sg.ID] {
			continue
		}
		for _, id := range sg.Nodes {
			if closesLoop(id, sg.ID, i) {
				db.emitWarning("CONTAINMENT_VIOLATION", `Container "`+sg.ID+`" cannot contain "`+id+`" because "`+id+
					`" already contains it. The nesting that would close the loop is dropped.`, id)
				continue
			}
			parentOf[id] = sg.ID
		}
	}

	byID := map[string]*GraphNode{}
	for i := len(subgraphs) - 1; i >= 0; i-- {
		sg := subgraphs[i]
		if hidden.has[sg.ID] {
			continue
		}
		collapsed := viewOf(sg.Metadata) == "collapsed"
		title := sg.Title
		shape := "flowGroup"
		if collapsed {
			shape = "collapsedGroup"
		}
		node := &GraphNode{ID: sg.ID, Label: &title, LabelType: sg.LabelType, CssStyles: []string{},
			CssCompiledStyles: compiled(sg.Classes), CssClasses: strings.Join(sg.Classes, " "), Shape: shape, Dir: sg.Dir, IsGroup: !collapsed}
		node.ParentID, node.HasParent = parentOf[sg.ID]
		nodes = append(nodes, node)
		byID[sg.ID] = node
	}

	for _, key := range db.Nodes.keys {
		vertex := db.Nodes.m[key]
		if hidden.has[vertex.ID] {
			continue
		}
		if existing, ok := byID[vertex.ID]; ok {
			existing.CssStyles = vertex.Styles
			existing.CssCompiledStyles = compiled(vertex.Classes)
			existing.CssClasses = strings.Join(vertex.Classes, " ")
			continue
		}
		typ := ""
		if vertex.Type != nil {
			typ = resolveShapeAlias(*vertex.Type)
		}
		shape := typ
		if typ == "square" || vertex.Type == nil {
			shape = "squareRect"
		}
		node := &GraphNode{ID: vertex.ID, Label: vertex.Text, LabelType: vertex.LabelType, CssStyles: vertex.Styles,
			CssCompiledStyles: compiled(append([]string{"default", "node"}, vertex.Classes...)), CssClasses: "default " + strings.Join(vertex.Classes, " "),
			Dir: vertex.Dir, Link: vertex.Link, LinkTarget: vertex.LinkTarget, Tooltip: db.Tooltips[vertex.ID], Shape: shape}
		node.ParentID, node.HasParent = parentOf[vertex.ID]
		nodes = append(nodes, node)
		byID[vertex.ID] = node
	}

	for index, raw := range db.Edges {
		start := raw.Start
		if into, ok := collapsedInto[start]; ok {
			start = into
		}
		end := raw.End
		if into, ok := collapsedInto[end]; ok {
			end = into
		}
		// An edge between two things inside one collapsed flow has nothing left to connect.
		if start == end && raw.Start != raw.End {
			continue
		}
		styles := append(append([]string{}, db.DefaultEdgeStyle...), raw.Style...)
		rawType := raw.Type
		arrow := "arrow_point"
		switch rawType {
		case "arrow_open":
			arrow = "none"
		case "arrow_cross", "arrow_point":
			arrow = rawType
		}
		id := ""
		if raw.ID != nil {
			id = *raw.ID
		}
		if id == "" {
			id = "L_" + start + "_" + end + "_" + js.Int(index)
		}
		typ := rawType
		if typ == "" {
			typ = "normal"
		}
		curve := ""
		if raw.Interpolate != nil {
			curve = *raw.Interpolate
		}
		if curve == "" && db.DefaultInterpolate != nil {
			curve = *db.DefaultInterpolate
		}
		if curve == "" {
			curve = defaultCurve
		}
		edges = append(edges, &GraphEdge{ID: id, UserDefined: raw.UserDefined, Start: start, End: end, Type: typ, Label: raw.Text, LabelType: raw.LabelType,
			Thickness: raw.Stroke, Minlen: raw.Length, Classes: "edge-thickness-normal edge-pattern-solid flowchart-link", ArrowTypeStart: "none",
			ArrowTypeEnd: arrow, CssCompiledStyles: compiled(raw.Classes), Style: styles, Curve: curve})
	}

	normaliseNodeShapes(nodes, db)

	// Containers are numbered in the order they were written: a walk of the nesting, parents first.
	// `subgraphs` itself is in the order the blocks closed, with a nested flow before its parent.
	children := map[string][]string{}
	for _, sg := range subgraphs {
		parent, ok := parentOf[sg.ID]
		if !ok {
			continue
		}
		children[parent] = append(children[parent], sg.ID)
	}
	order := map[string]int{}
	for _, sg := range subgraphs {
		if _, ok := parentOf[sg.ID]; ok {
			continue
		}
		pending := []string{sg.ID}
		for len(pending) > 0 {
			id := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if _, ok := order[id]; ok {
				continue
			}
			order[id] = len(order)
			kids := children[id]
			for i := len(kids) - 1; i >= 0; i-- {
				pending = append(pending, kids[i])
			}
		}
	}

	assignColorSlots(nodes, func(id string) string {
		if _, ok := db.Connectors.get(id); ok {
			return "connector"
		}
		if vertex, ok := db.Nodes.get(id); ok {
			return db.vertexKind(vertex)
		}
		return ""
	}, order, paletteLength)

	return &AgentGraph{Nodes: nodes, Edges: edges}
}
