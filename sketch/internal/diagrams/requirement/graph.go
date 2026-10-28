// Ported from pele src/diagrams/requirement/graph.ts at 8aff183be57021b533f319a29341a529be934b08.

package requirement

import (
	"strconv"
	"strings"
)

type GraphNode struct {
	id          string
	requirement *Requirement
	element     *RequirementElement
	cssStyles   []string
	cssClasses  string
}

type GraphEdge struct {
	id    string
	start string
	end   string
	// hasStart and hasEnd are false for a name that is neither a requirement nor an element.
	hasStart bool
	hasEnd   bool
	typ      string
	contains bool
}

type RequirementGraph struct {
	nodes     []GraphNode
	edges     []GraphEdge
	direction string
}

// buildRequirementGraph gives the nodes and edges to lay out: requirements first, then elements, as Mermaid orders them.
func buildRequirementGraph(model *RequirementDb) RequirementGraph {
	nodes := make([]GraphNode, 0, len(model.requirements)+len(model.elements))
	for _, requirement := range model.requirements {
		nodes = append(nodes, GraphNode{id: requirement.name, requirement: requirement, cssStyles: requirement.cssStyles,
			cssClasses: strings.Join(requirement.classes, " ")})
	}
	for _, element := range model.elements {
		nodes = append(nodes, GraphNode{id: element.name, element: element, cssStyles: element.cssStyles,
			cssClasses: strings.Join(element.classes, " ")})
	}
	has := func(id string) bool { return model.requirementByName[id] != nil || model.elementByName[id] != nil }
	edges := make([]GraphEdge, len(model.relations))
	for index, relation := range model.relations {
		edges[index] = GraphEdge{
			id:       relation.src + "-" + relation.dst + "-" + strconv.Itoa(index),
			start:    relation.src,
			end:      relation.dst,
			hasStart: has(relation.src),
			hasEnd:   has(relation.dst),
			typ:      relation.typ,
			contains: relation.typ == "contains",
		}
	}
	return RequirementGraph{nodes: nodes, edges: edges, direction: model.direction}
}
