package block

// Block is one shape for blocks, edges, and the statements that set columns, classes, and styles, as in Mermaid.
type Block struct {
	id             string
	typ            string
	label          string
	children       []*Block
	columns        float64
	widthInColumns float64
	width          float64
	directions     []string
	classes        []string
	styles         []string
	colorIndex     int
	start          string
	end            string
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
