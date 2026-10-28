// Ported from pele src/diagrams/c4/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package c4

import (
	"strings"

	"tuios.dev/sketch/internal/js"
)

// parseInt is the global parseInt without a radix: 0x starts a hexadecimal number.
func parseInt(s string) float64 {
	t := js.TrimStart(s)
	body := t
	if body != "" && (body[0] == '+' || body[0] == '-') {
		body = body[1:]
	}
	if len(body) >= 2 && body[0] == '0' && (body[1] == 'x' || body[1] == 'X') {
		return js.ParseIntPrefix(t, 16)
	}
	return js.ParseIntPrefix(t, 10)
}

// set assigns a named argument to an element field: Mermaid assigns a named argument to whatever
// property it names, structural ones included. Here it reaches only the fields of ELEMENT_FIELDS;
// any other name is dropped (pele keeps it aside in `extra`, which nothing reads).
func (el *C4Element) set(attr *C4Named) {
	v := attr.value
	switch attr.key {
	case "label":
		el.label = v
	case "type":
		el.typ = v
	case "descr":
		el.descr = v
	case "techn":
		el.techn = v
	case "sprite":
		el.sprite = v
	case "tags":
		el.tags = v
	case "link":
		el.link = v
	case "bgColor":
		el.bgColor = v
	case "fontColor":
		el.fontColor = v
	case "borderColor":
		el.borderColor = v
	case "shadowing":
		el.shadowing = v
	case "shape":
		el.shape = v
	case "legendText":
		el.legendText = v
	case "legendSprite":
		el.legendSprite = v
	}
}

// set assigns a named argument to a relation field, of REL_FIELDS or an offset.
func (rel *C4Rel) set(attr *C4Named) {
	v := attr.value
	switch attr.key {
	case "offsetX":
		rel.offsetX = parseInt(v)
	case "offsetY":
		rel.offsetY = parseInt(v)
	case "label":
		rel.label = v
	case "descr":
		rel.descr = v
	case "techn":
		rel.techn = v
	case "sprite":
		rel.sprite = v
	case "tags":
		rel.tags = v
	case "link":
		rel.link = v
	case "textColor":
		rel.textColor = v
	case "lineColor":
		rel.lineColor = v
	}
}

// put: a positional argument fills the field of its slot. A named one fills the field it names, whichever slot it is in.
func (el *C4Element) put(field *string, attr *C4Attr) {
	if attr == nil {
		return
	}
	if attr.named == nil {
		*field = attr.str
	} else {
		el.set(attr.named)
	}
}

func (rel *C4Rel) put(field *string, attr *C4Attr) {
	if attr == nil {
		return
	}
	if attr.named == nil {
		*field = attr.str
	} else {
		rel.set(attr.named)
	}
}

func (el *C4Element) setLabel(attr *C4Attr) {
	el.labelAttr = nil
	if attr != nil && attr.named != nil {
		el.label = ""
		el.labelAttr = attr.named
		el.set(attr.named)
	} else if attr != nil {
		el.label = attr.str
	} else {
		el.label = ""
	}
}

func (rel *C4Rel) setLabel(attr *C4Attr) {
	rel.labelAttr = nil
	if attr != nil && attr.named != nil {
		rel.label = ""
		rel.labelAttr = attr.named
		rel.set(attr.named)
	} else if attr != nil {
		rel.label = attr.str
	} else {
		rel.label = ""
	}
}

// arg is attrs[i], or nil past the end.
func arg(attrs []C4Attr, i int) *C4Attr {
	if i < len(attrs) {
		return &attrs[i]
	}
	return nil
}

type C4Db struct {
	c4Type   string
	title    string
	accTitle string
	accDescr string
	shapes   []*C4Shape
	// The first boundary is the unnamed one that holds everything at the top level.
	boundaries    []*C4Boundary
	rels          []*C4Rel
	shapeInRow    float64
	boundaryInRow float64

	shapeIndex map[string]*C4Shape
	// The top-level boundary is not listed, so a boundary a diagram names `global` is its own box.
	// In Mermaid it replaces the top level and nothing is drawn.
	boundaryIndex map[string]*C4Boundary
	relIndex      map[string]map[string]*C4Rel
	current       *C4Boundary
	stack         []*C4Boundary
}

func newC4Db() *C4Db {
	root := &C4Boundary{C4Element: C4Element{alias: "global", label: "global", typ: "global"}}
	return &C4Db{
		boundaries:    []*C4Boundary{root},
		shapeInRow:    4,
		boundaryInRow: 2,
		shapeIndex:    map[string]*C4Shape{},
		boundaryIndex: map[string]*C4Boundary{},
		relIndex:      map[string]map[string]*C4Rel{},
		current:       root,
	}
}

func (db *C4Db) setC4Type(typ string) {
	db.c4Type = typ
}

func (db *C4Db) setTitle(title string) {
	db.title = title
}

// setAccDescription keeps descr.replace(/\n\s+/g, '\n').
func (db *C4Db) setAccDescription(descr string) {
	if !strings.Contains(descr, "\n") {
		db.accDescr = descr
		return
	}
	var b strings.Builder
	for i := 0; i < len(descr); {
		if descr[i] == '\n' {
			q := i + 1
			for {
				size := spaceAt(descr, q)
				if size == 0 {
					break
				}
				q += size
			}
			b.WriteByte('\n')
			if q > i+1 {
				i = q
				continue
			}
			i++
			continue
		}
		b.WriteByte(descr[i])
		i++
	}
	db.accDescr = b.String()
}

func (db *C4Db) shape(alias, title *C4Attr) *C4Shape {
	var el *C4Shape
	if alias.named == nil {
		el = db.shapeIndex[alias.str]
	}
	if el == nil {
		el = &C4Shape{}
		db.shapes = append(db.shapes, el)
		if alias.named == nil {
			el.alias = alias.str
			db.shapeIndex[alias.str] = el
		} else {
			el.aliasAttr = alias.named
			el.set(alias.named)
		}
	}
	el.setLabel(title)
	return el
}

func (db *C4Db) boundaryElement(alias, title *C4Attr) *C4Boundary {
	var el *C4Boundary
	if alias.named == nil {
		el = db.boundaryIndex[alias.str]
	}
	if el == nil {
		el = &C4Boundary{}
		db.boundaries = append(db.boundaries, el)
		if alias.named == nil {
			el.alias = alias.str
			db.boundaryIndex[alias.str] = el
		} else {
			el.aliasAttr = alias.named
			el.set(alias.named)
		}
	}
	el.setLabel(title)
	return el
}

// addPersonOrSystem: alias, label, ?descr, ?sprite, ?tags, $link
func (db *C4Db) addPersonOrSystem(kind string, attrs []C4Attr) {
	shape := db.shape(arg(attrs, 0), arg(attrs, 1))
	shape.put(&shape.descr, arg(attrs, 2))
	db.finishShape(shape, kind, attrs, 3)
}

// addContainer: alias, label, ?techn, ?descr, ?sprite, ?tags, $link
func (db *C4Db) addContainer(kind string, attrs []C4Attr) {
	shape := db.shape(arg(attrs, 0), arg(attrs, 1))
	shape.put(&shape.techn, arg(attrs, 2))
	shape.put(&shape.descr, arg(attrs, 3))
	db.finishShape(shape, kind, attrs, 4)
}

func (db *C4Db) addComponent(kind string, attrs []C4Attr) {
	db.addContainer(kind, attrs)
}

func (db *C4Db) finishShape(shape *C4Shape, kind string, attrs []C4Attr, at int) {
	shape.put(&shape.sprite, arg(attrs, at))
	shape.put(&shape.tags, arg(attrs, at+1))
	shape.put(&shape.link, arg(attrs, at+2))
	shape.kind = kind
	shape.parent = db.current
}

func (db *C4Db) boundary(attrs []C4Attr, fallback string) *C4Boundary {
	boundary := db.boundaryElement(arg(attrs, 0), arg(attrs, 1))
	if t := arg(attrs, 2); t == nil {
		boundary.typ = fallback
	} else {
		boundary.put(&boundary.typ, t)
	}
	return boundary
}

func (db *C4Db) open(boundary *C4Boundary) {
	boundary.parent = db.current
	db.stack = append(db.stack, db.current)
	db.current = boundary
}

// addPersonOrSystemBoundary: alias, label, ?type, ?tags, $link
func (db *C4Db) addPersonOrSystemBoundary(attrs []C4Attr, fallback string) {
	boundary := db.boundary(attrs, fallback)
	boundary.put(&boundary.tags, arg(attrs, 3))
	boundary.put(&boundary.link, arg(attrs, 4))
	db.open(boundary)
}

func (db *C4Db) addContainerBoundary(attrs []C4Attr) {
	db.addPersonOrSystemBoundary(attrs, "container")
}

// addDeploymentNode: alias, label, ?type, ?descr, ?sprite, ?tags, $link
func (db *C4Db) addDeploymentNode(nodeType string, attrs []C4Attr) {
	boundary := db.boundary(attrs, "node")
	boundary.put(&boundary.descr, arg(attrs, 3))
	// Mermaid drops whatever is in the sprite slot, a named argument included.
	boundary.put(&boundary.sprite, arg(attrs, 4))
	boundary.put(&boundary.tags, arg(attrs, 5))
	boundary.put(&boundary.link, arg(attrs, 6))
	boundary.nodeType = nodeType
	db.open(boundary)
}

func (db *C4Db) popBoundaryParseStack() {
	if n := len(db.stack); n > 0 {
		db.current = db.stack[n-1]
		db.stack = db.stack[:n-1]
	} else {
		db.current = db.boundaries[0]
	}
}

// addRel: from, to, label, ?techn, ?descr, ?sprite, ?tags, $link
func (db *C4Db) addRel(typ string, attrs []C4Attr) {
	from := arg(attrs, 0)
	to := arg(attrs, 1)
	if from == nil || to == nil || arg(attrs, 2) == nil {
		return
	}
	known := from.named == nil && to.named == nil
	var rel *C4Rel
	if known {
		rel = db.relIndex[from.str][to.str]
	}
	if rel == nil {
		rel = newRel(typ)
		db.rels = append(db.rels, rel)
		if known {
			targets := db.relIndex[from.str]
			if targets == nil {
				targets = map[string]*C4Rel{}
				db.relIndex[from.str] = targets
			}
			targets[to.str] = rel
		}
		if from.named == nil {
			rel.from = from.str
		} else {
			rel.fromAttr = from.named
		}
		if to.named == nil {
			rel.to = to.str
		} else {
			rel.toAttr = to.named
		}
	}
	rel.typ = typ
	rel.setLabel(arg(attrs, 2))
	rel.put(&rel.techn, arg(attrs, 3))
	rel.put(&rel.descr, arg(attrs, 4))
	rel.put(&rel.sprite, arg(attrs, 5))
	rel.put(&rel.tags, arg(attrs, 6))
	rel.put(&rel.link, arg(attrs, 7))
}

// getC4Shape is the shape or boundary with an alias, or nil.
func (db *C4Db) getC4Shape(alias string) *C4Element {
	if s := db.shapeIndex[alias]; s != nil {
		return &s.C4Element
	}
	if b := db.boundaryIndex[alias]; b != nil {
		return &b.C4Element
	}
	return nil
}

// updateElStyle: elementName, ?bgColor, ?fontColor, ?borderColor, ?shadowing, ?shape, ?sprite, ?techn, ?legendText, ?legendSprite
func (db *C4Db) updateElStyle(attrs []C4Attr) {
	if attrs[0].named != nil {
		return
	}
	el := db.getC4Shape(attrs[0].str)
	if el == nil {
		return
	}
	el.put(&el.bgColor, arg(attrs, 1))
	el.put(&el.fontColor, arg(attrs, 2))
	el.put(&el.borderColor, arg(attrs, 3))
	el.put(&el.shadowing, arg(attrs, 4))
	el.put(&el.shape, arg(attrs, 5))
	el.put(&el.sprite, arg(attrs, 6))
	el.put(&el.techn, arg(attrs, 7))
	el.put(&el.legendText, arg(attrs, 8))
	el.put(&el.legendSprite, arg(attrs, 9))
}

// updateRelStyle: from, to, ?textColor, ?lineColor, ?offsetX, ?offsetY
func (db *C4Db) updateRelStyle(attrs []C4Attr) {
	from := arg(attrs, 0)
	to := arg(attrs, 1)
	if from == nil || to == nil || from.named != nil || to.named != nil {
		return
	}
	rel := db.relIndex[from.str][to.str]
	if rel == nil {
		return
	}
	rel.put(&rel.textColor, arg(attrs, 2))
	rel.put(&rel.lineColor, arg(attrs, 3))
	if x := arg(attrs, 4); x != nil && x.named == nil {
		rel.offsetX = parseInt(x.str)
	} else if x != nil {
		rel.set(x.named)
	}
	if y := arg(attrs, 5); y != nil && y.named == nil {
		rel.offsetY = parseInt(y.str)
	} else if y != nil {
		rel.set(y.named)
	}
}

// updateLayoutConfig: ?c4ShapeInRow, ?c4BoundaryInRow. As in Mermaid, the slot decides which one a value sets, not its name.
func (db *C4Db) updateLayoutConfig(attrs []C4Attr) {
	count := func(attr *C4Attr) float64 {
		if attr == nil {
			return 0 // NaN in pele; only `>= 1` reads it.
		}
		if attr.named == nil {
			return parseInt(attr.str)
		}
		return parseInt(attr.named.value)
	}
	shapes := count(arg(attrs, 0))
	boundaries := count(arg(attrs, 1))
	if shapes >= 1 {
		db.shapeInRow = shapes
	}
	if boundaries >= 1 {
		db.boundaryInRow = boundaries
	}
}
