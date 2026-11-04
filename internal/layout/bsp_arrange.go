package layout

import "slices"

// The rules that decide where a pane goes in a workspace's tree. They are
// here, beside the tree, so every client that lays panes out by the tree
// places a pane the same way: the terminal client and the desktop app put a
// new pane, and a pane the tree is missing, in the same leaf on the same
// side.

// AddWindow puts a new window into the tree by splitting target: toward
// preselect when one is set, else on the side the tree's auto scheme picks,
// at an even split. A target the tree does not hold, 0 included, is the
// tree's last leaf, and into an empty tree the window becomes the root.
func (t *BSPTree) AddWindow(windowID, target int, preselect PreselectionDir, bounds Rect, gap int) {
	if !t.HasWindow(target) {
		target = t.lastLeaf()
	}
	if preselect != PreselectionNone {
		t.InsertWindowWithPreselection(windowID, target, preselect, bounds, gap)
		return
	}
	t.InsertWindow(windowID, target, SplitNone, 0.5, bounds, gap)
}

// InsertMissing puts every window of ids the tree does not hold into it, in
// the order of ids, each one splitting the tree's last leaf by the auto
// scheme. Into an empty tree that is a chain, each window splitting the one
// before it, which is how a workspace is tiled from nothing. It reports
// whether any window went in.
func (t *BSPTree) InsertMissing(ids []int, bounds Rect, gap int) bool {
	added := false
	for _, id := range ids {
		if t.HasWindow(id) {
			continue
		}
		t.InsertWindow(id, t.lastLeaf(), SplitNone, 0.5, bounds, gap)
		added = true
	}
	return added
}

// PruneTo removes every window not in ids from the tree, and the split each
// one shared goes to its sibling, so the arrangement of the windows that stay
// is kept. It reports whether any window went.
func (t *BSPTree) PruneTo(ids []int) bool {
	pruned := false
	for _, id := range t.GetAllWindowIDs() {
		if !slices.Contains(ids, id) {
			t.RemoveWindow(id)
			pruned = true
		}
	}
	return pruned
}

// lastLeaf is the window of the tree's last leaf in reading order, or 0 for
// an empty tree.
func (t *BSPTree) lastLeaf() int {
	ids := t.GetAllWindowIDs()
	if len(ids) == 0 {
		return 0
	}
	return ids[len(ids)-1]
}
