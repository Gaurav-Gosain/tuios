// Ported from pele src/diagrams/architecture/db.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// An optional text of pele's model is "" here for none: pele reads each of them only for
// its truthiness or as text, and an id is never empty.

package architecture

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// ArchNode is a service or a junction.
type ArchNode struct {
	id string
	// typ is "service" or "junction".
	typ      string
	icon     string
	iconText string
	title    string
	in       string
}

type ArchGroup struct {
	id    string
	icon  string
	title string
	in    string
}

type ArchEdge struct {
	lhsId    string
	lhsDir   string
	lhsInto  bool
	lhsGroup bool
	rhsId    string
	rhsDir   string
	rhsInto  bool
	rhsGroup bool
	title    string
}

type ArchLayoutHint struct {
	direction string
	members   []string
}

func fail(message string) {
	types.Throw(message, types.CodeSemantic, types.ErrorInfo{Type: "architecture"})
}

type ArchitectureDb struct {
	title    string
	accTitle string
	accDescr string
	// nodes and groups keep the order pele's maps do.
	nodes       []*ArchNode
	nodeById    map[string]*ArchNode
	groups      []*ArchGroup
	edges       []ArchEdge
	layoutHints []ArchLayoutHint
	// registered is "node" or "group" for each id.
	registered map[string]string
}

func newArchitectureDb() *ArchitectureDb {
	return &ArchitectureDb{nodeById: map[string]*ArchNode{}, registered: map[string]string{}}
}

func (db *ArchitectureDb) register(kind, id, parent string) {
	if was, ok := db.registered[id]; ok {
		fail("The " + kind + " id [" + id + "] is already in use by another " + was)
	}
	if parent != "" {
		if id == parent {
			fail("The " + kind + " [" + id + "] cannot be placed within itself")
		}
		was, ok := db.registered[parent]
		if !ok {
			fail("The " + kind + " [" + id + "]'s parent does not exist. Please make sure the parent is created before this " + kind)
		}
		if was == "node" {
			fail("The " + kind + " [" + id + "]'s parent is not a group")
		}
	}
	if kind == "group" {
		db.registered[id] = "group"
	} else {
		db.registered[id] = "node"
	}
}

// setNode: register has refused an id in use, so every node is new.
func (db *ArchitectureDb) setNode(node *ArchNode) {
	db.nodes = append(db.nodes, node)
	db.nodeById[node.id] = node
}

func (db *ArchitectureDb) addService(s ServiceAst) {
	db.register("service", s.id, s.in)
	db.setNode(&ArchNode{id: s.id, typ: "service", icon: s.icon, iconText: s.iconText, title: s.title, in: s.in})
}

func (db *ArchitectureDb) addJunction(j JunctionAst) {
	db.register("junction", j.id, j.in)
	db.setNode(&ArchNode{id: j.id, typ: "junction", in: j.in})
}

func (db *ArchitectureDb) addGroup(g GroupAst) {
	db.register("group", g.id, g.in)
	db.groups = append(db.groups, &ArchGroup{id: g.id, icon: g.icon, title: g.title, in: g.in})
}

func (db *ArchitectureDb) addEdge(e EdgeAst) {
	type end struct {
		side    string
		id      string
		crosses bool
	}
	ends := [2]end{{"left", e.lhsId, e.lhsGroup}, {"right", e.rhsId, e.rhsGroup}}
	for _, en := range ends {
		if _, ok := db.registered[en.id]; !ok {
			fail("The " + en.side + "-hand id [" + en.id + "] does not yet exist. Please create the service/group before declaring an edge to it.")
		}
	}
	// Mermaid lets a group id through the check above and then fails with a TypeError.
	for _, en := range ends {
		if db.nodeById[en.id] == nil {
			fail("The " + en.side + "-hand id [" + en.id + "] is a group. Add {group} to a service inside it instead.")
		}
	}
	parent := db.nodeById[e.lhsId].in
	if parent != "" && parent == db.nodeById[e.rhsId].in {
		for _, en := range ends {
			if en.crosses {
				fail("The " + en.side + "-hand id [" + en.id + "] is modified to traverse the group boundary, but the edge does not pass through two groups.")
			}
		}
	}
	db.edges = append(db.edges, ArchEdge{lhsId: e.lhsId, lhsDir: e.lhsDir, lhsInto: e.lhsInto, lhsGroup: e.lhsGroup,
		rhsId: e.rhsId, rhsDir: e.rhsDir, rhsInto: e.rhsInto, rhsGroup: e.rhsGroup, title: e.title})
}

func (db *ArchitectureDb) addLayoutHint(hint ArchLayoutHint) {
	if len(hint.members) < 2 {
		fail("An align directive requires at least two members; got " + strconv.Itoa(len(hint.members)))
	}
	seen := map[string]bool{}
	for _, id := range hint.members {
		if db.registered[id] != "node" {
			fail("align " + hint.direction + " references [" + id + "], which is not a service or junction")
		}
		if seen[id] {
			fail("align " + hint.direction + " lists [" + id + "] more than once")
		}
		seen[id] = true
	}
	db.layoutHints = append(db.layoutHints, hint)
}

// collapseIndents is text.replace(/\n\s+/g, '\n').
func collapseIndents(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			b.WriteByte(text[i])
			i++
			continue
		}
		b.WriteByte('\n')
		i++
		for i < len(text) {
			r, size := rune(text[i]), 1
			if r >= 0x80 {
				r, size = utf8.DecodeRuneInString(text[i:])
			}
			if !js.IsSpace(r) {
				break
			}
			i += size
		}
	}
	return b.String()
}

// populate is Mermaid's populateDb: every group, then services, junctions, edges, and alignments.
func populate(ast *ArchitectureAst, db *ArchitectureDb) {
	if ast.accDescr != "" {
		db.accDescr = collapseIndents(ast.accDescr)
	}
	if ast.accTitle != "" {
		db.accTitle = js.TrimStart(ast.accTitle)
	}
	if ast.title != "" {
		db.title = ast.title
	}
	for _, group := range ast.groups {
		db.addGroup(group)
	}
	for _, service := range ast.services {
		db.addService(service)
	}
	for _, junction := range ast.junctions {
		db.addJunction(junction)
	}
	for _, edge := range ast.edges {
		db.addEdge(edge)
	}
	for _, alignment := range ast.alignments {
		db.addLayoutHint(ArchLayoutHint{direction: alignment.direction, members: append([]string{}, alignment.members...)})
	}
}
