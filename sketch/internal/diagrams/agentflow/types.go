package agentflow

// Metadata is a parsed metadata block: map keys are YAML keys, values as util.ParseYaml gives them.
type Metadata = map[string]any

type FlowText struct {
	Text string
	Type string
}

// Loc is a source span as Jison reports it: lines start at 1, columns at 0. Columns count UTF-16 units.
// A FirstLine of 0 is no span.
type Loc struct {
	FirstLine, FirstColumn, LastLine, LastColumn int
}

type AgentNode struct {
	ID          string
	Text        *string
	LabelType   string
	Type        *string
	Styles      []string
	Classes     []string
	Dir         *string
	Props       map[string]string
	Link        *string
	LinkTarget  *string
	Metadata    Metadata
	IsConnector bool
}

type AgentEdge struct {
	Start string
	End   string
	// Type and Stroke are "" for none.
	Type         string
	Text         string
	LabelType    string
	Stroke       string
	EdgeSemantic string
	// Length is -1 for none.
	Length      int
	Style       []string
	Classes     []string
	ID          *string
	UserDefined bool
	Interpolate *string
	// Animate and Animation are nil for none, or the YAML value.
	Animate   any
	Animation any
	Metadata  Metadata
}

type AgentClassDef struct {
	ID         string
	Styles     []string
	TextStyles []string
}

type AgentSubgraph struct {
	ID        string
	Nodes     []string
	Title     string
	Classes   []string
	Dir       *string
	LabelType string
	Type      string
	Metadata  Metadata
}

type LinkInfo struct {
	Type   string
	Stroke string
	// Length is -1 for none.
	Length       int
	EdgeSemantic string
	Text         *FlowText
	ID           *string
}

// DocItem is a statement of a block: a string, a list of node ids, or a direction.
type DocItem struct {
	Text string
	IDs  []string
	Dir  string
	Kind int
}

const (
	docText = iota
	docIDs
	docDir
)

type Diagnostic struct {
	ID       string
	Severity string
	Message  string
	NodeID   string
	EdgeID   string
}
