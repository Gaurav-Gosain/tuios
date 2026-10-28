// Ported from pele src/diagrams/usecase/graph.ts at 8aff183be57021b533f319a29341a529be934b08.

package usecase

import (
	"slices"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
)

type JsonRow struct {
	// Blank on the second and later values of an array.
	key string
	// The key again, for every row.
	accessibleKey string
	value         string
}

type UsecaseGraphNode struct {
	id        string
	label     string
	labelType string
	// Mermaid's shape names.
	shape             string
	isGroup           bool
	padding           float64
	cssClasses        string
	cssStyles         []string
	cssCompiledStyles []string
	actorType         string
	business          bool
	icon              string
	stereotype        string
	parentID          string
	noteTarget        string
	jsonRows          []JsonRow
	isJSON            bool
	boundaryType      string
}

type UsecaseGraphEdge struct {
	id                string
	start             string
	end               string
	relationshipType  string
	pattern           string
	arrowTypeStart    string
	arrowTypeEnd      string
	label             *string
	labelType         string
	classes           string
	style             []string
	cssCompiledStyles []string
	animate           bool
	animation         string
	minlen            int
	explicitID        bool
	// The dotted line from a note to what it is about.
	internal bool
}

type UsecaseGraph struct {
	nodes []UsecaseGraphNode
	edges []UsecaseGraphEdge
}

var ACTOR_SHAPES = map[string]string{
	"normal":  "usecaseActor",
	"hollow":  "usecaseActorHollow",
	"awesome": "usecaseActorAwesome",
	"icon":    "usecaseActorIcon",
}

// MARKERS are the start and end markers for each arrow type, in the order of the arrow types.
var MARKERS = [...][2]string{
	{"none", "arrow_point"},
	{"arrow_point", "none"},
	{"none", "none"},
	{"none", "arrow_circle"},
	{"none", "arrow_cross"},
	{"arrow_circle", "none"},
	{"arrow_cross", "none"},
}

type missing struct{}

func scalar(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return "null"
	case missing:
		return "undefined"
	}
	return js.String(value)
}

type jsonItem struct {
	value   any
	path    *jsonPath
	pointer int
}

// jsonPath is a row's key path, kept as its last piece and its parent so that a deep text
// does not copy the path at every level. Not in pele.
type jsonPath struct {
	parent *jsonPath
	piece  string
	length int
}

func (p *jsonPath) add(piece string) *jsonPath {
	return &jsonPath{parent: p, piece: piece, length: p.length + len(piece)}
}

func (p *jsonPath) String() string {
	if p.parent == nil {
		return p.piece
	}
	b := make([]byte, p.length)
	at := p.length
	for q := p; q != nil; q = q.parent {
		at -= len(q.piece)
		copy(b[at:], q.piece)
	}
	return string(b)
}

// flattenJsonRows gives one row for each leaf of a JSON object, depth first in the order the keys were written.
// Nesting is kept on a stack of its own, so depth is limited by memory and not by the call stack.
func flattenJsonRows(value *jsonObject, propertyOrder *keyOrder) []JsonRow {
	var rows []JsonRow
	stack := []jsonItem{{value, &jsonPath{}, 0}}
	var children []jsonItem
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		children = children[:0]
		path, pointer := cur.path, cur.pointer
		switch current := cur.value.(type) {
		case []any:
			scalars := true
			for _, item := range current {
				switch item.(type) {
				case []any, *jsonObject:
					scalars = false
				}
			}
			switch {
			case len(current) == 0:
				p := path.String()
				rows = append(rows, JsonRow{p, p, "[]"})
			case scalars:
				p := path.String()
				for i, item := range current {
					key := ""
					if i == 0 {
						key = p
					}
					rows = append(rows, JsonRow{key, p, scalar(item)})
				}
			default:
				for i, item := range current {
					index := strconv.Itoa(i)
					children = append(children, jsonItem{item, path.add("[" + index + "]"), propertyOrder.lookup(pointer, index)})
				}
			}
		case *jsonObject:
			keys, ok := propertyOrder.get(pointer)
			if !ok {
				keys = current.Keys
			}
			if len(keys) == 0 {
				p := path.String()
				rows = append(rows, JsonRow{p, p, "{}"})
			}
			for _, key := range keys {
				var child any = missing{}
				if v, ok := current.Values[key]; ok {
					child = v
				}
				childPath := &jsonPath{piece: key, length: len(key)}
				if path.length > 0 {
					childPath = path.add("." + key)
				}
				children = append(children, jsonItem{child, childPath, propertyOrder.lookup(pointer, pointerKey(key))})
			}
		default:
			p := path.String()
			rows = append(rows, JsonRow{p, p, scalar(current)})
		}
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, children[i])
		}
	}
	return rows
}

// classList joins four names and a list with spaces, leaving out the empty ones.
func classList(a, b, c, d string, rest []string) string {
	size := len(a) + len(b) + len(c) + len(d) + 4
	for _, name := range rest {
		size += len(name) + 1
	}
	var out strings.Builder
	out.Grow(size)
	add := func(name string) {
		if name == "" {
			return
		}
		if out.Len() > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(name)
	}
	add(a)
	add(b)
	add(c)
	add(d)
	for _, name := range rest {
		add(name)
	}
	return out.String()
}

func markers(relationship *Relationship) [2]string {
	if relationship.typ == "generalization" {
		return [2]string{"none", "extension"}
	}
	if relationship.typ != "association" {
		return MARKERS[SOLID_ARROW]
	}
	if relationship.arrowType >= 0 && relationship.arrowType < len(MARKERS) {
		return MARKERS[relationship.arrowType]
	}
	return MARKERS[LINE_SOLID]
}

func when(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

// buildUsecaseGraph resolves a parsed use case diagram into what is drawn: every actor, use case, note, JSON table
// and boundary as a node with its class styles, and every relationship and note connector as an
// edge with its markers. Ports getData from Mermaid's use case DB.
func buildUsecaseGraph(model *UsecaseModel) UsecaseGraph {
	nodes := make([]UsecaseGraphNode, 0, model.actors.size+model.useCases.size+model.notes.size+model.jsonNodes.size+model.systemBoundaries.size)
	edges := make([]UsecaseGraphEdge, 0, len(model.relationships)+model.notes.size)

	// compiled: a later class wins a property but keeps the place the property first took.
	compiled := func(classNames []string) []string {
		if model.classDefs.size == 0 {
			return nil
		}
		var props, styles []string
		for k := -1; k < len(classNames); k++ {
			name := "default"
			if k >= 0 {
				name = classNames[k]
			}
			definition, ok := model.classDefs.get(name)
			if !ok {
				continue
			}
			for _, raw := range definition.styles {
				style := js.Trim(raw)
				property := style
				if colon := strings.IndexByte(style, ':'); colon != -1 {
					property = style[:colon]
				}
				property = js.Trim(property)
				if property == "" {
					continue
				}
				if at := slices.Index(props, property); at >= 0 {
					styles[at] = style
				} else {
					props = append(props, property)
					styles = append(styles, style)
				}
			}
		}
		return styles
	}

	for _, actor := range model.actors.values() {
		nodes = append(nodes, UsecaseGraphNode{
			id: actor.id, label: actor.label, labelType: actor.labelType, shape: ACTOR_SHAPES[actor.typ], padding: 10,
			cssClasses: classList("default", "usecase-actor", "usecase-actor-"+actor.typ, when(actor.business, "usecase-business"), actor.classes),
			cssStyles:  append([]string{}, actor.styles...), cssCompiledStyles: compiled(actor.classes),
			actorType: actor.typ, business: actor.business, icon: actor.icon, stereotype: actor.stereotype, parentID: actor.parentID,
		})
	}
	for _, useCase := range model.useCases.values() {
		ellipse := useCase.shape == "ellipse"
		shape := "rect"
		padding := 10.0
		if ellipse {
			shape = "usecaseEllipse"
			if useCase.business {
				shape = "usecaseBusiness"
			}
			padding = 20
		}
		nodes = append(nodes, UsecaseGraphNode{
			id: useCase.id, label: useCase.label, labelType: useCase.labelType, shape: shape, padding: padding,
			cssClasses: classList("default", "usecase-element", "usecase-"+useCase.shape, when(useCase.business, "usecase-business"), useCase.classes),
			cssStyles:  append([]string{}, useCase.styles...), cssCompiledStyles: compiled(useCase.classes),
			business: useCase.business, stereotype: useCase.stereotype, parentID: useCase.parentID,
		})
	}
	for _, note := range model.notes.values() {
		nodes = append(nodes, UsecaseGraphNode{
			id: note.id, label: note.label, labelType: note.labelType, shape: "note", padding: 10,
			cssClasses: "default usecase-note", cssStyles: []string{}, cssCompiledStyles: compiled(nil), noteTarget: note.target,
		})
	}
	for _, json := range model.jsonNodes.values() {
		nodes = append(nodes, UsecaseGraphNode{
			id: json.id, label: json.id, labelType: "text", shape: "usecaseJsonTable", padding: 10,
			cssClasses: classList("default", "usecase-json-table", "", "", json.classes),
			cssStyles:  append([]string{}, json.styles...), cssCompiledStyles: compiled(json.classes),
			jsonRows: flattenJsonRows(json.value, json.propertyOrder), isJSON: true,
		})
	}
	for _, boundary := range model.systemBoundaries.values() {
		nodes = append(nodes, UsecaseGraphNode{
			id: boundary.id, label: boundary.label, labelType: boundary.labelType, shape: "usecaseSystemBoundary", isGroup: true, padding: 20,
			cssClasses: classList("default", "system-boundary", "system-boundary-"+boundary.typ, "", boundary.classes),
			cssStyles:  append([]string{}, boundary.styles...), cssCompiledStyles: compiled(boundary.classes), boundaryType: boundary.typ,
		})
	}

	for _, relationship := range model.relationships {
		semantic := relationship.typ == "include" || relationship.typ == "extend"
		m := markers(relationship)
		animated := relationship.animate || relationship.animation != ""
		animation := relationship.animation
		if animation == "" {
			animation = "fast"
		}
		pattern := "solid"
		if semantic {
			pattern = "dotted"
		}
		classes := classList("default", "relationship", "relationship-"+relationship.typ, "", relationship.classes)
		if animated {
			classes += " edge-animation-" + animation
		}
		edge := UsecaseGraphEdge{
			id: relationship.id, start: relationship.source, end: relationship.target, relationshipType: relationship.typ, pattern: pattern,
			arrowTypeStart: m[0], arrowTypeEnd: m[1], classes: classes,
			style: append([]string{}, relationship.styles...), cssCompiledStyles: compiled(relationship.classes),
			animate: relationship.animate, animation: relationship.animation, minlen: relationship.minlen, explicitID: relationship.explicitID,
		}
		// Include and extend are labelled with their kind, whatever label the relationship carries.
		if semantic {
			label := relationship.typ
			edge.label = &label
			edge.labelType = "text"
		} else if relationship.typ == "association" {
			if relationship.label != nil && *relationship.label != "" {
				edge.label = relationship.label
			}
			if relationship.labelType != "" {
				edge.labelType = relationship.labelType
			}
		}
		edges = append(edges, edge)
	}
	for _, note := range model.notes.values() {
		edges = append(edges, UsecaseGraphEdge{
			id: note.id + "-edge", start: note.id, end: note.target, relationshipType: "note", pattern: "dotted",
			arrowTypeStart: "none", arrowTypeEnd: "none", classes: "default relationship relationship-note",
			style: []string{}, cssCompiledStyles: compiled(nil), minlen: 1, internal: true,
		})
	}
	return UsecaseGraph{nodes: nodes, edges: edges}
}
