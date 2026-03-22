package layout

import (
	"testing"
)

// TestNewBSPTree tests creating a new BSP tree
func TestNewBSPTree(t *testing.T) {
	tree := NewBSPTree()

	if tree == nil {
		t.Fatal("NewBSPTree returned nil")
	}
	if !tree.IsEmpty() {
		t.Error("New tree should be empty")
	}
	if tree.WindowCount() != 0 {
		t.Errorf("New tree should have 0 windows, got %d", tree.WindowCount())
	}
	if tree.AutoScheme != SchemeSpiral {
		t.Errorf("Expected default scheme SchemeSpiral, got %v", tree.AutoScheme)
	}
	if tree.DefaultRatio != 0.5 {
		t.Errorf("Expected default ratio 0.5, got %f", tree.DefaultRatio)
	}
}

// TestTileNode_NewLeafNode tests creating a leaf node
func TestTileNode_NewLeafNode(t *testing.T) {
	node := NewLeafNode(42)

	if node == nil {
		t.Fatal("NewLeafNode returned nil")
	}
	if node.WindowID != 42 {
		t.Errorf("Expected WindowID 42, got %d", node.WindowID)
	}
	if !node.IsLeaf() {
		t.Error("Node should be a leaf")
	}
	if !node.IsRoot() {
		t.Error("Node without parent should be considered root")
	}
	if node.ID == 0 {
		t.Error("Node ID should not be 0")
	}
}

// TestTileNode_NewInternalNode tests creating an internal node
func TestTileNode_NewInternalNode(t *testing.T) {
	left := NewLeafNode(1)
	right := NewLeafNode(2)
	parent := NewInternalNode(SplitVertical, 0.6, left, right)

	if parent == nil {
		t.Fatal("NewInternalNode returned nil")
	}
	if parent.IsLeaf() {
		t.Error("Internal node should not be a leaf")
	}
	if parent.SplitType != SplitVertical {
		t.Errorf("Expected SplitVertical, got %v", parent.SplitType)
	}
	if parent.SplitRatio != 0.6 {
		t.Errorf("Expected ratio 0.6, got %f", parent.SplitRatio)
	}
	if parent.WindowID != -1 {
		t.Errorf("Internal node should have WindowID -1, got %d", parent.WindowID)
	}

	// Check parent-child relationships
	if left.Parent != parent {
		t.Error("Left child's parent should be the internal node")
	}
	if right.Parent != parent {
		t.Error("Right child's parent should be the internal node")
	}
	if parent.Left != left {
		t.Error("Parent's left should be the left child")
	}
	if parent.Right != right {
		t.Error("Parent's right should be the right child")
	}
}

// TestTileNode_Sibling tests sibling node access
func TestTileNode_Sibling(t *testing.T) {
	left := NewLeafNode(1)
	right := NewLeafNode(2)
	_ = NewInternalNode(SplitVertical, 0.5, left, right)

	if left.Sibling() != right {
		t.Error("Left node's sibling should be right node")
	}
	if right.Sibling() != left {
		t.Error("Right node's sibling should be left node")
	}

	// Root node has no sibling
	root := NewLeafNode(3)
	if root.Sibling() != nil {
		t.Error("Root node should have no sibling")
	}
}

// TestTileNode_IsLeftChild tests left child detection
func TestTileNode_IsLeftChild(t *testing.T) {
	left := NewLeafNode(1)
	right := NewLeafNode(2)
	_ = NewInternalNode(SplitVertical, 0.5, left, right)

	if !left.IsLeftChild() {
		t.Error("Left node should be identified as left child")
	}
	if right.IsLeftChild() {
		t.Error("Right node should not be identified as left child")
	}

	// Root node is not a left child
	root := NewLeafNode(3)
	if root.IsLeftChild() {
		t.Error("Root node should not be a left child")
	}
}

// TestTileNode_Depth tests node depth calculation
func TestTileNode_Depth(t *testing.T) {
	// Build a small tree:
	//        parent2
	//       /      \
	//   parent1    leaf3
	//   /     \
	// leaf1  leaf2
	leaf1 := NewLeafNode(1)
	leaf2 := NewLeafNode(2)
	parent1 := NewInternalNode(SplitVertical, 0.5, leaf1, leaf2)
	leaf3 := NewLeafNode(3)
	parent2 := NewInternalNode(SplitHorizontal, 0.5, parent1, leaf3)

	if parent2.Depth() != 0 {
		t.Errorf("Root should have depth 0, got %d", parent2.Depth())
	}
	if parent1.Depth() != 1 {
		t.Errorf("Parent1 should have depth 1, got %d", parent1.Depth())
	}
	if leaf3.Depth() != 1 {
		t.Errorf("Leaf3 should have depth 1, got %d", leaf3.Depth())
	}
	if leaf1.Depth() != 2 {
		t.Errorf("Leaf1 should have depth 2, got %d", leaf1.Depth())
	}
	if leaf2.Depth() != 2 {
		t.Errorf("Leaf2 should have depth 2, got %d", leaf2.Depth())
	}
}

// TestBSPTree_InsertFirstWindow tests inserting the first window
func TestBSPTree_InsertFirstWindow(t *testing.T) {
	tree := NewBSPTree()
	bounds := Rect{X: 0, Y: 0, W: 100, H: 100}

	tree.InsertWindow(1, 0, SplitNone, 0.5, bounds)

	if tree.IsEmpty() {
		t.Error("Tree should not be empty after inserting window")
	}
	if tree.WindowCount() != 1 {
		t.Errorf("Expected 1 window, got %d", tree.WindowCount())
	}
	if !tree.HasWindow(1) {
		t.Error("Tree should contain window 1")
	}
	if tree.Root == nil {
		t.Fatal("Root should not be nil")
	}
	if !tree.Root.IsLeaf() {
		t.Error("Root should be a leaf for single window")
	}
	if tree.Root.WindowID != 1 {
		t.Errorf("Root should contain window 1, got %d", tree.Root.WindowID)
	}
}

// TestBSPTree_InsertSecondWindow tests inserting a second window
func TestBSPTree_InsertSecondWindow(t *testing.T) {
	tree := NewBSPTree()
	bounds := Rect{X: 0, Y: 0, W: 100, H: 100}

	// Insert first window
	tree.InsertWindow(1, 0, SplitNone, 0.5, bounds)

	// Insert second window
	tree.InsertWindow(2, 1, SplitVertical, 0.5, bounds)

	if tree.WindowCount() != 2 {
		t.Errorf("Expected 2 windows, got %d", tree.WindowCount())
	}
	if !tree.HasWindow(1) || !tree.HasWindow(2) {
		t.Error("Tree should contain both windows")
	}
	if tree.Root.IsLeaf() {
		t.Error("Root should now be an internal node")
	}
	if tree.Root.SplitType != SplitVertical {
		t.Errorf("Expected vertical split, got %v", tree.Root.SplitType)
	}
}

// TestBSPTree_InsertDuplicate tests that duplicate windows are not inserted
func TestBSPTree_InsertDuplicate(t *testing.T) {
	tree := NewBSPTree()
	bounds := Rect{X: 0, Y: 0, W: 100, H: 100}

	tree.InsertWindow(1, 0, SplitNone, 0.5, bounds)
	tree.InsertWindow(1, 0, SplitNone, 0.5, bounds) // Try to insert again

	if tree.WindowCount() != 1 {
		t.Errorf("Duplicate insert should be ignored, expected 1 window, got %d", tree.WindowCount())
	}
}

// TestBSPTree_RemoveWindow tests removing a window
func TestBSPTree_RemoveWindow(t *testing.T) {
	tree := NewBSPTree()
	bounds := Rect{X: 0, Y: 0, W: 100, H: 100}

	// Insert two windows
	tree.InsertWindow(1, 0, SplitNone, 0.5, bounds)
	tree.InsertWindow(2, 1, SplitVertical, 0.5, bounds)

	// Remove first window
	tree.RemoveWindow(1)

	if tree.WindowCount() != 1 {
		t.Errorf("Expected 1 window after removal, got %d", tree.WindowCount())
	}
	if tree.HasWindow(1) {
		t.Error("Window 1 should be removed")
	}
	if !tree.HasWindow(2) {
		t.Error("Window 2 should still be present")
	}
	if tree.Root == nil {
		t.Fatal("Root should not be nil")
	}
	if !tree.Root.IsLeaf() {
		t.Error("Root should be a leaf after removing sibling")
	}
	if tree.Root.WindowID != 2 {
		t.Errorf("Root should now be window 2, got %d", tree.Root.WindowID)
	}
}

// TestBSPTree_RemoveLastWindow tests removing the last window
func TestBSPTree_RemoveLastWindow(t *testing.T) {
	tree := NewBSPTree()
	bounds := Rect{X: 0, Y: 0, W: 100, H: 100}

	tree.InsertWindow(1, 0, SplitNone, 0.5, bounds)
	tree.RemoveWindow(1)

	if !tree.IsEmpty() {
		t.Error("Tree should be empty after removing last window")
	}
	if tree.WindowCount() != 0 {
		t.Errorf("Expected 0 windows, got %d", tree.WindowCount())
	}
	if tree.Root != nil {
		t.Error("Root should be nil after removing last window")
	}
}

// TestBSPTree_GetAllWindowIDs tests getting all window IDs
func TestBSPTree_GetAllWindowIDs(t *testing.T) {
	tree := NewBSPTree()
	bounds := Rect{X: 0, Y: 0, W: 100, H: 100}

	// Insert windows
	tree.InsertWindow(1, 0, SplitNone, 0.5, bounds)
	tree.InsertWindow(2, 1, SplitVertical, 0.5, bounds)
	tree.InsertWindow(3, 2, SplitHorizontal, 0.5, bounds)

	ids := tree.GetAllWindowIDs()

	if len(ids) != 3 {
		t.Fatalf("Expected 3 IDs, got %d", len(ids))
	}

	// Check all IDs are present (order doesn't matter)
	found := make(map[int]bool)
	for _, id := range ids {
		found[id] = true
	}

	for _, expectedID := range []int{1, 2, 3} {
		if !found[expectedID] {
			t.Errorf("Expected to find window ID %d", expectedID)
		}
	}
}

// TestBSPTree_SwapWindows tests swapping two windows in the tree
func TestBSPTree_SwapWindows(t *testing.T) {
	tree := NewBSPTree()
	bounds := Rect{X: 0, Y: 0, W: 100, H: 100}

	// Insert two windows
	tree.InsertWindow(1, 0, SplitNone, 0.5, bounds)
	tree.InsertWindow(2, 1, SplitVertical, 0.5, bounds)

	// Get original positions
	node1Before := tree.FindNode(1)
	node2Before := tree.FindNode(2)
	isLeft1 := node1Before.IsLeftChild()
	isLeft2 := node2Before.IsLeftChild()

	// Swap windows
	tree.SwapWindows(1, 2)

	// Verify positions are swapped
	node1After := tree.FindNode(1)
	node2After := tree.FindNode(2)

	if node1After.IsLeftChild() == isLeft1 {
		t.Error("Window 1 should have moved to different position")
	}
	if node2After.IsLeftChild() == isLeft2 {
		t.Error("Window 2 should have moved to different position")
	}
}

// TestSplitType_String tests string representation of split types
func TestSplitType_String(t *testing.T) {
	tests := []struct {
		split    SplitType
		expected string
	}{
		{SplitNone, "none"},
		{SplitVertical, "vertical"},
		{SplitHorizontal, "horizontal"},
	}

	for _, tt := range tests {
		if tt.split.String() != tt.expected {
			t.Errorf("SplitType %d: expected %q, got %q",
				tt.split, tt.expected, tt.split.String())
		}
	}
}

// TestAutoScheme_String tests string representation of auto schemes
func TestAutoScheme_String(t *testing.T) {
	tests := []struct {
		scheme   AutoScheme
		expected string
	}{
		{SchemeLongestSide, "longest_side"},
		{SchemeAlternate, "alternate"},
		{SchemeSpiral, "spiral"},
	}

	for _, tt := range tests {
		if tt.scheme.String() != tt.expected {
			t.Errorf("AutoScheme %d: expected %q, got %q",
				tt.scheme, tt.expected, tt.scheme.String())
		}
	}
}

// TestParseAutoScheme tests parsing auto scheme strings
func TestParseAutoScheme(t *testing.T) {
	tests := []struct {
		input    string
		expected AutoScheme
	}{
		{"alternate", SchemeAlternate},
		{"spiral", SchemeSpiral},
		{"longest_side", SchemeLongestSide},
		{"invalid", SchemeLongestSide}, // Default
		{"", SchemeLongestSide},        // Default
	}

	for _, tt := range tests {
		result := ParseAutoScheme(tt.input)
		if result != tt.expected {
			t.Errorf("ParseAutoScheme(%q): expected %v, got %v",
				tt.input, tt.expected, result)
		}
	}
}

// BenchmarkBSPTree_Insert benchmarks inserting windows
func BenchmarkBSPTree_Insert(b *testing.B) {
	bounds := Rect{X: 0, Y: 0, W: 1920, H: 1080}

	for b.Loop() {
		tree := NewBSPTree()
		lastID := 0
		for j := range 10 {
			tree.InsertWindow(j+1, lastID, SplitNone, 0.5, bounds)
			lastID = j + 1
		}
	}
}

// BenchmarkBSPTree_ApplyLayout benchmarks layout calculation
func BenchmarkBSPTree_ApplyLayout(b *testing.B) {
	tree := NewBSPTree()
	bounds := Rect{X: 0, Y: 0, W: 1920, H: 1080}

	// Build a tree with 10 windows
	lastID := 0
	for j := range 10 {
		tree.InsertWindow(j+1, lastID, SplitNone, 0.5, bounds)
		lastID = j + 1
	}

	b.ResetTimer()
	for b.Loop() {
		_ = tree.ApplyLayout(bounds)
	}
}
