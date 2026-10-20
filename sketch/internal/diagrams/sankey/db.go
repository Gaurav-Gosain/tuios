// Ported from pele src/diagrams/sankey/db.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

package sankey

type SankeyNode struct {
	id    string
	index int
}

type SankeyLink struct {
	source *SankeyNode
	target *SankeyNode
	value  float64
}

type SankeyDb struct {
	nodes    []*SankeyNode
	links    []SankeyLink
	title    string
	accTitle string
	accDescr string

	lookup map[string]*SankeyNode
}

func newSankeyDb() *SankeyDb {
	return &SankeyDb{lookup: map[string]*SankeyNode{}}
}

func (db *SankeyDb) findOrCreateNode(id string) *SankeyNode {
	node, ok := db.lookup[id]
	if !ok {
		node = &SankeyNode{id: id, index: len(db.nodes)}
		db.lookup[id] = node
		db.nodes = append(db.nodes, node)
	}
	return node
}

func (db *SankeyDb) addLink(source, target *SankeyNode, value float64) {
	db.links = append(db.links, SankeyLink{source, target, value})
}
