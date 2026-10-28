// Ported from pele src/diagrams/usecase/types.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// Spans are byte offsets into the source, where pele's are UTF-16 offsets. Every place
// that shows an offset to the reader turns it into UTF-16 units first. An optional text
// field is "" for none.

package usecase

type Span [2]int

type Actor struct {
	id         string
	label      string
	labelType  string
	typ        string
	icon       string
	business   bool
	stereotype string
	parentID   string
	classes    []string
	styles     []string
}

type UseCase struct {
	id         string
	label      string
	labelType  string
	shape      string
	business   bool
	stereotype string
	parentID   string
	classes    []string
	styles     []string
}

type SystemBoundary struct {
	id        string
	label     string
	labelType string
	typ       string
	members   []string
	classes   []string
	styles    []string
}

// Arrow types, in the order of Mermaid's ARROW_TYPE.
const (
	SOLID_ARROW = iota
	BACK_ARROW
	LINE_SOLID
	CIRCLE_ARROW
	CROSS_ARROW
	CIRCLE_ARROW_REVERSED
	CROSS_ARROW_REVERSED
)

type Relationship struct {
	id         string
	explicitID bool
	source     string
	target     string
	typ        string
	arrowType  int
	// label is nil for none.
	label     *string
	labelType string
	minlen    int
	classes   []string
	styles    []string
	animate   bool
	animation string
}

type UsecaseNote struct {
	id        string
	target    string
	label     string
	labelType string
}

type UsecaseJsonNode struct {
	id    string
	value *jsonObject
	// The keys of each object in the order they were written, by JSON Pointer.
	propertyOrder *keyOrder
	classes       []string
	styles        []string
}

type ClassDef struct {
	id     string
	styles []string
}

type MetadataOccurrence struct {
	key       string
	span      Span
	keySpan   Span
	valueSpan Span
}

type NodeOccurrence struct {
	id     string
	span   Span
	idSpan Span
	// labelSpan is the label's place, where hasLabel says there is one.
	labelSpan      Span
	hasLabel       bool
	defines        bool
	stereotypeSpan *Span
	metadata       []MetadataOccurrence
	classSpans     []Span
}

type EdgeOccurrence struct {
	id        string
	span      Span
	labelSpan *Span
	idSpan    *Span
	metadata  []MetadataOccurrence
}

// GraphStatement is one statement of the source with the positions of its parts, as in Mermaid's use case AST.
type GraphStatement struct {
	kind       string
	span       Span
	nodes      []NodeOccurrence
	edges      []EdgeOccurrence
	group      string
	idSpan     *Span
	titleSpan  *Span
	endSpan    *Span
	ref        string
	refSpan    *Span
	children   []*GraphStatement
	metadata   []MetadataOccurrence
	classSpans []Span
}

type UsecaseModel struct {
	actors           omap[*Actor]
	useCases         omap[*UseCase]
	systemBoundaries omap[*SystemBoundary]
	relationships    []*Relationship
	notes            omap[*UsecaseNote]
	jsonNodes        omap[*UsecaseJsonNode]
	classDefs        omap[*ClassDef]
	direction        string
	// title is nil for none.
	title    *string
	accTitle string
	accDescr string
	// The accessible description as written, before the indentation of its lines is dropped.
	accDescription string
	source         string
	headerSpan     Span
	statements     []*GraphStatement
}

func emptyModel() *UsecaseModel {
	return &UsecaseModel{direction: "LR"}
}

// omap is a JavaScript Map with string keys: it keeps the order keys were first set in,
// and a key deleted and set again goes to the end. Not in pele.
type omap[V any] struct {
	entries []omapEntry[V]
	size    int
	// at indexes entries by key once there are more than a few; below that a scan is cheaper.
	at map[string]int
}

type omapEntry[V any] struct {
	key   string
	value V
	alive bool
}

const omapScan = 8

func newOmap[V any]() *omap[V] { return &omap[V]{} }

func (m *omap[V]) find(k string) int {
	if m.at != nil {
		if i, ok := m.at[k]; ok {
			return i
		}
		return -1
	}
	for i := range m.entries {
		if m.entries[i].alive && m.entries[i].key == k {
			return i
		}
	}
	return -1
}

func (m *omap[V]) get(k string) (V, bool) {
	if i := m.find(k); i >= 0 {
		return m.entries[i].value, true
	}
	var zero V
	return zero, false
}

func (m *omap[V]) set(k string, v V) {
	if i := m.find(k); i >= 0 {
		m.entries[i].value = v
		return
	}
	if m.entries == nil {
		m.entries = make([]omapEntry[V], 0, omapScan)
	}
	m.entries = append(m.entries, omapEntry[V]{k, v, true})
	m.size++
	if m.at != nil {
		m.at[k] = len(m.entries) - 1
	} else if len(m.entries) > omapScan {
		m.at = make(map[string]int, 2*len(m.entries))
		for i := range m.entries {
			if m.entries[i].alive {
				m.at[m.entries[i].key] = i
			}
		}
	}
}

func (m *omap[V]) del(k string) {
	i := m.find(k)
	if i < 0 {
		return
	}
	m.entries[i].alive = false
	var zero V
	m.entries[i].value = zero
	m.size--
	if m.at != nil {
		delete(m.at, k)
	}
}

func (m *omap[V]) clear() {
	m.entries = m.entries[:0:0]
	m.size = 0
	m.at = nil
}

// values are the values in the map's order.
func (m *omap[V]) values() []V {
	out := make([]V, 0, m.size)
	for i := range m.entries {
		if m.entries[i].alive {
			out = append(out, m.entries[i].value)
		}
	}
	return out
}

// clone is new Map(m).
func (m *omap[V]) clone() *omap[V] {
	out := &omap[V]{entries: make([]omapEntry[V], 0, m.size)}
	for i := range m.entries {
		if m.entries[i].alive {
			out.set(m.entries[i].key, m.entries[i].value)
		}
	}
	return out
}
