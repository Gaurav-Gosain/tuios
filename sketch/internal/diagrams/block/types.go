// Ported from pele src/diagrams/block/types.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
//
// An optional field of pele's block is "" or nil here for none, where pele reads it only
// for its truthiness or as text. The numbers that pele reads with `?? 1` or `?? -1` start
// at that value.

package block

// Block is one shape for blocks, edges, and the statements that set columns, classes, and styles, as in Mermaid.
type Block struct {
	// id is "" for none: a composite whose header was a link statement.
	id             string
	typ            string
	label          string
	children       []*Block
	columns        float64
	widthInColumns float64
	// Number of cells a `space` statement asked for.
	width      float64
	directions []string
	classes    []string
	styles     []string
	colorIndex int
	start      string
	end        string
	// arrowTypeStart and arrowTypeEnd are Mermaid marker names; "" draws nothing.
	arrowTypeStart string
	arrowTypeEnd   string
	thickness      string
	pattern        string
	css            string
	styleClass     string
	stylesStr      string
}

func newBlock() *Block {
	return &Block{columns: -1, widthInColumns: 1, width: 1}
}

type BlockClassDef struct {
	id         string
	styles     []string
	textStyles []string
}
