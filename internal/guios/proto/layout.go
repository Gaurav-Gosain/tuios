package proto

// Layout arranges a session's panes, as a tree of splits: a leaf is a
// pane; a split shows A and B side by side (Row) or one above the other,
// A taking Ratio of the room.
type Layout struct {
	Pane  string  `json:"pane,omitempty"`
	Row   bool    `json:"row,omitempty"`
	Ratio float64 `json:"ratio,omitempty"`
	A     *Layout `json:"a,omitempty"`
	B     *Layout `json:"b,omitempty"`
}

// Leaf reports whether the node is a pane.
func (l *Layout) Leaf() bool { return l != nil && l.A == nil && l.B == nil }

// Panes returns the panes of a layout, in order: left to right, top to
// bottom.
func (l *Layout) Panes() []string {
	if l == nil {
		return nil
	}
	if l.Leaf() {
		return []string{l.Pane}
	}
	return append(l.A.Panes(), l.B.Panes()...)
}

// Clone returns a deep copy.
func (l *Layout) Clone() *Layout {
	if l == nil {
		return nil
	}
	c := *l
	c.A, c.B = l.A.Clone(), l.B.Clone()
	return &c
}

// SameShape reports whether o splits the same panes the same ways as l,
// whatever its ratios.
func (l *Layout) SameShape(o *Layout) bool {
	if l == nil || o == nil {
		return l == o
	}
	return l.Pane == o.Pane && l.Row == o.Row && l.A.SameShape(o.A) && l.B.SameShape(o.B)
}

// Split returns the layout with pane at, the leaf of an existing pane,
// split to show pane next to it: right, down, left or up of it, taking
// ratio of the room.
func (l *Layout) Split(at, pane, direction string, ratio float64) *Layout {
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	if l == nil {
		l = &Layout{Pane: at}
	}
	if l.Leaf() {
		if l.Pane != at {
			return l
		}
		old, nu := &Layout{Pane: at}, &Layout{Pane: pane}
		switch direction {
		case "left":
			return &Layout{Row: true, Ratio: ratio, A: nu, B: old}
		case "up":
			return &Layout{Ratio: ratio, A: nu, B: old}
		case "down":
			return &Layout{Ratio: 1 - ratio, A: old, B: nu}
		}
		return &Layout{Row: true, Ratio: 1 - ratio, A: old, B: nu}
	}
	c := *l
	c.A, c.B = l.A.Split(at, pane, direction, ratio), l.B.Split(at, pane, direction, ratio)
	return &c
}

// Remove returns the layout without a pane: its sibling takes its split's
// room. It returns nil when no pane is left.
func (l *Layout) Remove(pane string) *Layout {
	if l == nil {
		return nil
	}
	if l.Leaf() {
		if l.Pane == pane {
			return nil
		}
		return l
	}
	a, b := l.A.Remove(pane), l.B.Remove(pane)
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	c := *l
	c.A, c.B = a, b
	return &c
}

// Neighbor returns the pane next to pane in a direction (left, right, up,
// down), as panes' centers on a unit square go, or "".
func (l *Layout) Neighbor(pane, direction string) string {
	rects := map[string][4]float64{}
	l.rects(0, 0, 1, 1, rects)
	r, ok := rects[pane]
	if !ok {
		return ""
	}
	cx, cy := r[0]+r[2]/2, r[1]+r[3]/2
	best, bestD := "", 9.0
	for id, o := range rects {
		if id == pane {
			continue
		}
		ox, oy := o[0]+o[2]/2, o[1]+o[3]/2
		var along, across float64
		switch direction {
		case "left":
			along, across = cx-ox, oy-cy
		case "right":
			along, across = ox-cx, oy-cy
		case "up":
			along, across = cy-oy, ox-cx
		default:
			along, across = oy-cy, ox-cx
		}
		if along <= 1e-9 {
			continue
		}
		if d := along + 2*abs(across); d < bestD {
			best, bestD = id, d
		}
	}
	return best
}

func (l *Layout) rects(x, y, w, h float64, out map[string][4]float64) {
	if l == nil {
		return
	}
	if l.Leaf() {
		out[l.Pane] = [4]float64{x, y, w, h}
		return
	}
	r := l.Ratio
	if r <= 0 || r >= 1 {
		r = 0.5
	}
	if l.Row {
		l.A.rects(x, y, w*r, h, out)
		l.B.rects(x+w*r, y, w*(1-r), h, out)
	} else {
		l.A.rects(x, y, w, h*r, out)
		l.B.rects(x, y+h*r, w, h*(1-r), out)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// Move returns the layout with pane taken from where it is and put next
// to target, on its side (left, right, up or down), each taking half.
func (l *Layout) Move(pane, target, side string) *Layout {
	if pane == target || l == nil {
		return l
	}
	rest := l.Remove(pane)
	if rest == nil || !contains(rest.Panes(), target) {
		return l
	}
	return rest.Split(target, pane, side, 0.5)
}

// Swap returns the layout with two panes in each other's places.
func (l *Layout) Swap(a, b string) *Layout {
	if l == nil {
		return nil
	}
	c := l.Clone()
	var walk func(n *Layout)
	walk = func(n *Layout) {
		if n == nil {
			return
		}
		if n.Leaf() {
			switch n.Pane {
			case a:
				n.Pane = b
			case b:
				n.Pane = a
			}
			return
		}
		walk(n.A)
		walk(n.B)
	}
	walk(c)
	return c
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
