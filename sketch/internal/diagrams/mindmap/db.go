package mindmap

import (
	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/types"
)

type MindmapNode struct {
	id       int
	nodeId   string
	level    int
	descr    string
	typ      int
	children []*MindmapNode
	isRoot   bool
	section  int
	class    string
	icon     string
}

const SECTIONS = 11

type MindmapDb struct {
	title string
	nodes []*MindmapNode

	baseLevel int
	open      []*MindmapNode
}

func (db *MindmapDb) GetType(start, end string) int { return common.GetType(start, end) }

func (db *MindmapDb) AddNode(level int, id, descr string, typ int, _ *string) {
	isRoot := len(db.nodes) == 0
	if isRoot {
		db.baseLevel = level
	}
	level -= db.baseLevel

	open := db.open
	depth := len(open)
	for depth > 0 && open[depth-1].level >= level {
		depth--
	}
	var parent *MindmapNode
	if depth > 0 {
		parent = open[depth-1]
	}
	if parent == nil && !isRoot {
		types.Throw(`There can be only one root. No parent could be found for ("`+descr+`")`, types.CodeSemantic, types.ErrorInfo{Type: "mindmap"})
	}
	node := &MindmapNode{id: len(db.nodes), nodeId: id, level: level, descr: descr, typ: typ, isRoot: isRoot}
	if parent != nil {
		if parent.isRoot {
			node.section = len(parent.children) % SECTIONS
		} else {
			node.section = parent.section
		}
		parent.children = append(parent.children, node)
	}
	db.open = append(open[:depth], node)
	db.nodes = append(db.nodes, node)
}

// DecorateNode: Mermaid throws a TypeError when a decoration comes before any node. Here it is ignored.
func (db *MindmapDb) DecorateNode(decoration common.Decoration) {
	if len(db.nodes) == 0 {
		return
	}
	node := db.nodes[len(db.nodes)-1]
	if decoration.Icon != "" {
		node.icon = decoration.Icon
	}
	if decoration.Class != "" {
		node.class = decoration.Class
	}
}
