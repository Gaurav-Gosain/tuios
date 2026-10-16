package sessiontree

import "slices"

// A space is a name a person files related sessions under (tuios new --space,
// set-session-space). Unlike a repository group, which the daemon infers from
// where a session sits, a space is chosen, and it can hold sessions from
// several repositories. So it is the outer group: repository grouping runs
// inside each space, and inside the sessions in no space.

// GroupBySpace returns the session list with the sessions filed under a space
// gathered under a KindSpace heading for it, and the sessions in no space after
// every space, with no heading. It is the grouping tuios ls prints: the spaces
// in name order, each followed by its own sessions.
//
// within, when not nil, groups each run of rows further: each space's members,
// and the sessions in no space, are passed through it separately. The rail
// passes GroupByRepo, so a repository's worktree sessions are grouped inside
// the space they are in.
//
// Members keep the order they are given in, so a rail the user has dragged
// into an order keeps it inside each space. A list with no session in any
// space is returned exactly as within makes it, so a person who never files a
// session sees the list they always had.
//
// Like GroupByRepo the result is a flat run. Every row drawn under a space's
// heading carries the space's name in Space, the heading included, and every
// other row carries none, which is what lets a surface step those rows in
// without walking the list.
//
// A row on another machine and a global session are never grouped here: the
// first is drawn in its machine's group and the second in the global group,
// and pulling either under a local heading would say it is somewhere it is
// not.
func GroupBySpace(nodes []Node, within func([]Node) []Node) []Node {
	if within == nil {
		within = func(n []Node) []Node { return n }
	}
	// The common case is no space at all. The rail rebuilds on every frame of
	// a marquee or a drag, so that case hands the list on without a copy.
	if !slices.ContainsFunc(nodes, func(n Node) bool { return n.Space != "" }) {
		return within(nodes)
	}
	members := map[string][]Node{}
	var rest []Node
	for _, n := range nodes {
		space := groupSpace(n)
		if space == "" {
			n.Space = ""
			rest = append(rest, n)
			continue
		}
		members[space] = append(members[space], n)
	}
	if len(members) == 0 {
		return within(rest)
	}

	spaces := make([]string, 0, len(members))
	for space := range members {
		spaces = append(spaces, space)
	}
	slices.Sort(spaces)

	out := make([]Node, 0, len(nodes)+2*len(spaces))
	for _, space := range spaces {
		out = append(out, Node{Kind: KindSpace, ID: space, Title: space, Space: space})
		for _, n := range within(members[space]) {
			n.Space = space
			out = append(out, n)
		}
	}
	return append(out, within(rest)...)
}

// groupSpace is the space a node is grouped under, or "" for a node that is
// not grouped by space at all.
func groupSpace(n Node) string {
	if n.Kind != KindSession || n.Host != "" || n.Global {
		return ""
	}
	return n.Space
}
