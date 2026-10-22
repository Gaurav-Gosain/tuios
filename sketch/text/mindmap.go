package text

import "tuios.dev/sketch"

// Mindmaps: a tree with the root at the top and each branch drawn with box
// characters, as tree(1) draws folders. Each branch from the root takes a
// series colour, as pele's do, for its lines and its brackets. A node keeps
// the brackets of its shape: [square], (rounded), ((circle)) and
// {{hexagon}}. A cloud and a bang are rounded: their source brackets,
// )cloud( and ))bang((, read as a typo in text.

var mindBrackets = map[string][2]string{
	"rect":    {"[", "]"},
	"rounded": {"(", ")"},
	"circle":  {"((", "))"},
	"cloud":   {"(", ")"},
	"bang":    {"(", ")"},
	"hex":     {"{{", "}}"},
}

func drawMindmap(l *sketch.Layout, opt Options) (*canvas, error) {
	nodes := l.Mindmap
	if len(nodes) == 0 {
		return nil, ErrUnsupported
	}
	children := make([][]int, len(nodes))
	depth := make([]int, len(nodes))
	deepest := 0
	for i, n := range nodes {
		// Nodes come after their parent; anything else is not a tree.
		if i > 0 && n.Parent >= 0 && n.Parent < i {
			children[n.Parent] = append(children[n.Parent], i)
			depth[i] = depth[n.Parent] + 1
			deepest = max(deepest, depth[i])
		}
	}
	// Each level sets the text in by four cells. Refuse before the prefixes
	// are built: a deep chain would need megabytes of them.
	if opt.Cols > 0 && 4*deepest+12 > opt.Cols {
		return nil, ErrTooWide
	}
	if int64(4*deepest+1)*int64(len(nodes)) > MaxCells {
		return nil, ErrTooLarge
	}
	type line struct {
		prefix string
		text   string
		role   uint8
		open   string
		close  string
		// branch is the child of the root the node is under, -1 for the
		// root, and top says the line is the first of that child.
		branch int
		top    bool
	}
	var lines []line
	// The walk is iterative: a deep chain of nodes must not grow the stack.
	type item struct {
		node   int
		indent string
		last   bool
		branch int
	}
	stack := []item{{0, "", true, -1}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := nodes[it.node]
		head, rest := "", ""
		if it.node > 0 {
			head, rest = it.indent+"├── ", it.indent+"│   "
			if it.last {
				head, rest = it.indent+"└── ", it.indent+"    "
			}
		}
		br := mindBrackets[n.Shape]
		room := 0
		if opt.Cols > 0 {
			room = max(opt.Cols-width(head)-len(br[0])-len(br[1]), 12)
		}
		var text []string
		for _, s := range n.Label {
			text = append(text, wrap(s, room)...)
		}
		if len(text) == 0 {
			text = []string{""}
		}
		role := uint8(roleText)
		if it.node == 0 {
			role = roleTitle
		}
		// Lines under the first are set in to where the text starts.
		pad := ""
		for range width(br[0]) {
			pad += " "
		}
		for k, s := range text {
			ln := line{prefix: rest + pad, text: s, role: role, branch: it.branch, top: depth[it.node] == 1}
			if k == 0 {
				ln.prefix, ln.open = head, br[0]
			} else {
				ln.top = false
			}
			if k == len(text)-1 {
				ln.close = br[1]
			}
			lines = append(lines, ln)
		}
		kids := children[it.node]
		inner := rest
		if it.node == 0 {
			inner = ""
		}
		for k := len(kids) - 1; k >= 0; k-- {
			branch := it.branch
			if it.node == 0 {
				branch = k
			}
			stack = append(stack, item{kids[k], inner, k == len(kids)-1, branch})
		}
	}
	w := 1
	for _, ln := range lines {
		w = max(w, width(ln.prefix)+width(ln.open)+width(ln.text)+width(ln.close))
	}
	if opt.Cols > 0 && w > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(w, len(lines))
	if err != nil {
		return nil, err
	}
	for y, ln := range lines {
		// The first four cells of a prefix are the root's lines, but for
		// the head of a child of the root. The rest are the branch's.
		branch := uint8(roleBox)
		if ln.branch >= 0 {
			branch = seriesRole(ln.branch)
		}
		trunk := uint8(roleLine)
		if ln.top {
			trunk = branch
		}
		head := []rune(ln.prefix)
		cut := min(len(head), 4)
		c.text(0, y, string(head[:cut]), trunk)
		c.text(cut, y, string(head[cut:]), branch)
		x := width(ln.prefix)
		c.text(x, y, ln.open, branch)
		x += width(ln.open)
		c.text(x, y, ln.text, ln.role)
		x += width(ln.text)
		c.text(x, y, ln.close, branch)
	}
	return withTitle(c, l.Title, opt.Cols)
}
