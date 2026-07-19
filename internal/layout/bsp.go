// Package layout provides window tiling and layout management for the terminal.
package layout

import (
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// nodeIDCounter is used to generate unique node IDs
var nodeIDCounter atomic.Uint64

// SplitType represents the direction of a split in the BSP tree
type SplitType int

const (
	SplitNone       SplitType = iota // Leaf node (contains a window)
	SplitVertical                    // Left/Right children (vertical divider)
	SplitHorizontal                  // Top/Bottom children (horizontal divider)
	SplitStacked                     // Stacked children (only active child visible, others show as title bars)
)

// String returns a string representation of the split type
func (s SplitType) String() string {
	switch s {
	case SplitNone:
		return "none"
	case SplitVertical:
		return "vertical"
	case SplitHorizontal:
		return "horizontal"
	case SplitStacked:
		return "stacked"
	default:
		return "unknown"
	}
}

// AutoScheme determines how new windows are automatically inserted
type AutoScheme int

const (
	// SchemeLongestSide splits along the longest dimension of the target area
	SchemeLongestSide AutoScheme = iota
	// SchemeAlternate alternates between vertical and horizontal splits
	SchemeAlternate
	// SchemeSpiral creates a spiral pattern (like bspwm's default)
	SchemeSpiral
	// SchemeSmartSplit chooses split direction based on focused window aspect ratio:
	// width > height*2 -> vertical (side by side), height > width -> horizontal (stacked),
	// otherwise alternates based on split depth.
	SchemeSmartSplit
)

// String returns a string representation of the auto scheme
func (s AutoScheme) String() string {
	switch s {
	case SchemeLongestSide:
		return "longest_side"
	case SchemeAlternate:
		return "alternate"
	case SchemeSpiral:
		return "spiral"
	case SchemeSmartSplit:
		return "smart_split"
	default:
		return "unknown"
	}
}

// ParseAutoScheme parses a string into an AutoScheme
func ParseAutoScheme(s string) AutoScheme {
	switch s {
	case "alternate":
		return SchemeAlternate
	case "spiral":
		return SchemeSpiral
	case "smart_split":
		return SchemeSmartSplit
	default:
		return SchemeLongestSide
	}
}

// PreselectionDir represents a direction for preselection
type PreselectionDir int

const (
	PreselectionNone PreselectionDir = iota
	PreselectionLeft
	PreselectionRight
	PreselectionUp
	PreselectionDown
)

// Rect represents a rectangle with position and size
type Rect struct {
	X, Y, W, H int
}

// TileNode represents a node in the binary space partition tree.
// Internal nodes have Left and Right children and define a split.
// Leaf nodes have a WindowID and represent an actual window.
type TileNode struct {
	ID                uint64    // Unique identifier for the node
	Parent            *TileNode // Parent node (nil for root)
	Left              *TileNode // Left/Top child (nil for leaf nodes)
	Right             *TileNode // Right/Bottom child (nil for leaf nodes)
	WindowID          int       // Window ID (-1 for internal nodes)
	SplitType         SplitType // How this node splits its space
	SplitRatio        float64   // Position of split (0.0-1.0), 0.5 = middle
	StackedActiveLeft bool      // For SplitStacked: true = left child is active (gets content area)
}

// newNodeID generates a unique node ID
func newNodeID() uint64 {
	return nodeIDCounter.Add(1)
}

// NewLeafNode creates a new leaf node for a window
func NewLeafNode(windowID int) *TileNode {
	return &TileNode{
		ID:         newNodeID(),
		WindowID:   windowID,
		SplitType:  SplitNone,
		SplitRatio: 0.5,
	}
}

// NewInternalNode creates a new internal node with the given split
func NewInternalNode(splitType SplitType, ratio float64, left, right *TileNode) *TileNode {
	node := &TileNode{
		ID:         newNodeID(),
		WindowID:   -1,
		SplitType:  splitType,
		SplitRatio: ratio,
		Left:       left,
		Right:      right,
	}
	if left != nil {
		left.Parent = node
	}
	if right != nil {
		right.Parent = node
	}
	return node
}

// IsLeaf returns true if this is a leaf node (contains a window)
func (n *TileNode) IsLeaf() bool {
	return n.SplitType == SplitNone
}

// IsRoot returns true if this is the root node
func (n *TileNode) IsRoot() bool {
	return n.Parent == nil
}

// Sibling returns the sibling node (other child of parent)
func (n *TileNode) Sibling() *TileNode {
	if n.Parent == nil {
		return nil
	}
	if n.Parent.Left == n {
		return n.Parent.Right
	}
	return n.Parent.Left
}

// IsLeftChild returns true if this node is the left/top child of its parent
func (n *TileNode) IsLeftChild() bool {
	return n.Parent != nil && n.Parent.Left == n
}

// Depth returns the depth of this node in the tree (root = 0)
func (n *TileNode) Depth() int {
	depth := 0
	current := n
	for current.Parent != nil {
		depth++
		current = current.Parent
	}
	return depth
}

// BSPTree manages the binary space partition for a workspace
type BSPTree struct {
	Root         *TileNode         // Root of the tree (nil if empty)
	WindowToNode map[int]*TileNode // Quick lookup: windowID -> leaf node
	AutoScheme   AutoScheme        // How to auto-insert new windows
	DefaultRatio float64           // Default split ratio for new splits
}

// NewBSPTree creates a new empty BSP tree
func NewBSPTree() *BSPTree {
	return &BSPTree{
		Root:         nil,
		WindowToNode: make(map[int]*TileNode),
		AutoScheme:   SchemeSpiral,
		DefaultRatio: 0.5,
	}
}

// IsEmpty returns true if the tree has no windows
func (t *BSPTree) IsEmpty() bool {
	return t.Root == nil
}

// WindowCount returns the number of windows in the tree
func (t *BSPTree) WindowCount() int {
	return len(t.WindowToNode)
}

// HasWindow returns true if the window is in the tree
func (t *BSPTree) HasWindow(windowID int) bool {
	_, ok := t.WindowToNode[windowID]
	return ok
}

// FindNode returns the leaf node for the given window ID
func (t *BSPTree) FindNode(windowID int) *TileNode {
	return t.WindowToNode[windowID]
}

// InsertWindow adds a new window to the tree by splitting the focused window.
// If direction is SplitNone, uses the auto scheme to determine split direction.
// The new window is inserted as the right/bottom child.
func (t *BSPTree) InsertWindow(windowID int, focusedWindowID int, direction SplitType, ratio float64, bounds Rect) {
	// Don't insert duplicates
	if t.HasWindow(windowID) {
		return
	}

	newLeaf := NewLeafNode(windowID)

	// First window - just make it the root
	if t.Root == nil {
		t.Root = newLeaf
		t.WindowToNode[windowID] = newLeaf
		return
	}

	// Find the node to split
	targetNode := t.WindowToNode[focusedWindowID]
	if targetNode == nil {
		// Fallback: find any leaf node (shouldn't happen in normal use)
		targetNode = t.findAnyLeaf()
		if targetNode == nil {
			// Tree is in invalid state, reset
			t.Root = newLeaf
			t.WindowToNode[windowID] = newLeaf
			return
		}
	}

	// Determine split direction if not specified
	if direction == SplitNone {
		direction = t.determineAutoSplit(targetNode, bounds)
	}

	// Use default ratio if not specified
	if ratio <= 0 || ratio >= 1 {
		ratio = t.DefaultRatio
	}

	// Create new internal node that replaces the target
	// The target becomes the left child, new window becomes right child
	oldLeaf := NewLeafNode(targetNode.WindowID)
	internalNode := NewInternalNode(direction, ratio, oldLeaf, newLeaf)

	// Replace target in tree
	if targetNode.Parent == nil {
		// Target was root
		t.Root = internalNode
	} else {
		// Update parent's child pointer
		internalNode.Parent = targetNode.Parent
		if targetNode.IsLeftChild() {
			targetNode.Parent.Left = internalNode
		} else {
			targetNode.Parent.Right = internalNode
		}
	}

	// Update window-to-node mapping
	t.WindowToNode[targetNode.WindowID] = oldLeaf
	t.WindowToNode[windowID] = newLeaf
}

// InsertWindowWithPreselection adds a new window using preselection direction.
// Preselection determines which side of the focused window to place the new window.
func (t *BSPTree) InsertWindowWithPreselection(windowID int, focusedWindowID int, preselect PreselectionDir, bounds Rect) {
	var direction SplitType
	var newWindowIsLeft bool

	switch preselect {
	case PreselectionLeft:
		direction = SplitVertical
		newWindowIsLeft = true
	case PreselectionRight:
		direction = SplitVertical
		newWindowIsLeft = false
	case PreselectionUp:
		direction = SplitHorizontal
		newWindowIsLeft = true
	case PreselectionDown:
		direction = SplitHorizontal
		newWindowIsLeft = false
	default:
		// No preselection, use normal insert
		t.InsertWindow(windowID, focusedWindowID, SplitNone, t.DefaultRatio, bounds)
		return
	}

	// Don't insert duplicates
	if t.HasWindow(windowID) {
		return
	}

	newLeaf := NewLeafNode(windowID)

	// First window
	if t.Root == nil {
		t.Root = newLeaf
		t.WindowToNode[windowID] = newLeaf
		return
	}

	// Find the target node
	targetNode := t.WindowToNode[focusedWindowID]
	if targetNode == nil {
		targetNode = t.findAnyLeaf()
		if targetNode == nil {
			t.Root = newLeaf
			t.WindowToNode[windowID] = newLeaf
			return
		}
	}

	// Create the split with correct ordering
	oldLeaf := NewLeafNode(targetNode.WindowID)
	var internalNode *TileNode
	if newWindowIsLeft {
		internalNode = NewInternalNode(direction, t.DefaultRatio, newLeaf, oldLeaf)
	} else {
		internalNode = NewInternalNode(direction, t.DefaultRatio, oldLeaf, newLeaf)
	}

	// Replace target in tree
	if targetNode.Parent == nil {
		t.Root = internalNode
	} else {
		internalNode.Parent = targetNode.Parent
		if targetNode.IsLeftChild() {
			targetNode.Parent.Left = internalNode
		} else {
			targetNode.Parent.Right = internalNode
		}
	}

	// Update mappings
	t.WindowToNode[targetNode.WindowID] = oldLeaf
	t.WindowToNode[windowID] = newLeaf
}

// RemoveWindow removes a window from the tree and collapses the tree structure.
// When a window is removed, its sibling takes over the parent's space.
func (t *BSPTree) RemoveWindow(windowID int) {
	node := t.WindowToNode[windowID]
	if node == nil {
		return
	}

	delete(t.WindowToNode, windowID)

	// If this is the only window, tree becomes empty
	if node.Parent == nil {
		t.Root = nil
		return
	}

	// Get sibling and grandparent
	sibling := node.Sibling()
	parent := node.Parent
	grandparent := parent.Parent

	// Sibling takes parent's place
	if grandparent == nil {
		// Parent was root, sibling becomes new root
		t.Root = sibling
		sibling.Parent = nil
	} else {
		// Connect sibling to grandparent
		sibling.Parent = grandparent
		if parent.IsLeftChild() {
			grandparent.Left = sibling
		} else {
			grandparent.Right = sibling
		}
	}
}

// ApplyLayout calculates positions for all windows in the tree.
// Returns a map of windowID -> Rect with the calculated layout.
func (t *BSPTree) ApplyLayout(bounds Rect) map[int]Rect {
	result := make(map[int]Rect)
	t.ApplyLayoutInto(bounds, result)
	return result
}

// ApplyLayoutInto is ApplyLayout writing into a caller-owned map. A mouse drag
// reapplies the layout on every motion event, and allocating a fresh map each
// time makes the cost of a single event scale with how many panes the workspace
// holds; reusing one keeps it flat.
func (t *BSPTree) ApplyLayoutInto(bounds Rect, result map[int]Rect) map[int]Rect {
	clear(result)
	if t.Root == nil {
		return result
	}
	t.applyLayoutRecursive(t.Root, bounds, result)
	// Keep leaves that were grown to the minimum size within the root bounds, so
	// a terminal too small to fit every pane pushes tiles back on-screen instead
	// of off the edge.
	for id, r := range result {
		r.X = max(bounds.X, min(r.X, bounds.X+bounds.W-r.W))
		r.Y = max(bounds.Y, min(r.Y, bounds.Y+bounds.H-r.H))
		result[id] = r
	}
	return result
}

func (t *BSPTree) applyLayoutRecursive(node *TileNode, bounds Rect, result map[int]Rect) {
	if node == nil {
		return
	}

	// Leaf node - this is a window
	if node.IsLeaf() {
		// Enforce minimum sizes
		w := bounds.W
		h := bounds.H
		if w < config.DefaultWindowWidth {
			w = config.DefaultWindowWidth
		}
		if h < config.DefaultWindowHeight {
			h = config.DefaultWindowHeight
		}
		result[node.WindowID] = Rect{X: bounds.X, Y: bounds.Y, W: w, H: h}
		return
	}

	leftBounds, rightBounds := childBounds(node, bounds)
	t.applyLayoutRecursive(node.Left, leftBounds, result)
	t.applyLayoutRecursive(node.Right, rightBounds, result)
}

// childBounds divides an internal node's rectangle between its two children.
// It is the single definition of the split model: every other part of the
// layout that needs to know where a divider sits, or what rectangle a node was
// laid out in, has to derive it from here rather than reimplement it. Resizing
// in particular depends on that, because a ratio computed against a rectangle
// the layout does not agree with puts the divider somewhere the drag did not
// ask for.
func childBounds(node *TileNode, bounds Rect) (leftBounds, rightBounds Rect) {
	if node.SplitType == SplitStacked {
		// Stacked: the active child gets the content area, the inactive one is
		// reduced to a single title bar row.
		const titleBarHeight = 1
		if node.StackedActiveLeft {
			leftBounds = Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: bounds.H - titleBarHeight}
			rightBounds = Rect{X: bounds.X, Y: bounds.Y + bounds.H - titleBarHeight, W: bounds.W, H: titleBarHeight}
		} else {
			leftBounds = Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: titleBarHeight}
			rightBounds = Rect{X: bounds.X, Y: bounds.Y + titleBarHeight, W: bounds.W, H: bounds.H - titleBarHeight}
		}
		return leftBounds, rightBounds
	}

	// Shared borders reserve one cell between the two children for the drawn
	// separator, so the far child starts one past the divider line.
	gap := 0
	if config.SharedBorders {
		gap = 1
	}

	if node.SplitType == SplitVertical {
		splitX := bounds.X + int(float64(bounds.W)*node.SplitRatio)
		if gap > 0 {
			// Keep the separator cell inside the node's own rectangle.
			splitX = max(bounds.X+1, min(splitX, bounds.X+bounds.W-2))
		}
		leftBounds = Rect{X: bounds.X, Y: bounds.Y, W: splitX - bounds.X, H: bounds.H}
		rightBounds = Rect{X: splitX + gap, Y: bounds.Y, W: bounds.X + bounds.W - splitX - gap, H: bounds.H}
		return leftBounds, rightBounds
	}

	splitY := bounds.Y + int(float64(bounds.H)*node.SplitRatio)
	if gap > 0 {
		splitY = max(bounds.Y+1, min(splitY, bounds.Y+bounds.H-2))
	}
	leftBounds = Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: splitY - bounds.Y}
	rightBounds = Rect{X: bounds.X, Y: splitY + gap, W: bounds.W, H: bounds.Y + bounds.H - splitY - gap}
	return leftBounds, rightBounds
}

// ResizeEdge names the side of a pane that a resize gesture drags.
type ResizeEdge int

const (
	ResizeEdgeRight ResizeEdge = iota
	ResizeEdgeLeft
	ResizeEdgeBottom
	ResizeEdgeTop
)

func (e ResizeEdge) vertical() bool {
	return e == ResizeEdgeRight || e == ResizeEdgeLeft
}

// far reports whether the edge is on the high side of the pane, which is the
// side the divider sits on when the pane's subtree is the near child.
func (e ResizeEdge) far() bool {
	return e == ResizeEdgeRight || e == ResizeEdgeBottom
}

// ResizeSplit moves the divider that owns one edge of a window to pos, given in
// the same coordinate space as bounds. It reports whether a divider was found:
// an edge that lies on the outer boundary of the whole layout has none, and the
// caller should leave the geometry alone rather than invent one.
//
// This is the only correct way to resize a BSP layout. Matching panes by
// geometry instead - collecting every window whose edge happens to fall on the
// dragged line - sweeps in panes from unrelated subtrees whenever two dividers
// coincide, which they do by default because fresh splits are all 0.5. The tree
// says exactly which two subtrees the divider separates, and nothing outside
// them may move.
func (t *BSPTree) ResizeSplit(windowID int, e ResizeEdge, pos int, bounds Rect) bool {
	leaf := t.WindowToNode[windowID]
	if leaf == nil {
		return false
	}

	// Walk up to the nearest ancestor that splits on this axis with the window's
	// subtree on the near side of the divider. Stacked ancestors have no
	// draggable divider, so they are skipped rather than treated as the split.
	var node *TileNode
	for cur := leaf; cur.Parent != nil; cur = cur.Parent {
		p := cur.Parent
		if p.SplitType == SplitStacked {
			continue
		}
		if (p.SplitType == SplitVertical) != e.vertical() {
			continue
		}
		if e.far() == cur.IsLeftChild() {
			node = p
			break
		}
	}
	if node == nil {
		return false
	}

	rect, ok := t.nodeBounds(node, bounds)
	if !ok {
		return false
	}

	gap := 0
	if config.SharedBorders {
		gap = 1
	}

	// The near child's far edge is the divider line itself; the far child's near
	// edge sits one separator cell past it.
	line := pos
	if !e.far() {
		line -= gap
	}

	origin, extent := rect.X, rect.W
	if !e.vertical() {
		origin, extent = rect.Y, rect.H
	}
	if extent <= 0 {
		return false
	}

	// Neither subtree may be squeezed below what its own leaves need, and a
	// subtree split along the same axis needs the sum of its children.
	lo := origin + minExtent(node.Left, e.vertical())
	hi := origin + extent - gap - minExtent(node.Right, e.vertical())
	if lo > hi {
		return false
	}
	line = max(lo, min(line, hi))

	// Aim at the middle of the target cell. applyLayoutRecursive truncates
	// ratio*extent, so a ratio derived from the cell's left edge can round down
	// to the previous cell and make a drag lose a step, or creep on re-apply.
	node.SplitRatio = (float64(line-origin) + 0.5) / float64(extent)
	return true
}

// minExtent is the smallest width (or height) a subtree can be laid out in
// without pushing one of its leaves under the minimum window size.
func minExtent(node *TileNode, vertical bool) int {
	if node == nil {
		return 0
	}
	if node.IsLeaf() {
		if vertical {
			return config.DefaultWindowWidth
		}
		return config.DefaultWindowHeight
	}

	left := minExtent(node.Left, vertical)
	right := minExtent(node.Right, vertical)

	if node.SplitType == SplitStacked {
		if vertical {
			return max(left, right)
		}
		// The inactive child is collapsed to a title bar row on top of the
		// active child's content.
		return max(left, right) + 1
	}

	if (node.SplitType == SplitVertical) != vertical {
		// Split runs the other way: both children span the full extent.
		return max(left, right)
	}

	gap := 0
	if config.SharedBorders {
		gap = 1
	}
	return left + right + gap
}

// nodeBounds returns the rectangle applyLayoutRecursive would hand to target.
// It descends from the root through childBounds rather than recomputing the
// split model, so a ratio derived from it lands the divider where the caller
// asked.
func (t *BSPTree) nodeBounds(target *TileNode, bounds Rect) (Rect, bool) {
	if target == nil || t.Root == nil {
		return Rect{}, false
	}

	// Path from the root down to target, collected by walking parent links up.
	var path []*TileNode
	for cur := target; cur != nil; cur = cur.Parent {
		path = append(path, cur)
	}
	if path[len(path)-1] != t.Root {
		return Rect{}, false
	}

	rect := bounds
	for i := len(path) - 1; i > 0; i-- {
		node := path[i]
		if node.IsLeaf() {
			return Rect{}, false
		}
		leftBounds, rightBounds := childBounds(node, rect)
		if path[i-1] == node.Left {
			rect = leftBounds
		} else {
			rect = rightBounds
		}
	}
	return rect, true
}

// SyncRatiosFromGeometry updates the tree's split ratios based on actual window positions.
// This is called after mouse resize to keep the tree in sync with reality.
func (t *BSPTree) SyncRatiosFromGeometry(windows map[int]Rect, bounds Rect) {
	if t.Root == nil {
		return
	}
	t.syncRatiosRecursive(t.Root, bounds, windows)
}

func (t *BSPTree) syncRatiosRecursive(node *TileNode, bounds Rect, windows map[int]Rect) {
	if node == nil || node.IsLeaf() {
		return
	}

	// Shared-border layouts reserve one cell for the separator, so the near edge
	// of the right/bottom subtree sits one cell past the divider. Sync has to use
	// the same model applyLayoutRecursive does; otherwise every ratio in the tree
	// is re-derived one cell off on each sync, and a resize on one axis walks the
	// dividers on the other axis. Nested bounds must account for it too.
	gap := 0
	if config.SharedBorders {
		gap = 1
	}

	if node.SplitType == SplitStacked {
		// Stacked nodes ignore SplitRatio; mirror applyLayoutRecursive's bounds so
		// descendants still resolve against the rectangle they were laid out in.
		const titleBarHeight = 1
		content := Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: bounds.H - titleBarHeight}
		title := Rect{X: bounds.X, Y: bounds.Y + bounds.H - titleBarHeight, W: bounds.W, H: titleBarHeight}
		if node.StackedActiveLeft {
			t.syncRatiosRecursive(node.Left, content, windows)
			t.syncRatiosRecursive(node.Right, title, windows)
		} else {
			title.Y = bounds.Y
			content.Y = bounds.Y + titleBarHeight
			t.syncRatiosRecursive(node.Left, title, windows)
			t.syncRatiosRecursive(node.Right, content, windows)
		}
		return
	}

	// A split whose divider already sits where its stored ratio puts it has
	// nothing to learn from the geometry, and re-deriving it is not free: the
	// ratio is a float, the geometry is whole cells, and applyLayoutRecursive
	// truncates. Reading a truncated divider back as line/extent therefore
	// rounds the ratio down every time, never up, so a split nobody dragged
	// walks off centre one pass at a time. An exact 0.5 in a 29-row region comes
	// back as 0.482759 after a single pass, and the next region size that ratio
	// is applied at hands the extra rows to one child instead of sharing them.
	//
	// So the rule is: sync may only move the ratios whose geometry actually
	// disagrees with the tree. That is exactly the set the paths this function
	// exists for change - master-stack, floating windows, windows outside the
	// tree, and the geometry-scan fallback - and it leaves every other split
	// holding the value a resize deliberately put there.
	expectedLeft, expectedRight := childBounds(node, bounds)

	// Calculate the actual split ratio from window geometry.
	if node.SplitType == SplitVertical {
		// The split boundary is the near edge of the right subtree's leftmost
		// leaf, which sits flush against the divider. Reading node.Right gives
		// the true boundary even when node.Left is itself split on this axis
		// (its leftmost leaf would report an inner divider, not the boundary).
		// Fall back to the left subtree's far edge only when the right subtree
		// has no known geometry.
		splitX, ok := -1, false
		if id := t.findAnyWindowInSubtree(node.Right); id != -1 {
			if r, found := windows[id]; found {
				splitX, ok = r.X-gap, true
			}
		}
		if !ok {
			if id := t.findAnyWindowInSubtree(node.Left); id != -1 {
				if r, found := windows[id]; found {
					splitX, ok = r.X+r.W, true
				}
			}
		}
		if !ok {
			return
		}
		leftBounds, rightBounds := expectedLeft, expectedRight
		if splitX != expectedLeft.X+expectedLeft.W {
			if bounds.W > 0 {
				node.SplitRatio = float64(splitX-bounds.X) / float64(bounds.W)
			}
			// Recurse with updated bounds
			leftBounds = Rect{X: bounds.X, Y: bounds.Y, W: splitX - bounds.X, H: bounds.H}
			rightBounds = Rect{X: splitX + gap, Y: bounds.Y, W: bounds.X + bounds.W - splitX - gap, H: bounds.H}
		}
		t.syncRatiosRecursive(node.Left, leftBounds, windows)
		t.syncRatiosRecursive(node.Right, rightBounds, windows)
	} else {
		// The split boundary is the near (top) edge of the bottom subtree's
		// topmost leaf. Fall back to the top subtree's bottom edge only when the
		// bottom subtree has no known geometry.
		splitY, ok := -1, false
		if id := t.findAnyWindowInSubtree(node.Right); id != -1 {
			if r, found := windows[id]; found {
				splitY, ok = r.Y-gap, true
			}
		}
		if !ok {
			if id := t.findAnyWindowInSubtree(node.Left); id != -1 {
				if r, found := windows[id]; found {
					splitY, ok = r.Y+r.H, true
				}
			}
		}
		if !ok {
			return
		}
		leftBounds, rightBounds := expectedLeft, expectedRight
		if splitY != expectedLeft.Y+expectedLeft.H {
			if bounds.H > 0 {
				node.SplitRatio = float64(splitY-bounds.Y) / float64(bounds.H)
			}
			// Recurse with updated bounds
			leftBounds = Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: splitY - bounds.Y}
			rightBounds = Rect{X: bounds.X, Y: splitY + gap, W: bounds.W, H: bounds.Y + bounds.H - splitY - gap}
		}
		t.syncRatiosRecursive(node.Left, leftBounds, windows)
		t.syncRatiosRecursive(node.Right, rightBounds, windows)
	}
}

// findAnyWindowInSubtree finds any window ID in the given subtree
func (t *BSPTree) findAnyWindowInSubtree(node *TileNode) int {
	if node == nil {
		return -1
	}
	if node.IsLeaf() {
		return node.WindowID
	}
	// Try left first, then right
	if id := t.findAnyWindowInSubtree(node.Left); id != -1 {
		return id
	}
	return t.findAnyWindowInSubtree(node.Right)
}

// determineAutoSplit determines the split direction based on the auto scheme
func (t *BSPTree) determineAutoSplit(targetNode *TileNode, bounds Rect) SplitType {
	switch t.AutoScheme {
	case SchemeLongestSide:
		// Split along the longest dimension
		if bounds.W >= bounds.H {
			return SplitVertical
		}
		return SplitHorizontal

	case SchemeAlternate:
		// Alternate V, H, V, H based on total number of splits (internal nodes) in tree
		// This gives a proper alternating pattern regardless of which window is split
		splitCount := t.countInternalNodes()
		// Even count (0, 2, 4...) = Vertical (left|right)
		// Odd count (1, 3, 5...) = Horizontal (top/bottom)
		if splitCount%2 == 0 {
			return SplitVertical
		}
		return SplitHorizontal

	case SchemeSpiral:
		// bspwm-style spiral: alternate the split axis on the depth of the
		// window being split, so repeatedly splitting the newest (deepest)
		// window rotates V, H, V, H. Unlike SchemeAlternate this keys off the
		// target node rather than the global split count, so splitting a
		// shallower window follows that window's own depth parity.
		if targetNode.Depth()%2 == 0 {
			return SplitVertical
		}
		return SplitHorizontal

	case SchemeSmartSplit:
		// Compute the focused window's actual rect from the BSP tree
		// nodeBounds mirrors the layout exactly, separator gap and stacked
		// title bars included, so the aspect ratio this heuristic reads is the
		// one on screen.
		r, ok := t.nodeBounds(targetNode, bounds)
		if !ok {
			r = bounds
		}
		w, h := r.W, r.H
		if w > h*2 {
			// Very wide window: split vertically (side by side)
			return SplitVertical
		}
		if h > w {
			// Tall window: split horizontally (stacked)
			return SplitHorizontal
		}
		// Otherwise alternate based on split depth
		depth := targetNode.Depth()
		if depth%2 == 0 {
			return SplitVertical
		}
		return SplitHorizontal

	default:
		return SplitVertical
	}
}

// countInternalNodes counts the number of internal (non-leaf) nodes in the tree
func (t *BSPTree) countInternalNodes() int {
	return countInternalNodesRecursive(t.Root)
}

func countInternalNodesRecursive(node *TileNode) int {
	if node == nil || node.IsLeaf() {
		return 0
	}
	return 1 + countInternalNodesRecursive(node.Left) + countInternalNodesRecursive(node.Right)
}

// findAnyLeaf finds any leaf node in the tree
func (t *BSPTree) findAnyLeaf() *TileNode {
	return findLeafInSubtree(t.Root)
}

func findLeafInSubtree(node *TileNode) *TileNode {
	if node == nil {
		return nil
	}
	if node.IsLeaf() {
		return node
	}
	if leaf := findLeafInSubtree(node.Left); leaf != nil {
		return leaf
	}
	return findLeafInSubtree(node.Right)
}

// RotateSplit toggles the split direction at the parent of the given window
func (t *BSPTree) RotateSplit(windowID int) {
	node := t.WindowToNode[windowID]
	if node == nil || node.Parent == nil {
		return
	}

	parent := node.Parent
	if parent.SplitType == SplitVertical {
		parent.SplitType = SplitHorizontal
	} else {
		parent.SplitType = SplitVertical
	}
}

// SwapWindows swaps the positions of two windows in the tree
func (t *BSPTree) SwapWindows(windowID1, windowID2 int) {
	node1 := t.WindowToNode[windowID1]
	node2 := t.WindowToNode[windowID2]
	if node1 == nil || node2 == nil {
		return
	}

	// Swap window IDs in the nodes
	node1.WindowID, node2.WindowID = node2.WindowID, node1.WindowID

	// Update the lookup map
	t.WindowToNode[windowID1] = node2
	t.WindowToNode[windowID2] = node1
}

// EqualizeRatios sets all split ratios to 0.5
func (t *BSPTree) EqualizeRatios() {
	equalizeRatiosRecursive(t.Root)
}

func equalizeRatiosRecursive(node *TileNode) {
	if node == nil || node.IsLeaf() {
		return
	}
	node.SplitRatio = 0.5
	equalizeRatiosRecursive(node.Left)
	equalizeRatiosRecursive(node.Right)
}

// SplitLine represents a separator line between two panes in shared border mode.
type SplitLine struct {
	Vertical bool // true = vertical line (│), false = horizontal line (─)
	Pos      int  // X coordinate for vertical, Y coordinate for horizontal
	From     int  // start Y for vertical, start X for horizontal
	To       int  // end Y for vertical, end X for horizontal
}

// CollectSplits returns all separator line positions for shared border rendering.
// Must be called with the same bounds used for ApplyLayout.
func (t *BSPTree) CollectSplits(bounds Rect) []SplitLine {
	var splits []SplitLine
	if t.Root == nil {
		return splits
	}
	t.collectSplitsRecursive(t.Root, bounds, &splits)
	return splits
}

func (t *BSPTree) collectSplitsRecursive(node *TileNode, bounds Rect, splits *[]SplitLine) {
	if node == nil || node.IsLeaf() {
		return
	}

	// The separator the user sees and grabs has to be the one the layout
	// actually put there, so the position comes from childBounds rather than
	// from a second copy of the split arithmetic. A separator drawn where no
	// divider is means the pointer offers a resize cursor over a line that
	// dragging will not move.
	leftBounds, rightBounds := childBounds(node, bounds)

	// A stacked node splits by raising one child's title bar, not by a
	// separator, and ResizeSplit will not move one. Emitting a line for it
	// would advertise a divider that cannot be dragged.
	if node.SplitType != SplitStacked {
		if node.SplitType == SplitVertical {
			*splits = append(*splits, SplitLine{
				Vertical: true,
				Pos:      bounds.X + leftBounds.W,
				From:     bounds.Y,
				To:       bounds.Y + bounds.H - 1,
			})
		} else {
			*splits = append(*splits, SplitLine{
				Vertical: false,
				Pos:      bounds.Y + leftBounds.H,
				From:     bounds.X,
				To:       bounds.X + bounds.W - 1,
			})
		}
	}

	t.collectSplitsRecursive(node.Left, leftBounds, splits)
	t.collectSplitsRecursive(node.Right, rightBounds, splits)
}

// GetAllWindowIDs returns all window IDs in the tree (in-order traversal)
func (t *BSPTree) GetAllWindowIDs() []int {
	var ids []int
	collectWindowIDs(t.Root, &ids)
	return ids
}

// GetNextSplitDirection returns the direction of the next auto-split ("V" or "H")
// based on the current tree state and auto scheme. It mirrors determineAutoSplit
// so the dock indicator agrees with the axis an auto-insert would actually pick.
func (t *BSPTree) GetNextSplitDirection() string {
	if t == nil {
		return "V" // Default to vertical for empty tree
	}

	if t.AutoScheme == SchemeSpiral {
		// Spiral alternates on the depth of the window being split. The next
		// auto-insert splits the deepest (most recently split) leaf, so predict
		// from that leaf's depth to stay consistent with determineAutoSplit.
		if t.deepestLeafDepth()%2 == 0 {
			return "V"
		}
		return "H"
	}

	// Alternate and the remaining schemes fall back to global split parity.
	if t.countInternalNodes()%2 == 0 {
		return "V" // Vertical split (left|right)
	}
	return "H" // Horizontal split (top/bottom)
}

// deepestLeafDepth returns the depth of the deepest leaf in the tree, or 0 if
// the tree is empty. In a spiral workflow the next window splits the most
// recently split (deepest) leaf, so its depth predicts the next split axis.
func (t *BSPTree) deepestLeafDepth() int {
	return maxLeafDepth(t.Root, 0)
}

func maxLeafDepth(node *TileNode, depth int) int {
	if node == nil {
		return 0
	}
	if node.IsLeaf() {
		return depth
	}
	left := maxLeafDepth(node.Left, depth+1)
	right := maxLeafDepth(node.Right, depth+1)
	if left > right {
		return left
	}
	return right
}

func collectWindowIDs(node *TileNode, ids *[]int) {
	if node == nil {
		return
	}
	if node.IsLeaf() {
		*ids = append(*ids, node.WindowID)
		return
	}
	collectWindowIDs(node.Left, ids)
	collectWindowIDs(node.Right, ids)
}

// Clone creates a deep copy of the tree
func (t *BSPTree) Clone() *BSPTree {
	if t == nil {
		return nil
	}

	newTree := &BSPTree{
		WindowToNode: make(map[int]*TileNode),
		AutoScheme:   t.AutoScheme,
		DefaultRatio: t.DefaultRatio,
	}

	if t.Root != nil {
		newTree.Root = cloneNode(t.Root, nil, newTree.WindowToNode)
	}

	return newTree
}

func cloneNode(node *TileNode, parent *TileNode, windowMap map[int]*TileNode) *TileNode {
	if node == nil {
		return nil
	}

	newNode := &TileNode{
		ID:         newNodeID(),
		Parent:     parent,
		WindowID:   node.WindowID,
		SplitType:  node.SplitType,
		SplitRatio: node.SplitRatio,
	}

	if node.IsLeaf() {
		windowMap[node.WindowID] = newNode
	} else {
		newNode.Left = cloneNode(node.Left, newNode, windowMap)
		newNode.Right = cloneNode(node.Right, newNode, windowMap)
	}

	return newNode
}

// SerializedNode represents a BSP tree node in a serializable format
type SerializedNode struct {
	WindowID   int             `json:"window_id"`       // -1 for internal nodes
	SplitType  int             `json:"split_type"`      // 0=none, 1=vertical, 2=horizontal
	SplitRatio float64         `json:"split_ratio"`     // Position of split (0.0-1.0)
	Left       *SerializedNode `json:"left,omitempty"`  // Left/Top child
	Right      *SerializedNode `json:"right,omitempty"` // Right/Bottom child
}

// SerializedBSPTree represents a BSP tree in a serializable format
type SerializedBSPTree struct {
	Root         *SerializedNode `json:"root,omitempty"`
	AutoScheme   int             `json:"auto_scheme"` // 0=longest_side, 1=alternate, 2=spiral
	DefaultRatio float64         `json:"default_ratio"`
}

// Serialize converts the BSP tree to a serializable format
func (t *BSPTree) Serialize() *SerializedBSPTree {
	if t == nil {
		return nil
	}
	return &SerializedBSPTree{
		Root:         serializeNode(t.Root),
		AutoScheme:   int(t.AutoScheme),
		DefaultRatio: t.DefaultRatio,
	}
}

func serializeNode(node *TileNode) *SerializedNode {
	if node == nil {
		return nil
	}
	return &SerializedNode{
		WindowID:   node.WindowID,
		SplitType:  int(node.SplitType),
		SplitRatio: node.SplitRatio,
		Left:       serializeNode(node.Left),
		Right:      serializeNode(node.Right),
	}
}

// Deserialize converts a serialized BSP tree back to a BSPTree
func (s *SerializedBSPTree) Deserialize() *BSPTree {
	if s == nil {
		return NewBSPTree()
	}
	tree := &BSPTree{
		WindowToNode: make(map[int]*TileNode),
		AutoScheme:   AutoScheme(s.AutoScheme),
		DefaultRatio: s.DefaultRatio,
	}
	tree.Root = deserializeNode(s.Root, nil, tree.WindowToNode)
	return tree
}

func deserializeNode(s *SerializedNode, parent *TileNode, windowMap map[int]*TileNode) *TileNode {
	if s == nil {
		return nil
	}
	node := &TileNode{
		ID:         newNodeID(),
		Parent:     parent,
		WindowID:   s.WindowID,
		SplitType:  SplitType(s.SplitType),
		SplitRatio: s.SplitRatio,
	}
	if node.IsLeaf() && node.WindowID >= 0 {
		windowMap[node.WindowID] = node
	}
	node.Left = deserializeNode(s.Left, node, windowMap)
	node.Right = deserializeNode(s.Right, node, windowMap)
	return node
}
