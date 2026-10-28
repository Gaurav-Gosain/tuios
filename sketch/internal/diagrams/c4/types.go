// Ported from pele src/diagrams/c4/types.ts at 8aff183be57021b533f319a29341a529be934b08.

package c4

import "math"

// C4Named is a `$key="value"` argument.
type C4Named struct {
	key   string
	value string
}

// C4Attr is a positional argument, or a named one when named is set.
type C4Attr struct {
	str   string
	named *C4Named
}

type C4Element struct {
	alias string
	label string
	// A named argument written where the alias or the label belongs. It is applied to the field it names.
	aliasAttr *C4Named
	labelAttr *C4Named
	parent    *C4Boundary
	typ       string
	// An empty field reads as none everywhere, so `??= ''` needs no mark here.
	descr, techn string
	sprite       string
	tags         string
	link         string
	bgColor      string
	fontColor    string
	borderColor  string
	shadowing    string
	shape        string
	legendText   string
	legendSprite string
	// Named arguments that set no known field are dropped: nothing reads them.
}

type C4Shape struct {
	C4Element
	// Mermaid's typeC4Shape: person, external_system_db, container_queue and so on.
	kind string
}

type C4Boundary struct {
	C4Element
	// Set on deployment nodes: node, nodeL or nodeR.
	nodeType string
}

type C4Rel struct {
	typ       string
	from      string
	to        string
	label     string
	fromAttr  *C4Named
	toAttr    *C4Named
	labelAttr *C4Named
	// An empty field reads as none everywhere, so `??= ''` needs no mark here.
	descr, techn string
	sprite       string
	tags         string
	link         string
	textColor    string
	lineColor    string
	// offsetX and offsetY are NaN for none, as parseInt gives for text that is not a number.
	offsetX, offsetY float64
}

func newRel(typ string) *C4Rel {
	return &C4Rel{typ: typ, offsetX: math.NaN(), offsetY: math.NaN()}
}
