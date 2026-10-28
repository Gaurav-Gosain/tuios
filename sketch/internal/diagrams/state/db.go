// Ported from pele src/diagrams/state/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package state

import (
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
)

// Mermaid's StateDB in its version 2 form, which is what Mermaid uses for both `stateDiagram`
// and `stateDiagram-v2`. The legacy version, which numbers `[*]` states, is not ported.
type StateDb struct {
	RootDoc   []Stmt
	States    map[string]*StateStmt
	Relations []StateRelation
	Classes   map[string]*StateClassDef
	Links     map[string]StateLink
	Graph     StateGraph
	Title     string
	AccTitle  string
	AccDescr  string

	dividerCount int
}

func NewStateDb() *StateDb {
	return &StateDb{
		States:  map[string]*StateStmt{},
		Classes: map[string]*StateClassDef{},
		Links:   map[string]StateLink{},
	}
}

// docDirection is the last `direction` statement of a document, or TB.
func docDirection(doc []Stmt) string {
	dir := "TB"
	for _, item := range doc {
		if d, ok := item.(*DirectionStmt); ok {
			dir = d.Value
		}
	}
	return dir
}

// The direction the root document is laid out in: its last `direction` statement.
func (db *StateDb) Direction() string {
	return docDirection(db.RootDoc)
}

func (db *StateDb) setRootDoc(doc []Stmt) {
	db.RootDoc = doc
	db.translate()
	db.extract(doc)
}

// Only the root document's statements become states, relations and classes here; the
// statements inside composites are read when the graph is built.
func (db *StateDb) extract(doc []Stmt) {
	db.States = map[string]*StateStmt{}
	db.Relations = nil
	db.Classes = map[string]*StateClassDef{}
	for _, item := range doc {
		switch item := item.(type) {
		case *StateStmt:
			db.addState(js.Trim(item.ID), item.Type, item.Doc, item.Description, item.Note, nil)
		case *RelationStmt:
			title := ""
			if item.Description != nil {
				title = *item.Description
			}
			db.addRelation(item.State1, item.State2, title)
		case *ClassDefStmt:
			db.addStyleClass(js.Trim(item.ID), item.Classes)
		case *StyleStmt:
			parts := strings.Split(item.StyleClass, ",")
			styles := make([]string, len(parts))
			for k, s := range parts {
				styles[k] = js.Trim(strings.ReplaceAll(s, ";", ""))
			}
			for _, state := range db.named(js.Trim(item.ID)) {
				state.Styles = append([]string{}, styles...)
			}
		case *ApplyClassStmt:
			db.setCssClass(js.Trim(item.ID), item.StyleClass)
		case *ClickStmt:
			db.addLink(item.ID.ID, item.URL, item.Tooltip)
		}
	}
	db.Graph = buildStateGraph(doc, db.States, db.Classes)
}

type pendingItem struct {
	parentId string
	node     any
	first    bool
}

// Mermaid's docTranslator: gives each `[*]` an id scoped to its parent, and turns a document
// split by `--` into one region per part.
func (db *StateDb) translate() {
	rootDoc := db.RootDoc
	pending := []pendingItem{{"root", &rootStmt{ID: "root", Doc: &rootDoc}, true}}
	for len(pending) > 0 {
		top := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		parentId, first := top.parentId, top.first
		var id string
		var doc **[]Stmt
		switch node := top.node.(type) {
		case *RelationStmt:
			pending = append(pending, pendingItem{parentId, node.State2, false}, pendingItem{parentId, node.State1, true})
			continue
		case *StateStmt:
			if node.ID == "[*]" {
				if first {
					node.ID = parentId + "_start"
				} else {
					node.ID = parentId + "_end"
				}
				node.Start = &first
			} else {
				node.ID = js.Trim(node.ID)
			}
			id = node.ID
			doc = &node.Doc
		case *rootStmt:
			id = node.ID
			doc = &node.Doc
		default:
			continue
		}
		if *doc == nil {
			continue
		}

		var regions []Stmt
		var current []Stmt
		for _, item := range **doc {
			if s, ok := item.(*StateStmt); ok && s.Type == "divider" && s.Doc == nil {
				region := *s
				cur := current
				region.Doc = &cur
				regions = append(regions, &region)
				current = nil
			} else {
				current = append(current, item)
			}
		}
		// Mermaid names the region after the last divider with a random id; here it continues the
		// divider numbering. Mermaid also leaves a document that ends with a divider ungrouped and
		// then fails to draw it; here the regions before that divider are kept.
		if len(regions) > 0 {
			if len(current) > 0 {
				cur := current
				regions = append(regions, &StateStmt{ID: db.getDividerId(), Type: "divider", Doc: &cur})
			}
			*doc = &regions
		}
		items := **doc
		for i := len(items) - 1; i >= 0; i-- {
			pending = append(pending, pendingItem{id, items[i], true})
		}
	}
}

func (db *StateDb) addState(id string, typ string, doc *[]Stmt, descr *Description, note *StateNote, classes []string) {
	if typ == "" {
		typ = "default"
	}
	trimmedId := js.Trim(id)
	state := db.States[trimmedId]
	if state == nil {
		state = &StateStmt{ID: trimmedId, Descriptions: []string{}, Type: typ, Doc: doc, Note: note, Classes: []string{}, Styles: []string{}, TextStyles: []string{}}
		db.States[trimmedId] = state
	} else {
		if state.Doc == nil {
			state.Doc = doc
		}
		if state.Type == "" {
			state.Type = typ
		}
	}
	if descr.truthy() {
		if descr.IsList {
			for _, text := range descr.List {
				db.addDescription(trimmedId, js.Trim(text))
			}
		} else {
			db.addDescription(trimmedId, js.Trim(descr.Text))
		}
	}
	if note != nil {
		state.Note = note
	}
	for _, name := range classes {
		db.setCssClass(trimmedId, js.Trim(name))
	}
}

func (db *StateDb) addLink(stateId, url, tooltip string) {
	db.Links[stateId] = StateLink{url, tooltip}
}

func (db *StateDb) addRelation(item1, item2 *StateStmt, title string) {
	for _, item := range []*StateStmt{item1, item2} {
		db.addState(item.ID, item.Type, item.Doc, item.Description, item.Note, item.Classes)
	}
	db.Relations = append(db.Relations, StateRelation{js.Trim(item1.ID), js.Trim(item2.ID), title})
}

func (db *StateDb) addDescription(id, descr string) {
	state := db.States[id]
	if state == nil {
		return
	}
	if strings.HasPrefix(descr, ":") {
		descr = js.Trim(descr[1:])
	}
	state.Descriptions = append(state.Descriptions, descr)
}

func (db *StateDb) getDividerId() string {
	db.dividerCount++
	return "divider-id-" + strconv.Itoa(db.dividerCount)
}

func (db *StateDb) addStyleClass(id, styleAttributes string) {
	found := db.Classes[id]
	if found == nil {
		found = &StateClassDef{ID: id, Styles: []string{}, TextStyles: []string{}}
		db.Classes[id] = found
	}
	if styleAttributes == "" {
		return
	}
	for _, attrib := range strings.Split(styleAttributes, ",") {
		fixed := js.Trim(strings.Replace(attrib, ";", "", 1))
		if strings.Contains(attrib, "color") {
			found.TextStyles = append(found.TextStyles, strings.Replace(strings.Replace(fixed, "fill", "bgFill", 1), "color", "fill", 1))
		}
		found.Styles = append(found.Styles, fixed)
	}
}

func (db *StateDb) setCssClass(itemIds, cssClassName string) {
	for _, state := range db.named(itemIds) {
		state.Classes = append(state.Classes, cssClassName)
	}
}

// The grammar calls this while it is still building the document, so the statement lands in the
// previous root document and the direction is read from the parsed `dir` statements instead.
func (db *StateDb) setDirection(dir string) {
	for _, item := range db.RootDoc {
		if d, ok := item.(*DirectionStmt); ok {
			d.Value = dir
			return
		}
	}
	db.RootDoc = append([]Stmt{&DirectionStmt{dir}}, db.RootDoc...)
}

func (db *StateDb) trimColon(text string) string {
	if strings.HasPrefix(text, ":") {
		return js.Trim(text[1:])
	}
	return js.Trim(text)
}

func (db *StateDb) setAccTitle(text string) {
	db.AccTitle = js.TrimStart(text)
}

var reAccDescr = lazyre.New(`\n[` + js.S + `]+`)

func (db *StateDb) setAccDescription(text string) {
	db.AccDescr = reAccDescr.ReplaceAllString(text, "\n")
}

// The states a comma-separated list of ids names, created where they do not exist yet. As in
// Mermaid, an id is looked up as written and trimmed only when it has to be created.
func (db *StateDb) named(ids string) []*StateStmt {
	var out []*StateStmt
	for _, id := range strings.Split(ids, ",") {
		state := db.States[id]
		if state == nil {
			db.addState(id, "", nil, nil, nil, nil)
			state = db.States[js.Trim(id)]
		}
		if state != nil {
			out = append(out, state)
		}
	}
	return out
}
