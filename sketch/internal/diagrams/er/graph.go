// Ported from pele src/diagrams/er/graph.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package er

import (
	"strings"

	"tuios.dev/sketch/internal/js"
)

type ErGraphNode struct {
	id         string
	label      string
	alias      string
	attributes []*ErAttribute
	isGroup    bool
	// parentId is undefined when hasParent is false.
	parentId          string
	hasParent         bool
	cssStyles         []string
	cssCompiledStyles []string
	cssClasses        string
	// dir is "" for none.
	dir       string
	labelType string
}

type ErGraphEdge struct {
	id             string
	start          string
	end            string
	label          string
	arrowTypeStart string
	arrowTypeEnd   string
	pattern        string
}

type ErGraph struct {
	nodes []*ErGraphNode
	edges []*ErGraphEdge
}

// Resolves a parsed ER diagram into what is drawn: subgraphs as groups, entities as nodes with
// their class styles, and relationships as edges with a marker type at each end.
func buildErGraph(model *ErDb) ErGraph {
	var nodes []*ErGraphNode
	var edges []*ErGraphEdge
	subgraphs := model.subgraphs

	compiled := func(classNames []string) []string {
		var out []string
		for _, name := range classNames {
			def, ok := model.classes.Get(name)
			if !ok {
				continue
			}
			for _, style := range def.styles {
				out = append(out, js.Trim(style))
			}
			for _, style := range def.textStyles {
				out = append(out, js.Trim(style))
			}
		}
		return out
	}

	parentOf := map[string]string{}
	subgraphIds := map[string]bool{}
	for i := len(subgraphs) - 1; i >= 0; i-- {
		subgraph := subgraphs[i]
		subgraphIds[subgraph.id] = true
		for _, id := range subgraph.nodes {
			parentOf[id] = subgraph.id
		}
	}

	for i := len(subgraphs) - 1; i >= 0; i-- {
		subgraph := subgraphs[i]
		parent, has := parentOf[subgraph.id]
		nodes = append(nodes, &ErGraphNode{
			id:                subgraph.id,
			label:             subgraph.title,
			isGroup:           true,
			parentId:          parent,
			hasParent:         has,
			cssStyles:         subgraph.cssStyles,
			cssCompiledStyles: compiled(subgraph.classes),
			cssClasses:        strings.Join(subgraph.classes, " "),
			dir:               subgraph.dir,
			labelType:         subgraph.labelType,
		})
	}

	// A name that is also a subgraph id stands for the subgraph. Mermaid drops such an entity and
	// leaves relationships written before the subgraph pointing at nothing; here they follow it.
	moved := map[string]string{}
	for _, name := range model.entities.keys {
		entity := model.entities.m[name]
		if subgraphIds[name] {
			moved[entity.id] = name
			continue
		}
		parent, has := parentOf[name]
		nodes = append(nodes, &ErGraphNode{
			id:                entity.id,
			label:             entity.label,
			alias:             entity.alias,
			attributes:        entity.attributes,
			parentId:          parent,
			hasParent:         has,
			cssStyles:         entity.cssStyles,
			cssCompiledStyles: compiled(strings.Split(entity.cssClasses, " ")),
			cssClasses:        entity.cssClasses,
			labelType:         "markdown",
		})
	}

	for index, relationship := range model.relationships {
		start, ok := moved[relationship.entityA]
		if !ok {
			start = relationship.entityA
		}
		end, ok := moved[relationship.entityB]
		if !ok {
			end = relationship.entityB
		}
		pattern := "dashed"
		if relationship.relSpec.relType == "IDENTIFYING" {
			pattern = "solid"
		}
		edges = append(edges, &ErGraphEdge{
			id:             "id_" + relationship.entityA + "_" + relationship.entityB + "_" + js.Int(index),
			start:          start,
			end:            end,
			label:          relationship.roleA,
			arrowTypeStart: strings.ToLower(relationship.relSpec.cardB),
			arrowTypeEnd:   strings.ToLower(relationship.relSpec.cardA),
			pattern:        pattern,
		})
	}

	return ErGraph{nodes: nodes, edges: edges}
}
