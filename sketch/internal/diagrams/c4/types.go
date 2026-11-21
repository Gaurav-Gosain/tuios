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
	alias        string
	label        string
	aliasAttr    *C4Named
	labelAttr    *C4Named
	parent       *C4Boundary
	typ          string
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
}

type C4Shape struct {
	C4Element
	kind string
}

type C4Boundary struct {
	C4Element
	nodeType string
}

type C4Rel struct {
	typ              string
	from             string
	to               string
	label            string
	fromAttr         *C4Named
	toAttr           *C4Named
	labelAttr        *C4Named
	descr, techn     string
	sprite           string
	tags             string
	link             string
	textColor        string
	lineColor        string
	offsetX, offsetY float64
}

func newRel(typ string) *C4Rel {
	return &C4Rel{typ: typ, offsetX: math.NaN(), offsetY: math.NaN()}
}
