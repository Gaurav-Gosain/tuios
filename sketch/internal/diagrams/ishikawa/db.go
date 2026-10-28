// Ported from pele src/diagrams/ishikawa/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package ishikawa

// IshikawaDb is the model: the root is the effect; its children are the categories of causes,
// theirs the causes, and so on.
type IshikawaDb struct {
	title string
	root  *IshikawaNode
	// The line of ancestors of the last node, each with its level.
	stack     []levelNode
	baseLevel int
	hasBase   bool
}

type levelNode struct {
	level int
	node  *IshikawaNode
}

// addNode: the first line is the effect. Every later line is a cause, nested by its indentation relative
// to the first cause, so the effect itself may be indented more than the causes.
func (db *IshikawaDb) addNode(rawLevel int, text string) {
	node := &IshikawaNode{text: text}
	if db.root == nil {
		db.root = node
		db.stack = []levelNode{{0, node}}
		return
	}
	if !db.hasBase {
		db.baseLevel, db.hasBase = rawLevel, true
	}
	level := max(rawLevel-db.baseLevel+1, 1)
	for len(db.stack) > 1 && db.stack[len(db.stack)-1].level >= level {
		db.stack = db.stack[:len(db.stack)-1]
	}
	parent := db.stack[len(db.stack)-1].node
	parent.children = append(parent.children, node)
	db.stack = append(db.stack, levelNode{level, node})
}
