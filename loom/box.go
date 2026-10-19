package loom

import (
	"strconv"
	"strings"

	"tuios.dev/tml"
)

// sizeKind is how a size attribute is given.
type sizeKind uint8

const (
	sizeAuto    sizeKind = iota // the content's own size
	sizeCells                   // a number of cells
	sizePercent                 // a share of the parent
	sizeFill                    // a weight of the free space
)

// size is a parsed w, h, min-w and the like: 12, 50%, fill, fill:2.
type size struct {
	kind sizeKind
	n    int
}

func parseSize(s string) size {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return size{}
	case s == "fill":
		return size{sizeFill, 1}
	case strings.HasPrefix(s, "fill:"):
		n, err := strconv.Atoi(s[5:])
		if err != nil || n < 1 {
			n = 1
		}
		return size{sizeFill, min(n, 100)}
	case strings.HasSuffix(s, "%"):
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil || n < 0 || n != n {
			return size{}
		}
		return size{sizePercent, int(min(n, 100))}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 || n != n {
		return size{}
	}
	return size{sizeCells, int(min(n, maxCells))}
}

// maxCells bounds any size, so arithmetic on sizes cannot overflow.
const maxCells = 1 << 16

// resolve returns the cells a size gives against the parent's size, or -1
// for auto and fill.
func (z size) resolve(parent int) int {
	switch z.kind {
	case sizeCells:
		return z.n
	case sizePercent:
		return parent * z.n / 100
	}
	return -1
}

// box is one node of the retained tree: a component after each and if, with
// its bindings evaluated, its measured size and its place on the screen.
type box struct {
	kind   string    // the component type
	src    *tml.Node // the source node
	ptr    string    // the source node's JSON Pointer
	key    string    // the view key: base, and the copy's identity inside each and list
	base   string    // the node's own key: the id, or the pointer
	scope  *scope    // the each and list names in reach
	parent *box
	kids   []*box
	w      widget // the component's drawing, nil for a layout box

	hidden bool // show is false: the box keeps its space and draws nothing

	width, height          size
	minW, maxW, minH, maxH size
	pad                    [4]int // top, right, bottom, left
	gap, grow              int
	align, justify         string

	rect   Rect // where the layout put it
	prev   Rect // where it was at the last paint
	dirty  bool // its content changed since the last paint
	ground string

	// The measures of the current layout pass, by width.
	mgen   uint64
	mcache [][3]int
}

// inner returns the box's rectangle inside its padding.
func (b *box) inner() Rect {
	r := b.rect
	r.Min.X = min(r.Min.X+b.pad[3], r.Max.X)
	r.Min.Y = min(r.Min.Y+b.pad[0], r.Max.Y)
	r.Max.X = max(r.Max.X-b.pad[1], r.Min.X)
	r.Max.Y = max(r.Max.Y-b.pad[2], r.Min.Y)
	return r
}

func (b *box) padW() int { return b.pad[1] + b.pad[3] }
func (b *box) padH() int { return b.pad[0] + b.pad[2] }

// scope is the names each and list bind, innermost first.
type scope struct {
	name   string
	value  any
	index  int
	ident  string // the copy's identity: its key attribute, or its index
	parent *scope
}

func (s *scope) with(name string, v any, i int) *scope {
	return &scope{name: name, value: v, index: i, ident: strconv.Itoa(i), parent: s}
}

// idents returns the identities of the copies b sits in, outermost first,
// so each copy keeps its own view state.
func (s *scope) idents() string {
	if s == nil {
		return ""
	}
	if s.ident == "" {
		return s.parent.idents()
	}
	return s.parent.idents() + "#" + s.ident
}

// lookup reads the view state of a box: its copy's own entry, else the
// node's, which actions such as sort('id') write.
func lookup[T any](m map[string]T, b *box) (T, bool) {
	if v, ok := m[b.key]; ok {
		return v, true
	}
	v, ok := m[b.base]
	return v, ok
}

// vars adds the names of the scope to m, inner names over outer.
func (s *scope) vars(m map[string]any) {
	if s == nil {
		return
	}
	s.parent.vars(m)
	m[s.name] = s.value
}

// widget is the drawing of one component.
type widget interface {
	// measure returns the natural size of the content within maxW cells.
	measure(s *Surface, b *box, maxW int) (w, h int)
	// paint draws the content inside r, which excludes the padding.
	paint(s *Surface, b *box, c *canvas, r Rect)
}

// parsePad reads one, two or four cell counts.
func parsePad(s string) [4]int {
	var n []int
	for _, f := range strings.Fields(s) {
		v, err := strconv.Atoi(f)
		if err != nil || v < 0 {
			v = 0
		}
		n = append(n, min(v, 100))
	}
	switch len(n) {
	case 1:
		return [4]int{n[0], n[0], n[0], n[0]}
	case 2:
		return [4]int{n[0], n[1], n[0], n[1]}
	case 4:
		return [4]int{n[0], n[1], n[2], n[3]}
	}
	return [4]int{}
}
