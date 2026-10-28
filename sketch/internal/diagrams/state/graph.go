// Ported from pele src/diagrams/state/graph.ts at 8aff183be57021b533f319a29341a529be934b08.

package state

import (
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type StateNode struct {
	ID string
	// rect, rectWithTitle, stateStart, stateEnd, choice, fork, join, divider, roundedWithTitle, note or noteGroup.
	// "" for none.
	Shape             string
	Label             string
	Description       []string
	CssClasses        string
	CssCompiledStyles []string
	CssStyles         []string
	// Type is "" for none.
	Type     string
	IsGroup  bool
	ParentID *string
	// The direction inside a composite: its last `direction` statement, or TB. "" for none.
	Dir      string
	Position *string
	NoteFor  *string
	// ColorIndex is nil for none. hasColor is set where pele's object has the key, even undefined.
	ColorIndex *int
	hasColor   bool
}

// Labels are markdown throughout, as in Mermaid.
type StateEdge struct {
	ID      string
	Start   string
	End     string
	Label   string
	Classes string
	// Pattern is "dashed" or "".
	Pattern string
}

type StateGraph struct {
	Nodes []*StateNode
	Edges []StateEdge
}

type draft struct {
	shape       string
	description Description
	cssClasses  string
	cssStyles   []string
	typ         string
	dir         string
	colorIndex  *int
}

type graphFrame struct {
	item *StateStmt
	doc  []Stmt
	at   int
	alt  bool
}

func direction(doc []Stmt) string {
	return docDirection(doc)
}

// flat is `[description].flat()`.
func flat(d *Description) []string {
	if d.IsList {
		return append([]string{}, d.List...)
	}
	return []string{d.Text}
}

// Mermaid's dataFetcher: walks the documents and resolves them into the nodes and edges that are drawn.
func buildStateGraph(rootDoc []Stmt, states map[string]*StateStmt, classes map[string]*StateClassDef) StateGraph {
	var nodes []*StateNode
	var edges []StateEdge
	byId := map[string]*StateNode{}
	drafts := map[string]*draft{}
	slots := map[string]*int{}
	counter := 0
	nextSlot := 0

	insert := func(node *StateNode) {
		// The two literal ids are what DOMPurify leaves of `<<fork>>` and `<<choice>>` in Mermaid; kept for parity.
		if node.ID == "" || node.ID == "</join></fork>" || node.ID == "</choice>" {
			return
		}
		if node.CssClasses != "" {
			// One copy, however many classes: a state named in a class statement many times has a long list.
			var compiled []string
			have := false
			for _, name := range strings.Split(node.CssClasses, " ") {
				def := classes[name]
				if def == nil {
					continue
				}
				if !have {
					compiled = append([]string{}, node.CssCompiledStyles...)
					have = true
				}
				compiled = append(compiled, def.Styles...)
			}
			if have {
				node.CssCompiledStyles = compiled
			}
		}
		existing := byId[node.ID]
		if existing != nil {
			parentId := node.ParentID
			if parentId == nil {
				parentId = existing.ParentID
			}
			// Object.assign: the keys pele's object has.
			existing.ID = node.ID
			existing.Shape = node.Shape
			existing.Label = node.Label
			existing.Description = node.Description
			existing.CssClasses = node.CssClasses
			existing.CssCompiledStyles = node.CssCompiledStyles
			existing.CssStyles = node.CssStyles
			existing.Type = node.Type
			existing.IsGroup = node.IsGroup
			existing.Dir = node.Dir
			if node.hasColor {
				existing.ColorIndex = node.ColorIndex
				existing.hasColor = true
			}
			if node.Position != nil {
				existing.Position = node.Position
			}
			if node.NoteFor != nil {
				existing.NoteFor = node.NoteFor
			}
			if parentId != nil {
				existing.ParentID = parentId
			}
		} else {
			byId[node.ID] = node
			nodes = append(nodes, node)
		}
	}

	visit := func(parent *StateStmt, item *StateStmt, alt bool) {
		id := item.ID
		if id == "root" {
			return
		}
		known := states[id]
		classStr := ""
		styles := []string{}
		if known != nil {
			classStr = strings.Join(known.Classes, " ")
			if known.Styles != nil {
				styles = known.Styles
			}
		}

		d := drafts[id]
		if d == nil {
			shape := "rect"
			if item.Start != nil {
				if *item.Start {
					shape = "stateStart"
				} else {
					shape = "stateEnd"
				}
			}
			if item.Type != "default" {
				shape = item.Type
			}
			d = &draft{
				shape:       shape,
				description: Description{Text: id},
				cssClasses:  classStr + " statediagram-state",
				cssStyles:   styles,
			}
			drafts[id] = d
		}

		if item.Description.truthy() {
			if d.description.IsList {
				d.shape = "rectWithTitle"
				// pele copies the list here. The draft owns its list (every branch below makes a new one),
				// so appending in place is the same and keeps a state with many descriptions linear.
				d.description.List = append(d.description.List, flat(item.Description)...)
			} else if d.description.Text != "" {
				d.shape = "rectWithTitle"
				if d.description.Text == id {
					d.description = Description{IsList: true, List: flat(item.Description)}
				} else {
					d.description = Description{IsList: true, List: append([]string{d.description.Text}, flat(item.Description)...)}
				}
			} else {
				d.shape = "rect"
				if item.Description.IsList {
					d.description = Description{IsList: true, List: append([]string{}, item.Description.List...)}
				} else {
					d.description = Description{Text: item.Description.Text}
				}
			}
		}
		length := js.Len(d.description.Text)
		if d.description.IsList {
			length = len(d.description.List)
		}
		if length == 1 && d.shape == "rectWithTitle" {
			if d.typ == "group" {
				d.shape = "roundedWithTitle"
			} else {
				d.shape = "rect"
			}
		}

		if d.typ == "" && item.Doc != nil {
			d.typ = "group"
			d.dir = direction(*item.Doc)
			if item.Type == "divider" {
				d.shape = "divider"
			} else {
				d.shape = "roundedWithTitle"
			}
			// A container takes the next palette slot unless it is a region, which shares its composite's.
			has := false
			if parent != nil {
				_, has = slots[parent.ID]
			}
			if d.shape == "divider" && has {
				d.colorIndex = slots[parent.ID]
			} else {
				slot := nextSlot
				nextSlot++
				if js.Trim(classStr) != "" || len(styles) > 0 {
					d.colorIndex = nil
				} else {
					d.colorIndex = &slot
				}
			}
			slots[id] = d.colorIndex
			alternate := ""
			if alt {
				alternate = "statediagram-cluster-alt"
			}
			d.cssClasses = d.cssClasses + " statediagram-cluster " + alternate
		}

		full := d.description
		node := &StateNode{
			ID:                id,
			Shape:             d.shape,
			Description:       []string{},
			CssClasses:        d.cssClasses,
			CssCompiledStyles: []string{},
			CssStyles:         d.cssStyles,
			Type:              d.typ,
			IsGroup:           d.typ == "group",
			Dir:               d.dir,
			ColorIndex:        d.colorIndex,
			hasColor:          true,
		}
		if full.IsList {
			if len(full.List) > 0 {
				node.Label = full.List[0]
				// Shared, not copied: a state is visited once per statement, and nothing writes the list.
				// The capped capacity makes an append copy rather than write into the draft's list.
				node.Description = full.List[1:len(full.List):len(full.List)]
			}
		} else {
			node.Label = full.Text
		}
		if node.Shape == "divider" {
			node.Label = ""
		}
		if parent != nil && parent.ID != "root" {
			parentId := parent.ID
			node.ParentID = &parentId
		}

		note := item.Note
		if note == nil {
			insert(node)
			return
		}
		// Mermaid puts each note in a group of its own, named after the state. Pele keeps that
		// group in the model and lays the note out beside its state instead.
		aside := func(asideId, shape, cssClasses string, isGroup bool) *StateNode {
			typ := "node"
			if isGroup {
				typ = "group"
			}
			position := note.Position
			return &StateNode{
				ID:                asideId,
				Shape:             shape,
				Label:             note.Text,
				Description:       []string{},
				CssClasses:        cssClasses,
				CssCompiledStyles: []string{},
				CssStyles:         []string{},
				Type:              typ,
				IsGroup:           isGroup,
				Position:          &position,
			}
		}
		noteNode := aside(id+"----note-"+strconv.Itoa(counter), "note", "statediagram-note", false)
		counter++
		groupId := id + "----parent"
		noteNode.ParentID = &groupId
		noteFor := id
		noteNode.NoteFor = &noteFor
		insert(aside(groupId, "noteGroup", d.cssClasses, true))
		insert(noteNode)
		insert(node)
		left := note.Position == "left of"
		from, to := id, noteNode.ID
		if left {
			from, to = noteNode.ID, id
		}
		edges = append(edges, StateEdge{ID: from + "-" + to, Start: from, End: to, Label: "", Classes: "transition note-edge", Pattern: "dashed"})
	}

	stack := []*graphFrame{{item: nil, doc: rootDoc, at: 0, alt: true}}
	for len(stack) > 0 {
		frame := stack[len(stack)-1]
		if frame.at >= len(frame.doc) {
			stack = stack[:len(stack)-1]
			continue
		}
		item := frame.doc[frame.at]
		frame.at++
		switch item := item.(type) {
		case *StateStmt:
			visit(frame.item, item, frame.alt)
			if item.Doc != nil {
				stack = append(stack, &graphFrame{item: item, doc: *item.Doc, at: 0, alt: !frame.alt})
			}
		case *RelationStmt:
			visit(frame.item, item.State1, frame.alt)
			visit(frame.item, item.State2, frame.alt)
			label := ""
			if item.Description != nil {
				label = *item.Description
			}
			edges = append(edges, StateEdge{ID: "edge" + strconv.Itoa(counter), Start: item.State1.ID, End: item.State2.ID, Label: label, Classes: "transition"})
			counter++
		}
	}

	for _, node := range nodes {
		if node.IsGroup && len(node.Description) > 0 {
			types.Throw(
				"Group nodes can only have label. Remove the additional description for node ["+node.ID+"]",
				types.CodeSemantic,
				types.ErrorInfo{Type: "state"},
			)
		}
	}
	return StateGraph{nodes, edges}
}
