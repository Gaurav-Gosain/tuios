package ishikawa

// IshikawaDb is the model: the root is the effect; its children are the categories of causes,
// theirs the causes, and so on.
type IshikawaDb struct {
	title     string
	root      *IshikawaNode
	stack     []levelNode
	baseLevel int
	hasBase   bool
}

type levelNode struct {
	level int
	node  *IshikawaNode
}

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
