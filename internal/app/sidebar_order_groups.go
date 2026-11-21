package app

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

type sidebarOrderBlock struct {
	key    string
	member bool
}

type sidebarOrderBlocks map[string]sidebarOrderBlock

const sidebarGlobalScope = "\x01global"

func sidebarRepoBlockKey(space, repo string) string {
	return space + "\x00r" + repo
}

func sidebarBlockScope(key string) string {
	scope, _, _ := strings.Cut(key, "\x00")
	return scope
}

func (m *OS) sidebarDrawnOrder() ([]string, sidebarOrderBlocks) {
	rows := sessiontree.GroupBySpace(localSessionNodes(m.BuildSessionTree().Sessions), m.sidebarRepoGrouping())
	order := make([]string, 0, len(rows))
	blocks := make(sidebarOrderBlocks, len(rows))
	for _, n := range rows {
		if n.Kind != sessiontree.KindSession {
			continue
		}
		order = append(order, n.ID)
		switch {
		case n.Worktree != nil:
			blocks[n.ID] = sidebarOrderBlock{key: sidebarRepoBlockKey(n.Space, n.Worktree.Repo), member: !n.GroupHead}
		case n.Global:
			blocks[n.ID] = sidebarOrderBlock{key: sidebarGlobalScope + "\x00s" + n.ID}
		default:
			blocks[n.ID] = sidebarOrderBlock{key: n.Space + "\x00s" + n.ID}
		}
	}
	return order, blocks
}

func (b sidebarOrderBlocks) moveGroup(order []string, own, target string, down bool) []string {
	moved := make([]string, 0, len(order))
	rest := make([]string, 0, len(order))
	for _, id := range order {
		if b[id].key == own {
			moved = append(moved, id)
		} else {
			rest = append(rest, id)
		}
	}
	at := slices.IndexFunc(rest, func(id string) bool { return b[id].key == target })
	if at < 0 {
		return order
	}
	if down {
		for at < len(rest) && b[rest[at]].key == target {
			at++
		}
	}
	return slices.Insert(rest, at, moved...)
}

func (b sidebarOrderBlocks) stepGroup(order []string, key string, delta int) ([]string, bool) {
	var keys []string
	for _, id := range order {
		k := b[id].key
		if sidebarBlockScope(k) == sidebarBlockScope(key) && (len(keys) == 0 || keys[len(keys)-1] != k) {
			keys = append(keys, k)
		}
	}
	at := slices.Index(keys, key)
	to := at + delta
	if at < 0 || to < 0 || to >= len(keys) {
		return order, false
	}
	return b.moveGroup(order, key, keys[to], delta > 0), true
}

func (b sidebarOrderBlocks) stepRow(order []string, id string, delta int) ([]string, bool) {
	own := b[id]
	if !own.member {
		return b.stepGroup(order, own.key, delta)
	}
	from := slices.Index(order, id)
	to := from + delta
	if from < 0 || to < 0 || to >= len(order) || b[order[to]].key != own.key || !b[order[to]].member {
		return order, false
	}
	order[from], order[to] = order[to], order[from]
	return order, true
}

func (m *OS) sidebarDragGroupMotion(d *sidebarDragState, y int) {
	own := d.Blocks[d.SessionID]
	if d.Repo {
		own = sidebarOrderBlock{key: sidebarRepoBlockKey(d.Space, d.SessionID)}
	}
	if own.member {
		target := m.sidebarSessionRowIDAt(y)
		if t := d.Blocks[target]; target != d.SessionID && t.key == own.key && t.member {
			d.Order = sidebarMoveRow(d.Order, d.SessionID, target)
		}
		return
	}
	target, first, last := m.sidebarBlockRowsAt(y, d.Blocks)
	if target == "" || target == own.key || sidebarBlockScope(target) != sidebarBlockScope(own.key) {
		return
	}
	isBlock := func(key string) func(string) bool {
		return func(id string) bool { return d.Blocks[id].key == key }
	}
	down := slices.IndexFunc(d.Order, isBlock(own.key)) < slices.IndexFunc(d.Order, isBlock(target))
	if (down && y < last) || (!down && y > first) {
		return
	}
	d.Order = d.Blocks.moveGroup(d.Order, own.key, target, down)
}

func sidebarMoveRow(order []string, id, target string) []string {
	from, to := slices.Index(order, id), slices.Index(order, target)
	if from < 0 || to < 0 {
		return order
	}
	return slices.Insert(slices.Delete(order, from, from+1), to, id)
}

func (m *OS) sidebarBlockRowsAt(y int, blocks sidebarOrderBlocks) (key string, first, last int) {
	keyOf := func(h sidebarRowHit) string {
		switch h.Kind {
		case sidebarRowSession:
			return blocks[h.SessionID].key
		case sidebarRowRepo:
			return sidebarRepoBlockKey(h.WindowID, h.SessionID)
		}
		return ""
	}
	top := ""
	for _, h := range m.SidebarHits {
		k := keyOf(h)
		if k == "" {
			continue
		}
		if top == "" {
			top = k
		}
		if y >= h.Y0 {
			key = k
		}
	}
	if key == "" {
		key = top
	}
	first, last = y, y
	seen := false
	for _, h := range m.SidebarHits {
		if keyOf(h) != key {
			continue
		}
		if !seen {
			first, seen = h.Y0, true
		}
		last = h.Y0
	}
	return key, first, last
}
