// Ported from pele src/diagrams/wardley/builder.ts at 8aff183be57021b533f319a29341a529be934b08.

package wardley

type SourceStrategy = string
type Flow = string

// WardleyNode: coordinates are percentages: x is evolution, y is visibility.
type WardleyNode struct {
	id    string
	label string
	x, y  float64
	// 'anchor', 'component', or 'pipeline-component'
	className        string
	labelOffsetX     *float64
	labelOffsetY     *float64
	inPipeline       bool
	isPipelineParent bool
	inertia          bool
	sourceStrategy   SourceStrategy
}

type WardleyLink struct {
	source, target string
	dashed         bool
	label          string
	flow           Flow
}

type WardleyTrend struct {
	nodeID           string
	targetX, targetY float64
}

type WardleyPipeline struct {
	nodeID       string
	componentIDs []string
}

type WardleyAnnotation struct {
	number      float64
	coordinates [][2]float64
	text        string
}

type WardleyNote struct {
	text string
	x, y float64
}

type WardleyForce struct {
	name string
	x, y float64
}

type WardleyAxesConfig struct {
	stages []string
	// Right edge of each stage, from 0 to 1.
	stageBoundaries []float64
	hasBoundaries   bool
}

type WardleyBuildResult struct {
	nodes          []*WardleyNode
	links          []WardleyLink
	trends         []WardleyTrend
	pipelines      []*WardleyPipeline
	annotations    []WardleyAnnotation
	notes          []WardleyNote
	accelerators   []WardleyForce
	deaccelerators []WardleyForce
	annotationsBox *[2]float64
	axes           WardleyAxesConfig
	size           *[2]float64
}

type WardleyBuilder struct {
	nodes          map[string]*WardleyNode
	order          []string
	labels         map[string]string
	links          []WardleyLink
	trends         map[string]int
	trendList      []WardleyTrend
	pipelines      map[string]*WardleyPipeline
	pipelineOrder  []string
	annotations    []WardleyAnnotation
	notes          []WardleyNote
	accelerators   []WardleyForce
	deaccelerators []WardleyForce
	annotationsBox *[2]float64
	axes           WardleyAxesConfig
	size           *[2]float64
}

func newWardleyBuilder() *WardleyBuilder {
	return &WardleyBuilder{nodes: map[string]*WardleyNode{}, trends: map[string]int{}, pipelines: map[string]*WardleyPipeline{}}
}

// nodeInput is what addNode is given. setsTraits says the caller passes inertia and a source
// strategy, which then replace the node's own even when unset, as an object spread does.
type nodeInput struct {
	id, label    string
	x, y         float64
	className    string
	labelOffsetX *float64
	labelOffsetY *float64
	setsTraits   bool
	inertia      bool
	strategy     SourceStrategy
}

// addNode: a node added again keeps its class and label offsets unless the new one sets them.
func (b *WardleyBuilder) addNode(node nodeInput) {
	existing, ok := b.nodes[node.id]
	merged := WardleyNode{id: node.id, label: node.label}
	if ok {
		merged = *existing
	} else {
		b.order = append(b.order, node.id)
	}
	merged.id, merged.label, merged.x, merged.y = node.id, node.label, node.x, node.y
	if node.className != "" {
		merged.className = node.className
	}
	if node.labelOffsetX != nil {
		merged.labelOffsetX = node.labelOffsetX
	}
	if node.labelOffsetY != nil {
		merged.labelOffsetY = node.labelOffsetY
	}
	if node.setsTraits {
		merged.inertia, merged.sourceStrategy = node.inertia, node.strategy
	}
	b.nodes[node.id] = &merged
	b.labels = nil
}

func (b *WardleyBuilder) addLink(link WardleyLink) { b.links = append(b.links, link) }

func (b *WardleyBuilder) addTrend(trend WardleyTrend) {
	if k, ok := b.trends[trend.nodeID]; ok {
		b.trendList[k] = trend
		return
	}
	b.trends[trend.nodeID] = len(b.trendList)
	b.trendList = append(b.trendList, trend)
}

func (b *WardleyBuilder) startPipeline(nodeID string) {
	if _, ok := b.pipelines[nodeID]; !ok {
		b.pipelineOrder = append(b.pipelineOrder, nodeID)
	}
	b.pipelines[nodeID] = &WardleyPipeline{nodeID: nodeID}
	if node := b.nodes[nodeID]; node != nil {
		node.isPipelineParent = true
	}
}

func (b *WardleyBuilder) addPipelineComponent(pipelineNodeID, componentID string) {
	if p := b.pipelines[pipelineNodeID]; p != nil {
		p.componentIDs = append(p.componentIDs, componentID)
	}
	if node := b.nodes[componentID]; node != nil {
		node.inPipeline = true
	}
}

func (b *WardleyBuilder) getNode(id string) *WardleyNode { return b.nodes[id] }

// resolveNodeId: a name is a node id, or else the label of the first node that has it. Pipeline components
// have ids like "Parent_Child" and are linked by label.
func (b *WardleyBuilder) resolveNodeId(name string) string {
	if _, ok := b.nodes[name]; ok {
		return name
	}
	if b.labels == nil {
		b.labels = map[string]string{}
		for _, id := range b.order {
			node := b.nodes[id]
			if _, ok := b.labels[node.label]; !ok {
				b.labels[node.label] = id
			}
		}
	}
	if id, ok := b.labels[name]; ok {
		return id
	}
	return name
}

func (b *WardleyBuilder) build() WardleyBuildResult {
	// pele throws for a node without coordinates. addNode is always given both, so none lacks them.
	nodes := make([]*WardleyNode, 0, len(b.order))
	for _, id := range b.order {
		nodes = append(nodes, b.nodes[id])
	}
	pipelines := make([]*WardleyPipeline, len(b.pipelineOrder))
	for i, id := range b.pipelineOrder {
		pipelines[i] = b.pipelines[id]
	}
	return WardleyBuildResult{nodes: nodes, links: b.links, trends: b.trendList, pipelines: pipelines, annotations: b.annotations,
		notes: b.notes, accelerators: b.accelerators, deaccelerators: b.deaccelerators, annotationsBox: b.annotationsBox, axes: b.axes, size: b.size}
}
