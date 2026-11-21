package loom

import (
	"strconv"
	"strings"

	"tuios.dev/tml"
)

type sizeKind uint8

const (
	sizeAuto sizeKind = iota
	sizeCells
	sizePercent
	sizeFill
)

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

const maxCells = 1 << 16

func (z size) resolve(parent int) int {
	switch z.kind {
	case sizeCells:
		return z.n
	case sizePercent:
		return parent * z.n / 100
	}
	return -1
}

type box struct {
	kind   string
	src    *tml.Node
	ptr    string
	key    string
	base   string
	scope  *scope
	parent *box
	kids   []*box
	w      widget

	hidden bool
	mo     *motion

	width, height          size
	minW, maxW, minH, maxH size
	pad                    [4]int
	gap, grow              int
	align, justify         string

	rect   Rect
	prev   Rect
	dirty  bool
	ground string

	mgen   uint64
	mcache [][3]int
	pgen   uint64
	pw     int
}

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

type scope struct {
	name   string
	value  any
	index  int
	ident  string
	parent *scope
}

func (s *scope) with(name string, v any, i int) *scope {
	return &scope{name: name, value: v, index: i, ident: strconv.Itoa(i), parent: s}
}

func (s *scope) idents() string {
	if s == nil {
		return ""
	}
	if s.ident == "" {
		return s.parent.idents()
	}
	return s.parent.idents() + "#" + s.ident
}

func lookup[T any](m map[string]T, b *box) (T, bool) {
	if v, ok := m[b.key]; ok {
		return v, true
	}
	v, ok := m[b.base]
	return v, ok
}

func (s *scope) vars(m map[string]any) {
	if s == nil {
		return
	}
	s.parent.vars(m)
	m[s.name] = s.value
}

type widget interface {
	measure(s *Surface, b *box, maxW int) (w, h int)
	paint(s *Surface, b *box, c *canvas, r Rect)
}

func parsePad(s string) [4]int {
	var n []int
	for f := range strings.FieldsSeq(s) {
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
