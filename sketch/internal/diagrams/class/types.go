// Ported from pele src/diagrams/class/types.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele's optional strings are pointers here where `undefined` and "" differ.

package class

// RelationEnd is a relationType number, or NONE for pele's 'none'.
type RelationEnd = int

const NONE RelationEnd = -1

type ClassNode struct {
	ID          string
	Type        string
	Label       string
	CssClasses  string
	Methods     []*ClassMember
	Members     []*ClassMember
	Annotations []string
	Styles      []string
	Parent      *string
	// "" for none.
	Link         string
	LinkTarget   string
	HaveCallback bool
	// "" for none.
	Tooltip string
}

type Relation struct {
	Type1    RelationEnd
	Type2    RelationEnd
	LineType int
}

type ClassRelation struct {
	ID1            string
	ID2            string
	RelationTitle1 string
	RelationTitle2 string
	Title          *string
	Relation       Relation
}

type ClassNote struct {
	ID     string
	Class  *string
	Text   string
	Index  int
	Parent *string
}

type ClassInterface struct {
	ID      string
	Label   string
	ClassID string
}

type NamespaceNode struct {
	ID       string
	Label    string
	Classes  *orderedMap[*ClassNode]
	Notes    *orderedMap[*ClassNote]
	Children *orderedMap[*NamespaceNode]
	Parent   *string
	// False for the ancestors created on the way to a dotted name such as `A.B.C`.
	Explicit bool
}

type StyleClass struct {
	ID         string
	Styles     []string
	TextStyles []string
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
