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
	ID         uint64    // Unique identifier for the node
	Parent     *TileNode // Parent node (nil for root)
	Left       *TileNode // Left/Top child (nil for leaf nodes)
	Right      *TileNode // Right/Bottom child (nil for leaf nodes)
	WindowID   int       // Window ID (-1 for internal nodes)
	SplitType  SplitType // How this node splits its space
	SplitRatio float64   // Position of split (0.0-1.0), 0.5 = middle
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
	if t.Root == nil {
		return result
	}
	t.applyLayoutRecursive(t.Root, bounds, result)
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

	// Internal node - split the bounds
	var leftBounds, rightBounds Rect

	if node.SplitType == SplitVertical {
		// Vertical split: left | right
		splitX := bounds.X + int(float64(bounds.W)*node.SplitRatio)
		leftBounds = Rect{X: bounds.X, Y: bounds.Y, W: splitX - bounds.X, H: bounds.H}
		rightBounds = Rect{X: splitX, Y: bounds.Y, W: bounds.X + bounds.W - splitX, H: bounds.H}
	} else {
		// Horizontal split: top / bottom
		splitY := bounds.Y + int(float64(bounds.H)*node.SplitRatio)
		leftBounds = Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: splitY - bounds.Y}
		rightBounds = Rect{X: bounds.X, Y: splitY, W: bounds.W, H: bounds.Y + bounds.H - splitY}
	}

	t.applyLayoutRecursive(node.Left, leftBounds, result)
	t.applyLayoutRecursive(node.Right, rightBounds, result)
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

	// Find a window in the left subtree to determine the split position
	leftWindowID := t.findAnyWindowInSubtree(node.Left)
	if leftWindowID == -1 {
		return
	}

	leftRect, ok := windows[leftWindowID]
	if !ok {
		return
	}

	// Calculate the actual split ratio from window geometry
	if node.SplitType == SplitVertical {
		// The right edge of left windows defines the split position
		splitX := leftRect.X + leftRect.W
		if bounds.W > 0 {
			node.SplitRatio = float64(splitX-bounds.X) / float64(bounds.W)
		}
		// Recurse with updated bounds
		leftBounds := Rect{X: bounds.X, Y: bounds.Y, W: splitX - bounds.X, H: bounds.H}
		rightBounds := Rect{X: splitX, Y: bounds.Y, W: bounds.X + bounds.W - splitX, H: bounds.H}
		t.syncRatiosRecursive(node.Left, leftBounds, windows)
		t.syncRatiosRecursive(node.Right, rightBounds, windows)
	} else {
		// The bottom edge of top windows defines the split position
		splitY := leftRect.Y + leftRect.H
		if bounds.H > 0 {
			node.SplitRatio = float64(splitY-bounds.Y) / float64(bounds.H)
		}
		// Recurse with updated bounds
		leftBounds := Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: splitY - bounds.Y}
		rightBounds := Rect{X: bounds.X, Y: splitY, W: bounds.W, H: bounds.Y + bounds.H - splitY}
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
func (t *BSPTree) determineAutoSplit(_ *TileNode, bounds Rect) SplitType {
	switch t.AutoScheme {
	case SchemeLongestSide:
		// Split along the longest dimension
		if bounds.W >= bounds.H {
			return SplitVertical
		}
		return SplitHorizontal

	case SchemeAlternate, SchemeSpiral:
		// Alternate V, H, V, H based on total number of splits (internal nodes) in tree
		// This gives a proper alternating pattern regardless of which window is split
		splitCount := t.countInternalNodes()
		// Even count (0, 2, 4...) = Vertical (left|right)
		// Odd count (1, 3, 5...) = Horizontal (top/bottom)
		if splitCount%2 == 0 {
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

// GetAllWindowIDs returns all window IDs in the tree (in-order traversal)
func (t *BSPTree) GetAllWindowIDs() []int {
	var ids []int
	collectWindowIDs(t.Root, &ids)
	return ids
}

// GetNextSplitDirection returns the direction of the next auto-split ("V" or "H")
// based on the current tree state and auto scheme.
func (t *BSPTree) GetNextSplitDirection() string {
	if t == nil {
		return "V" // Default to vertical for empty tree
	}

	// For spiral/alternate schemes, based on internal node count
	splitCount := t.countInternalNodes()
	if splitCount%2 == 0 {
		return "V" // Vertical split (left|right)
	}
	return "H" // Horizontal split (top/bottom)
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

// BuildTreeFromWindows creates a BSP tree from existing window geometries.
// This is used when enabling tiling mode to create a tree that matches
// the current window layout.
func BuildTreeFromWindows(windows []Rect, windowIDs []int, bounds Rect, scheme AutoScheme) *BSPTree {
	tree := NewBSPTree()
	tree.AutoScheme = scheme

	if len(windows) == 0 || len(windowIDs) == 0 {
		return tree
	}

	if len(windows) != len(windowIDs) {
		return tree
	}

	// For a single window, just create a leaf
	if len(windows) == 1 {
		tree.Root = NewLeafNode(windowIDs[0])
		tree.WindowToNode[windowIDs[0]] = tree.Root
		return tree
	}

	// Build tree by recursively partitioning windows
	tree.Root = buildTreeRecursive(windows, windowIDs, bounds, tree)
	return tree
}

// buildTreeRecursive builds a subtree from windows within the given bounds
func buildTreeRecursive(windows []Rect, windowIDs []int, bounds Rect, tree *BSPTree) *TileNode {
	if len(windows) == 0 {
		return nil
	}

	if len(windows) == 1 {
		node := NewLeafNode(windowIDs[0])
		tree.WindowToNode[windowIDs[0]] = node
		return node
	}

	// Try to find a split that partitions the windows
	// First, try vertical split
	splitX, leftWindows, leftIDs, rightWindows, rightIDs := findVerticalSplit(windows, windowIDs, bounds)
	if len(leftWindows) > 0 && len(rightWindows) > 0 {
		ratio := float64(splitX-bounds.X) / float64(bounds.W)
		leftBounds := Rect{X: bounds.X, Y: bounds.Y, W: splitX - bounds.X, H: bounds.H}
		rightBounds := Rect{X: splitX, Y: bounds.Y, W: bounds.X + bounds.W - splitX, H: bounds.H}

		leftNode := buildTreeRecursive(leftWindows, leftIDs, leftBounds, tree)
		rightNode := buildTreeRecursive(rightWindows, rightIDs, rightBounds, tree)
		return NewInternalNode(SplitVertical, ratio, leftNode, rightNode)
	}

	// Try horizontal split
	splitY, topWindows, topIDs, bottomWindows, bottomIDs := findHorizontalSplit(windows, windowIDs, bounds)
	if len(topWindows) > 0 && len(bottomWindows) > 0 {
		ratio := float64(splitY-bounds.Y) / float64(bounds.H)
		topBounds := Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: splitY - bounds.Y}
		bottomBounds := Rect{X: bounds.X, Y: splitY, W: bounds.W, H: bounds.Y + bounds.H - splitY}

		topNode := buildTreeRecursive(topWindows, topIDs, topBounds, tree)
		bottomNode := buildTreeRecursive(bottomWindows, bottomIDs, bottomBounds, tree)
		return NewInternalNode(SplitHorizontal, ratio, topNode, bottomNode)
	}

	// Can't partition cleanly - just take the first window
	// This handles overlapping or irregular layouts
	node := NewLeafNode(windowIDs[0])
	tree.WindowToNode[windowIDs[0]] = node
	return node
}

// findVerticalSplit finds a vertical split line that partitions windows into left and right groups
func findVerticalSplit(windows []Rect, windowIDs []int, bounds Rect) (splitX int, leftWindows []Rect, leftIDs []int, rightWindows []Rect, rightIDs []int) {
	// Collect all right edges as potential split points
	splitCandidates := make(map[int]bool)
	for _, w := range windows {
		rightEdge := w.X + w.W
		if rightEdge > bounds.X && rightEdge < bounds.X+bounds.W {
			splitCandidates[rightEdge] = true
		}
	}

	// Try each candidate split
	for candidate := range splitCandidates {
		var left, right []Rect
		var leftI, rightI []int

		for i, w := range windows {
			center := w.X + w.W/2
			if center < candidate {
				left = append(left, w)
				leftI = append(leftI, windowIDs[i])
			} else {
				right = append(right, w)
				rightI = append(rightI, windowIDs[i])
			}
		}

		// Valid split if both sides have windows
		if len(left) > 0 && len(right) > 0 {
			return candidate, left, leftI, right, rightI
		}
	}

	return 0, nil, nil, nil, nil
}

// findHorizontalSplit finds a horizontal split line that partitions windows into top and bottom groups
func findHorizontalSplit(windows []Rect, windowIDs []int, bounds Rect) (splitY int, topWindows []Rect, topIDs []int, bottomWindows []Rect, bottomIDs []int) {
	// Collect all bottom edges as potential split points
	splitCandidates := make(map[int]bool)
	for _, w := range windows {
		bottomEdge := w.Y + w.H
		if bottomEdge > bounds.Y && bottomEdge < bounds.Y+bounds.H {
			splitCandidates[bottomEdge] = true
		}
	}

	// Try each candidate split
	for candidate := range splitCandidates {
		var top, bottom []Rect
		var topI, bottomI []int

		for i, w := range windows {
			center := w.Y + w.H/2
			if center < candidate {
				top = append(top, w)
				topI = append(topI, windowIDs[i])
			} else {
				bottom = append(bottom, w)
				bottomI = append(bottomI, windowIDs[i])
			}
		}

		// Valid split if both sides have windows
		if len(top) > 0 && len(bottom) > 0 {
			return candidate, top, topI, bottom, bottomI
		}
	}

	return 0, nil, nil, nil, nil
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
