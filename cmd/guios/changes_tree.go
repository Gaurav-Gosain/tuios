package main

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// The Changes tab's tree is the files changed, in their folders: a click
// on a file opens the viewer on their diffs, scrolled to it, one on a
// folder folds it, and while the viewer shows them the file whose diff is
// at the top shows as the one there, the tree scrolling along to it. It
// lists them in the diffs' order, the worktree's paths', so that it reads
// down as they do.

// treeNode is a folder of the tree, or a file: a folder with a folder
// alone in it is one, named "internal/auth", as GitHub shows them.
type treeNode struct {
	name string
	path string // the folder's, or the file's
	file int    // the file's index in the list, -1 for a folder
	kids []*treeNode
}

// treeItem is a row of the tree as it shows, its folders' folds as the
// user left them.
type treeItem struct {
	*treeNode
	depth int
	open  bool
}

// fileTree makes the tree of a list's files, in their order.
func fileTree(files []proto.GitFile) []*treeNode {
	root := &treeNode{file: -1}
	dirs := map[string]*treeNode{}
	for i, f := range files {
		parent := root
		parts := strings.Split(f.Path, "/")
		for j, name := range parts[:len(parts)-1] {
			p := strings.Join(parts[:j+1], "/")
			d := dirs[p]
			if d == nil {
				d = &treeNode{name: name, path: p, file: -1}
				dirs[p] = d
				parent.kids = append(parent.kids, d)
			}
			parent = d
		}
		parent.kids = append(parent.kids, &treeNode{name: parts[len(parts)-1], path: f.Path, file: i})
	}
	var squash func(n *treeNode)
	squash = func(n *treeNode) {
		for _, k := range n.kids {
			for k.file < 0 && len(k.kids) == 1 && k.kids[0].file < 0 {
				only := k.kids[0]
				k.name, k.path, k.kids = k.name+"/"+only.name, only.path, only.kids
			}
			squash(k)
		}
	}
	squash(root)
	return root.kids
}

// treeItems are the rows of a tree, but those inside a folder closed.
func treeItems(nodes []*treeNode, closed func(dir string) bool) []treeItem {
	var items []treeItem
	var walk func(nodes []*treeNode, depth int)
	walk = func(nodes []*treeNode, depth int) {
		for _, n := range nodes {
			open := n.file < 0 && !closed(n.path)
			items = append(items, treeItem{n, depth, open})
			if open {
				walk(n.kids, depth+1)
			}
		}
	}
	walk(nodes, 0)
	return items
}

// changesTree is the tree of the files the Changes tab lists.
func (a *App) changesTree(c *ui.Context, pal *palette, s *proto.Session, cf *changesFrame) {
	ch := &a.side.changes
	res, scope := cf.list, cf.scope
	if ch.treeOf != res {
		ch.tree, ch.treeOf = fileTree(res.Files), res
	}
	items := treeItems(ch.tree, func(dir string) bool { return ch.treeClosed[scope+"|"+dir] })

	// The file there, while the viewer shows the diffs: the one whose diff
	// is at the top; at the end of the list, where the file chosen last
	// may not come to the top, that one while it shows.
	cur := ""
	if ls := cf.ls; ls != nil {
		first, last := ls.Visible()
		if first >= 0 && first < len(cf.rows) {
			cur = cf.rows[first].f.Path
		}
		if ch.picked != "" && ls.AtEnd() {
			for i, f := range res.Files {
				if f.Path == ch.picked && cf.heads[i] >= first && cf.heads[i] <= last {
					cur = f.Path
				}
			}
		}
	}

	if ch.treePlaces == nil {
		ch.treePlaces = map[string]*ui.ListState{}
	}
	tl := ch.treePlaces[scope]
	if tl == nil {
		tl = &ui.ListState{}
		ch.treePlaces[scope] = tl
	}
	tl.Key = func(i int) any { return treeKey{items[i].path, items[i].file < 0} }
	if cur != ch.current {
		ch.current = cur
		for i, it := range items {
			if it.file >= 0 && it.path == cur {
				tl.ScrollIntoView(i)
			}
		}
	}
	ui.List(c.Key("tree"), tl, len(items), func(i int) {
		it := items[i]
		if it.file < 0 {
			a.treeFolder(c, pal, it.name, it.path, it.depth, it.open, false, func() {
				if ch.treeClosed == nil {
					ch.treeClosed = map[string]bool{}
				}
				ch.treeClosed[scope+"|"+it.path] = it.open
			})
			return
		}
		f := res.Files[it.file]
		a.treeFile(c, pal, it, f, f.Path == cur, func() {
			ch.picked = f.Path
			a.focusViewer()
			if cf.ls != nil {
				cf.ls.ScrollTo(cf.heads[it.file], ui.Start)
				return
			}
			ch.reveal = f.Path
			a.openView(s, sideChanges, nil)
		})
	}).Grow(1).MinHeight(0).Padding(0, 10, 16, 10)
}

// treeKey is what a row of a tree is, as its list keeps its state by: a
// file and a folder may have the same path, as one became the other.
type treeKey struct {
	path string
	dir  bool
}

// treeRow is a row of a tree, depth folders in, the one there chosen as
// a session's row is.
func treeRow(c *ui.Context, pal *palette, depth int, current bool) ui.Element {
	row := ui.Row(c.Key("row")).Height(26).Padding(0, 8, 0, 6+12*float32(depth)).Gap(5).AlignItems(ui.Center).Radius(rRow)
	if pal.dark {
		// Room for the chosen row's edge in every row.
		row.Border(1, ui.Transparent)
	}
	switch {
	case current:
		row.Background(pal.rowSelected)
		chosen(row, pal)
	case row.Hovered():
		row.Background(pal.rowHover)
	}
	return row
}

// treeFolder is a folder of a tree, which a click folds or unfolds;
// faded, it is one git ignores.
func (a *App) treeFolder(c *ui.Context, pal *palette, name, tip string, depth int, open, faded bool, toggle func()) ui.Element {
	row := treeRow(c, pal, depth, false).Tooltip(tip)
	if faded {
		row.Opacity(0.55)
	}
	if row.Clicked() {
		toggle()
	}
	row.Children(func() {
		chev := ui.Icon(c, chevronIcon).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
		chev.Rotate(chev.AnimateTo("turn", 90*b2f(open), ui.Snappy))
		fileIconImage(c, pal, name, true, open)
		ui.Text(c, name).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0)
	})
	return row
}

// treeFile is a changed file of the tree: its icon, its name, and its
// change as a letter in its color at the right, as VS Code shows them.
func (a *App) treeFile(c *ui.Context, pal *palette, it treeItem, f proto.GitFile, current bool, pick func()) {
	tip := f.Path
	if f.OldPath != "" {
		tip = f.OldPath + " → " + f.Path
	}
	row := treeRow(c, pal, it.depth, current).Tooltip(tip)
	if row.Clicked() {
		pick()
	}
	row.Children(func() {
		ui.Box(c).Size(9, 9).Shrink(0)
		fileIconImage(c, pal, it.name, false, false)
		fg := pal.text
		if f.Status == "deleted" {
			fg = pal.textMuted
		}
		ui.Text(c, it.name).FontSize(fsBody).TextColor(fg).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
		letter, col := a.statusLetter(pal, f.Status)
		ui.Text(c, letter).FontSize(fsCaption).FontWeight(wSemibold).TextColor(col).SingleLine().Shrink(0)
	})
}
