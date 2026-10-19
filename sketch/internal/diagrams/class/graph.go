// Ported from pele src/diagrams/class/graph.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package class

import "strconv"

// NodeShape is 'rect', 'classBox', 'note' or 'interface'.
type NodeShape = string

// EndMarker is 'aggregation', 'extension', 'composition', 'dependency', 'lollipop' or 'none'.
type EndMarker = string

type GraphNode struct {
	ID       string
	Label    string
	IsGroup  bool
	Shape    NodeShape
	ParentID *string
	// Index of the namespace node this one is drawn in, or -1.
	Parent    int
	ClassNode *ClassNode
	// Position among the classes, in declaration order. Namespaces and notes have none (-1).
	ColorIndex int
}

type GraphEdge struct {
	ID    string
	Start string
	End   string
	// Indexes into the node list. Ids alone are ambiguous: a class may be named `note0` or `interface0`.
	Source          int
	Target          int
	Label           string
	ArrowTypeStart  EndMarker
	ArrowTypeEnd    EndMarker
	StartLabelRight string
	EndLabelLeft    string
	// 'solid', 'dashed' or 'dotted'.
	Pattern string
	Classes string
}

type ClassGraph struct {
	Nodes []*GraphNode
	Edges []*GraphEdge
}

var MARKERS = [...]EndMarker{"aggregation", "extension", "composition", "dependency", "lollipop"}

func marker(typ RelationEnd) EndMarker {
	if typ >= 0 && typ < len(MARKERS) {
		return MARKERS[typ]
	}
	return "none"
}

// Resolves a parsed class diagram into the nodes and edges that are drawn. With `hierarchical`
// off, only namespaces the source declares are kept and each is labeled with its full name.
func buildClassGraph(model *ClassDb, hierarchical bool) ClassGraph {
	var nodes []*GraphNode
	var edges []*GraphEdge
	namespaceAt := map[string]int{}
	classAt := map[string]int{}
	interfaceAt := map[string]int{}

	declared := map[string]*string{}
	declaredAncestor := func(id *string) *string {
		if hierarchical || id == nil {
			return id
		}
		var chain []string
		current := id
		var found *string
		for current != nil && *current != "" {
			if f, ok := declared[*current]; ok {
				found = f
				break
			}
			ns, ok := model.Namespaces.Get(*current)
			if !ok {
				break
			}
			if ns.Explicit {
				found = current
				break
			}
			chain = append(chain, *current)
			current = ns.Parent
		}
		for _, visited := range chain {
			declared[visited] = found
		}
		return found
	}
	group := func(id *string) int {
		if id == nil {
			return -1
		}
		if at, ok := namespaceAt[*id]; ok {
			return at
		}
		return -1
	}

	for _, ns := range model.Namespaces.Values() {
		if !hierarchical && !ns.Explicit {
			continue
		}
		namespaceAt[ns.ID] = len(nodes)
		node := &GraphNode{ID: ns.ID, Label: ns.ID, IsGroup: true, Shape: "rect", Parent: -1, ColorIndex: -1}
		if hierarchical {
			node.Label = ns.Label
			node.ParentID = ns.Parent
		}
		nodes = append(nodes, node)
	}
	for _, node := range nodes {
		node.Parent = group(node.ParentID)
	}

	colorIndex := 0
	for _, classNode := range model.Classes.Values() {
		parentId := declaredAncestor(classNode.Parent)
		classAt[classNode.ID] = len(nodes)
		nodes = append(nodes, &GraphNode{
			ID:         classNode.ID,
			Label:      classNode.Label,
			Shape:      "classBox",
			ParentID:   parentId,
			Parent:     group(parentId),
			ClassNode:  classNode,
			ColorIndex: colorIndex,
		})
		colorIndex++
	}

	for _, note := range model.Notes.Values() {
		parentId := declaredAncestor(note.Parent)
		target := -1
		if note.Class != nil {
			if at, ok := classAt[*note.Class]; ok {
				target = at
			}
		}
		if target != -1 {
			edges = append(edges, &GraphEdge{
				ID:             "edgeNote" + strconv.Itoa(note.Index),
				Start:          note.ID,
				End:            nodes[target].ID,
				Source:         len(nodes),
				Target:         target,
				ArrowTypeStart: "none",
				ArrowTypeEnd:   "none",
				Pattern:        "dotted",
				Classes:        "relation",
			})
		}
		nodes = append(nodes, &GraphNode{ID: note.ID, Label: note.Text, Shape: "note", ParentID: parentId, Parent: group(parentId), ColorIndex: -1})
	}

	// Mermaid leaves every lollipop interface outside the namespaces. Here it stays beside its class.
	for _, item := range model.Interfaces {
		parent := -1
		if owner, ok := classAt[item.ClassID]; ok {
			parent = nodes[owner].Parent
		}
		interfaceAt[item.ID] = len(nodes)
		nodes = append(nodes, &GraphNode{ID: item.ID, Label: item.Label, Shape: "interface", Parent: parent, ColorIndex: -1})
	}

	count := 0
	for _, relation := range model.Relations {
		count++
		type1, type2 := relation.Relation.Type1, relation.Relation.Type2
		// addRelation swaps in an interface for the lollipop end of a relation with no other marker.
		startInterface := type1 == RELATION_TYPE_LOLLIPOP && type2 == NONE
		endInterface := !startInterface && type2 == RELATION_TYPE_LOLLIPOP && type1 == NONE
		startAt, endAt := classAt, classAt
		if startInterface {
			startAt = interfaceAt
		}
		if endInterface {
			endAt = interfaceAt
		}
		source, ok := startAt[relation.ID1]
		if !ok {
			source = -1
		}
		target, ok := endAt[relation.ID2]
		if !ok {
			target = -1
		}
		edge := &GraphEdge{
			ID:              "id_" + relation.ID1 + "_" + relation.ID2 + "_" + strconv.Itoa(count),
			Start:           relation.ID1,
			End:             relation.ID2,
			Source:          source,
			Target:          target,
			ArrowTypeStart:  marker(type1),
			ArrowTypeEnd:    marker(type2),
			StartLabelRight: relation.RelationTitle1,
			EndLabelLeft:    relation.RelationTitle2,
			Pattern:         "solid",
			Classes:         "relation",
		}
		if relation.Title != nil {
			edge.Label = *relation.Title
		}
		if relation.RelationTitle1 == "none" {
			edge.StartLabelRight = ""
		}
		if relation.RelationTitle2 == "none" {
			edge.EndLabelLeft = ""
		}
		if relation.Relation.LineType == 1 {
			edge.Pattern = "dashed"
		}
		edges = append(edges, edge)
	}

	return ClassGraph{Nodes: nodes, Edges: edges}
}
