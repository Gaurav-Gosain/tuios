// Ported from pele src/diagrams/flowchart/types.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// An optional string of pele is "" here where pele never tells it from an
// empty one, and a pointer where it does.

// Package flowchart parses and draws Mermaid flowcharts.
package flowchart

import "tuios.dev/sketch/internal/types"

type LabelType = string

type FlowText struct {
	Text string
	Type LabelType
}

type FlowNode struct {
	ID         string
	Text       *string
	LabelType  LabelType
	Type       string
	Styles     []string
	Classes    []string
	Dir        string
	Props      map[string]string
	Link       *string
	LinkTarget *string
	Icon       string
	Form       string
	Pos        string
	Img        string
	Constraint string
	// nil for none.
	AssetWidth  *float64
	AssetHeight *float64
}

type FlowEdge struct {
	Start     string
	End       string
	Type      *string
	Text      string
	LabelType LabelType
	Stroke    *string
	Length    *int
	Style     []string
	Classes   []string
	ID        string
	// IsUserDefinedId is true when the author gave the edge its id.
	IsUserDefinedId bool
	Interpolate     string
	Animate         *bool
	Animation       *string
}

type FlowClassDef struct {
	ID         string
	Styles     []string
	TextStyles []string
}

type FlowSubgraph struct {
	ID        string
	Nodes     []string
	Title     string
	Classes   []string
	Dir       string
	LabelType LabelType
	// nil for none.
	Metadata map[string]types.YamlValue
}

type LinkInfo struct {
	Type   string
	Stroke string
	Length *int
	Text   *FlowText
	ID     *string
}

// DocItem is a string, a list of strings, or a `direction` statement.
type DocItem struct {
	Str   string
	List  []string
	IsDir bool
	// Value is the direction of a `direction` statement.
	Value  string
	IsList bool
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

func (o *orderedMap[V]) Has(k string) bool {
	_, ok := o.m[k]
	return ok
}

func (o *orderedMap[V]) Values() []V {
	out := make([]V, 0, len(o.keys))
	for _, k := range o.keys {
		out = append(out, o.m[k])
	}
	return out
}

func (o *orderedMap[V]) Len() int { return len(o.keys) }
