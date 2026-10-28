// Ported from pele src/diagrams/er/types.ts at 8aff183be57021b533f319a29341a529be934b08.

package er

// Cardinality is 'ZERO_OR_ONE' | 'ZERO_OR_MORE' | 'ONE_OR_MORE' | 'ONLY_ONE' | 'MD_PARENT'.
type Cardinality = string

// Identification is 'NON_IDENTIFYING' | 'IDENTIFYING'.
type Identification = string

type ErAttribute struct {
	typ     string
	name    string
	keys    []string
	comment string
}

// What the parser hands over for one attribute; keys and comment are present only when written.
// The port uses ErAttribute for it: an absent value is the empty one.
type ParsedAttribute = ErAttribute

type ErEntity struct {
	id         string
	label      string
	alias      string
	attributes []*ErAttribute
	cssClasses string
	cssStyles  []string
}

// cardB sits at entityA's end of the line and cardA at entityB's, as in Mermaid.
type RelSpec struct {
	cardA   Cardinality
	relType Identification
	cardB   Cardinality
}

// entityA and entityB hold an entity's id, or a subgraph's id when the name was a subgraph at the time.
type ErRelationship struct {
	entityA string
	roleA   string
	entityB string
	relSpec RelSpec
}

type ErClassDef struct {
	id         string
	styles     []string
	textStyles []string
}

type ErSubgraph struct {
	id        string
	nodes     []string
	title     string
	classes   []string
	cssStyles []string
	// dir is "" for none.
	dir       string
	labelType string
}

// DocItem is string | { stmt: 'dir'; value: string }: a string when dir is false.
type DocItem struct {
	dir   bool
	value string
}

// orderedMap is a Map that keeps insertion order.
type orderedMap[V any] struct {
	keys []string
	m    map[string]V
}

func newOrderedMap[V any]() *orderedMap[V] { return &orderedMap[V]{m: map[string]V{}} }

func (o *orderedMap[V]) Get(k string) (V, bool) {
	v, ok := o.m[k]
	return v, ok
}

func (o *orderedMap[V]) Set(k string, v V) {
	if _, ok := o.m[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}

func (o *orderedMap[V]) Len() int { return len(o.keys) }
