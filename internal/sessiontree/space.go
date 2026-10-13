package sessiontree

// A space is a name a person files related sessions under. Unlike a
// repository group, which the daemon infers from where a session sits, a space
// is chosen, so it is the outer group: a space can hold sessions from several
// repositories, and the sessions of one repository can be spread over several
// spaces. Repository grouping therefore runs inside each space, and inside the
// sessions that are in no space, rather than the other way round.

// GroupBySpace returns the session list with every session that is filed under
// a space gathered under a KindSpace parent row for it, and the sessions in no
// space after all of them.
//
// within, when not nil, groups each run of rows further: each space's members,
// and the remainder, are passed through it separately. The rail passes
// GroupByRepo, so a repository's worktree sessions are grouped inside the
// space they are in.
//
// Spaces appear in the order of their first member, so a rail the user has
// dragged into an order keeps it within and across the spaces. A list with no
// session in any space is returned exactly as within makes it: a person who
// never files a session sees no change at all.
//
// Like GroupByRepo the result is a flat run. Every row inside a space carries
// the space's name in Space, the header included, which is what lets a
// surface step those rows in under the header without walking the list.
//
// Rows on another machine and the global session are never filed under a
// space here: the first is drawn in its machine's group and the second above
// the machines, and pulling either into a local space would say it is
// somewhere it is not.
func GroupBySpace(nodes []Node, within func([]Node) []Node) []Node {
	if within == nil {
		within = func(n []Node) []Node { return n }
	}
	members := map[string][]Node{}
	var spaces []string
	var rest []Node
	for _, n := range nodes {
		space := groupSpace(n)
		if space == "" {
			rest = append(rest, n)
			continue
		}
		if _, seen := members[space]; !seen {
			spaces = append(spaces, space)
		}
		members[space] = append(members[space], n)
	}
	if len(spaces) == 0 {
		return within(nodes)
	}

	out := make([]Node, 0, len(nodes)+2*len(spaces))
	for _, space := range spaces {
		group := members[space]
		out = append(out, spaceNode(space, group))
		for _, n := range within(group) {
			n.Space = space
			out = append(out, n)
		}
	}
	return append(out, within(rest)...)
}

// SpaceOf is the space GroupBySpace files a node under, or "" for a node it
// leaves with the sessions in no space.
func SpaceOf(n Node) string { return groupSpace(n) }

// groupSpace is the space a node is filed under, or "" for a node that is not
// grouped by space at all.
func groupSpace(n Node) string {
	if n.Kind != KindSession || n.Host != "" || n.Global {
		return ""
	}
	return n.Space
}

// spaceNode builds the parent row for one space. Like a repository's row it
// carries its members' roll-up and their count, which a folded space shows in
// place of the rows it hides.
func spaceNode(space string, members []Node) Node {
	node := repoNode(space, members)
	node.Kind = KindSpace
	node.Space = space
	return node
}

// SpaceRows is one KindSpace row per space, in the order GroupBySpace puts the
// spaces, then one row with an empty ID standing for the sessions in no space
// when there are any. It is nil when no session is in a space, so a surface
// that draws it draws nothing for a person who never files one.
//
// Each row carries its members' roll-up and count as a space's header does,
// plus whether any member has a client attached.
func SpaceRows(nodes []Node) []Node {
	members := map[string][]Node{}
	var spaces []string
	var rest []Node
	for _, n := range nodes {
		if n.Kind != KindSession || n.Host != "" {
			continue
		}
		space := groupSpace(n)
		if space == "" {
			rest = append(rest, n)
			continue
		}
		if _, seen := members[space]; !seen {
			spaces = append(spaces, space)
		}
		members[space] = append(members[space], n)
	}
	if len(spaces) == 0 {
		return nil
	}
	out := make([]Node, 0, len(spaces)+1)
	for _, space := range spaces {
		out = append(out, spaceSummary(spaceNode(space, members[space]), members[space]))
	}
	if len(rest) > 0 {
		none := spaceSummary(spaceNode("", rest), rest)
		none.ID, none.Title = "", ""
		out = append(out, none)
	}
	return out
}

// spaceSummary adds what a row standing for a whole space says beyond its
// header in the sessions section: whether any member has a client attached.
func spaceSummary(node Node, members []Node) Node {
	for _, m := range members {
		node.Attached = node.Attached || m.Attached
	}
	return node
}
