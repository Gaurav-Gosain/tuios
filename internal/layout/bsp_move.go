package layout

func (s Side) split() SplitType {
	if s == SideLeft || s == SideRight {
		return SplitVertical
	}
	return SplitHorizontal
}

// Preselection is the preselection direction that puts a pane on this side,
// for BSPTree.MoveWindow.
func (s Side) Preselection() PreselectionDir {
	switch s {
	case SideLeft:
		return PreselectionLeft
	case SideRight:
		return PreselectionRight
	case SideUp:
		return PreselectionUp
	case SideDown:
		return PreselectionDown
	}
	return PreselectionNone
}

func (s Side) near() bool { return s == SideLeft || s == SideUp }

func (t *BSPTree) detach(leaf *TileNode) {
	delete(t.WindowToNode, leaf.WindowID)
	parent := leaf.Parent
	leaf.Parent = nil
	if parent == nil {
		t.Root = nil
		return
	}
	sibling := parent.Left
	if sibling == leaf {
		sibling = parent.Right
	}
	grand := parent.Parent
	sibling.Parent = grand
	switch {
	case grand == nil:
		t.Root = sibling
	case grand.Left == parent:
		grand.Left = sibling
	default:
		grand.Right = sibling
	}
	parent.Left, parent.Right, parent.Parent = nil, nil, nil
}

func (t *BSPTree) pair(leaf, other *TileNode, side Side, ratio float64) {
	parent := other.Parent
	var node *TileNode
	if side.near() {
		node = NewInternalNode(side.split(), ratio, leaf, other)
	} else {
		node = NewInternalNode(side.split(), ratio, other, leaf)
	}
	node.Parent = parent
	switch {
	case parent == nil:
		t.Root = node
	case parent.Left == other:
		parent.Left = node
	default:
		parent.Right = node
	}
	t.WindowToNode[leaf.WindowID] = leaf
}

// MoveWindowToRoot takes windowID out of its place and puts it on side of the
// whole layout: a new root splits it from everything else, with a ratio of
// 0.5. It reports false and changes nothing when the window is not in the tree
// or is the only one.
func (t *BSPTree) MoveWindowToRoot(windowID int, side Side) bool {
	leaf := t.WindowToNode[windowID]
	if leaf == nil || leaf.Parent == nil {
		return false
	}
	t.detach(leaf)
	t.pair(leaf, t.Root, side, 0.5)
	return true
}

// FindSplit returns the split node with the given ID, or nil.
func (t *BSPTree) FindSplit(id uint64) *TileNode {
	var walk func(n *TileNode) *TileNode
	walk = func(n *TileNode) *TileNode {
		if n == nil || n.IsLeaf() {
			return nil
		}
		if n.ID == id {
			return n
		}
		if f := walk(n.Left); f != nil {
			return f
		}
		return walk(n.Right)
	}
	return walk(t.Root)
}

// SetSplitRatio sets the ratio of the split with the given ID. The ratio is
// held where neither side goes under the space its panes need (see
// lineBounds), the rule a divider drag follows. A side that is already under
// it keeps its line as a bound, so a drag from there never jumps. It returns the ratio the split
// now has, and false when no split has the ID.
//
// bounds and gap are the ones the layout is applied with, because the space a
// side needs is in cells and the ratio is a fraction of the split's own box.
func (t *BSPTree) SetSplitRatio(id uint64, ratio float64, bounds Rect, gap int) (float64, bool) {
	node := t.FindSplit(id)
	if node == nil {
		return 0, false
	}
	ratio = min(max(ratio, 0.01), 0.99)
	rect, ok := t.nodeBounds(node, bounds, gap)
	if !ok {
		node.SplitRatio = ratio
		return ratio, true
	}
	vertical := node.SplitType == SplitVertical
	extent := rect.W
	if !vertical {
		extent = rect.H
	}
	if extent <= 0 {
		node.SplitRatio = ratio
		return ratio, true
	}
	lo, hi := lineBounds(node, rect, gap, vertical)
	origin := rect.X
	if !vertical {
		origin = rect.Y
	}
	lo, hi = lo-origin, hi-origin
	line := int(float64(extent) * ratio)
	if line < lo || line > hi {
		ratio = (float64(max(lo, min(line, hi))) + 0.5) / float64(extent)
	}
	node.SplitRatio = ratio
	return ratio, true
}

// SplitBounds divides a split's box between its two children, as the layout
// does. It is childBounds for a caller outside the package that walks the tree
// itself.
func SplitBounds(node *TileNode, bounds Rect, gap int) (near, far Rect) {
	return childBounds(node, bounds, gap)
}

func (t *BSPTree) shareRatio(side Side, share float64) float64 {
	if share <= 0 || share >= 1 {
		return t.splitRatio()
	}
	share = clampSplitRatio(share)
	if side.near() {
		return share
	}
	return 1 - share
}

// SplitWindowAt puts windowID beside targetID, on side of it, under a new
// split in targetID's place. share is the new pane's share of the target's
// space, and 0 takes the tree's default. It reports false and changes nothing
// when the target is not in the tree or the window already is.
func (t *BSPTree) SplitWindowAt(windowID, targetID int, side Side, share float64) bool {
	target := t.WindowToNode[targetID]
	if target == nil || t.HasWindow(windowID) {
		return false
	}
	t.splitLeaf(target, NewLeafNode(windowID), side.split(), side.near(), t.shareRatio(side, share))
	return true
}

// MoveWindowBeside is MoveWindow toward side, with share as the moved pane's
// share of the split (0 for the tree's default). A window already beside the
// target on that side only takes the share. It reports whether the tree
// changed.
func (t *BSPTree) MoveWindowBeside(windowID, targetID int, side Side, share float64) bool {
	moved := t.MoveWindow(windowID, targetID, side.Preselection())
	leaf, target := t.WindowToNode[windowID], t.WindowToNode[targetID]
	if share <= 0 || leaf == nil || target == nil || leaf.Parent == nil || leaf.Parent != target.Parent {
		return moved
	}
	p := leaf.Parent
	if p.SplitType != side.split() || (p.Left == leaf) != side.near() {
		return moved
	}
	r := t.shareRatio(side, share)
	changed := moved || p.SplitRatio != r
	p.SplitRatio = r
	return changed
}

// NudgeBorder moves one border of windowID's pane toward side by amount, a
// share of the whole layout's width (left, right) or height (up, down). The
// border is the one on that side of the pane, or the one on the other side
// when the pane is at the edge there. So right grows a pane with a neighbour
// on its right, and shrinks a pane at the right edge from its left. The split
// keeps to the tiler's ratio range. It reports whether a ratio changed.
func (t *BSPTree) NudgeBorder(windowID int, side Side, amount float64) bool {
	leaf := t.WindowToNode[windowID]
	if leaf == nil || amount <= 0 {
		return false
	}
	axis, far := side.split(), !side.near()
	border := func(onLeft bool) *TileNode {
		for c := leaf; c.Parent != nil; c = c.Parent {
			if p := c.Parent; p.SplitType == axis && (p.Left == c) == onLeft {
				return p
			}
		}
		return nil
	}
	p := border(far)
	if p == nil {
		p = border(!far)
	}
	if p == nil {
		return false
	}
	span := 1.0
	for c := p; c.Parent != nil; c = c.Parent {
		if q := c.Parent; q.SplitType == axis {
			if q.Left == c {
				span *= q.SplitRatio
			} else {
				span *= 1 - q.SplitRatio
			}
		}
	}
	delta := amount / max(span, 0.01)
	if !far {
		delta = -delta
	}
	r := clampSplitRatio(p.SplitRatio + delta)
	if r == p.SplitRatio {
		return false
	}
	p.SplitRatio = r
	return true
}
