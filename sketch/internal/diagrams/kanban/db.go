// Ported from pele src/diagrams/kanban/db.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// An optional field of pele's node is "" here for none: pele reads each of them only
// for its truthiness or as text.

package kanban

import (
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

type KanbanNode struct {
	id         string
	level      int
	label      string
	icon       string
	assigned   string
	ticket     string
	priority   string
	cssClasses string
	// The cards of a section; nil for a card.
	items []*KanbanNode
}

type KanbanDb struct {
	title    string
	sections []*KanbanNode

	last         *KanbanNode
	sectionLevel int
	misplaced    *KanbanNode
}

func (db *KanbanDb) GetType(start, end string) int { return common.GetType(start, end) }

func (db *KanbanDb) AddNode(level int, id, descr string, _ int, shapeData *string) {
	node := &KanbanNode{id: id, level: level, label: descr}

	if shapeData != nil {
		yaml := "{\n" + *shapeData + "\n}"
		if strings.Contains(*shapeData, "\n") {
			yaml = *shapeData + "\n"
		}
		doc, err := util.ParseYaml(yaml)
		if err != nil {
			types.Throw(`Metadata of "`+node.id+`" is not valid YAML. `+err.Error(), types.CodeSyntax, types.ErrorInfo{Type: "kanban"})
		}
		// Mermaid throws a TypeError on metadata that is empty or not a mapping. Here it is ignored.
		if m, ok := doc.(map[string]any); ok {
			shape := ""
			if js.Truthy(m["shape"]) {
				shape = js.String(m["shape"])
			}
			if shape != strings.ToLower(shape) || strings.Contains(shape, "_") {
				types.Throw("No such shape: "+shape+". Shape names should be lowercase.", types.CodeSemantic, types.ErrorInfo{Type: "kanban"})
			}
			text := func(key string, field *string) {
				if js.Truthy(m[key]) {
					*field = js.String(m[key])
				}
			}
			text("label", &node.label)
			text("icon", &node.icon)
			text("assigned", &node.assigned)
			text("ticket", &node.ticket)
			text("priority", &node.priority)
		}
	}

	// A node indented less than the first column becomes a card, and Mermaid rejects whatever follows it.
	if db.misplaced != nil {
		types.Throw(`Items without section detected, found section ("`+db.misplaced.label+`")`, types.CodeSemantic, types.ErrorInfo{Type: "kanban"})
	}
	if len(db.sections) == 0 {
		db.sectionLevel = level
	}
	db.last = node
	if level == db.sectionLevel {
		node.items = []*KanbanNode{}
		db.sections = append(db.sections, node)
	} else {
		section := db.sections[len(db.sections)-1]
		section.items = append(section.items, node)
		if level < db.sectionLevel {
			db.misplaced = node
		}
	}
}

// DecorateNode: Mermaid throws a TypeError when a decoration comes before any node. Here it is ignored.
func (db *KanbanDb) DecorateNode(decoration common.Decoration) {
	node := db.last
	if node == nil {
		return
	}
	if decoration.Icon != "" {
		node.icon = decoration.Icon
	}
	if decoration.Class != "" {
		node.cssClasses = decoration.Class
	}
}
